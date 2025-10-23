package codegen

import (
	"fmt"
	"strings"

	"github.com/alexhholmes/layout/internal/analyzer"
	"github.com/alexhholmes/layout/internal/parser"
)

// Generator generates marshal/unmarshal code for binary layouts
type Generator struct {
	analyzed   *analyzer.AnalyzedLayout
	layout     *parser.TypeLayout   // Original parsed layout (for indirect slices)
	allLayouts []*parser.TypeLayout // All parsed layouts (for type lookups)
	registry   *analyzer.TypeRegistry
	endian     string // "little" or "big"
	mode       string // "copy" or "zerocopy"
	align      int    // alignment requirement (0 = none)
	allocator  string // custom allocator function name (optional)
}

// typeEmitter holds marshal/unmarshal code generators for a type
type typeEmitter struct {
	marshal   func(ctx emitCtx) string
	unmarshal func(ctx emitCtx) string
}

// emitCtx carries context for code emission
type emitCtx struct {
	field      string
	start, end int
	needsCast  bool
	origType   string
}

// NewGenerator creates a new code generator
func NewGenerator(analyzed *analyzer.AnalyzedLayout, layout *parser.TypeLayout, allLayouts []*parser.TypeLayout, reg *analyzer.TypeRegistry, endian string, mode string, align int, allocator string) *Generator {
	if endian == "" {
		endian = "little"
	}
	if mode == "" {
		mode = "copy"
	}
	return &Generator{
		analyzed:   analyzed,
		layout:     layout,
		allLayouts: allLayouts,
		registry:   reg,
		endian:     endian,
		mode:       mode,
		align:      align,
		allocator:  allocator,
	}
}

// needsFmt returns true if zerocopy mode requires fmt package
func (g *Generator) needsFmt() bool {
	// Custom allocator needs fmt.Sprintf for panic
	if g.allocator != "" {
		return true
	}

	// Check regions for complex types that need error handling
	for _, region := range g.analyzed.Regions {
		resolvedType := g.registry.ResolveType(region.Field.GoType)

		// Struct fields need fmt.Errorf
		if region.Kind == analyzer.FixedRegion {
			// Check if it's a struct type (not primitive, not []byte array)
			if !strings.HasPrefix(resolvedType, "[") &&
				resolvedType != "uint8" && resolvedType != "int8" && resolvedType != "byte" &&
				resolvedType != "uint16" && resolvedType != "int16" &&
				resolvedType != "uint32" && resolvedType != "int32" &&
				resolvedType != "uint64" && resolvedType != "int64" {
				return true
			}
		}

		// Dynamic struct slices need fmt.Errorf
		if region.Kind == analyzer.DynamicRegion && region.ElementType != "byte" && region.ElementType != "" {
			return true
		}
	}

	return false
}

// Generate returns the generated code for this type (without package header/imports)
func (g *Generator) Generate() (string, error) {
	var out strings.Builder

	// Generate code based on mode
	if g.mode == "zerocopy" {
		// Zerocopy mode: generate accessor methods
		accessors := g.generateZeroCopyAccessors()
		out.WriteString(accessors)
	} else {
		// Copy mode: generate marshal/unmarshal methods
		marshal := g.GenerateMarshal()
		out.WriteString(marshal)
		out.WriteString("\n")

		unmarshal := g.GenerateUnmarshal()
		out.WriteString(unmarshal)
	}

	return out.String(), nil
}

// GenerateMarshal generates the MarshalLayout method
func (g *Generator) GenerateMarshal() string {
	if g.mode == "zerocopy" {
		return g.generateZeroCopyMarshal()
	}
	return g.generateCopyMarshal()
}

// generateCopyMarshal generates copy-mode marshal (existing behavior)
func (g *Generator) generateCopyMarshal() string {
	var code strings.Builder

	// Function signature
	code.WriteString(fmt.Sprintf("func (p *%s) MarshalLayout() ([]byte, error) {\n", g.analyzed.TypeName))
	code.WriteString(fmt.Sprintf("\tbuf := make([]byte, %d)\n", g.analyzed.BufferSize))

	// Declare offset only if we have dynamic regions or indirect slices
	hasDynamic := false
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.DynamicRegion {
			hasDynamic = true
			break
		}
	}
	if !hasDynamic && g.layout != nil {
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				hasDynamic = true
				break
			}
		}
	}
	if hasDynamic {
		code.WriteString("\tvar offset int\n")
	}
	code.WriteString("\n")

	// Generate code for each region
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.FixedRegion {
			code.WriteString(g.generateFixedOp(region, "marshal"))
		} else {
			code.WriteString(g.generateDynamicMarshal(region))
		}
	}

	// Generate indirect slice marshal ([][]byte with metadata indirection)
	var hasIndirect bool
	var metadataField string
	if g.layout != nil {
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				code.WriteString(g.generateIndirectMarshal(field))
				hasIndirect = true
				metadataField = field.Layout.From
			}
		}
	}

	// Re-marshal metadata Elements after indirect slices have updated offsets
	if hasIndirect && metadataField != "" {
		code.WriteString(fmt.Sprintf("\t// Re-marshal %s after updating offsets\n", metadataField))

		// Find the metadata region
		for _, region := range g.analyzed.Regions {
			if region.Field.Name == metadataField {
				code.WriteString(fmt.Sprintf("\toffset = %d\n", region.Start))
				code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", metadataField))
				code.WriteString(fmt.Sprintf("\t\telemBuf, err := p.%s[i].MarshalLayout()\n", metadataField))
				code.WriteString("\t\tif err != nil {\n")
				code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"remarshal %s[%%d]: %%w\", i, err)\n", metadataField))
				code.WriteString("\t\t}\n")
				code.WriteString(fmt.Sprintf("\t\tcopy(buf[offset:offset+%d], elemBuf)\n", region.ElementSize))
				code.WriteString(fmt.Sprintf("\t\toffset += %d\n", region.ElementSize))
				code.WriteString("\t}\n\n")
				break
			}
		}
	}

	code.WriteString("\treturn buf, nil\n")
	code.WriteString("}\n")

	return code.String()
}

// generateZeroCopyMarshal generates zero-copy marshal that writes to p.buf
func (g *Generator) generateZeroCopyMarshal() string {
	var code strings.Builder

	// Generate New function for zerocopy mode (always required for buffer management)
	code.WriteString(g.generateNewFunction())
	code.WriteString("\n")

	code.WriteString(fmt.Sprintf("func (p *%s) MarshalLayout() ([]byte, error) {\n", g.analyzed.TypeName))

	// Generate code for each region, writing to p.buf
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.FixedRegion {
			code.WriteString(g.generateFixedOp(region, "marshal"))
		} else {
			code.WriteString(g.generateZeroCopyDynamicMarshal(region))
		}
	}

	code.WriteString("\treturn p.buf[:], nil\n")
	code.WriteString("}\n")

	return code.String()
}

// GenerateUnmarshal generates the UnmarshalLayout method
func (g *Generator) GenerateUnmarshal() string {
	if g.mode == "zerocopy" {
		return g.generateZeroCopyUnmarshal()
	}
	return g.generateCopyUnmarshal()
}

// generateCopyUnmarshal generates copy-mode unmarshal (existing behavior)
func (g *Generator) generateCopyUnmarshal() string {
	var code strings.Builder

	// Function signature
	code.WriteString(fmt.Sprintf("func (p *%s) UnmarshalLayout(buf []byte) error {\n", g.analyzed.TypeName))

	// Buffer size check
	code.WriteString(fmt.Sprintf("\tif len(buf) != %d {\n", g.analyzed.BufferSize))
	code.WriteString(fmt.Sprintf("\t\treturn fmt.Errorf(\"expected %d bytes, got %%d\", len(buf))\n", g.analyzed.BufferSize))
	code.WriteString("\t}\n\n")

	// Generate code for each region
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.FixedRegion {
			code.WriteString(g.generateFixedOp(region, "unmarshal"))
		} else {
			code.WriteString(g.generateDynamicUnmarshal(region))
		}
	}

	// Generate indirect slice unmarshal ([][]byte with metadata indirection)
	if g.layout != nil {
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				code.WriteString(g.generateIndirectUnmarshal(field))
			}
		}
	}

	code.WriteString("\treturn nil\n")
	code.WriteString("}\n")

	return code.String()
}

// generateZeroCopyUnmarshal generates zero-copy unmarshal using unsafe pointers
func (g *Generator) generateZeroCopyUnmarshal() string {
	var code strings.Builder

	// UnmarshalLayout: keep buf parameter for backward compatibility, but use p.buf
	code.WriteString(fmt.Sprintf("func (p *%s) UnmarshalLayout(buf []byte) error {\n", g.analyzed.TypeName))
	code.WriteString(fmt.Sprintf("\t// Zero-copy mode: copy buf into p.buf if different\n"))
	code.WriteString("\tif len(buf) > 0 && len(p.buf) > 0 {\n")
	code.WriteString("\t\tif &buf[0] != &p.buf[0] {\n")
	code.WriteString("\t\t\tcopy(p.buf, buf)\n")
	code.WriteString("\t\t}\n")
	code.WriteString("\t}\n\n")

	// Generate code for each region
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.FixedRegion {
			code.WriteString(g.generateFixedOp(region, "unmarshal"))
		} else {
			code.WriteString(g.generateZeroCopyDynamicUnmarshal(region))
		}
	}

	// Rebuild indirect slices from metadata
	if g.layout != nil {
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				code.WriteString(g.generateIndirectUnmarshal(field))
			}
		}
	}

	code.WriteString("\treturn nil\n")
	code.WriteString("}\n\n")

	// Add LoadFrom helper
	code.WriteString(g.generateLoadFromHelper())

	// Add RebuildIndirectSlices helper if there are indirect slices
	if g.layout != nil {
		hasIndirect := false
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				hasIndirect = true
				break
			}
		}
		if hasIndirect {
			code.WriteString(g.generateRebuildIndirectSlices())
		}
	}

	return code.String()
}

