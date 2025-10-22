package codegen

import (
	"strings"
	"testing"

	analyzer2 "layout/internal/analyzer"
	parser2 "layout/internal/parser"
)

func TestGenerateFixedFields(t *testing.T) {
	// type Page struct {
	//     Header uint64 `layout:"@0"`
	//     Footer uint64 `layout:"@4088"`
	// }
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Header", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Footer", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 4088, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	code, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Check that code contains expected elements
	if !strings.Contains(code, "func (p *Page) MarshalLayout() ([]byte, error)") {
		t.Error("Missing MarshalLayout function")
	}

	if !strings.Contains(code, "func (p *Page) UnmarshalLayout(buf []byte) error") {
		t.Error("Missing UnmarshalLayout function")
	}

	if !strings.Contains(code, "binary.LittleEndian.PutUint64") {
		t.Error("Missing binary.LittleEndian.PutUint64 for marshal")
	}

	if !strings.Contains(code, "binary.LittleEndian.Uint64") {
		t.Error("Missing binary.LittleEndian.Uint64 for unmarshal")
	}

	// Check buffer size
	if !strings.Contains(code, "make([]byte, 4096)") {
		t.Error("Missing buffer allocation with correct size")
	}

	// Check size validation
	if !strings.Contains(code, "if len(buf) != 4096") {
		t.Error("Missing buffer size validation")
	}
}

func TestGenerateMarshalFixedUint16(t *testing.T) {
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Header", GoType: "uint16", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	marshal := gen.GenerateMarshal()

	// Check for correct binary operation
	if !strings.Contains(marshal, "binary.LittleEndian.PutUint16(buf[0:2], p.Header)") {
		t.Errorf("Expected PutUint16 for uint16, got:\n%s", marshal)
	}
}

func TestGenerateUnmarshalFixedUint32(t *testing.T) {
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Magic", GoType: "uint32", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	unmarshal := gen.GenerateUnmarshal()

	// Check for correct binary operation
	if !strings.Contains(unmarshal, "p.Magic = binary.LittleEndian.Uint32(buf[0:4])") {
		t.Errorf("Expected Uint32 for uint32, got:\n%s", unmarshal)
	}
}

