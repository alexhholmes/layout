package analyzer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SizeOf returns the size in bytes of a Go type
// Returns -1 for dynamic-sized types (slices)
// Returns error for unsupported types
func SizeOf(goType string) (int, error) {
	// Primitive types
	switch goType {
	case "uint8", "int8", "byte", "bool":
		return 1, nil
	case "uint16", "int16":
		return 2, nil
	case "uint32", "int32", "float32":
		return 4, nil
	case "uint64", "int64", "float64":
		return 8, nil
	}

	// Slice: []T (dynamic) - check before array
	if strings.HasPrefix(goType, "[]") {
		return -1, nil
	}

	// Array: [N]T
	if strings.HasPrefix(goType, "[") && strings.Contains(goType, "]") {
		return arraySize(goType)
	}

	// Pointer (not supported for binary layout)
	if strings.HasPrefix(goType, "*") {
		return 0, fmt.Errorf("pointer types not supported: %s", goType)
	}

	// Unknown/struct type - needs type registry
	return 0, fmt.Errorf("unknown type: %s (use type registry for structs)", goType)
}

var arrayRe = regexp.MustCompile(`^\[(\d+)\](.+)$`)

func arraySize(goType string) (int, error) {
	// Parse: [16]byte → 16 * 1
	matches := arrayRe.FindStringSubmatch(goType)
	if matches == nil {
		return 0, fmt.Errorf("invalid array type: %s", goType)
	}

	n, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid array length: %s", matches[1])
	}

	elemType := matches[2]
	elemSize, err := SizeOf(elemType)
	if err != nil {
		return 0, fmt.Errorf("array element: %w", err)
	}

	if elemSize < 0 {
		return 0, fmt.Errorf("array of dynamic type not supported: %s", goType)
	}

	return n * elemSize, nil
}

// TypeRegistry tracks struct sizes and type aliases for layout analysis
type TypeRegistry struct {
	types   map[string]int    // type name → size in bytes
	aliases map[string]string // alias → underlying type
}

func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{
		types:   make(map[string]int),
		aliases: make(map[string]string),
	}
}

// Register adds a struct type with its size
func (r *TypeRegistry) Register(name string, size int) {
	r.types[name] = size
}

// RegisterAlias adds a type alias mapping (e.g., type PageID uint64)
func (r *TypeRegistry) RegisterAlias(alias, underlying string) {
	r.aliases[alias] = underlying
}

// Lookup returns the size of a registered type
func (r *TypeRegistry) Lookup(name string) (int, bool) {
	size, ok := r.types[name]
	return size, ok
}

// ResolveType resolves type aliases to their underlying types
// Returns the original type if not an alias
func (r *TypeRegistry) ResolveType(goType string) string {
	// Recursively resolve aliases
	for {
		if underlying, ok := r.aliases[goType]; ok {
			goType = underlying
		} else {
			break
		}
	}
	return goType
}

// SizeOfWithRegistry calculates size using registry for struct types
func (r *TypeRegistry) SizeOf(goType string) (int, error) {
	// Handle slices (dynamic)
	if strings.HasPrefix(goType, "[]") {
		return -1, nil
	}

	// Handle arrays of registered types: [N]RegisteredType
	if strings.HasPrefix(goType, "[") {
		matches := arrayRe.FindStringSubmatch(goType)
		if matches != nil {
			n, _ := strconv.Atoi(matches[1])
			elemType := matches[2]
			elemSize, err := r.SizeOf(elemType) // Recursive
			if err != nil {
				return 0, err
			}
			if elemSize < 0 {
				return 0, fmt.Errorf("array of dynamic type not supported: %s", goType)
			}
			return n * elemSize, nil
		}
	}

	// Resolve type aliases
	resolved := r.ResolveType(goType)

	// Try built-in types
	size, err := SizeOf(resolved)
	if err == nil {
		return size, nil
	}

	// Check if it's a registered struct
	if size, ok := r.Lookup(resolved); ok {
		return size, nil
	}

	return 0, fmt.Errorf("unknown type: %s (not registered)", goType)
}