// generateNewFunction generates New<TypeName>() constructor for buffer allocation
func (g *Generator) generateNewFunction() string {
	var code strings.Builder

	code.WriteString(fmt.Sprintf("func New%s() *%s {\n", g.analyzed.TypeName, g.analyzed.TypeName))
	code.WriteString(fmt.Sprintf("\tp := &%s{}\n", g.analyzed.TypeName))

	if g.align > 0 {
		// Aligned allocation
		requiredSize := g.analyzed.BufferSize + g.align - 1

		if g.allocator != "" {
			// Custom allocator with validation
			code.WriteString(fmt.Sprintf("\t// IMPORTANT: %s() must return a buffer of at least %d bytes\n", g.allocator, requiredSize))
			code.WriteString(fmt.Sprintf("\t// (%d bytes for data + %d bytes for %d-byte alignment)\n",
				g.analyzed.BufferSize, g.align-1, g.align))
			code.WriteString(fmt.Sprintf("\tp.backing = %s()\n", g.allocator))
			code.WriteString("\t\n")
			code.WriteString("\t// Validate buffer size to prevent out-of-bounds access\n")
			code.WriteString(fmt.Sprintf("\tif len(p.backing) < %d {\n", requiredSize))
			code.WriteString(fmt.Sprintf("\t\tpanic(fmt.Sprintf(\"%s returned buffer of %%d bytes, need at least %d\", len(p.backing)))\n",
				g.allocator, requiredSize))
			code.WriteString("\t}\n")
		} else {
			// Default allocation
			code.WriteString(fmt.Sprintf("\t// Allocate %d + %d to guarantee %d-byte alignment\n",
				g.analyzed.BufferSize, g.align-1, g.align))
			code.WriteString(fmt.Sprintf("\tp.backing = make([]byte, %d)\n", requiredSize))
		}

		code.WriteString("\t\n")
		code.WriteString(fmt.Sprintf("\t// Find %d-byte aligned offset\n", g.align))
		code.WriteString("\taddr := uintptr(unsafe.Pointer(&p.backing[0]))\n")
		code.WriteString(fmt.Sprintf("\toffset := int(((addr + %d) &^ %d) - addr)\n", g.align-1, g.align-1))
		code.WriteString("\t\n")
		code.WriteString("\t// Slice aligned region\n")
		code.WriteString(fmt.Sprintf("\tp.buf = p.backing[offset : offset+%d]\n", g.analyzed.BufferSize))
	} else {
		// No alignment, direct allocation
		if g.allocator != "" {
			// Custom allocator with validation
			code.WriteString(fmt.Sprintf("\t// IMPORTANT: %s() must return a buffer of at least %d bytes\n", g.allocator, g.analyzed.BufferSize))
			code.WriteString(fmt.Sprintf("\tp.backing = %s()\n", g.allocator))
			code.WriteString("\t\n")
			code.WriteString("\t// Validate buffer size to prevent out-of-bounds access\n")
			code.WriteString(fmt.Sprintf("\tif len(p.backing) < %d {\n", g.analyzed.BufferSize))
			code.WriteString(fmt.Sprintf("\t\tpanic(fmt.Sprintf(\"%s returned buffer of %%d bytes, need at least %d\", len(p.backing)))\n",
				g.allocator, g.analyzed.BufferSize))
			code.WriteString("\t}\n")
			code.WriteString("\t\n")
			code.WriteString("\t// Use buffer directly (no alignment required)\n")
			code.WriteString(fmt.Sprintf("\tp.buf = p.backing[:%d]\n", g.analyzed.BufferSize))
		} else {
			// This shouldn't happen as we only call this function if align > 0 or allocator != ""
			// But handle it anyway for completeness
			code.WriteString(fmt.Sprintf("\tp.backing = make([]byte, %d)\n", g.analyzed.BufferSize))
			code.WriteString(fmt.Sprintf("\tp.buf = p.backing\n"))
		}
	}

	// Initialize dynamic []byte fields with len=0, cap=max
	code.WriteString("\t\n")
	code.WriteString("\t// Initialize dynamic slices\n")
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.DynamicRegion && region.Field.GoType == "[]byte" {
			start := region.Start
			boundary := region.Boundary

			if region.Direction == parser.StartEnd {
				// Forward: p.Field = p.buf[start:start:boundary]
				code.WriteString(fmt.Sprintf("\tp.%s = p.buf[%d:%d:%d]\n",
					region.Field.Name, start, start, boundary))
			} else {
				// Backward (end-start): don't initialize, will be set during unmarshal
				// These regions are packed backward during marshal, not appendable
				code.WriteString(fmt.Sprintf("\t// %s: end-start region, initialized during unmarshal\n",
					region.Field.Name))
			}
		}
	}

	code.WriteString("\treturn p\n")
	code.WriteString("}\n")

	return code.String()
}

// generateLoadFromHelper generates LoadFrom and WriteTo helpers for zerocopy mode
func (g *Generator) generateLoadFromHelper() string {
	var code strings.Builder

	// LoadFrom: read from io.Reader into p.buf
	code.WriteString(fmt.Sprintf("func (p *%s) LoadFrom(r io.Reader) error {\n", g.analyzed.TypeName))
	code.WriteString("\tif _, err := io.ReadFull(r, p.buf[:]); err != nil {\n")
	code.WriteString("\t\treturn err\n")
	code.WriteString("\t}\n")
	code.WriteString("\treturn p.UnmarshalLayout(p.buf)\n")
	code.WriteString("}\n\n")

	// WriteTo: marshal and write p.buf to io.Writer
	code.WriteString(fmt.Sprintf("func (p *%s) WriteTo(w io.Writer) error {\n", g.analyzed.TypeName))
	code.WriteString("\tif _, err := p.MarshalLayout(); err != nil {\n")
	code.WriteString("\t\treturn err\n")
	code.WriteString("\t}\n")
	code.WriteString("\t_, err := w.Write(p.buf[:])\n")
	code.WriteString("\treturn err\n")
	code.WriteString("}\n")

	return code.String()
}

// binaryPutFunc returns the binary.PutXXX function name for a type
func (g *Generator) binaryPutFunc(goType string) string {
	// Resolve type aliases
	resolved := g.registry.ResolveType(goType)

	switch resolved {
	case "uint16":
		return "PutUint16"
	case "uint32":
		return "PutUint32"
	case "uint64":
		return "PutUint64"
	case "int16":
		return "PutUint16"
	case "int32":
		return "PutUint32"
	case "int64":
		return "PutUint64"
	default:
		return "PutUint32" // fallback
	}
}

// binaryGetFunc returns the binary.Uint32() function name for a type
func (g *Generator) binaryGetFunc(goType string) string {
	// Resolve type aliases
	resolved := g.registry.ResolveType(goType)

	switch resolved {
	case "uint16":
		return "Uint16"
	case "uint32":
		return "Uint32"
	case "uint64":
		return "Uint64"
	case "int16":
		return "Uint16"
	case "int32":
		return "Uint32"
	case "int64":
		return "Uint64"
	default:
		return "Uint32" // fallback
	}
}

// endianPrefix returns "binary.LittleEndian" or "binary.BigEndian"
func (g *Generator) endianPrefix() string {
	if g.endian == "big" {
		return "binary.BigEndian"
	}
	return "binary.LittleEndian"
}

