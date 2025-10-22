package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TypeAnnotation holds parsed @layout annotation
type TypeAnnotation struct {
	Size   int    // Buffer size in bytes
	Endian string // "little" or "big"
}

// ParseAnnotation parses @layout annotation from comment text
//
// Expected format:
//   // @layout size=4096
//   // @layout size=4096 endian=big
//   // @layout size=8192 endian=little
//
// Params are space-separated key=value pairs
func ParseAnnotation(comment string) (*TypeAnnotation, error) {
	// Match: @layout <params>
	re := regexp.MustCompile(`@layout\s+(.+)`)
	matches := re.FindStringSubmatch(comment)
	if len(matches) < 2 {
		return nil, fmt.Errorf("no @layout annotation found")
	}

	params := matches[1]
	return parseLayoutParams(params)
}

func parseLayoutParams(params string) (*TypeAnnotation, error) {
	anno := &TypeAnnotation{
		Endian: "little", // Default
	}

	// Extract key=value pairs: "size=4096 endian=big"
	pairRe := regexp.MustCompile(`(\w+)=(\w+)`)
	pairs := pairRe.FindAllStringSubmatch(params, -1)

	if len(pairs) == 0 {
		return nil, fmt.Errorf("no parameters found in: %s", params)
	}

	foundSize := false
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
			foundSize = true

		case "endian":
			if value != "little" && value != "big" {
				return nil, fmt.Errorf("endian must be 'little' or 'big', got: %s", value)
			}
			anno.Endian = value

		default:
			return nil, fmt.Errorf("unknown parameter: %s", key)
		}
	}

	if !foundSize {
		return nil, fmt.Errorf("size parameter is required")
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