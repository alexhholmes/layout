package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexhholmes/layout/internal/parser"
)

// Region represents a memory region in the layout
type Region struct {
	Kind        RegionKind
	Start       int // Byte offset where region begins
	Boundary    int // Byte offset where region must stop (-1 if end of buffer)
	Direction   parser.PackDirection
	Field       parser.Field // The field occupying this region
	ElementSize int          // Size of each element (for []StructType), 1 for []byte, 0 for fixed fields
	ElementType string       // Type name of slice elements (e.g., "LeafElement" for []LeafElement)
}

type RegionKind int

const (
	FixedRegion   RegionKind = iota // Fixed-size field at specific offset
	DynamicRegion                   // Variable-size field (slice)
)

// AnalyzedLayout contains the analyzed memory layout with regions
type AnalyzedLayout struct {
	TypeName   string
	BufferSize int
	Regions    []Region
	Errors     []string // Validation errors
}

// Analyze performs layout analysis on a parsed type
func Analyze(layout *parser.TypeLayout, registry *TypeRegistry) (*AnalyzedLayout, error) {
	if layout == nil {
		return nil, fmt.Errorf("layout is nil")
	}

	a := &AnalyzedLayout{
		TypeName:   layout.Name,
		BufferSize: layout.Anno.Size,
	}

	// Phase 1: Build regions from fields
	for _, field := range layout.Fields {
		region, err := buildRegion(field, layout.Anno.Size, registry)
		if err != nil {
			a.Errors = append(a.Errors, fmt.Sprintf("%s: %v", field.Name, err))
			continue
		}
		a.Regions = append(a.Regions, region)
	}

	if len(a.Errors) > 0 {
		return a, fmt.Errorf("layout has %d errors", len(a.Errors))
	}

	// Phase 2: Calculate dynamic region start points and boundaries
	if err := calculateBoundaries(a); err != nil {
		a.Errors = append(a.Errors, err.Error())
		return a, err
	}

	// Phase 3: Validate count fields
	if err := validateCountFields(a, layout); err != nil {
		a.Errors = append(a.Errors, err.Error())
		return a, err
	}

	// Phase 4: Detect collisions
	detectCollisions(a)

	return a, nil
}

func buildRegion(field parser.Field, bufferSize int, registry *TypeRegistry) (Region, error) {
	r := Region{
		Field:    field,
		Boundary: -1, // Unknown until calculateBoundaries
	}

	if field.Layout.Direction == parser.Fixed {
		// Fixed field: calculate size and end offset
		size, err := registry.SizeOf(field.GoType)
		if err != nil {
			return r, fmt.Errorf("cannot determine size: %w", err)
		}
		if size < 0 {
			return r, fmt.Errorf("fixed field cannot have dynamic type: %s", field.GoType)
		}

		r.Kind = FixedRegion
		r.Start = field.Layout.Offset
		r.Boundary = field.Layout.Offset + size
		r.Direction = parser.Fixed

		if r.Boundary > bufferSize {
			return r, fmt.Errorf("field [%d, %d) exceeds buffer size %d",
				r.Start, r.Boundary, bufferSize)
		}

		return r, nil
	}

	// Dynamic field
	size, err := registry.SizeOf(field.GoType)
	if err != nil {
		return r, fmt.Errorf("cannot determine element type: %w", err)
	}
	if size != -1 {
		return r, fmt.Errorf("dynamic field must be slice type, got: %s", field.GoType)
	}

	// Extract element type from []Type
	elementType := extractElementType(field.GoType)
	if elementType == "" {
		return r, fmt.Errorf("cannot extract element type from: %s", field.GoType)
	}

	// Get element size
	elementSize, err := registry.SizeOf(elementType)
	if err != nil {
		return r, fmt.Errorf("cannot determine element size for %s: %w", elementType, err)
	}
	if elementSize < 0 {
		return r, fmt.Errorf("element type cannot be dynamic: %s", elementType)
	}

	r.Kind = DynamicRegion
	r.Direction = field.Layout.Direction
	r.ElementSize = elementSize
	r.ElementType = elementType

	// Set start point
	if field.Layout.StartAt >= 0 {
		// Explicit start: @N,direction
		r.Start = field.Layout.StartAt
	} else {
		// Implicit start: calculated in Phase 2
		if field.Layout.Direction == parser.EndStart {
			r.Start = bufferSize // Grows backward from end
		} else {
			r.Start = 0 // Temporary, calculated in Phase 2
		}
	}

	return r, nil
}

