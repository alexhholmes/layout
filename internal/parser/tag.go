package parser

import (
	"fmt"
	"strconv"
	"strings"
)

type PackDirection int

const (
	Fixed    PackDirection = iota // @0, @4, etc.
	StartEnd                      // start-end
	EndStart                      // end-start
)

func (d PackDirection) String() string {
	switch d {
	case Fixed:
		return "fixed"
	case StartEnd:
		return "start-end"
	case EndStart:
		return "end-start"
	default:
		return "unknown"
	}
}

type FieldLayout struct {
	Offset     int // -1 if dynamic; for Fixed, the byte position
	Direction  PackDirection
	StartAt    int    // -1 if unspecified; for directional, where growth begins
	CountField string // Field name containing count/length for slices (empty if not specified)

	// Indirect slice fields ([][]byte with metadata indirection)
	From        string // Source slice field name (e.g., "Elements")
	OffsetField string // Field in element that holds offset (e.g., "KeyOffset")
	SizeField   string // Field in element that holds size (e.g., "KeySize")
	Region      string // Region field that this slices into (e.g., "Data")
}

// ParseTag parses layout struct tags
//
// Semantics:
//   - "@N"                      : Fixed field at byte offset N
//   - "start-end"               : Dynamic region growing forward →
//   - "end-start"               : Dynamic region growing backward ←
//   - "@N,start-end"            : Dynamic region starting at byte N, growing forward →
//   - "@N,end-start"            : Dynamic region starting at byte N, growing backward ←
//   - "direction,count=Field"   : Dynamic region with count from Field
//
// Count semantics (validated by analyzer):
//   - end-start growing to offset 0 or fixed field: NO count needed (implicit boundary)
//   - end-start growing into dynamic space: count= required
//   - start-end growing to fixed field: NO count needed (implicit boundary)
//   - start-end growing into dynamic space: count= required
//
// Examples:
//
//	"@0"                        → Fixed field at offset 0
//	"end-start"                 → Grow backward (boundary determined by analyzer)
//	"end-start,count=NumElems"  → Grow backward, length from NumElems
//	"start-end,count=BodyLen"   → Grow forward, length from BodyLen
//	"@1999,end-start,count=N"   → Grow backward from 1999, length from N
func ParseTag(tag string) (*FieldLayout, error) {
	if tag == "" {
		return nil, fmt.Errorf("empty layout tag")
	}

	f := &FieldLayout{
		Offset:  -1,
		StartAt: -1,
	}

	parts := strings.Split(tag, ",")

	// Check for indirect slice syntax: from=X,offset=Y,size=Z,region=W
	if strings.HasPrefix(parts[0], "from=") {
		return parseIndirectSlice(parts)
	}

	// Check for fixed offset: @N
	if strings.HasPrefix(parts[0], "@") {
		// Extract offset: "@8" → 8
		offsetStr := strings.TrimPrefix(parts[0], "@")
		offset, err := strconv.Atoi(offsetStr)
		if err != nil {
			return nil, fmt.Errorf("invalid offset: %s", parts[0])
		}

		// No other parts: fixed field at offset
		if len(parts) == 1 {
			f.Offset = offset
			f.Direction = Fixed
			return f, nil
		}

		// Has direction: dynamic region starting at offset
		// e.g., "@1999,end-start" or "@1999,end-start,count=N"
		dir, countField, err := parseDirectionAndCount(parts[1:])
		if err != nil {
			return nil, err
		}
		f.Offset = -1 // Dynamic
		f.Direction = dir
		f.StartAt = offset
		f.CountField = countField
	} else {
		// Pure directional: "start-end" or "start-end,count=Len"
		dir, countField, err := parseDirectionAndCount(parts)
		if err != nil {
			return nil, err
		}
		f.Direction = dir
		f.Offset = -1
		f.StartAt = -1
		f.CountField = countField
	}

	return f, nil
}

// parseDirectionAndCount extracts direction and optional count=Field from parts
// Input: ["start-end"] or ["end-start", "count=NumElems"]
func parseDirectionAndCount(parts []string) (PackDirection, string, error) {
	if len(parts) == 0 {
		return 0, "", fmt.Errorf("missing direction")
	}

	// First part is direction
	dir, err := parseDirection(parts[0])
	if err != nil {
		return 0, "", err
	}

	// Check for count= in remaining parts
	countField := ""
	for _, part := range parts[1:] {
		if strings.HasPrefix(part, "count=") {
			countField = strings.TrimPrefix(part, "count=")
			if countField == "" {
				return 0, "", fmt.Errorf("count= requires field name")
			}
		} else {
			return 0, "", fmt.Errorf("unknown parameter: %s", part)
		}
	}

	return dir, countField, nil
}

func parseDirection(s string) (PackDirection, error) {
	switch s {
	case "start-end":
		return StartEnd, nil
	case "end-start":
		return EndStart, nil
	default:
		return 0, fmt.Errorf("invalid direction: %s (expected start-end or end-start)", s)
	}
}

// parseIndirectSlice parses indirect slice tags: from=X,offset=Y,size=Z,region=W
func parseIndirectSlice(parts []string) (*FieldLayout, error) {
	f := &FieldLayout{
		Offset:  -1,
		StartAt: -1,
	}

	// Parse all key=value pairs
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid indirect slice parameter: %s", part)
		}

		switch kv[0] {
		case "from":
			f.From = kv[1]
		case "offset":
			f.OffsetField = kv[1]
		case "size":
			f.SizeField = kv[1]
		case "region":
			f.Region = kv[1]
		default:
			return nil, fmt.Errorf("unknown indirect slice parameter: %s", kv[0])
		}
	}

	// Validate all 4 required params are present
	if f.From == "" || f.OffsetField == "" || f.SizeField == "" || f.Region == "" {
		return nil, fmt.Errorf("indirect slice requires all 4 params: from, offset, size, region")
	}

	return f, nil
}
