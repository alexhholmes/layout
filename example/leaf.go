package example

// @layout
type LeafElement struct {
	Key    uint32 `layout:"@0"`
	Offset uint32 `layout:"@4"`
}

// @layout
type LeafHeader struct {
	NumKeys  uint16 `layout:"@0"`
	Flags    uint16 `layout:"@2"`
	NextPage uint32 `layout:"@4"`
	PrevPage uint32 `layout:"@8"`
	Reserved uint32 `layout:"@12"`
}

// @layout size=4096
type LeafNode struct {
	Header   LeafHeader    `layout:"@0"`
	Elements []LeafElement `layout:"start-end,count=Header.NumKeys"`
	Footer   uint64        `layout:"@4088"`
}
