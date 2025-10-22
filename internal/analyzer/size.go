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

// TypeRegistry tracks struct sizes for layout analysis
type TypeRegistry struct {
	types map[string]int // type name → size in bytes
}

func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{
		types: make(map[string]int),
	}
}

// Register adds a struct type with its size
func (r *TypeRegistry) Register(name string, size int) {
	r.types[name] = size
}

// Lookup returns the size of a registered type
func (r *TypeRegistry) Lookup(name string) (int, bool) {
	size, ok := r.types[name]
	return size, ok
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

	// Try built-in types
	size, err := SizeOf(goType)
	if err == nil {
		return size, nil
	}

	// Check if it's a registered struct
	if size, ok := r.Lookup(goType); ok {
		return size, nil
	}

	return 0, fmt.Errorf("unknown type: %s (not registered)", goType)
}