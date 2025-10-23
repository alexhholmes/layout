package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TypeAnnotation holds parsed @layout annotation
type TypeAnnotation struct {
	Size      int    // Buffer size in bytes
	Endian    string // "little" or "big"
	Mode      string // "copy" or "zerocopy"
	Align     int    // Alignment in bytes (0 = no alignment requirement)
	Allocator string // Custom allocator function name (optional)
}

// ParseAnnotation parses @layout annotation from comment text
//
// Expected format:
//   // @layout
//   // @layout size=4096
//   // @layout size=4096 endian=big
//   // @layout size=8192 endian=little
//
// Params are space-separated key=value pairs. Size is optional and will be calculated from fields if not specified.
func ParseAnnotation(comment string) (*TypeAnnotation, error) {
	// Match: @layout with optional params
	re := regexp.MustCompile(`@layout(?:\s+(.+))?`)
	matches := re.FindStringSubmatch(comment)
	if len(matches) < 1 {
		return nil, fmt.Errorf("no @layout annotation found")
	}

	// If no params, return default annotation with size=0 (calculate from fields)
	if len(matches) < 2 || matches[1] == "" {
		return &TypeAnnotation{
			Endian: "little",
			Mode:   "copy",
			Size:   0,
		}, nil
	}

	params := matches[1]
	return parseLayoutParams(params)
}

func parseLayoutParams(params string) (*TypeAnnotation, error) {
	anno := &TypeAnnotation{
		Endian: "little", // Default
		Mode:   "copy",   // Default
		Size:   0,        // 0 means calculate from fields
	}

	// Extract key=value pairs: "size=4096 endian=big"
	// Allow negative numbers in values
	pairRe := regexp.MustCompile(`(\w+)=([\w-]+)`)
	pairs := pairRe.FindAllStringSubmatch(params, -1)

	// Allow @layout with no parameters (size will be calculated)
	if len(pairs) == 0 {
		return anno, nil
	}

	for _, pair := range pairs {
		key := pair[1]
		value := pair[2]

		switch key {
		case "size":
			size, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid size: %s", value)
			}
			if size <= 0 {
				return nil, fmt.Errorf("size must be positive, got: %d", size)
			}
			anno.Size = size

		case "endian":
			if value != "little" && value != "big" {
				return nil, fmt.Errorf("endian must be 'little' or 'big', got: %s", value)
			}
			anno.Endian = value

		case "mode":
			if value != "copy" && value != "zerocopy" {
				return nil, fmt.Errorf("mode must be 'copy' or 'zerocopy', got: %s", value)
			}
			anno.Mode = value

		case "align":
			align, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid align value: %s", value)
			}
			if align <= 0 || (align&(align-1)) != 0 {
				return nil, fmt.Errorf("align must be a power of 2, got: %d", align)
			}
			anno.Align = align

		case "allocator":
			anno.Allocator = value

		default:
			return nil, fmt.Errorf("unknown parameter: %s", key)
		}
	}

	return anno, nil
}

// FindAnnotation searches comment lines for @layout annotation
// Returns the annotation and true if found
func FindAnnotation(comments []string) (*TypeAnnotation, bool) {
	for _, comment := range comments {
		// Try to parse this line
		anno, err := ParseAnnotation(comment)
		if err == nil {
			return anno, true
		}
	}
	return nil, false
}

// CleanComment removes comment markers from a line
// "// @layout size=4096" → "@layout size=4096"
// "/* @layout size=4096 */" → "@layout size=4096"
func CleanComment(line string) string {
	line = strings.TrimSpace(line)

	// Remove // prefix
	if strings.HasPrefix(line, "//") {
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		return line
	}

	// Remove /* */ wrapper
	if strings.HasPrefix(line, "/*") && strings.HasSuffix(line, "*/") {
		line = strings.TrimPrefix(line, "/*")
		line = strings.TrimSuffix(line, "*/")
		line = strings.TrimSpace(line)
		return line
	}

	return line
}