// emitters returns type-specific code generators based on mode
func (g *Generator) emitters() map[string]typeEmitter {
	if g.mode == "zerocopy" {
		return map[string]typeEmitter{
			"uint8": {
				marshal: func(c emitCtx) string {
					cast := "p." + c.field
					if c.needsCast {
						cast = "byte(" + cast + ")"
					}
					return fmt.Sprintf("\tp.buf[%d] = %s\n\n", c.start, cast)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %sp.buf[%d]%s\n\n", c.field, cast, c.start, suffix)
				},
			},
			"byte": {
				marshal: func(c emitCtx) string {
					return fmt.Sprintf("\tp.buf[%d] = p.%s\n\n", c.start, c.field)
				},
				unmarshal: func(c emitCtx) string {
					return fmt.Sprintf("\tp.%s = p.buf[%d]\n\n", c.field, c.start)
				},
			},
			"int8": {
				marshal: func(c emitCtx) string {
					cast := "p." + c.field
					if c.needsCast {
						cast = "int8(" + cast + ")"
					}
					return fmt.Sprintf("\tp.buf[%d] = byte(%s)\n\n", c.start, cast)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %sint8(p.buf[%d])%s\n\n", c.field, cast, c.start, suffix)
				},
			},
			"uint16": {
				marshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = "uint16("
						suffix = ")"
					}
					return fmt.Sprintf("\t*(*uint16)(unsafe.Pointer(&p.buf[%d])) = %sp.%s%s\n\n",
						c.start, cast, c.field, suffix)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %s*(*uint16)(unsafe.Pointer(&p.buf[%d]))%s\n\n",
						c.field, cast, c.start, suffix)
				},
			},
			"int16": {
				marshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = "int16("
						suffix = ")"
					}
					return fmt.Sprintf("\t*(*int16)(unsafe.Pointer(&p.buf[%d])) = %sp.%s%s\n\n",
						c.start, cast, c.field, suffix)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %s*(*int16)(unsafe.Pointer(&p.buf[%d]))%s\n\n",
						c.field, cast, c.start, suffix)
				},
			},
			"uint32": {
				marshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = "uint32("
						suffix = ")"
					}
					return fmt.Sprintf("\t*(*uint32)(unsafe.Pointer(&p.buf[%d])) = %sp.%s%s\n\n",
						c.start, cast, c.field, suffix)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %s*(*uint32)(unsafe.Pointer(&p.buf[%d]))%s\n\n",
						c.field, cast, c.start, suffix)
				},
			},
			"int32": {
				marshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = "int32("
						suffix = ")"
					}
					return fmt.Sprintf("\t*(*int32)(unsafe.Pointer(&p.buf[%d])) = %sp.%s%s\n\n",
						c.start, cast, c.field, suffix)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %s*(*int32)(unsafe.Pointer(&p.buf[%d]))%s\n\n",
						c.field, cast, c.start, suffix)
				},
			},
			"uint64": {
				marshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = "uint64("
						suffix = ")"
					}
					return fmt.Sprintf("\t*(*uint64)(unsafe.Pointer(&p.buf[%d])) = %sp.%s%s\n\n",
						c.start, cast, c.field, suffix)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %s*(*uint64)(unsafe.Pointer(&p.buf[%d]))%s\n\n",
						c.field, cast, c.start, suffix)
				},
			},
			"int64": {
				marshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = "int64("
						suffix = ")"
					}
					return fmt.Sprintf("\t*(*int64)(unsafe.Pointer(&p.buf[%d])) = %sp.%s%s\n\n",
						c.start, cast, c.field, suffix)
				},
				unmarshal: func(c emitCtx) string {
					cast := ""
					suffix := ""
					if c.needsCast {
						cast = c.origType + "("
						suffix = ")"
					}
					return fmt.Sprintf("\tp.%s = %s*(*int64)(unsafe.Pointer(&p.buf[%d]))%s\n\n",
						c.field, cast, c.start, suffix)
				},
			},
		}
	}

	// Copy mode
	return map[string]typeEmitter{
		"uint8": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "uint8(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\tbuf[%d] = %s\n\n", c.start, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %sbuf[%d]%s\n\n", c.field, cast, c.start, suffix)
			},
		},
		"byte": {
			marshal: func(c emitCtx) string {
				return fmt.Sprintf("\tbuf[%d] = p.%s\n\n", c.start, c.field)
			},
			unmarshal: func(c emitCtx) string {
				return fmt.Sprintf("\tp.%s = buf[%d]\n\n", c.field, c.start)
			},
		},
		"int8": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "int8(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\tbuf[%d] = byte(%s)\n\n", c.start, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %sint8(buf[%d])%s\n\n", c.field, cast, c.start, suffix)
			},
		},
		"uint16": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "uint16(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\t%s.PutUint16(buf[%d:%d], %s)\n\n",
					g.endianPrefix(), c.start, c.end, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %s%s.Uint16(buf[%d:%d])%s\n\n",
					c.field, cast, g.endianPrefix(), c.start, c.end, suffix)
			},
		},
		"int16": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "int16(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\t%s.PutUint16(buf[%d:%d], uint16(%s))\n\n",
					g.endianPrefix(), c.start, c.end, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %sint16(%s.Uint16(buf[%d:%d]))%s\n\n",
					c.field, cast, g.endianPrefix(), c.start, c.end, suffix)
			},
		},
		"uint32": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "uint32(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\t%s.PutUint32(buf[%d:%d], %s)\n\n",
					g.endianPrefix(), c.start, c.end, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %s%s.Uint32(buf[%d:%d])%s\n\n",
					c.field, cast, g.endianPrefix(), c.start, c.end, suffix)
			},
		},
		"int32": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "int32(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\t%s.PutUint32(buf[%d:%d], uint32(%s))\n\n",
					g.endianPrefix(), c.start, c.end, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %sint32(%s.Uint32(buf[%d:%d]))%s\n\n",
					c.field, cast, g.endianPrefix(), c.start, c.end, suffix)
			},
		},
		"uint64": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "uint64(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\t%s.PutUint64(buf[%d:%d], %s)\n\n",
					g.endianPrefix(), c.start, c.end, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %s%s.Uint64(buf[%d:%d])%s\n\n",
					c.field, cast, g.endianPrefix(), c.start, c.end, suffix)
			},
		},
		"int64": {
			marshal: func(c emitCtx) string {
				fieldExpr := "p." + c.field
				if c.needsCast {
					fieldExpr = "int64(" + fieldExpr + ")"
				}
				return fmt.Sprintf("\t%s.PutUint64(buf[%d:%d], uint64(%s))\n\n",
					g.endianPrefix(), c.start, c.end, fieldExpr)
			},
			unmarshal: func(c emitCtx) string {
				cast := ""
				suffix := ""
				if c.needsCast {
					cast = c.origType + "("
					suffix = ")"
				}
				return fmt.Sprintf("\tp.%s = %sint64(%s.Uint64(buf[%d:%d]))%s\n\n",
					c.field, cast, g.endianPrefix(), c.start, c.end, suffix)
			},
		},
	}
}

// generateFixedOp generates marshal/unmarshal code for fixed-size field using emission table
func (g *Generator) generateFixedOp(region analyzer.Region, op string) string {
	field := region.Field
	resolvedType := g.registry.ResolveType(field.GoType)
	needsCast := resolvedType != field.GoType

	// Try primitive emitter first
	emitter, ok := g.emitters()[resolvedType]
	if ok {
		ctx := emitCtx{
			field:     field.Name,
			start:     region.Start,
			end:       region.Boundary,
			needsCast: needsCast,
			origType:  field.GoType,
		}

		code := fmt.Sprintf("\t// %s: %s at [%d, %d)\n", field.Name, field.GoType, ctx.start, ctx.end)
		switch op {
		case "marshal":
			code += emitter.marshal(ctx)
		case "unmarshal":
			code += emitter.unmarshal(ctx)
		}
		return code
	}

	// Handle complex types (arrays, structs)
	return g.generateComplexFixedOp(region, op)
}

// generateComplexFixedOp handles arrays and struct types
func (g *Generator) generateComplexFixedOp(region analyzer.Region, op string) string {
	var code strings.Builder
	field := region.Field
	start := region.Start
	end := region.Boundary

	code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d)\n", field.Name, field.GoType, start, end))

	// Byte arrays
	if strings.HasPrefix(field.GoType, "[") && strings.Contains(field.GoType, "]byte") {
		if op == "marshal" {
			if g.mode == "zerocopy" {
				code.WriteString(fmt.Sprintf("\tcopy(p.buf[%d:%d], p.%s[:])\n\n", start, end, field.Name))
			} else {
				code.WriteString(fmt.Sprintf("\tcopy(buf[%d:%d], p.%s[:])\n\n", start, end, field.Name))
			}
		} else {
			if g.mode == "zerocopy" {
				code.WriteString(fmt.Sprintf("\tcopy(p.%s[:], p.buf[%d:%d])\n\n", field.Name, start, end))
			} else {
				code.WriteString(fmt.Sprintf("\tcopy(p.%s[:], buf[%d:%d])\n\n", field.Name, start, end))
			}
		}
		return code.String()
	}

	// Struct types
	if op == "marshal" {
		code.WriteString(fmt.Sprintf("\telemBuf, err := p.%s.MarshalLayout()\n", field.Name))
		code.WriteString("\tif err != nil {\n")
		code.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"marshal %s: %%w\", err)\n", field.Name))
		code.WriteString("\t}\n")
		if g.mode == "zerocopy" {
			code.WriteString(fmt.Sprintf("\tcopy(p.buf[%d:%d], elemBuf)\n\n", start, end))
		} else {
			code.WriteString(fmt.Sprintf("\tcopy(buf[%d:%d], elemBuf)\n\n", start, end))
		}
	} else {
		if g.mode == "zerocopy" {
			code.WriteString(fmt.Sprintf("\tif err := p.%s.UnmarshalLayout(p.buf[%d:%d]); err != nil {\n", field.Name, start, end))
		} else {
			code.WriteString(fmt.Sprintf("\tif err := p.%s.UnmarshalLayout(buf[%d:%d]); err != nil {\n", field.Name, start, end))
		}
		code.WriteString(fmt.Sprintf("\t\treturn fmt.Errorf(\"unmarshal %s: %%w\", err)\n", field.Name))
		code.WriteString("\t}\n\n")
	}

	return code.String()
}

