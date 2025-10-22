package parser

import (
	"fmt"
)

// Example demonstrating tag parsing for a complex layout
func ExampleParseTag() {
	// Example layout with fixed fields, dynamic regions, and count fields
	// // @layout size=4096
	// type Page struct {
	//   Header      uint64 `layout:"@0"`
	//   BodyLen     uint16 `layout:"@8"`
	//   Body        []byte `layout:"start-end,count=BodyLen"`
	//   Footer      uint64 `layout:"@4088"`
	//   NumElements uint16 `layout:"@10"`
	//   Elements    []Item `layout:"end-start,count=NumElements"`
	// }

	tags := []string{
		"@0",                          // Header: fixed at byte 0
		"@8",                          // BodyLen: fixed at byte 8
		"start-end,count=BodyLen",     // Body: grow forward, length from BodyLen
		"@4088",                       // Footer: fixed at byte 4088
		"@10",                         // NumElements: fixed at byte 10
		"end-start,count=NumElements", // Elements: grow backward, count from NumElements
	}

	for i, tag := range tags {
		layout, err := ParseTag(tag)
		if err != nil {
			fmt.Printf("Field%d: ERROR: %v\n", i+1, err)
			continue
		}

		fmt.Printf("Field%d (%s): ", i+1, tag)
		if layout.Direction == Fixed {
			fmt.Printf("fixed at byte %d\n", layout.Offset)
		} else {
			fmt.Printf("%s", layout.Direction)
			if layout.StartAt >= 0 {
				fmt.Printf(" from byte %d", layout.StartAt)
			} else {
				fmt.Printf(" (start calculated)")
			}
			if layout.CountField != "" {
				fmt.Printf(", count=%s", layout.CountField)
			}
			fmt.Println()
		}
	}

	// Output:
	// Field1 (@0): fixed at byte 0
	// Field2 (@8): fixed at byte 8
	// Field3 (start-end,count=BodyLen): start-end (start calculated), count=BodyLen
	// Field4 (@4088): fixed at byte 4088
	// Field5 (@10): fixed at byte 10
	// Field6 (end-start,count=NumElements): end-start (start calculated), count=NumElements
}