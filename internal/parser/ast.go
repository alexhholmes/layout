package parser

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
)

// TypeLayout represents a parsed struct with layout annotation
type TypeLayout struct {
	Name   string
	Anno   *TypeAnnotation
	Fields []Field
}

// Field represents a struct field with layout tag
type Field struct {
	Name   string
	GoType string
	Layout *FieldLayout
}

// ParseFile parses a Go source file and extracts types with @layout annotations
// Returns type layouts and a registry with type aliases
func ParseFile(filename string) ([]*TypeLayout, map[string]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse error: %w", err)
	}

	types, aliases := extractTypes(file)
	return types, aliases, nil
}

func extractTypes(file *ast.File) ([]*TypeLayout, map[string]string) {
	var types []*TypeLayout
	aliases := make(map[string]string)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec := spec.(*ast.TypeSpec)

			// Check for type alias: type PageID uint64
			if ident, ok := typeSpec.Type.(*ast.Ident); ok {
				// This is a type alias to a basic type
				aliases[typeSpec.Name.Name] = ident.Name
				continue
			}

			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue // Not a struct
			}

			// Extract @layout annotation from comments directly above type
			anno := extractAnnotation(genDecl.Doc)
			if anno == nil {
				continue // No @layout, skip this type
			}

			// Extract fields with layout tags
			fields := extractFields(structType)
			if len(fields) == 0 {
				continue // No layout tags, skip
			}

			// Calculate size from fields if not specified
			if anno.Size == 0 {
				calculatedSize := calculateSize(fields)
				if calculatedSize == 0 {
					fmt.Printf("Warning: %s: cannot calculate size (no fixed fields or only dynamic fields), size must be specified\n", typeSpec.Name.Name)
					continue
				}
				anno.Size = calculatedSize
			}

			// Validate struct has required fields for zerocopy mode
			if err := validateStructFields(structType, anno); err != nil {
				// TODO: collect errors instead of skipping
				fmt.Printf("Warning: %s: %v\n", typeSpec.Name.Name, err)
				continue
			}

			types = append(types, &TypeLayout{
				Name:   typeSpec.Name.Name,
				Anno:   anno,
				Fields: fields,
			})
		}
	}

	return types, aliases
}

func extractAnnotation(doc *ast.CommentGroup) *TypeAnnotation {
	if doc == nil {
		return nil
	}

	// Extract comment text lines
	var lines []string
	for _, comment := range doc.List {
		cleaned := CleanComment(comment.Text)
		lines = append(lines, cleaned)
	}

	// Search for @layout annotation
	anno, found := FindAnnotation(lines)
	if !found {
		return nil
	}

	return anno
}

// validateStructFields checks that struct has required fields based on annotation
func validateStructFields(structType *ast.StructType, anno *TypeAnnotation) error {
	if anno.Mode != "zerocopy" {
		return nil // No special requirements for copy mode
	}

	// Extract all field names and types (not just ones with layout tags)
	fieldMap := make(map[string]string)
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue // Embedded field
		}
		fieldName := field.Names[0].Name
		fieldType := typeToString(field.Type)
		fieldMap[fieldName] = fieldType
	}

	// Zerocopy with alignment or custom allocator requires backing and buf fields
	if anno.Align > 0 || anno.Allocator != "" {
		// Check for backing []byte
		backingType, hasBackingField := fieldMap["backing"]
		if !hasBackingField {
			if anno.Align > 0 {
				return fmt.Errorf("zerocopy mode with align=%d requires field: backing []byte", anno.Align)
			}
			return fmt.Errorf("zerocopy mode with allocator=%s requires field: backing []byte", anno.Allocator)
		}
		if backingType != "[]byte" {
			return fmt.Errorf("backing field must be []byte, got %s", backingType)
		}

		// Check for buf []byte
		bufType, hasBufField := fieldMap["buf"]
		if !hasBufField {
			if anno.Align > 0 {
				return fmt.Errorf("zerocopy mode with align=%d requires field: buf []byte", anno.Align)
			}
			return fmt.Errorf("zerocopy mode with allocator=%s requires field: buf []byte", anno.Allocator)
		}
		if bufType != "[]byte" {
			return fmt.Errorf("buf field must be []byte when using align or allocator, got %s", bufType)
		}
	} else {
		// Zerocopy without alignment or allocator requires buf [size]byte
		bufType, hasBufField := fieldMap["buf"]
		if !hasBufField {
			return fmt.Errorf("zerocopy mode requires field: buf [%d]byte", anno.Size)
		}
		expectedType := fmt.Sprintf("[%d]byte", anno.Size)
		if bufType != expectedType {
			return fmt.Errorf("buf field must be %s, got %s", expectedType, bufType)
		}
	}

	return nil
}

