# Layout

Binary layout code generator for Go. Generates type-safe marshal/unmarshal code for fixed-size buffers with bidirectional packing.

Built for B-tree pages, database records, network protocols - anywhere you need deterministic memory layout without reflection overhead.

## Features

- **Bidirectional packing**: Fixed fields, forward-growing regions (`start-end`), backward-growing regions (`end-start`)
- **Zero-allocation unmarshaling**: Buffer reuse via capacity checks (5.5x faster)
- **Compile-time layout validation**: Collision detection, boundary calculation, count field validation
- **Type-safe generated code**: No reflection, no unsafe, pure `encoding/binary`

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

## Supported Types

### Fixed-size fields
- `uint8`, `uint16`, `uint32`, `uint64`
- `int8`, `int16`, `int32`, `int64`
- `byte`, `bool`
- `[N]byte` - byte arrays

### Dynamic fields
- `[]byte` - byte slices (with or without count)

Future: `[]StructType` for fixed-size struct slices.

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

**Performance**: 5.5x faster, zero allocations (see `BUFFER_REUSE.md`)

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

Count field only required when dynamic region has **no fixed boundary**:

```go
// NO count needed - Footer provides boundary
Body []byte `layout:"start-end"`  // [2, 4088)
Footer uint64 `layout:"@4088"`

// Count REQUIRED - no fixed boundary after Values
Values []byte `layout:"start-end,count=NumValues"`  // Length unknown
Keys   []byte `layout:"end-start"`  // Also dynamic
```

See `COUNT_SEMANTICS.md` for details.

## Error Detection

Compile-time checks:
- **Overlapping fixed fields**: `collision: Field1 [0, 8) overlaps Field2 [4, 12)`
- **Missing count fields**: `field 'Body' requires count= (no fixed boundary)`
- **Invalid count types**: `count field 'Len' must be uint8/16/32/64, got: string`
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

## Architecture

```
Input (page.go)
    ↓
parser.ParseFile()      → TypeLayout with annotations
    ↓
analyzer.Analyze()      → AnalyzedLayout with regions
    ↓
codegen.Generate()      → Generated Go source
    ↓
Output (page_layout.go)
```

See implementation docs:
- `parser/` - AST parsing, tag extraction
- `analyzer/` - Layout analysis, boundary calculation
- `codegen/` - Code generation

## Testing

```bash
go test ./...
```

71 tests covering:
- Tag parsing (22 tests)
- Annotation parsing (18 tests)
- Type size calculation
- Layout analysis (9 tests)
- Code generation (14 unit tests)
- Integration tests (3 end-to-end)

## Performance

**Unmarshal benchmark** (4KB page):
```
Without reuse:  2500 ns/op   4096 B/op   1 allocs/op
With reuse:      450 ns/op      0 B/op   0 allocs/op
```

5.5x faster, zero allocations.

## Limitations

Current:
- Only `[]byte` for dynamic fields (no `[]StructType` yet)
- Single buffer per type (no nested layouts)
- No padding/alignment control

Planned:
- `[]StructType` with inline marshaling
- Nested struct support
- Explicit alignment directives

## Design

See planning docs:
- `Design.md` - Original spec
- `ANALYZER_PLAN.md` - Layout analysis algorithm
- `CODEGEN_PLAN.md` - Code generation strategy
- `BUFFER_REUSE.md` - Zero-allocation optimization
- `COUNT_SEMANTICS.md` - Count field rules

## License

MIT