// generateDynamicMarshal generates marshal code for a dynamic field
func (g *Generator) generateDynamicMarshal(region analyzer.Region) string {
	// Check element type to determine marshal strategy
	if region.ElementType == "byte" {
		return g.generateByteMarshal(region)
	}
	return g.generateStructMarshal(region)
}

// generateByteMarshal generates byte-by-byte marshal for []byte
func (g *Generator) generateByteMarshal(region analyzer.Region) string {
	var code strings.Builder

	field := region.Field
	start := region.Start
	boundary := region.Boundary
	countField := field.Layout.CountField

	// Comment
	if countField != "" {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s\n",
			field.Name, field.GoType, start, boundary, countField))
	} else {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d)\n",
			field.Name, field.GoType, start, boundary))
	}

	if region.Direction == parser.StartEnd {
		// Forward growth
		code.WriteString(fmt.Sprintf("\toffset = %d\n", start))

		// Count validation if count field exists
		if countField != "" {
			code.WriteString(fmt.Sprintf("\tif len(p.%s) != int(p.%s) {\n", field.Name, countField))
			code.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s length mismatch: have %%d, want %%d\", len(p.%s), p.%s)\n",
				field.Name, field.Name, countField))
			code.WriteString("\t}\n")
		}

		// Marshal loop
		code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\tif offset >= %d {\n", boundary))
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"%s collision at offset %%d\", offset)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\tbuf[offset] = p.%s[i]\n", field.Name))
		code.WriteString("\t\toffset++\n")
		code.WriteString("\t}\n\n")
	} else {
		// Backward growth (end-start)
		code.WriteString(fmt.Sprintf("\toffset = %d\n", start))

		// Count validation if count field exists
		if countField != "" {
			code.WriteString(fmt.Sprintf("\tif len(p.%s) != int(p.%s) {\n", field.Name, countField))
			code.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s length mismatch: have %%d, want %%d\", len(p.%s), p.%s)\n",
				field.Name, field.Name, countField))
			code.WriteString("\t}\n")
		}

		// Marshal backward
		code.WriteString(fmt.Sprintf("\tfor i := len(p.%s) - 1; i >= 0; i-- {\n", field.Name))
		code.WriteString("\t\toffset--\n")
		code.WriteString(fmt.Sprintf("\t\tif offset < %d {\n", boundary))
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"%s collision at offset %%d\", offset)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\tbuf[offset] = p.%s[i]\n", field.Name))
		code.WriteString("\t}\n\n")
	}

	return code.String()
}

// generateStructMarshal generates element-by-element marshal for []StructType
func (g *Generator) generateStructMarshal(region analyzer.Region) string {
	var code strings.Builder

	field := region.Field
	start := region.Start
	boundary := region.Boundary
	countField := field.Layout.CountField
	elementSize := region.ElementSize

	// Comment
	if countField != "" {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s (element size: %d)\n",
			field.Name, field.GoType, start, boundary, countField, elementSize))
	} else {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) (element size: %d)\n",
			field.Name, field.GoType, start, boundary, elementSize))
	}

	if region.Direction == parser.StartEnd {
		// Forward growth
		code.WriteString(fmt.Sprintf("\toffset = %d\n", start))

		// Count validation if count field exists
		if countField != "" {
			code.WriteString(fmt.Sprintf("\tif len(p.%s) != int(p.%s) {\n", field.Name, countField))
			code.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s length mismatch: have %%d, want %%d\", len(p.%s), p.%s)\n",
				field.Name, field.Name, countField))
			code.WriteString("\t}\n")
		}

		// Marshal loop for structs
		code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\tif offset + %d > %d {\n", elementSize, boundary))
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"%s collision at offset %%d\", offset)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\telemBuf, err := p.%s[i].MarshalLayout()\n", field.Name))
		code.WriteString("\t\tif err != nil {\n")
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"marshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\tcopy(buf[offset:offset+%d], elemBuf)\n", elementSize))
		code.WriteString(fmt.Sprintf("\t\toffset += %d\n", elementSize))
		code.WriteString("\t}\n\n")
	} else {
		// Backward growth (end-start)
		code.WriteString(fmt.Sprintf("\toffset = %d\n", start))

		// Count validation if count field exists
		if countField != "" {
			code.WriteString(fmt.Sprintf("\tif len(p.%s) != int(p.%s) {\n", field.Name, countField))
			code.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s length mismatch: have %%d, want %%d\", len(p.%s), p.%s)\n",
				field.Name, field.Name, countField))
			code.WriteString("\t}\n")
		}

		// Marshal backward for structs
		code.WriteString(fmt.Sprintf("\tfor i := len(p.%s) - 1; i >= 0; i-- {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\toffset -= %d\n", elementSize))
		code.WriteString(fmt.Sprintf("\t\tif offset < %d {\n", boundary))
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"%s collision at offset %%d\", offset)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\telemBuf, err := p.%s[i].MarshalLayout()\n", field.Name))
		code.WriteString("\t\tif err != nil {\n")
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"marshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\tcopy(buf[offset:offset+%d], elemBuf)\n", elementSize))
		code.WriteString("\t}\n\n")
	}

	return code.String()
}

// generateDynamicUnmarshal generates unmarshal code for a dynamic field
func (g *Generator) generateDynamicUnmarshal(region analyzer.Region) string {
	// Check element type to determine unmarshal strategy
	if region.ElementType == "byte" {
		return g.generateByteUnmarshal(region)
	}
	return g.generateStructUnmarshal(region)
}

// generateByteUnmarshal generates byte-by-byte unmarshal for []byte
func (g *Generator) generateByteUnmarshal(region analyzer.Region) string {
	var code strings.Builder

	field := region.Field
	start := region.Start
	boundary := region.Boundary
	countField := field.Layout.CountField

	// Comment
	if countField != "" {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s\n",
			field.Name, field.GoType, start, boundary, countField))
	} else {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d)\n",
			field.Name, field.GoType, start, boundary))
	}

	// Calculate length
	if countField != "" {
		// Explicit count
		code.WriteString(fmt.Sprintf("\t// Reuse buffer if capacity allows\n"))
		code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= int(p.%s) {\n", field.Name, countField))
		code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:p.%s]\n", field.Name, field.Name, countField))
		code.WriteString("\t} else {\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s = make([]byte, p.%s)\n", field.Name, countField))
		code.WriteString("\t}\n")

		if region.Direction == parser.StartEnd {
			code.WriteString(fmt.Sprintf("\tcopy(p.%s, buf[%d:%d+p.%s])\n\n", field.Name, start, start, countField))
		} else {
			// Backward: copy from (start - count) to start
			code.WriteString(fmt.Sprintf("\tcopy(p.%s, buf[%d-p.%s:%d])\n\n", field.Name, start, countField, start))
		}
	} else {
		// Implicit length from boundaries
		lenVar := fmt.Sprintf("%sLen", strings.ToLower(string(field.Name[0])))
		if region.Direction == parser.StartEnd {
			code.WriteString(fmt.Sprintf("\t%s := %d - %d\n", lenVar, boundary, start))
		} else {
			code.WriteString(fmt.Sprintf("\t%s := %d - %d\n", lenVar, start, boundary))
		}

		code.WriteString(fmt.Sprintf("\t// Reuse buffer if capacity allows\n"))
		code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= %s {\n", field.Name, lenVar))
		code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:%s]\n", field.Name, field.Name, lenVar))
		code.WriteString("\t} else {\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s = make([]byte, %s)\n", field.Name, lenVar))
		code.WriteString("\t}\n")

		if region.Direction == parser.StartEnd {
			code.WriteString(fmt.Sprintf("\tcopy(p.%s, buf[%d:%d])\n\n", field.Name, start, boundary))
		} else {
			code.WriteString(fmt.Sprintf("\tcopy(p.%s, buf[%d:%d])\n\n", field.Name, boundary, start))
		}
	}

	return code.String()
}

// generateStructUnmarshal generates element-by-element unmarshal for []StructType
func (g *Generator) generateStructUnmarshal(region analyzer.Region) string {
	var code strings.Builder

	field := region.Field
	start := region.Start
	boundary := region.Boundary
	countField := field.Layout.CountField
	elementSize := region.ElementSize
	elementType := region.ElementType

	// Comment
	if countField != "" {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s (element size: %d)\n",
			field.Name, field.GoType, start, boundary, countField, elementSize))
	} else {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) (element size: %d)\n",
			field.Name, field.GoType, start, boundary, elementSize))
	}

	// Calculate number of elements
	if countField != "" {
		// Explicit count
		code.WriteString(fmt.Sprintf("\t// Reuse slice if capacity allows\n"))
		code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= int(p.%s) {\n", field.Name, countField))
		code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:p.%s]\n", field.Name, field.Name, countField))
		code.WriteString("\t} else {\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s = make([]%s, p.%s)\n", field.Name, elementType, countField))
		code.WriteString("\t}\n")
	} else {
		// Implicit count from region size
		numElements := (boundary - start) / elementSize
		if region.Direction == parser.EndStart {
			numElements = (start - boundary) / elementSize
		}
		code.WriteString(fmt.Sprintf("\tnumElements := %d // (%d bytes / %d bytes per element)\n",
			numElements, abs(boundary-start), elementSize))
		code.WriteString(fmt.Sprintf("\t// Reuse slice if capacity allows\n"))
		code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= numElements {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:numElements]\n", field.Name, field.Name))
		code.WriteString("\t} else {\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s = make([]%s, numElements)\n", field.Name, elementType))
		code.WriteString("\t}\n")
	}

	// Unmarshal loop
	code.WriteString(fmt.Sprintf("\toffset := %d\n", start))
	code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", field.Name))

	if region.Direction == parser.StartEnd {
		code.WriteString(fmt.Sprintf("\t\tif err := p.%s[i].UnmarshalLayout(buf[offset:offset+%d]); err != nil {\n",
			field.Name, elementSize))
		code.WriteString(fmt.Sprintf("\t\t\treturn fmt.Errorf(\"unmarshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\toffset += %d\n", elementSize))
	} else {
		// Backward
		code.WriteString(fmt.Sprintf("\t\tif err := p.%s[i].UnmarshalLayout(buf[offset-%d:offset]); err != nil {\n",
			field.Name, elementSize))
		code.WriteString(fmt.Sprintf("\t\t\treturn fmt.Errorf(\"unmarshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\toffset -= %d\n", elementSize))
	}

	code.WriteString("\t}\n\n")

	return code.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// generateZeroCopyDynamicUnmarshal generates zero-copy unmarshal for dynamic field
