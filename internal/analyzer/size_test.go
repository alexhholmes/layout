package analyzer

import (
	"testing"
)

func TestSizeOf(t *testing.T) {
	tests := []struct {
		goType   string
		wantSize int
		wantErr  bool
	}{
		// Primitive types
		{"uint8", 1, false},
		{"int8", 1, false},
		{"byte", 1, false},
		{"bool", 1, false},
		{"uint16", 2, false},
		{"int16", 2, false},
		{"uint32", 4, false},
		{"int32", 4, false},
		{"float32", 4, false},
		{"uint64", 8, false},
		{"int64", 8, false},
		{"float64", 8, false},

		// Arrays
		{"[16]byte", 16, false},
		{"[8]uint32", 32, false},
		{"[4]uint64", 32, false},
		{"[10]uint8", 10, false},

		// Slices (dynamic)
		{"[]byte", -1, false},
		{"[]uint32", -1, false},
		{"[]MyStruct", -1, false},

		// Error cases
		{"*uint32", 0, true},     // pointer
		{"MyStruct", 0, true},    // unknown struct
		{"[16]*byte", 0, true},   // array of pointers
		{"[abc]byte", 0, true},   // invalid array size
		{"[][]byte", -1, false},  // slice of slice (dynamic)
	}

	for _, tt := range tests {
		t.Run(tt.goType, func(t *testing.T) {
			got, err := SizeOf(tt.goType)

			if tt.wantErr {
				if err == nil {
					t.Errorf("SizeOf(%q) expected error, got nil", tt.goType)
				}
				return
			}

			if err != nil {
				t.Fatalf("SizeOf(%q) unexpected error: %v", tt.goType, err)
			}

			if got != tt.wantSize {
				t.Errorf("SizeOf(%q) = %d, want %d", tt.goType, got, tt.wantSize)
			}
		})
	}
}

func TestTypeRegistry(t *testing.T) {
	reg := NewTypeRegistry()

	// Register some struct types
	reg.Register("LeafElement", 8)
	reg.Register("Node", 24)

	tests := []struct {
		goType   string
		wantSize int
		wantErr  bool
	}{
		// Built-in types still work
		{"uint64", 8, false},
		{"[]byte", -1, false},

		// Registered structs
		{"LeafElement", 8, false},
		{"Node", 24, false},

		// Arrays of registered structs
		{"[4]LeafElement", 32, false},

		// Slices of registered structs (dynamic)
		{"[]LeafElement", -1, false},
		{"[]Node", -1, false},

		// Unregistered struct
		{"UnknownStruct", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.goType, func(t *testing.T) {
			got, err := reg.SizeOf(tt.goType)

			if tt.wantErr {
				if err == nil {
					t.Errorf("SizeOf(%q) expected error, got nil", tt.goType)
				}
				return
			}

			if err != nil {
				t.Fatalf("SizeOf(%q) unexpected error: %v", tt.goType, err)
			}

			if got != tt.wantSize {
				t.Errorf("SizeOf(%q) = %d, want %d", tt.goType, got, tt.wantSize)
			}
		})
	}
}

func TestTypeRegistryArrayOfStruct(t *testing.T) {
	reg := NewTypeRegistry()
	reg.Register("Item", 12)

	size, err := reg.SizeOf("[10]Item")
	if err != nil {
		t.Fatalf("SizeOf([10]Item) error: %v", err)
	}

	if size != 120 {
		t.Errorf("SizeOf([10]Item) = %d, want 120", size)
	}
}