# Layout

Binary layout code generator for Go. Generates type-safe marshal/unmarshal code for fixed-size buffers with bidirectional packing.

Built for B-tree pages, database records, network protocols - anywhere you need deterministic memory layout without reflection overhead.

## Features

- **Bidirectional packing**: Fixed fields, forward-growing regions (`start-end`), backward-growing regions (`end-start`)
- **Zero-allocation unmarshaling**: Buffer reuse via capacity checks (5.5x faster)
- **True zero-copy mode**: Direct memory access with `unsafe.Pointer`, no allocations
- **Aligned buffers**: Generate aligned buffers for O_DIRECT I/O (512/4096-byte alignment)
- **Custom allocators**: Integrate with buffer pools via `allocator=` annotation
- **Compile-time layout validation**: Collision detection, boundary calculation, count field validation, struct field requirements
- **Type-safe generated code**: `encoding/binary` or `unsafe` depending on mode

## Quick Start

```go
// page.go
package btree

// @layout size=4096
type Page struct {
    Header uint16 `layout:"@0"`
    Body   []byte `layout:"start-end"`
    Footer uint64 `layout:"@4088"`
}
```

Generate code:
```bash
$ layout generate page.go
Generated: page_layout.go
  - Page.MarshalLayout() ([]byte, error)
  - Page.UnmarshalLayout([]byte) error
```

Use generated methods:
```go
page := &Page{
    Header: 42,
    Body:   []byte{1, 2, 3},
    Footer: 0xdeadbeef,
}

buf, _ := page.MarshalLayout()  // []byte of length 4096
page2 := &Page{}
page2.UnmarshalLayout(buf)      // Zero allocations if pre-allocated
```

## Tag Syntax

### Fixed Offset: `@N`
Place field at byte offset N.

```go
Header uint64 `layout:"@0"`     // [0, 8)
Footer uint64 `layout:"@4088"`  // [4088, 4096)
```

### Forward Growth: `start-end`
Grow from previous field/offset towards end of buffer.

```go
// @layout size=4096
type Page struct {
    Header uint16 `layout:"@0"`        // [0, 2)
    Body   []byte `layout:"start-end"` // [2, 4088) - fills to Footer
    Footer uint64 `layout:"@4088"`     // [4088, 4096)
}
```

### Backward Growth: `end-start`
Grow from end of buffer backwards.

```go
// @layout size=4096
type Page struct {
    Header uint16 `layout:"@0"`        // [0, 2)
    Keys   []byte `layout:"end-start"` // [4096, 2) - grows backward
}
```

### Explicit Start: `@N,direction`
Start dynamic region at specific offset.

```go
Values []byte `layout:"@8,start-end"`  // Start at offset 8, grow forward
Keys   []byte `layout:"@4096,end-start"` // Start at 4096, grow backward
```

### Count Fields: `count=FieldName`
Explicit slice length (required when boundary is ambiguous).

```go
type Page struct {
    NumKeys uint16 `layout:"@0"`
    Keys    []byte `layout:"start-end,count=NumKeys"`
}
```

Without count field, length is implicit from boundaries:
```go
// Body length = 4088 - 2 = 4086 bytes
Body []byte `layout:"start-end"`  // Fills from Header to Footer
```

## Type Annotation

Required at type level to specify buffer size:

```go
// @layout size=4096
type Page struct { ... }

// @layout size=8192 endian=big
type Record struct { ... }
```

Parameters:
- `size=N`: Buffer size in bytes (required)
- `endian=little|big`: Byte order (default: little)
- `mode=copy|zerocopy`: Marshal/unmarshal mode (default: copy)
- `align=N`: Buffer alignment in bytes (power of 2, requires mode=zerocopy)
- `allocator=FuncName`: Custom allocator function (requires mode=zerocopy with align)

## Zero-Copy Mode

True zero-copy I/O: no allocations, slice directly into embedded buffer.

### Basic Zero-Copy

```go
// @layout size=4096 mode=zerocopy
type Page struct {
    buf    [4096]byte  // Required: fixed-size buffer
    Header uint16      `layout:"@0"`
    Body   []byte      `layout:"start-end"`
    Footer uint64      `layout:"@4088"`
}
```

**Required field**: `buf [size]byte` matching annotation size