// extractElementType extracts element type from slice type
// "[]byte" -> "byte", "[]LeafElement" -> "LeafElement"
func extractElementType(sliceType string) string {
	if len(sliceType) < 3 || !strings.HasPrefix(sliceType, "[]") {
		return ""
	}
	return sliceType[2:]
}

// calculateBoundaries determines start points and boundaries for dynamic regions
func calculateBoundaries(a *AnalyzedLayout) error {
	// Sort regions by start offset for boundary calculation
	sort.Slice(a.Regions, func(i, j int) bool {
		return a.Regions[i].Start < a.Regions[j].Start
	})

	// Calculate implicit start points for start-end regions
	for i := range a.Regions {
		r := &a.Regions[i]
		if r.Kind == DynamicRegion && r.Direction == parser.StartEnd && r.Field.Layout.StartAt < 0 {
			// Find end of previous fixed region or start of buffer
			r.Start = findPreviousEnd(a.Regions, i)
		}
	}

	// Calculate boundaries
	for i := range a.Regions {
		r := &a.Regions[i]
		if r.Kind == FixedRegion {
			continue // Fixed regions have boundaries set
		}

		// Find boundary for dynamic region
		if r.Direction == parser.StartEnd {
			// Growing forward: boundary is start of next region
			r.Boundary = findNextStart(a.Regions, i, a.BufferSize)
		} else {
			// Growing backward: boundary is end of previous region
			r.Boundary = findPreviousEnd(a.Regions, i)
		}
	}

	return nil
}

func findPreviousEnd(regions []Region, idx int) int {
	// Find the end offset of the last region before idx
	for i := idx - 1; i >= 0; i-- {
		if regions[i].Kind == FixedRegion {
			return regions[i].Boundary
		}
		if regions[i].Direction == parser.StartEnd && regions[i].Field.Layout.StartAt >= 0 {
			// Previous dynamic region with explicit start
			return regions[i].Start
		}
	}
	return 0 // Start of buffer
}

func findNextStart(regions []Region, idx int, bufferSize int) int {
	// Find the start offset of the next region after idx
	for i := idx + 1; i < len(regions); i++ {
		if regions[i].Kind == FixedRegion {
			return regions[i].Start
		}
		if regions[i].Field.Layout.StartAt >= 0 {
			return regions[i].Start
		}
	}
	return bufferSize // End of buffer
}

