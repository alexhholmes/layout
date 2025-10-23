package example

// @layout size=4096 mode=zerocopy
type PageZeroCopy struct {
	buf    [4096]byte
	Header uint16 `layout:"@0"`
	Body   []byte `layout:"start-end"`
	Footer uint64 `layout:"@4088"`
}
