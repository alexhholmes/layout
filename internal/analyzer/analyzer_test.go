package analyzer

import (
	"testing"

	parser2 "layout/internal/parser"
)

func TestAnalyze_SimpleFixed(t *testing.T) {
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

	reg := NewTypeRegistry()
	analyzed, err := Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if !analyzed.IsValid() {
		t.Errorf("Layout should be valid, errors: %v", analyzed.Errors)
	}

	if len(analyzed.Regions) != 2 {
		t.Fatalf("Expected 2 regions, got %d", len(analyzed.Regions))
	}

	// Check Header region
	r0 := analyzed.Regions[0]
	if r0.Start != 0 || r0.Boundary != 8 {
		t.Errorf("Header region: got [%d, %d), want [0, 8)", r0.Start, r0.Boundary)
	}

	// Check Footer region
	r1 := analyzed.Regions[1]
	if r1.Start != 4088 || r1.Boundary != 4096 {
		t.Errorf("Footer region: got [%d, %d), want [4088, 4096)", r1.Start, r1.Boundary)
	}
}

func TestAnalyze_DynamicWithBoundary(t *testing.T) {
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

	reg := NewTypeRegistry()
	analyzed, err := Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if !analyzed.IsValid() {
		t.Errorf("Layout should be valid, errors: %v", analyzed.Errors)
	}

	if len(analyzed.Regions) != 3 {
		t.Fatalf("Expected 3 regions, got %d", len(analyzed.Regions))
	}

	// Body should fill [2, 4088)
	bodyRegion := analyzed.Regions[1]
	if bodyRegion.Start != 2 {
		t.Errorf("Body start: got %d, want 2", bodyRegion.Start)
	}
	if bodyRegion.Boundary != 4088 {
		t.Errorf("Body boundary: got %d, want 4088", bodyRegion.Boundary)
	}
}

func TestAnalyze_MissingCountField(t *testing.T) {
	// type Page struct {
	//     Header  uint16 `layout:"@0"`
	//     Body    []byte `layout:"start-end"` // No count, no boundary
	//     Tail    []byte `layout:"end-start"` // Dynamic field after
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
				CountField: "", // Missing count
			}},
			{Name: "Tail", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.EndStart, StartAt: -1,
			}},
		},
	}

	reg := NewTypeRegistry()
	analyzed, err := Analyze(layout, reg)

	// Should have error about missing count
	if err == nil {
		t.Error("Expected error about missing count field")
	}

	if analyzed.IsValid() {
		t.Error("Layout should be invalid")
	}
}

func TestAnalyze_FixedOverlap(t *testing.T) {
	// type Page struct {
	//     Field1 uint64 `layout:"@0"`   // [0, 8)
	//     Field2 uint64 `layout:"@4"`   // [4, 12) - overlaps!
	// }
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "Field1", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Field2", GoType: "uint64", Layout: &parser2.FieldLayout{
				Offset: 4, Direction: parser2.Fixed,
			}},
		},
	}

	reg := NewTypeRegistry()
	analyzed, err := Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if analyzed.IsValid() {
		t.Error("Layout should be invalid due to overlap")
	}

	// Check for collision error
	foundCollision := false
	for _, err := range analyzed.Errors {
		if len(err) > 0 && err[:9] == "collision" {
			foundCollision = true
			break
		}
	}
	if !foundCollision {
		t.Errorf("Expected collision error, got: %v", analyzed.Errors)
	}
}

func TestAnalyze_WithCountField(t *testing.T) {
	// type Page struct {
	//     BodyLen uint16 `layout:"@0"`
	//     Body    []byte `layout:"start-end,count=BodyLen"`
	//     Tail    []byte `layout:"end-start"`
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
			{Name: "Tail", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.EndStart, StartAt: -1,
			}},
		},
	}

	reg := NewTypeRegistry()
	analyzed, err := Analyze(layout, reg)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if !analyzed.IsValid() {
		t.Errorf("Layout should be valid, errors: %v", analyzed.Errors)
	}
}

func TestAnalyze_InvalidCountFieldType(t *testing.T) {
	// type Page struct {
	//     BodyLen string `layout:"@0"`  // Wrong type!
	//     Body    []byte `layout:"start-end,count=BodyLen"`
	// }
	layout := &parser2.TypeLayout{
		Name: "Page",
		Anno: &parser2.TypeAnnotation{Size: 4096},
		Fields: []parser2.Field{
			{Name: "BodyLen", GoType: "string", Layout: &parser2.FieldLayout{
				Offset: 0, Direction: parser2.Fixed,
			}},
			{Name: "Body", GoType: "[]byte", Layout: &parser2.FieldLayout{
				Offset: -1, Direction: parser2.StartEnd, StartAt: -1,
				CountField: "BodyLen",
			}},
		},
	}

	reg := NewTypeRegistry()
	_, err := Analyze(layout, reg)

	if err == nil {
		t.Error("Expected error about invalid count field type")
	}
}