**Generated methods**:
```go
func (p *Page) MarshalLayout() ([]byte, error)   // Writes to p.buf using unsafe
func (p *Page) UnmarshalLayout() error            // Reads from p.buf, no params
func (p *Page) LoadFrom(r io.Reader) error        // Helper: read then unmarshal
func (p *Page) WriteTo(w io.Writer) error         // Helper: marshal then write
```

**Usage**:
```go
page := &Page{}

// Direct load: complete control
n, err := io.ReadFull(disk, page.buf[:])
page.UnmarshalLayout()

// Or use helper
page.LoadFrom(disk)
```

**Performance**: No allocations, direct memory access via `unsafe.Pointer`.

### Zero-Copy with Alignment

For O_DIRECT I/O requiring aligned buffers:

```go
// @layout size=4096 mode=zerocopy align=512
type Page struct {
    backing []byte  // Required: over-allocated buffer
    buf     []byte  // Required: aligned slice
    Header  uint16  `layout:"@0"`
    Body    []byte  `layout:"start-end"`
    Footer  uint64  `layout:"@4088"`
}
```

**Required fields**:
- `backing []byte` - Over-allocated buffer for alignment
- `buf []byte` - Slice into aligned region

**Generated constructor**:
```go
func New() *Page {
    p := &Page{}
    // Allocate size + (align-1) to guarantee alignment
    p.backing = make([]byte, 4607)  // 4096 + 511

    // Find 512-byte aligned offset
    addr := uintptr(unsafe.Pointer(&p.backing[0]))
    offset := int(((addr + 511) &^ 511) - addr)

    // Slice aligned region
    p.buf = p.backing[offset : offset+4096]
    return p
}
```

**Usage**:
```go
page := New()  // Allocates aligned buffer

// Open file with O_DIRECT
file, _ := os.OpenFile("data.db", os.O_RDWR|syscall.O_DIRECT, 0644)

// Direct I/O - no kernel buffering
io.ReadFull(file, page.buf[:])
page.UnmarshalLayout()

// Modify and write back
page.Header = 42
page.MarshalLayout()
file.Write(page.buf[:])
```

### Custom Allocator

Use buffer pools with custom allocators:

```go
var pagePool = sync.Pool{
    New: func() interface{} {
        // Allocate 4096 + 511 for 512-byte alignment
        return make([]byte, 4607)
    },
}

func AllocateAlignedPage() []byte {
    return pagePool.Get().([]byte)
}

// @layout size=4096 mode=zerocopy align=512 allocator=AllocateAlignedPage
type Page struct {
    backing []byte
    buf     []byte
    Header  uint16 `layout:"@0"`
    Body    []byte `layout:"start-end"`
    Footer  uint64 `layout:"@4088"`
}
```

**Generated code includes validation**:
```go
func New() *Page {
    p := &Page{}
    // IMPORTANT: AllocateAlignedPage() must return a buffer of at least 4607 bytes
    // (4096 bytes for data + 511 bytes for 512-byte alignment)
    p.backing = AllocateAlignedPage()

    // Validate buffer size to prevent out-of-bounds access
    if len(p.backing) < 4607 {
        panic(fmt.Sprintf("AllocateAlignedPage returned buffer of %d bytes, need at least 4607", len(p.backing)))
    }

    // Find aligned offset...
}
```

**Usage**:
```go
page := New()  // Gets buffer from pool

// Use page...
io.ReadFull(disk, page.buf[:])
page.UnmarshalLayout()

// Return to pool when done
pagePool.Put(page.backing)
```

### Field Requirements by Mode

| Mode | Alignment | Required Fields |
|------|-----------|-----------------|
| `copy` | N/A | None (generated code allocates) |
| `zerocopy` | None | `buf [size]byte` |
| `zerocopy` | Yes | `backing []byte` + `buf []byte` |

**Validation**: Parser checks struct has required fields, prints warning if missing.

## Supported Types

### Fixed-size fields
- `uint8`, `uint16`, `uint32`, `uint64`
- `int8`, `int16`, `int32`, `int64`
- `byte`, `bool`
- `[N]byte` - byte arrays
- Struct types with `@layout` annotation

### Dynamic fields
- `[]byte` - byte slices (with or without count)
- `[]StructType` - struct slices (requires count field)
- `[][]byte` - indirect slices via metadata (see Indirect Slices)

## Generated Code

Input:
```go
// @layout size=4096
type Page struct {
    Header uint16 `layout:"@0"`
    Body   []byte `layout:"start-end"`
    Footer uint64 `layout:"@4088"`
}
```

