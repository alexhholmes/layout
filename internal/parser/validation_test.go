package parser

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestValidateStructFields(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		wantError bool
		errMsg    string
	}{
		{
			name: "copy mode - no requirements",
			code: `package test
type Page struct {
	Header uint16
	Body   []byte
}`,
			wantError: false,
		},
		{
			name: "zerocopy without align - requires buf [size]byte",
			code: `package test
type Page struct {
	buf    [4096]byte
	Header uint16
	Body   []byte
}`,
			wantError: false,
		},
		{
			name: "zerocopy without align - missing buf",
			code: `package test
type Page struct {
	Header uint16
	Body   []byte
}`,
			wantError: true,
			errMsg:    "zerocopy mode requires field: buf [4096]byte",
		},
		{
			name: "zerocopy without align - wrong buf type",
			code: `package test
type Page struct {
	buf    []byte
	Header uint16
	Body   []byte
}`,
			wantError: true,
			errMsg:    "buf field must be [4096]byte, got []byte",
		},
		{
			name: "zerocopy with align - requires backing and buf []byte",
			code: `package test
type Page struct {
	backing []byte
	buf     []byte
	Header  uint16
	Body    []byte
}`,
			wantError: false,
		},
		{
			name: "zerocopy with align - missing backing",
			code: `package test
type Page struct {
	buf    []byte
	Header uint16
	Body   []byte
}`,
			wantError: true,
			errMsg:    "zerocopy mode with align=512 requires field: backing []byte",
		},
		{
			name: "zerocopy with align - missing buf",
			code: `package test
type Page struct {
	backing []byte
	Header  uint16
	Body    []byte
}`,
			wantError: true,
			errMsg:    "zerocopy mode with align=512 requires field: buf []byte",
		},
		{
			name: "zerocopy with align - wrong buf type",
			code: `package test
type Page struct {
	backing []byte
	buf     [4096]byte
	Header  uint16
	Body    []byte
}`,
			wantError: true,
			errMsg:    "buf field must be []byte when using align or allocator, got [4096]byte",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse the code
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "test.go", tt.code, 0)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}

			// Extract the struct type
			var structType *ast.StructType
			for _, decl := range file.Decls {
				if genDecl, ok := decl.(*ast.GenDecl); ok {
					for _, spec := range genDecl.Specs {
						if typeSpec, ok := spec.(*ast.TypeSpec); ok {
							if st, ok := typeSpec.Type.(*ast.StructType); ok {
								structType = st
								break
							}
						}
					}
				}
			}

			if structType == nil {
				t.Fatal("No struct type found")
			}

			// Create annotation based on test case
			anno := &TypeAnnotation{
				Size:   4096,
				Endian: "little",
			}

			// Set mode based on test name
			if tt.name == "copy mode - no requirements" {
				anno.Mode = "copy"
			} else {
				anno.Mode = "zerocopy"
				// Set align if test mentions it
				if tt.name == "zerocopy with align - requires backing and buf []byte" ||
					tt.name == "zerocopy with align - missing backing" ||
					tt.name == "zerocopy with align - missing buf" ||
					tt.name == "zerocopy with align - wrong buf type" {
					anno.Align = 512
				}
			}

			// Validate
			err = validateStructFields(structType, anno)

			if tt.wantError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				} else if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("Expected error %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}