func (g *Generator) generateZeroCopyDynamicUnmarshal(region analyzer.Region) string {
	var code strings.Builder

	field := region.Field
	start := region.Start
	boundary := region.Boundary
	countField := field.Layout.CountField

	// Handle []byte - slice directly from p.buf
	if field.GoType == "[]byte" {
		// Comment
		if countField != "" {
			code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s\n",
				field.Name, field.GoType, start, boundary, countField))
		} else {
			code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d)\n",
				field.Name, field.GoType, start, boundary))
		}

		// Check if this region is referenced by indirect slices
		isIndirectRegion := false
		if g.layout != nil {
			for _, f := range g.layout.Fields {
				if f.Layout.From != "" && f.Layout.Region == field.Name {
					isIndirectRegion = true
					break
				}
			}
		}

		// For end-start regions used as data regions for indirect slices,
		// skip automatic initialization - it will be set by RebuildIndirectSlices
		if region.Direction == parser.EndStart && isIndirectRegion {
			code.WriteString(fmt.Sprintf("\t// %s: end-start data region, set by indirect slice reconstruction\n\n", field.Name))
			return code.String()
		}

		// Slice directly into buffer
		if countField != "" {
			// Count-dependent slicing
			if region.Direction == parser.StartEnd {
				// Forward: slice from start with count
				code.WriteString(fmt.Sprintf("\tp.%s = p.buf[%d:%d+p.%s]\n\n", field.Name, start, start, countField))
			} else {
				// Backward: slice from (start - count) to start
				code.WriteString(fmt.Sprintf("\tp.%s = p.buf[%d-p.%s:%d]\n\n", field.Name, start, countField, start))
			}
		} else {
			// Implicit length from boundaries
			if region.Direction == parser.StartEnd {
				code.WriteString(fmt.Sprintf("\tp.%s = p.buf[%d:%d]\n\n", field.Name, start, boundary))
			} else {
				code.WriteString(fmt.Sprintf("\tp.%s = p.buf[%d:%d]\n\n", field.Name, boundary, start))
			}
		}
		return code.String()
	}

	// Handle struct slices - need to unmarshal each element
	elementSize := region.ElementSize
	elementType := region.ElementType

	// Comment
	if countField != "" {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s (element size: %d)\n",
			field.Name, field.GoType, start, boundary, countField, elementSize))
	} else {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) (element size: %d)\n",
			field.Name, field.GoType, start, boundary, elementSize))
	}

	// Calculate number of elements
	if countField != "" {
		// Explicit count
		code.WriteString(fmt.Sprintf("\t// Reuse slice if capacity allows\n"))
		code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= int(p.%s) {\n", field.Name, countField))
		code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:p.%s]\n", field.Name, field.Name, countField))
		code.WriteString("\t} else {\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s = make([]%s, p.%s)\n", field.Name, elementType, countField))
		code.WriteString("\t}\n")
	} else {
		// Implicit count from region size
		numElements := (boundary - start) / elementSize
		if region.Direction == parser.EndStart {
			numElements = (start - boundary) / elementSize
		}
		code.WriteString(fmt.Sprintf("\tnumElements := %d // (%d bytes / %d bytes per element)\n",
			numElements, abs(boundary-start), elementSize))
		code.WriteString(fmt.Sprintf("\t// Reuse slice if capacity allows\n"))
		code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= numElements {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:numElements]\n", field.Name, field.Name))
		code.WriteString("\t} else {\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s = make([]%s, numElements)\n", field.Name, elementType))
		code.WriteString("\t}\n")
	}

	// Unmarshal loop
	code.WriteString(fmt.Sprintf("\toffset := %d\n", start))
	code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", field.Name))

	if region.Direction == parser.StartEnd {
		code.WriteString(fmt.Sprintf("\t\tif err := p.%s[i].UnmarshalLayout(p.buf[offset:offset+%d]); err != nil {\n",
			field.Name, elementSize))
		code.WriteString(fmt.Sprintf("\t\t\treturn fmt.Errorf(\"unmarshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\toffset += %d\n", elementSize))
	} else {
		// Backward
		code.WriteString(fmt.Sprintf("\t\tif err := p.%s[i].UnmarshalLayout(p.buf[offset-%d:offset]); err != nil {\n",
			field.Name, elementSize))
		code.WriteString(fmt.Sprintf("\t\t\treturn fmt.Errorf(\"unmarshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\toffset -= %d\n", elementSize))
	}

	code.WriteString("\t}\n\n")

	return code.String()
}

// generateZeroCopyDynamicMarshal generates marshal code for dynamic field into p.buf
func (g *Generator) generateZeroCopyDynamicMarshal(region analyzer.Region) string {
	var code strings.Builder

	field := region.Field
	start := region.Start
	boundary := region.Boundary
	countField := field.Layout.CountField

	// Handle []byte - already in p.buf, no copy needed
	if field.GoType == "[]byte" {
		// Comment
		if countField != "" {
			code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s\n",
				field.Name, field.GoType, start, boundary, countField))
		} else {
			code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d)\n",
				field.Name, field.GoType, start, boundary))
		}
		code.WriteString(fmt.Sprintf("\t// %s is already sliced from p.buf, no copy needed\n\n", field.Name))
		return code.String()
	}

	// Handle struct slices - need to marshal each element
	elementSize := region.ElementSize

	// Comment
	if countField != "" {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) with count=%s (element size: %d)\n",
			field.Name, field.GoType, start, boundary, countField, elementSize))
	} else {
		code.WriteString(fmt.Sprintf("\t// %s: %s at [%d, %d) (element size: %d)\n",
			field.Name, field.GoType, start, boundary, elementSize))
	}

	// Count validation if count field exists
	if countField != "" {
		code.WriteString(fmt.Sprintf("\tif len(p.%s) != int(p.%s) {\n", field.Name, countField))
		code.WriteString(fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s length mismatch: have %%d, want %%d\", len(p.%s), p.%s)\n",
			field.Name, field.Name, countField))
		code.WriteString("\t}\n")
	}

	// Marshal loop for structs
	if region.Direction == parser.StartEnd {
		// Forward growth
		code.WriteString(fmt.Sprintf("\toffset := %d\n", start))
		code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\tif offset + %d > %d {\n", elementSize, boundary))
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"%s collision at offset %%d\", offset)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\telemBuf, err := p.%s[i].MarshalLayout()\n", field.Name))
		code.WriteString("\t\tif err != nil {\n")
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"marshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\tcopy(p.buf[offset:offset+%d], elemBuf)\n", elementSize))
		code.WriteString(fmt.Sprintf("\t\toffset += %d\n", elementSize))
		code.WriteString("\t}\n\n")
	} else {
		// Backward growth
		code.WriteString(fmt.Sprintf("\toffset := %d\n", start))
		code.WriteString(fmt.Sprintf("\tfor i := len(p.%s) - 1; i >= 0; i-- {\n", field.Name))
		code.WriteString(fmt.Sprintf("\t\toffset -= %d\n", elementSize))
		code.WriteString(fmt.Sprintf("\t\tif offset < %d {\n", boundary))
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"%s collision at offset %%d\", offset)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\telemBuf, err := p.%s[i].MarshalLayout()\n", field.Name))
		code.WriteString("\t\tif err != nil {\n")
		code.WriteString(fmt.Sprintf("\t\t\treturn nil, fmt.Errorf(\"marshal %s[%%d]: %%w\", i, err)\n", field.Name))
		code.WriteString("\t\t}\n")
		code.WriteString(fmt.Sprintf("\t\tcopy(p.buf[offset:offset+%d], elemBuf)\n", elementSize))
		code.WriteString("\t}\n\n")
	}

	return code.String()
}