Output:
```go
func (p *Page) MarshalLayout() ([]byte, error) {
    buf := make([]byte, 4096)

    // Header: uint16 at [0, 2)
    binary.LittleEndian.PutUint16(buf[0:2], p.Header)

    // Body: []byte at [2, 4088)
    offset := 2
    for i := range p.Body {
        if offset >= 4088 {
            return nil, fmt.Errorf("Body collision at offset %d", offset)
        }
        buf[offset] = p.Body[i]
        offset++
    }

    // Footer: uint64 at [4088, 4096)
    binary.LittleEndian.PutUint64(buf[4088:4096], p.Footer)

    return buf, nil
}

func (p *Page) UnmarshalLayout(buf []byte) error {
    if len(buf) != 4096 {
        return fmt.Errorf("expected 4096 bytes, got %d", len(buf))
    }

    // Header: uint16 at [0, 2)
    p.Header = binary.LittleEndian.Uint16(buf[0:2])

    // Body: []byte at [2, 4088)
    bLen := 4088 - 2
    // Reuse buffer if capacity allows
    if cap(p.Body) >= bLen {
        p.Body = p.Body[:bLen]
    } else {
        p.Body = make([]byte, bLen)
    }
    copy(p.Body, buf[2:4088])

    // Footer: uint64 at [4088, 4096)
    p.Footer = binary.LittleEndian.Uint64(buf[4088:4096])

    return nil
}
```

## Buffer Reuse Pattern

Zero-allocation unmarshaling via capacity checks:

```go
// One-time allocation
page := &Page{
    Body: make([]byte, 0, 4096),  // Pre-allocate capacity
}

// Subsequent unmarshals reuse backing array
page.UnmarshalLayout(diskBuf1)  // No allocation
page.UnmarshalLayout(diskBuf2)  // No allocation
page.UnmarshalLayout(diskBuf3)  // No allocation
```

## Examples

### B-tree Page

```go
// @layout size=4096
type BTreePage struct {
    NumKeys  uint16   `layout:"@0"`
    Keys     []uint32 `layout:"end-start,count=NumKeys"`
    NumVals  uint16   `layout:"@2"`
    Values   []byte   `layout:"start-end,count=NumVals"`
}
```

Layout:
```
[0      2      4                              ?              4096]
[NumKeys|NumVals|Values--->              <---Keys              ]
```

### Network Protocol

```go
// @layout size=1024 endian=big
type Packet struct {
    Magic    uint32   `layout:"@0"`
    Length   uint16   `layout:"@4"`
    Payload  []byte   `layout:"start-end,count=Length"`
    Checksum uint32   `layout:"@1020"`
}
```

### Database Record

```go
// @layout size=8192
type Record struct {
    RecordID uint64 `layout:"@0"`
    Flags    uint16 `layout:"@8"`
    Data     []byte `layout:"start-end"`
    Footer   [16]byte `layout:"@8176"`  // Last 16 bytes
}
```

## Count Field Semantics

### Basic Count Fields

Count field only required when dynamic region has **no fixed boundary**:

```go
// NO count needed - Footer provides boundary
Body []byte `layout:"start-end"`  // [2, 4088)
Footer uint64 `layout:"@4088"`

// Count REQUIRED - no fixed boundary after Values
Values []byte `layout:"start-end,count=NumValues"`  // Length unknown
Keys   []byte `layout:"end-start"`  // Also dynamic
```

### Nested Count Fields

Reference struct fields using dot notation:

```go
// @layout size=4096
type LeafPage struct {
    Header   PageHeader     `layout:"@0"`
    Elements []LeafElement  `layout:"start-end,count=Header.NumKeys"`
    Data     []byte         `layout:"end-start"`
}
```

Supports one level of nesting: `Header.NumKeys` is valid, `A.B.C` is not.

### Count Field Validation

Count fields must be integer types and sized appropriately:

```go
// ✓ Valid
NumKeys uint16 `layout:"@0"`
Keys []byte `layout:"start-end,count=NumKeys"`

// ✗ Invalid - wrong type
NumKeys string `layout:"@0"`
Keys []byte `layout:"start-end,count=NumKeys"`  // Error: count field must be integer

// ✗ Invalid - overflow
NumKeys uint8 `layout:"@0"`  // Max 255
Keys [4000]byte `layout:"start-end,count=NumKeys"`  // Error: max 4000 elements exceeds uint8
```

