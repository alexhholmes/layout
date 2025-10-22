package testdata

// @layout size=4096
type LeafPage struct {
	NumElements uint16        `layout:"@0"`
	Elements    []LeafElement `layout:"end-start"`
	Data        [][]byte      `layout:"start-end"`
}

type LeafElement struct {
	Key    uint32 `layout:"@0"`
	Offset uint16 `layout:"@4"`
	Length uint16 `layout:"@6"`
}

// @layout size=8192 endian=big
type BigPage struct {
	Header [16]byte `layout:"@0"`
	Body   []byte   `layout:"start-end"`
}

// No annotation - should be skipped
type IgnoredType struct {
	Field uint32 `layout:"@0"`
}