// generateIndirectUnmarshal generates unmarshal code for [][]byte with metadata indirection
func (g *Generator) generateIndirectUnmarshal(field parser.Field) string {
	var code strings.Builder

	// Comment
	code.WriteString(fmt.Sprintf("\t// %s: [][]byte from=%s offset=%s size=%s region=%s\n",
		field.Name, field.Layout.From, field.Layout.OffsetField, field.Layout.SizeField, field.Layout.Region))

	// For end-start data regions, we need to calculate where Data should start
	// This only needs to be done once for all indirect slices
	// Check if this is the first indirect field and if Data region needs initialization
	if g.layout != nil {
		isFirstIndirect := true
		for _, f := range g.layout.Fields {
			if f.Layout.From != "" {
				if f.Name == field.Name {
					break
				} else {
					isFirstIndirect = false
					break
				}
			}
		}

		// If this is the first indirect slice, initialize the Data region
		if isFirstIndirect {
			// Find the metadata region to calculate elementsEnd
			for _, region := range g.analyzed.Regions {
				if region.Kind == analyzer.DynamicRegion &&
				   region.Direction == parser.StartEnd &&
				   region.ElementType != "byte" &&
				   region.Field.Name == field.Layout.From {
					code.WriteString(fmt.Sprintf("\t// Initialize %s data region after metadata\n", field.Layout.Region))
					code.WriteString(fmt.Sprintf("\telementsEnd := %d + int(p.%s)*%d\n",
						region.Start, region.Field.Layout.CountField, region.ElementSize))

					// Use appropriate buffer reference based on mode
					if g.mode == "zerocopy" {
						code.WriteString(fmt.Sprintf("\tp.%s = p.buf[elementsEnd:%d]\n\n", field.Layout.Region, g.analyzed.BufferSize))
					} else {
						code.WriteString(fmt.Sprintf("\tp.%s = buf[elementsEnd:%d]\n\n", field.Layout.Region, g.analyzed.BufferSize))
					}
					break
				}
			}
		}
	}

	// Allocate slice matching source length
	code.WriteString(fmt.Sprintf("\t// Reuse slice if capacity allows\n"))
	code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= len(p.%s) {\n", field.Name, field.Layout.From))
	code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:len(p.%s)]\n", field.Name, field.Name, field.Layout.From))
	code.WriteString("\t} else {\n")
	code.WriteString(fmt.Sprintf("\t\tp.%s = make([][]byte, len(p.%s))\n", field.Name, field.Layout.From))
	code.WriteString("\t}\n")

	// Loop through source elements and create slices
	code.WriteString(fmt.Sprintf("\tfor i := range p.%s {\n", field.Layout.From))
	code.WriteString(fmt.Sprintf("\t\toffset := int(p.%s[i].%s)\n", field.Layout.From, field.Layout.OffsetField))
	code.WriteString(fmt.Sprintf("\t\tsize := int(p.%s[i].%s)\n", field.Layout.From, field.Layout.SizeField))

	// Handle absolute vs relative offset mode
	if field.Layout.OffsetMode == "absolute" {
		code.WriteString("\t\t// Offset is absolute from page start, adjust to region-relative\n")
		code.WriteString("\t\tregionOffset := offset - elementsEnd\n")
		code.WriteString(fmt.Sprintf("\t\tp.%s[i] = p.%s[regionOffset:regionOffset+size]\n", field.Name, field.Layout.Region))
	} else {
		// Default: relative mode (backwards compatible)
		code.WriteString(fmt.Sprintf("\t\tp.%s[i] = p.%s[offset:offset+size]\n", field.Name, field.Layout.Region))
	}
	code.WriteString("\t}\n\n")

	return code.String()
}

// getMetadataFieldType looks up the type of a field in the metadata struct
func (g *Generator) getMetadataFieldType(fromField, fieldName string) string {
	// Find the source field in current layout
	var sourceField *parser.Field
	for i := range g.layout.Fields {
		if g.layout.Fields[i].Name == fromField {
			sourceField = &g.layout.Fields[i]
			break
		}
	}
	if sourceField == nil {
		return "uint32" // fallback
	}

	// Extract element type from slice: []LeafElement → LeafElement
	elemType := strings.TrimPrefix(sourceField.GoType, "[]")

	// Find the element type's layout in all parsed layouts
	for _, layout := range g.allLayouts {
		if layout.Name == elemType {
			// Find the field in this layout
			for _, f := range layout.Fields {
				if f.Name == fieldName {
					return f.GoType
				}
			}
		}
	}

	return "uint32" // fallback if not found
}

// generateIndirectMarshal generates marshal code for [][]byte with backward packing
func (g *Generator) generateIndirectMarshal(field parser.Field) string {
	var code strings.Builder

	// Comment
	code.WriteString(fmt.Sprintf("\t// %s: [][]byte packed backward into %s, updating %s metadata\n",
		field.Name, field.Layout.Region, field.Layout.From))

	// Find the region field to determine pack start point
	var regionField *parser.Field
	for i := range g.layout.Fields {
		if g.layout.Fields[i].Name == field.Layout.Region {
			regionField = &g.layout.Fields[i]
			break
		}
	}

	// Pack backward from region end
	var packStart string
	if regionField != nil && regionField.Layout.Direction == parser.EndStart {
		// Region is end-start, so it starts at bufferSize and grows backward
		packStart = fmt.Sprintf("%d", g.analyzed.BufferSize)
	} else {
		// Default: pack from buffer end
		packStart = fmt.Sprintf("%d", g.analyzed.BufferSize)
	}

	// Look up actual field types for offset and size
	offsetType := g.getMetadataFieldType(field.Layout.From, field.Layout.OffsetField)
	sizeType := g.getMetadataFieldType(field.Layout.From, field.Layout.SizeField)

	// Calculate elementsEnd only for relative offset mode
	var elementsEnd string
	if field.Layout.OffsetMode != "absolute" {
		// Find the metadata region to calculate where it ends
		for _, region := range g.analyzed.Regions {
			if region.Kind == analyzer.DynamicRegion &&
			   region.Direction == parser.StartEnd &&
			   region.ElementType != "byte" &&
			   region.Field.Name == field.Layout.From {
				code.WriteString(fmt.Sprintf("\telementsEnd := %d + len(p.%s)*%d\n",
					region.Start, field.Layout.From, region.ElementSize))
				elementsEnd = "elementsEnd"
				break
			}
		}
	}

	code.WriteString(fmt.Sprintf("\toffset = %s\n", packStart))
	code.WriteString(fmt.Sprintf("\tfor i := len(p.%s) - 1; i >= 0; i-- {\n", field.Name))
	code.WriteString(fmt.Sprintf("\t\tsize := len(p.%s[i])\n", field.Name))
	code.WriteString("\t\toffset -= size\n")
	code.WriteString(fmt.Sprintf("\t\tcopy(buf[offset:offset+size], p.%s[i])\n", field.Name))

	// Store offset based on offset mode
	if field.Layout.OffsetMode == "absolute" {
		// Store absolute offset from page start
		code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(offset)\n",
			field.Layout.From, field.Layout.OffsetField, offsetType))
	} else {
		// Store region-relative offset (default, backwards compatible)
		if elementsEnd != "" {
			code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(offset - %s)\n",
				field.Layout.From, field.Layout.OffsetField, offsetType, elementsEnd))
		} else {
			// Fallback: assume region starts at 0 (shouldn't happen)
			code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(offset)\n",
				field.Layout.From, field.Layout.OffsetField, offsetType))
		}
	}

	code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(size)\n", field.Layout.From, field.Layout.SizeField, sizeType))
	code.WriteString("\t}\n\n")

	return code.String()
}

