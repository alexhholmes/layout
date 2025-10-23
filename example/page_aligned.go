package example

// @layout size=4096 mode=zerocopy align=512
type PageAligned struct {
	backing []byte
	buf     []byte
	Header  uint16 `layout:"@0"`
	Body    []byte `layout:"start-end"`
	Footer  uint64 `layout:"@4088"`
}