func TestGenerateByteField(t *testing.T) {
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 16},
		Fields: []parser2.Field{
			{Name: "Flag", GoType: "byte", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	marshal := gen.GenerateMarshal()
	unmarshal := gen.GenerateUnmarshal()

	// Marshal: direct assignment
	if !strings.Contains(marshal, "buf[0] = p.Flag") {
		t.Errorf("Expected direct assignment for byte marshal, got:\n%s", marshal)
	}

	// Unmarshal: direct read
	if !strings.Contains(unmarshal, "p.Flag = buf[0]") {
		t.Errorf("Expected direct read for byte unmarshal, got:\n%s", unmarshal)
	}
}

func TestGenerateByteArray(t *testing.T) {
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 32},
		Fields: []parser2.Field{
			{Name: "UUID", GoType: "[16]byte", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	marshal := gen.GenerateMarshal()
	unmarshal := gen.GenerateUnmarshal()

	// Marshal: copy from array
	if !strings.Contains(marshal, "copy(buf[0:16], p.UUID[:])") {
		t.Errorf("Expected copy for array marshal, got:\n%s", marshal)
	}

	// Unmarshal: copy to array
	if !strings.Contains(unmarshal, "copy(p.UUID[:], buf[0:16])") {
		t.Errorf("Expected copy for array unmarshal, got:\n%s", unmarshal)
	}
}

func TestGenerateBigEndian(t *testing.T) {
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 16, Endian: "big"},
		Fields: []parser2.Field{
			{Name: "Value", GoType: "uint32", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "big")
	marshal := gen.GenerateMarshal()

	// Check for big endian
	if !strings.Contains(marshal, "binary.BigEndian.PutUint32") {
		t.Errorf("Expected BigEndian for big endian, got:\n%s", marshal)
	}
}

func TestBinaryHelpers(t *testing.T) {
	gen := &Generator{endian: "little"}

	tests := []struct {
		goType  string
		putFunc string
		getFunc string
	}{
		{"uint16", "PutUint16", "Uint16"},
		{"uint32", "PutUint32", "Uint32"},
		{"uint64", "PutUint64", "Uint64"},
		{"int16", "PutUint16", "Uint16"},
		{"int32", "PutUint32", "Uint32"},
		{"int64", "PutUint64", "Uint64"},
	}

	for _, tt := range tests {
		putFunc := gen.binaryPutFunc(tt.goType)
		if putFunc != tt.putFunc {
			t.Errorf("binaryPutFunc(%s) = %s, want %s", tt.goType, putFunc, tt.putFunc)
		}

		getFunc := gen.binaryGetFunc(tt.goType)
		if getFunc != tt.getFunc {
			t.Errorf("binaryGetFunc(%s) = %s, want %s", tt.goType, getFunc, tt.getFunc)
		}
	}
}

func TestGenerateDynamicStartEnd(t *testing.T) {
	// type Page struct {
	//     Header uint16 `layout:"@0"`
	//     Body   []byte `layout:"start-end"`
	//     Footer uint64 `layout:"@4088"`
	// }
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Header", GoType: "uint16", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Body", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.StartEnd, StartAt: -1,
			}},
			{Name: "Footer", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 4088, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	marshal := gen.GenerateMarshal()
	unmarshal := gen.GenerateUnmarshal()

	// Marshal checks
	if !strings.Contains(marshal, "offset := 2") {
		t.Error("Expected offset initialization for dynamic field")
	}
	if !strings.Contains(marshal, "for i := range p.Body") {
		t.Error("Expected loop over Body")
	}
	if !strings.Contains(marshal, "if offset >= 4088") {
		t.Error("Expected collision check")
	}
	if !strings.Contains(marshal, "buf[offset] = p.Body[i]") {
		t.Error("Expected byte-by-byte marshal")
	}

	// Unmarshal checks
	if !strings.Contains(unmarshal, "if cap(p.Body) >=") {
		t.Error("Expected buffer reuse check")
	}
	if !strings.Contains(unmarshal, "p.Body = p.Body[") {
		t.Error("Expected buffer reuse slice operation")
	}
	if !strings.Contains(unmarshal, "copy(p.Body, buf[2:4088])") {
		t.Error("Expected copy from buffer")
	}
}

func TestGenerateDynamicWithCount(t *testing.T) {
	// type Page struct {
	//     BodyLen uint16 `layout:"@0"`
	//     Body    []byte `layout:"start-end,count=BodyLen"`
	// }
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "BodyLen", GoType: "uint16", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Body", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.StartEnd, StartAt: -1,
				CountField: "BodyLen",
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	marshal := gen.GenerateMarshal()
	unmarshal := gen.GenerateUnmarshal()

	// Marshal checks - should validate count
	if !strings.Contains(marshal, "if len(p.Body) != int(p.BodyLen)") {
		t.Error("Expected count validation in marshal")
	}
	if !strings.Contains(marshal, "Body length mismatch") {
		t.Error("Expected length mismatch error")
	}

	// Unmarshal checks
	if !strings.Contains(unmarshal, "if cap(p.Body) >= int(p.BodyLen)") {
		t.Error("Expected buffer reuse with count field")
	}
	if !strings.Contains(unmarshal, "p.Body = p.Body[:p.BodyLen]") {
		t.Error("Expected buffer reuse with count")
	}
	if !strings.Contains(unmarshal, "copy(p.Body, buf[2:2+p.BodyLen])") {
		t.Error("Expected copy with count-based range")
	}
}

func TestGenerateDynamicEndStart(t *testing.T) {
	// type Page struct {
	//     Header uint16 `layout:"@0"`
	//     Keys   []byte `layout:"end-start"`
	// }
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Header", GoType: "uint16", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Keys", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.EndStart, StartAt: -1,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	marshal := gen.GenerateMarshal()
	unmarshal := gen.GenerateUnmarshal()

	// Marshal checks - backward iteration
	if !strings.Contains(marshal, "for i := len(p.Keys) - 1; i >= 0; i--") {
		t.Error("Expected backward iteration for end-start")
	}
	if !strings.Contains(marshal, "offset--") {
		t.Error("Expected offset decrement")
	}
	if !strings.Contains(marshal, "if offset < 2") {
		t.Error("Expected collision check with lower bound")
	}

	// Unmarshal checks - implicit length
	if !strings.Contains(unmarshal, "kLen := 4096 - 2") {
		t.Error("Expected length calculation for end-start")
	}
	if !strings.Contains(unmarshal, "copy(p.Keys, buf[2:4096])") {
		t.Error("Expected copy from boundary to start")
	}
}

func TestGenerateComplete(t *testing.T) {
	// Test complete generation with mixed fields
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Header", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Body", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.StartEnd, StartAt: -1,
			}},
			{Name: "Footer", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 4088, Direction: parser2.Fixed,
			}},
		},
	}

	reg := analyzer2.NewTypeRegistry()
	analyzed, err := analyzer2.Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	gen := NewGenerator(analyzed, reg, "little")
	code, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	// Verify file structure
	if !strings.Contains(code, "// Code generated by layoutgen. DO NOT EDIT.") {
		t.Error("Missing generation comment")
	}
	if !strings.Contains(code, "package main") {
		t.Error("Missing package declaration")
	}
	if !strings.Contains(code, "import (") {
		t.Error("Missing imports")
	}
	if !strings.Contains(code, "\"encoding/binary\"") {
		t.Error("Missing binary import")
	}
	if !strings.Contains(code, "\"fmt\"") {
		t.Error("Missing fmt import")
	}

	// Verify both methods generated
	if !strings.Contains(code, "func (p *Page) MarshalLayout()") {
		t.Error("Missing MarshalLayout method")
	}
	if !strings.Contains(code, "func (p *Page) UnmarshalLayout(") {
		t.Error("Missing UnmarshalLayout method")
	}
}
