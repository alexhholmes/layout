package example

import (
	"testing"
)

func TestLeafNodeMarshalUnmarshal(t *testing.T) {
	// Create a leaf node with 3 elements and nested header count
	node := &LeafNode{
		Header: LeafHeader{
			NumKeys:  3,
			Flags:    0x1234,
			NextPage: 42,
			PrevPage: 0,
			Reserved: 0,
		},
		Elements: []LeafElement{
			{Key: 100, Offset: 1000},
			{Key: 200, Offset: 2000},
			{Key: 300, Offset: 3000},
		},
		Footer: 0xDEADBEEFCAFEBABE,
	}

	// Marshal
	buf, err := node.MarshalLayout()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if len(buf) != 4096 {
		t.Fatalf("Expected 4096 bytes, got %d", len(buf))
	}

	// Unmarshal into new node
	node2 := &LeafNode{}
	if err := node2.UnmarshalLayout(buf); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify Header
	if node2.Header.NumKeys != 3 {
		t.Errorf("Header.NumKeys: expected 3, got %d", node2.Header.NumKeys)
	}
	if node2.Header.Flags != 0x1234 {
		t.Errorf("Header.Flags: expected 0x1234, got 0x%x", node2.Header.Flags)
	}
	if node2.Header.NextPage != 42 {
		t.Errorf("Header.NextPage: expected 42, got %d", node2.Header.NextPage)
	}

	// Verify Elements (nested count field works)
	if len(node2.Elements) != 3 {
		t.Fatalf("Elements length: expected 3, got %d", len(node2.Elements))
	}

	expected := []LeafElement{
		{Key: 100, Offset: 1000},
		{Key: 200, Offset: 2000},
		{Key: 300, Offset: 3000},
	}

	for i, elem := range expected {
		if node2.Elements[i].Key != elem.Key {
			t.Errorf("Elements[%d].Key: expected %d, got %d", i, elem.Key, node2.Elements[i].Key)
		}
		if node2.Elements[i].Offset != elem.Offset {
			t.Errorf("Elements[%d].Offset: expected %d, got %d", i, elem.Offset, node2.Elements[i].Offset)
		}
	}

	// Verify Footer
	if node2.Footer != 0xDEADBEEFCAFEBABE {
		t.Errorf("Footer: expected 0xDEADBEEFCAFEBABE, got 0x%x", node2.Footer)
	}
}