func validateCountFields(a *AnalyzedLayout, layout *parser.TypeLayout) error {
	// Check if dynamic fields require count fields
	for _, region := range a.Regions {
		if region.Kind != DynamicRegion {
			continue
		}

		countField := region.Field.Layout.CountField

		// Struct slices ALWAYS require count (can't infer element count from boundary)
		if region.ElementType != "byte" {
			if countField == "" {
				return fmt.Errorf("field '%s' (type %s) requires count= (struct slices must specify element count)",
					region.Field.Name, region.Field.GoType)
			}
		}

		// For []byte, count is optional if there are fixed boundaries
		// But if there's no fixed boundary, count is required
		if region.ElementType == "byte" && countField == "" {
			needsCount := false
			if region.Direction == parser.EndStart {
				// end-start: needs count if boundary is not 0 and not a fixed field
				needsCount = region.Boundary == 0 && hasNonFixedBefore(a.Regions, region)
			} else {
				// start-end: needs count if boundary is buffer end and not a fixed field
				needsCount = region.Boundary == a.BufferSize && hasNonFixedAfter(a.Regions, region)
			}

			if needsCount {
				return fmt.Errorf("field '%s' requires count= (no fixed boundary)", region.Field.Name)
			}
		}

		// If count specified, validate it exists and has sufficient capacity
		if countField != "" {
			countFieldType, err := getCountFieldType(countField, layout)
			if err != nil {
				return err
			}

			// Validate count field capacity (only for non-nested fields where we know the type)
			if !strings.Contains(countField, ".") {
				if err := validateCountCapacity(region, countFieldType, a.BufferSize); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func hasNonFixedBefore(regions []Region, target Region) bool {
	for _, r := range regions {
		if r.Start < target.Start && r.Kind == DynamicRegion {
			return true
		}
	}
	return false
}

func hasNonFixedAfter(regions []Region, target Region) bool {
	for _, r := range regions {
		if r.Start > target.Start && r.Kind == DynamicRegion {
			return true
		}
	}
	return false
}

// getCountFieldType returns the Go type of a count field
func getCountFieldType(countField string, layout *parser.TypeLayout) (string, error) {
	// Check for nested field reference (e.g., "Header.NumKeys")
	if strings.Contains(countField, ".") {
		parts := strings.Split(countField, ".")
		if len(parts) != 2 {
			return "", fmt.Errorf("count field '%s' has invalid nested reference (only one level supported)", countField)
		}

		parentField := parts[0]
		// Validate parent field exists
		found := false
		for _, f := range layout.Fields {
			if f.Name == parentField {
				found = true
				// We can't easily validate the nested field type without parsing the parent struct
				// For nested fields, we can't validate type - assume user knows what they're doing
				break
			}
		}
		if !found {
			return "", fmt.Errorf("count field parent '%s' not found in '%s'", parentField, countField)
		}
		return "", nil // Return empty string for nested fields (can't determine type)
	}

	// Simple field reference - validate type and return it
	for _, f := range layout.Fields {
		if f.Name == countField {
			// Validate type is an integer type
			if !isCountType(f.GoType) {
				return "", fmt.Errorf("count field '%s' must be an integer type (int8/16/32/64 or uint8/16/32/64), got: %s",
					countField, f.GoType)
			}
			return f.GoType, nil
		}
	}
	return "", fmt.Errorf("count field '%s' not found", countField)
}

// validateCountCapacity checks if count field type can hold max possible element count
func validateCountCapacity(region Region, countFieldType string, bufferSize int) error {
	if countFieldType == "" {
		return nil // No count field, skip validation
	}

	// Calculate max possible elements that could fit
	maxSpace := 0
	if region.Direction == parser.StartEnd {
		maxSpace = region.Boundary - region.Start
	} else {
		maxSpace = region.Start - region.Boundary
	}

	if maxSpace < 0 {
		maxSpace = 0
	}

	maxElements := maxSpace / region.ElementSize
	if maxSpace%region.ElementSize != 0 {
		maxElements++ // Round up
	}

	// Get max value for count field type
	maxCountValue := getMaxCountValue(countFieldType)
	if maxCountValue < 0 {
		return nil // Unknown type, skip
	}

	if maxElements > maxCountValue {
		return fmt.Errorf("count field '%s' (type %s, max value %d) cannot hold max possible elements %d",
			region.Field.Layout.CountField, countFieldType, maxCountValue, maxElements)
	}

	return nil
}

// getMaxCountValue returns the maximum value a count type can hold
func getMaxCountValue(countType string) int {
	switch countType {
	case "uint8", "int8":
		return 255
	case "uint16", "int16":
		return 65535
	case "uint32", "int32":
		return 2147483647 // Max int32
	case "uint64", "int64":
		return 2147483647 // Effectively unlimited for our purposes
	default:
		return -1
	}
}

func isCountType(goType string) bool {
	switch goType {
	case "uint8", "uint16", "uint32", "uint64",
		"int8", "int16", "int32", "int64":
		return true
	}
	return false
}

func detectCollisions(a *AnalyzedLayout) {
	// Check for overlapping regions
	for i := 0; i < len(a.Regions)-1; i++ {
		r1 := a.Regions[i]
		r2 := a.Regions[i+1]

		// Check if regions overlap
		if r1.Kind == FixedRegion && r2.Kind == FixedRegion {
			if r1.Boundary > r2.Start {
				a.Errors = append(a.Errors,
					fmt.Sprintf("collision: %s [%d, %d) overlaps %s [%d, %d)",
						r1.Field.Name, r1.Start, r1.Boundary,
						r2.Field.Name, r2.Start, r2.Boundary))
			}
		}
	}
}

// IsValid returns true if layout has no errors
func (a *AnalyzedLayout) IsValid() bool {
	return len(a.Errors) == 0
}