**Compile-time checks**:
- Count field type: Must be `int8/16/32/64` or `uint8/16/32/64`
- Count capacity: Validates count type can hold maximum possible elements

### Struct Slices

`[]StructType` requires count field (always):

```go
// @layout
type LeafElement struct {
    Key    uint32 `layout:"@0"`
    Offset uint32 `layout:"@4"`
}

// @layout size=4096
type LeafPage struct {
    Header   PageHeader     `layout:"@0"`
    Elements []LeafElement  `layout:"@24,start-end,count=Header.NumKeys"`
}
```

Generated code calls `MarshalLayout`/`UnmarshalLayout` on each element.

See `COUNT_SEMANTICS.md` for details.

## Indirect Slices

`[][]byte` fields with metadata indirection - slices backed by a single data region with offsets stored in a separate metadata array.

**Use case**: B-tree leaf pages where keys/values are variable-length and stored in a packed data region.

### Syntax

```go
Keys [][]byte `layout:"from=Elements,offset=KeyOffset,size=KeySize,region=Data"`
```

**Required parameters**:
- `from=FieldName` - Source slice containing metadata (must be `[]StructType`)
- `offset=FieldName` - Field in source elements holding offset (must be integer type)
- `size=FieldName` - Field in source elements holding size (must be integer type)
- `region=FieldName` - Data region field (must be `[]byte`)

### Example: B-tree Leaf Page

```go
// @layout
type LeafElement struct {
    KeyOffset   uint32 `layout:"@0"`
    KeySize     uint32 `layout:"@4"`
    ValueOffset uint32 `layout:"@8"`
    ValueSize   uint32 `layout:"@12"`
}

// @layout size=4096
type LeafPage struct {
    Header   PageHeader     `layout:"@0"`
    Elements []LeafElement  `layout:"@24,start-end,count=Header.NumKeys"`
    Data     []byte         `layout:"end-start"`
    Keys     [][]byte       `layout:"from=Elements,offset=KeyOffset,size=KeySize,region=Data"`
    Values   [][]byte       `layout:"from=Elements,offset=ValueOffset,size=ValueSize,region=Data"`
}
```

### Generated Code

**Unmarshal**: Loop through metadata, slice into buffer

```go
// Keys: [][]byte from=Elements offset=KeyOffset size=KeySize region=Data
for i := range p.Elements {
    offset := int(p.Elements[i].KeyOffset)
    size := int(p.Elements[i].KeySize)
    p.Keys[i] = buf[offset:offset+size]
}
```

**Marshal**: Pack backward, update metadata

```go
// Keys: [][]byte packed backward into Data, updating Elements metadata
offset = 4096
for i := len(p.Keys) - 1; i >= 0; i-- {
    size := len(p.Keys[i])
    offset -= size
    copy(buf[offset:offset+size], p.Keys[i])
    p.Elements[i].KeyOffset = uint32(offset)
    p.Elements[i].KeySize = uint32(size)
}
```

**Offsets**: Absolute offsets into buffer (not relative to Data region).

**Memory**: Zero-copy - `Keys[i]` slices directly into `buf`, no allocation.

## Error Detection

Compile-time checks:
- **Overlapping fixed fields**: `collision: Field1 [0, 8) overlaps Field2 [4, 12)`
- **Missing count fields**: `field 'Body' requires count= (no fixed boundary)`
- **Invalid count types**: `count field 'Len' must be int/uint 8/16/32/64, got: string`
- **Count capacity overflow**: `count field 'Count' (type uint8, max 255) cannot hold max 512 elements`
- **Nested count field errors**: `count field 'A.B.C' has invalid nested reference (only one level supported)`
- **Indirect slice validation**: `field 'Keys': source field 'Elements' must be a struct slice, not []byte`
- **Out of bounds**: `field [4088, 4100) exceeds buffer size 4096`

Runtime checks:
- **Collision detection**: `return nil, fmt.Errorf("Body collision at offset %d", offset)`
- **Count mismatches**: `return nil, fmt.Errorf("Body length mismatch: have %d, want %d")`
- **Buffer size validation**: `return fmt.Errorf("expected 4096 bytes, got %d", len(buf))`

## Installation

```bash
go install github.com/alexhholmes/layout/cmd/layout@latest
```

After install, `layout` command available globally.

## Usage

### As go generate directive
```go
//go:generate layout generate page.go
```

### Command line
```bash
layout generate page.go           # Generate page_layout.go
layout generate btree/*.go        # Generate for package
```

## License

MIT