func extractFields(structType *ast.StructType) []Field {
	var fields []Field

	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue // Embedded field, skip
		}

		if field.Tag == nil {
			continue // No tags
		}

		// Parse struct tag
		tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
		layoutTag := tag.Get("layout")
		if layoutTag == "" {
			continue // No layout tag
		}

		// Parse layout tag
		layout, err := ParseTag(layoutTag)
		if err != nil {
			// TODO: collect errors instead of skipping
			continue
		}

		fields = append(fields, Field{
			Name:   field.Names[0].Name,
			GoType: typeToString(field.Type),
			Layout: layout,
		})
	}

	return fields
}

// typeToString converts AST type expression to string
// Only supports types with defined binary layout
func typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		// Simple type: uint16, LeafElement, etc.
		return t.Name

	case *ast.ArrayType:
		if t.Len == nil {
			// Slice: []byte, []LeafElement
			return "[]" + typeToString(t.Elt)
		}
		// Array: [8]byte
		return fmt.Sprintf("[%s]%s", exprToString(t.Len), typeToString(t.Elt))

	case *ast.StarExpr:
		// Pointer: *Node (not supported for binary layout)
		return "*" + typeToString(t.X)

	default:
		return "unknown"
	}
}

func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Value
	case *ast.Ident:
		return e.Name
	default:
		return "?"
	}
}

// calculateSize determines the minimum buffer size needed based on field offsets
// Returns 0 if size cannot be determined (e.g., only dynamic fields)
func calculateSize(fields []Field) int {
	maxEnd := 0

	for _, field := range fields {
		// Only consider fixed fields for size calculation
		if field.Layout.Direction != Fixed {
			continue
		}

		// Get field size from type
		fieldSize := getFixedTypeSize(field.GoType)
		if fieldSize <= 0 {
			// Can't determine size for this type (struct or unknown)
			// For struct types, we'd need the registry, so skip for now
			continue
		}

		endOffset := field.Layout.Offset + fieldSize
		if endOffset > maxEnd {
			maxEnd = endOffset
		}
	}

	return maxEnd
}

// getFixedTypeSize returns the size in bytes for basic fixed-size types
// Returns -1 for variable-size types (slices, unknown types)
func getFixedTypeSize(goType string) int {
	switch goType {
	case "uint8", "int8", "byte", "bool":
		return 1
	case "uint16", "int16":
		return 2
	case "uint32", "int32", "float32":
		return 4
	case "uint64", "int64", "float64":
		return 8
	default:
		// Check for fixed arrays like [16]byte
		if strings.HasPrefix(goType, "[") && strings.Contains(goType, "]") {
			// Parse [N]Type
			parts := strings.Split(goType[1:], "]")
			if len(parts) == 2 {
				// parts[0] is the count, parts[1] is the element type
				var count int
				fmt.Sscanf(parts[0], "%d", &count)
				elemSize := getFixedTypeSize(parts[1])
				if elemSize > 0 {
					return count * elemSize
				}
			}
		}
		// For structs or unknown types, return -1
		return -1
	}
}