// generateRebuildIndirectSlices generates a helper function to rebuild Elements and Data from indirect slices
func (g *Generator) generateRebuildIndirectSlices() string {
	var code strings.Builder

	code.WriteString(fmt.Sprintf("\n// RebuildIndirectSlices rebuilds the physical layout from logical slices\n"))
	code.WriteString(fmt.Sprintf("// Call this after modifying Keys/Values before calling MarshalLayout\n"))
	code.WriteString(fmt.Sprintf("func (p *%s) RebuildIndirectSlices() {\n", g.analyzed.TypeName))

	// Find the metadata slice (Elements) and data region (Data)
	var metadataRegion *analyzer.Region
	var dataRegion *analyzer.Region
	
	for i := range g.analyzed.Regions {
		region := &g.analyzed.Regions[i]
		if region.Kind == analyzer.DynamicRegion {
			if region.Direction == parser.StartEnd && region.ElementType != "byte" {
				metadataRegion = region
			} else if region.Direction == parser.EndStart && region.Field.GoType == "[]byte" {
				dataRegion = region
			}
		}
	}

	if metadataRegion == nil || dataRegion == nil {
		// Can't rebuild without both metadata and data regions
		code.WriteString("\t// No metadata/data regions to rebuild\n")
		code.WriteString("}\n")
		return code.String()
	}

	// Calculate where metadata ends
	code.WriteString(fmt.Sprintf("\t// Calculate where %s ends\n", metadataRegion.Field.Name))
	code.WriteString(fmt.Sprintf("\telementsEnd := %d + int(p.%s)*%d\n",
		metadataRegion.Start,
		metadataRegion.Field.Layout.CountField,
		metadataRegion.ElementSize))

	// Initialize Data buffer after Elements
	code.WriteString(fmt.Sprintf("\t\n\t// Initialize %s buffer after %s\n", dataRegion.Field.Name, metadataRegion.Field.Name))
	code.WriteString(fmt.Sprintf("\tp.%s = p.buf[elementsEnd:elementsEnd:%d]\n", dataRegion.Field.Name, g.analyzed.BufferSize))

	// Find non-indirect fields in metadata element type that need to be preserved
	var preserveFields []string
	if g.layout != nil {
		// Get the element type name
		elemType := metadataRegion.ElementType

		// Find the layout for this element type
		for _, layout := range g.allLayouts {
			if layout.Name == elemType {
				// Get all field names used by indirect slices
				usedFields := make(map[string]bool)
				for _, field := range g.layout.Fields {
					if field.Layout.From != "" && field.Layout.From == metadataRegion.Field.Name {
						usedFields[field.Layout.OffsetField] = true
						usedFields[field.Layout.SizeField] = true
					}
				}

				// Find fields that aren't used by indirect slices
				for _, f := range layout.Fields {
					if !usedFields[f.Name] {
						preserveFields = append(preserveFields, f.Name)
					}
				}
				break
			}
		}
	}

	// Save non-indirect metadata fields if any exist
	if len(preserveFields) > 0 {
		code.WriteString(fmt.Sprintf("\t\n\t// Save non-indirect metadata fields before rebuilding %s\n", metadataRegion.Field.Name))
		code.WriteString(fmt.Sprintf("\tsaved%s := make([]%s, len(p.%s))\n",
			metadataRegion.Field.Name, metadataRegion.ElementType, metadataRegion.Field.Name))
		code.WriteString(fmt.Sprintf("\tcopy(saved%s, p.%s)\n", metadataRegion.Field.Name, metadataRegion.Field.Name))
	}

	// Rebuild metadata slice if needed
	code.WriteString(fmt.Sprintf("\t\n\t// Rebuild %s array\n", metadataRegion.Field.Name))
	code.WriteString(fmt.Sprintf("\tif cap(p.%s) >= int(p.%s) {\n",
		metadataRegion.Field.Name,
		metadataRegion.Field.Layout.CountField))
	code.WriteString(fmt.Sprintf("\t\tp.%s = p.%s[:p.%s]\n",
		metadataRegion.Field.Name,
		metadataRegion.Field.Name,
		metadataRegion.Field.Layout.CountField))
	code.WriteString("\t} else {\n")
	code.WriteString(fmt.Sprintf("\t\tp.%s = make([]%s, p.%s)\n",
		metadataRegion.Field.Name,
		metadataRegion.ElementType,
		metadataRegion.Field.Layout.CountField))
	code.WriteString("\t}\n")

	// Pack all indirect slices into Data backward from the end
	code.WriteString("\t\n\t// Pack indirect slices into Data region backward from end\n")
	code.WriteString(fmt.Sprintf("\toffset := %d\n", g.analyzed.BufferSize))

	// Collect all indirect slice fields
	var indirectFields []parser.Field
	if g.layout != nil {
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				indirectFields = append(indirectFields, field)
			}
		}
	}

	// Determine count field from first indirect field (used in multiple places)
	var firstFrom string
	if len(indirectFields) > 0 {
		firstFrom = indirectFields[0].Layout.From
	}

	// Generate packing code: pack all fields for each element together
	if len(indirectFields) > 0 {
		code.WriteString(fmt.Sprintf("\t\n\t// Pack all indirect slices backward from end (elements in forward order)\n"))
		code.WriteString(fmt.Sprintf("\tfor i := 0; i < len(p.%s); i++ {\n", indirectFields[0].Name))

		// Pack each field for this element in forward order (Key before Value)
		for j := 0; j < len(indirectFields); j++ {
			field := indirectFields[j]
			offsetType := g.getMetadataFieldType(field.Layout.From, field.Layout.OffsetField)
			sizeType := g.getMetadataFieldType(field.Layout.From, field.Layout.SizeField)
			sizeVar := fmt.Sprintf("size%d", j)

			code.WriteString(fmt.Sprintf("\t\t// Pack %s[i]\n", field.Name))
			code.WriteString(fmt.Sprintf("\t\t%s := len(p.%s[i])\n", sizeVar, field.Name))
			code.WriteString(fmt.Sprintf("\t\toffset -= %s\n", sizeVar))
			code.WriteString(fmt.Sprintf("\t\tcopy(p.buf[offset:offset+%s], p.%s[i])\n", sizeVar, field.Name))

			// Store offset based on offset mode
			if field.Layout.OffsetMode == "absolute" {
				// Store absolute offset from page start
				code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(offset)\n",
					firstFrom, field.Layout.OffsetField, offsetType))
			} else {
				// Store region-relative offset (default, backwards compatible)
				code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(offset - elementsEnd)\n",
					firstFrom, field.Layout.OffsetField, offsetType))
			}

			code.WriteString(fmt.Sprintf("\t\tp.%s[i].%s = %s(%s)\n",
				firstFrom, field.Layout.SizeField, sizeType, sizeVar))
		}

		// Restore non-indirect metadata fields (only if saved element exists)
		if len(preserveFields) > 0 {
			code.WriteString("\n")
			code.WriteString(fmt.Sprintf("\t\t// Restore non-indirect fields (only if saved element exists)\n"))
			code.WriteString(fmt.Sprintf("\t\tif i < len(saved%s) {\n", metadataRegion.Field.Name))
			for _, fieldName := range preserveFields {
				code.WriteString(fmt.Sprintf("\t\t\tp.%s[i].%s = saved%s[i].%s\n",
					firstFrom, fieldName, metadataRegion.Field.Name, fieldName))
			}
			code.WriteString("\t\t}\n")
		}

		code.WriteString("\t}\n")
	}

	// Update Data to span the full packed region
	code.WriteString("\t\n\t// Update Data to span full packed region\n")
	code.WriteString(fmt.Sprintf("\tp.%s = p.buf[elementsEnd:%d]\n", dataRegion.Field.Name, g.analyzed.BufferSize))

	code.WriteString("}\n")

	return code.String()
}
// generateZeroCopyAccessors generates accessor-based zerocopy code
func (g *Generator) generateZeroCopyAccessors() string {
	var code strings.Builder

	// Generate New<Type>() constructor
	code.WriteString(g.generateNewFunction())
	code.WriteString("\n")

	// Generate Clone() helper
	code.WriteString(g.generateClone())
	code.WriteString("\n")

	// Generate Get/Set accessors for each field
	for _, region := range g.analyzed.Regions {
		if region.Kind == analyzer.FixedRegion {
			code.WriteString(g.generateFixedAccessors(region))
		} else {
			code.WriteString(g.generateDynamicAccessors(region))
		}
	}

	// Generate indirect slice accessors
	if g.layout != nil {
		for _, field := range g.layout.Fields {
			if field.Layout.From != "" {
				code.WriteString(g.generateIndirectAccessors(field))
			}
		}
	}

	return code.String()
}

// generateClone generates Clone() method for CoW
func (g *Generator) generateClone() string {
	var code strings.Builder

	code.WriteString(fmt.Sprintf("// Clone creates a copy of the %s\n", g.analyzed.TypeName))
	code.WriteString(fmt.Sprintf("func (p *%s) Clone() *%s {\n", g.analyzed.TypeName, g.analyzed.TypeName))
	code.WriteString(fmt.Sprintf("\tclone := New%s()\n", g.analyzed.TypeName))
	code.WriteString("\tcopy(clone.buf, p.buf)\n")
	code.WriteString("\treturn clone\n")
	code.WriteString("}\n")

	return code.String()
}

