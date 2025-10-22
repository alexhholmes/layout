package main

import (
	"fmt"
	"os"

	parser2 "layout/internal/parser"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <file.go>\n", os.Args[0])
		os.Exit(1)
	}

	filename := os.Args[1]
	types, err := parser2.ParseFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(types) == 0 {
		fmt.Println("No types with @layout annotations found")
		return
	}

	for _, t := range types {
		fmt.Printf("\n%s (size=%d, endian=%s)\n", t.Name, t.Anno.Size, t.Anno.Endian)
		fmt.Println("Fields:")
		for _, f := range t.Fields {
			fmt.Printf("  %-15s %-20s ", f.Name, f.GoType)
			if f.Layout.Direction == parser2.Fixed {
				fmt.Printf("@%d", f.Layout.Offset)
			} else {
				if f.Layout.StartAt >= 0 {
					fmt.Printf("@%d,%s", f.Layout.StartAt, f.Layout.Direction)
				} else {
					fmt.Printf("%s", f.Layout.Direction)
				}
			}
			fmt.Println()
		}
	}
}
