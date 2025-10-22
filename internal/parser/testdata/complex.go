package testdata

// @layout size=4096
type LeafNode struct {
	Field1 uint64 `layout:"@0"`
	Field2 []byte `layout:"start-end"`
	Field3 []byte `layout:"end-start"`
	Field4 uint64 `layout:"@2000"`
	Field5 []byte `layout:"@1999,end-start"`
}