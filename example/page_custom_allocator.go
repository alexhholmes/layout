package example

// @layout size=4096 mode=zerocopy align=512 allocator=AllocateAlignedPage
type PageCustomAllocator struct {
	buf    []byte // Aligned region (backing is handled by allocator)
	Header uint16 `layout:"@0"`
	Body   []byte `layout:"start-end"`
	Footer uint64 `layout:"@4088"`
}

// AllocateAlignedPage is a custom allocator function
// It must return a buffer of at least 4096 + 511 = 4607 bytes
func AllocateAlignedPage() []byte {
	// Example: allocate from a buffer pool
	return make([]byte, 4096+512) // Allocate enough for alignment
}