// generateFixedAccessors generates Get/Set for fixed fields
func (g *Generator) generateFixedAccessors(region analyzer.Region) string {
	var code strings.Builder
	field := region.Field
	resolvedType := g.registry.ResolveType(field.GoType)
	start := region.Start
	end := region.Boundary

	// Generate getter
	code.WriteString(fmt.Sprintf("// Get%s returns %s at offset %d\n", field.Name, field.GoType, start))
	code.WriteString(fmt.Sprintf("func (p *%s) Get%s() %s {\n", g.analyzed.TypeName, field.Name, field.GoType))

	switch resolvedType {
	case "uint8", "byte":
		code.WriteString(fmt.Sprintf("\treturn p.buf[%d]\n", start))
	case "int8":
		code.WriteString(fmt.Sprintf("\treturn int8(p.buf[%d])\n", start))
	case "uint16":
		code.WriteString(fmt.Sprintf("\treturn *(*uint16)(unsafe.Pointer(&p.buf[%d]))\n", start))
	case "int16":
		code.WriteString(fmt.Sprintf("\treturn *(*int16)(unsafe.Pointer(&p.buf[%d]))\n", start))
	case "uint32":
		code.WriteString(fmt.Sprintf("\treturn *(*uint32)(unsafe.Pointer(&p.buf[%d]))\n", start))
	case "int32":
		code.WriteString(fmt.Sprintf("\treturn *(*int32)(unsafe.Pointer(&p.buf[%d]))\n", start))
	case "uint64":
		code.WriteString(fmt.Sprintf("\treturn *(*uint64)(unsafe.Pointer(&p.buf[%d]))\n", start))
	case "int64":
		code.WriteString(fmt.Sprintf("\treturn *(*int64)(unsafe.Pointer(&p.buf[%d]))\n", start))
	default:
		// Handle arrays and structs
		if strings.HasPrefix(field.GoType, "[") && strings.Contains(field.GoType, "]byte") {
			// Byte array
			code.WriteString(fmt.Sprintf("\tvar v %s\n", field.GoType))
			code.WriteString(fmt.Sprintf("\tcopy(v[:], p.buf[%d:%d])\n", start, end))
			code.WriteString("\treturn v\n")
		} else {
			// Struct type - needs unmarshal
			code.WriteString(fmt.Sprintf("\tvar v %s\n", field.GoType))
			code.WriteString(fmt.Sprintf("\tv.UnmarshalLayout(p.buf[%d:%d])\n", start, end))
			code.WriteString("\treturn v\n")
		}
	}
	code.WriteString("}\n\n")

	// Generate setter
	code.WriteString(fmt.Sprintf("// Set%s sets %s at offset %d\n", field.Name, field.GoType, start))
	code.WriteString(fmt.Sprintf("func (p *%s) Set%s(v %s) {\n", g.analyzed.TypeName, field.Name, field.GoType))

	switch resolvedType {
	case "uint8", "byte":
		code.WriteString(fmt.Sprintf("\tp.buf[%d] = v\n", start))
	case "int8":
		code.WriteString(fmt.Sprintf("\tp.buf[%d] = byte(v)\n", start))
	case "uint16":
		code.WriteString(fmt.Sprintf("\t*(*uint16)(unsafe.Pointer(&p.buf[%d])) = v\n", start))
	case "int16":
		code.WriteString(fmt.Sprintf("\t*(*int16)(unsafe.Pointer(&p.buf[%d])) = v\n", start))
	case "uint32":
		code.WriteString(fmt.Sprintf("\t*(*uint32)(unsafe.Pointer(&p.buf[%d])) = v\n", start))
	case "int32":
		code.WriteString(fmt.Sprintf("\t*(*int32)(unsafe.Pointer(&p.buf[%d])) = v\n", start))
	case "uint64":
		code.WriteString(fmt.Sprintf("\t*(*uint64)(unsafe.Pointer(&p.buf[%d])) = v\n", start))
	case "int64":
		code.WriteString(fmt.Sprintf("\t*(*int64)(unsafe.Pointer(&p.buf[%d])) = v\n", start))
	default:
		// Handle arrays and structs
		if strings.HasPrefix(field.GoType, "[") && strings.Contains(field.GoType, "]byte") {
			// Byte array
			code.WriteString(fmt.Sprintf("\tcopy(p.buf[%d:%d], v[:])\n", start, end))
		} else {
			// Struct type - needs marshal
			code.WriteString("\tbuf, _ := v.MarshalLayout()\n")
			code.WriteString(fmt.Sprintf("\tcopy(p.buf[%d:%d], buf)\n", start, end))
		}
	}
	code.WriteString("}\n\n")

	return code.String()
}

// generateDynamicAccessors generates accessors for dynamic slices
func (g *Generator) generateDynamicAccessors(region analyzer.Region) string {
	var code strings.Builder
	field := region.Field

	// Only generate for struct slices (not []byte which are data regions)
	if field.GoType == "[]byte" || region.ElementType == "byte" {
		return ""
	}

	elementType := region.ElementType
	start := region.Start
	elementSize := region.ElementSize
	countField := field.Layout.CountField

	// Generate count getter
	code.WriteString(fmt.Sprintf("// Get%sCount returns the number of %s elements\n", field.Name, field.Name))
	code.WriteString(fmt.Sprintf("func (p *%s) Get%sCount() int {\n", g.analyzed.TypeName, field.Name))
	code.WriteString(fmt.Sprintf("\treturn int(p.Get%s())\n", countField))
	code.WriteString("}\n\n")

	// Generate element getter
	code.WriteString(fmt.Sprintf("// Get%sAt returns the %s element at index idx\n", field.Name, elementType))
	code.WriteString(fmt.Sprintf("func (p *%s) Get%sAt(idx int) %s {\n", g.analyzed.TypeName, field.Name, elementType))
	code.WriteString(fmt.Sprintf("\tif idx >= p.Get%sCount() {\n", field.Name))
	code.WriteString("\t\tpanic(\"index out of bounds\")\n")
	code.WriteString("\t}\n")
	code.WriteString(fmt.Sprintf("\toffset := %d + idx*%d\n", start, elementSize))
	code.WriteString(fmt.Sprintf("\tvar elem %s\n", elementType))
	code.WriteString(fmt.Sprintf("\telem.UnmarshalLayout(p.buf[offset:offset+%d])\n", elementSize))
	code.WriteString("\treturn elem\n")
	code.WriteString("}\n\n")

	// Generate element setter
	code.WriteString(fmt.Sprintf("// Set%sAt sets the %s element at index idx\n", field.Name, elementType))
	code.WriteString(fmt.Sprintf("func (p *%s) Set%sAt(idx int, elem %s) {\n", g.analyzed.TypeName, field.Name, elementType))
	code.WriteString(fmt.Sprintf("\tif idx >= p.Get%sCount() {\n", field.Name))
	code.WriteString("\t\tpanic(\"index out of bounds\")\n")
	code.WriteString("\t}\n")
	code.WriteString(fmt.Sprintf("\toffset := %d + idx*%d\n", start, elementSize))
	code.WriteString("\tbuf, _ := elem.MarshalLayout()\n")
	code.WriteString(fmt.Sprintf("\tcopy(p.buf[offset:offset+%d], buf)\n", elementSize))
	code.WriteString("}\n\n")

	return code.String()
}

// generateIndirectAccessors generates accessors for indirect slices (Keys/Values)
func (g *Generator) generateIndirectAccessors(field parser.Field) string {
	var code strings.Builder

	// Find the metadata region to calculate elementsEnd
	var metadataRegion *analyzer.Region
	for i := range g.analyzed.Regions {
		region := &g.analyzed.Regions[i]
		if region.Kind == analyzer.DynamicRegion &&
		   region.Direction == parser.StartEnd &&
		   region.ElementType != "byte" &&
		   region.Field.Name == field.Layout.From {
			metadataRegion = region
			break
		}
	}

	if metadataRegion == nil {
		return ""
	}

	// Generate getter
	code.WriteString(fmt.Sprintf("// Get%s returns the %s at index idx\n", field.Name, field.Name))
	code.WriteString(fmt.Sprintf("func (p *%s) Get%s(idx int) []byte {\n", g.analyzed.TypeName, field.Name))
	code.WriteString(fmt.Sprintf("\tif idx >= p.Get%sCount() {\n", metadataRegion.Field.Name))
	code.WriteString("\t\tpanic(\"index out of bounds\")\n")
	code.WriteString("\t}\n")
	code.WriteString(fmt.Sprintf("\telem := p.Get%sAt(idx)\n", metadataRegion.Field.Name))

	// Calculate elementsEnd for offset adjustment
	code.WriteString(fmt.Sprintf("\telementsEnd := %d + p.Get%sCount()*%d\n",
		metadataRegion.Start, metadataRegion.Field.Name, metadataRegion.ElementSize))

	// Handle offset mode
	if field.Layout.OffsetMode == "absolute" {
		code.WriteString(fmt.Sprintf("\tstart := int(elem.%s)\n", field.Layout.OffsetField))
	} else {
		code.WriteString(fmt.Sprintf("\tstart := elementsEnd + int(elem.%s)\n", field.Layout.OffsetField))
	}

	code.WriteString(fmt.Sprintf("\tsize := int(elem.%s)\n", field.Layout.SizeField))
	code.WriteString("\treturn p.buf[start:start+size]\n")
	code.WriteString("}\n\n")

	// Generate in-place setter (requires same size)
	singularName := strings.TrimSuffix(field.Name, "s") // Keys -> Key, Values -> Value
	code.WriteString(fmt.Sprintf("// Set%sInPlace updates %s at index idx (size must match)\n", singularName, field.Name))
	code.WriteString(fmt.Sprintf("func (p *%s) Set%sInPlace(idx int, data []byte) {\n", g.analyzed.TypeName, singularName))
	code.WriteString(fmt.Sprintf("\tif idx >= p.Get%sCount() {\n", metadataRegion.Field.Name))
	code.WriteString("\t\tpanic(\"index out of bounds\")\n")
	code.WriteString("\t}\n")
	code.WriteString(fmt.Sprintf("\telem := p.Get%sAt(idx)\n", metadataRegion.Field.Name))
	code.WriteString(fmt.Sprintf("\tif uint16(len(data)) != elem.%s {\n", field.Layout.SizeField))
	code.WriteString("\t\tpanic(\"size mismatch: use Update instead of SetInPlace\")\n")
	code.WriteString("\t}\n")

	// Calculate start position
	code.WriteString(fmt.Sprintf("\telementsEnd := %d + p.Get%sCount()*%d\n",
		metadataRegion.Start, metadataRegion.Field.Name, metadataRegion.ElementSize))

	if field.Layout.OffsetMode == "absolute" {
		code.WriteString(fmt.Sprintf("\tstart := int(elem.%s)\n", field.Layout.OffsetField))
	} else {
		code.WriteString(fmt.Sprintf("\tstart := elementsEnd + int(elem.%s)\n", field.Layout.OffsetField))
	}

	code.WriteString("\tcopy(p.buf[start:], data)\n")
	code.WriteString("}\n\n")

	return code.String()
}