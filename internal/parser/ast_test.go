package parser

import (
	"testing"
)

func TestParseFile(t *testing.T) {
	types, err := ParseFile("testdata/simple.go")
	if err != nil {
		t.Fatalf("ParseFile() error: %v", err)
	}

	// Should find 2 types: LeafPage and BigPage
	// LeafElement has no @layout annotation
	// IgnoredType has no @layout annotation
	if len(types) != 2 {
		t.Fatalf("ParseFile() found %d types, want 2", len(types))
	}

	// Test LeafPage
	leafPage := types[0]
	if leafPage.Name != "LeafPage" {
		t.Errorf("types[0].Name = %q, want %q", leafPage.Name, "LeafPage")
	}
	if leafPage.Anno.Size != 4096 {
		t.Errorf("LeafPage.Anno.Size = %d, want 4096", leafPage.Anno.Size)
	}
	if leafPage.Anno.Endian != "little" {
		t.Errorf("LeafPage.Anno.Endian = %q, want %q", leafPage.Anno.Endian, "little")
	}
	if len(leafPage.Fields) != 3 {
		t.Fatalf("LeafPage has %d fields, want 3", len(leafPage.Fields))
	}

	// Check fields
	f0 := leafPage.Fields[0]
	if f0.Name != "NumElements" {
		t.Errorf("fields[0].Name = %q, want %q", f0.Name, "NumElements")
	}
	if f0.GoType != "uint16" {
		t.Errorf("fields[0].GoType = %q, want %q", f0.GoType, "uint16")
	}
	if f0.Layout.Direction != Fixed || f0.Layout.Offset != 0 {
		t.Errorf("fields[0].Layout = {offset=%d, dir=%v}, want {offset=0, dir=Fixed}",
			f0.Layout.Offset, f0.Layout.Direction)
	}

	f1 := leafPage.Fields[1]
	if f1.Name != "Elements" {
		t.Errorf("fields[1].Name = %q, want %q", f1.Name, "Elements")
	}
	if f1.GoType != "[]LeafElement" {
		t.Errorf("fields[1].GoType = %q, want %q", f1.GoType, "[]LeafElement")
	}
	if f1.Layout.Direction != EndStart {
		t.Errorf("fields[1].Layout.Direction = %v, want EndStart", f1.Layout.Direction)
	}

	f2 := leafPage.Fields[2]
	if f2.Name != "Data" {
		t.Errorf("fields[2].Name = %q, want %q", f2.Name, "Data")
	}
	if f2.GoType != "[][]byte" {
		t.Errorf("fields[2].GoType = %q, want %q", f2.GoType, "[][]byte")
	}
	if f2.Layout.Direction != StartEnd {
		t.Errorf("fields[2].Layout.Direction = %v, want StartEnd", f2.Layout.Direction)
	}

	// Test BigPage
	bigPage := types[1]
	if bigPage.Name != "BigPage" {
		t.Errorf("types[1].Name = %q, want %q", bigPage.Name, "BigPage")
	}
	if bigPage.Anno.Size != 8192 {
		t.Errorf("BigPage.Anno.Size = %d, want 8192", bigPage.Anno.Size)
	}
	if bigPage.Anno.Endian != "big" {
		t.Errorf("BigPage.Anno.Endian = %q, want %q", bigPage.Anno.Endian, "big")
	}
	if len(bigPage.Fields) != 2 {
		t.Fatalf("BigPage has %d fields, want 2", len(bigPage.Fields))
	}
}

func TestTypeToString(t *testing.T) {
	// Note: We can't easily test this without constructing AST nodes
	// The real test is in TestParseFile which uses actual parsed code
	// This is tested implicitly above
}