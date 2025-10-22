package example

// @layout size=4096
type Page struct {
	Header uint16 `layout:"@0"`
	Body   []byte `layout:"start-end"`
	Footer uint64 `layout:"@4088"`
}