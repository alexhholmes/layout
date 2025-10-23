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
func ParseFile(filename string) ([]*TypeLayout, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	return extractTypes(file), nil
}

func extractTypes(file *ast.File) []*TypeLayout {
	var types []*TypeLayout

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec := spec.(*ast.TypeSpec)
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue // Not a struct
			}

			// Extract @layout annotation from comments directly above type
			anno := extractAnnotation(genDecl.Doc)
			if anno == nil {
				continue // No @layout, skip this type
			}

			// Validate struct has required fields for zerocopy mode
			if err := validateStructFields(structType, anno); err != nil {
				// TODO: collect errors instead of skipping
				fmt.Printf("Warning: %s: %v\n", typeSpec.Name.Name, err)
				continue
			}

			// Extract fields with layout tags
			fields := extractFields(structType)
			if len(fields) == 0 {
				continue // No layout tags, skip
			}

			types = append(types, &TypeLayout{
				Name:   typeSpec.Name.Name,
				Anno:   anno,
				Fields: fields,
			})
		}
	}

	return types
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

	// Zerocopy with alignment requires backing and buf fields
	if anno.Align > 0 {
		// Check for backing []byte
		backingType, hasBackingField := fieldMap["backing"]
		if !hasBackingField {
			return fmt.Errorf("zerocopy mode with align=%d requires field: backing []byte", anno.Align)
		}
		if backingType != "[]byte" {
			return fmt.Errorf("backing field must be []byte, got %s", backingType)
		}

		// Check for buf []byte
		bufType, hasBufField := fieldMap["buf"]
		if !hasBufField {
			return fmt.Errorf("zerocopy mode with align=%d requires field: buf []byte", anno.Align)
		}
		if bufType != "[]byte" {
			return fmt.Errorf("buf field must be []byte when using align, got %s", bufType)
		}
	} else {
		// Zerocopy without alignment requires buf [size]byte
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