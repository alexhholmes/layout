package parser

import (
	"testing"
)

func TestParseAnnotation(t *testing.T) {
	tests := []struct {
		comment    string
		wantSize   int
		wantEndian string
		wantErr    bool
	}{
		// Valid annotations
		{"@layout size=4096", 4096, "little", false},
		{"@layout size=8192", 8192, "little", false},
		{"@layout size=4096 endian=big", 4096, "big", false},
		{"@layout size=4096 endian=little", 4096, "little", false},
		{"@layout endian=big size=4096", 4096, "big", false}, // Order doesn't matter
		{"@layout", 0, "little", false},                      // no params, size will be calculated
		{"@layout endian=big", 0, "big", false},              // size optional, will be calculated

		// Error cases
		{"", 0, "", true},                                     // no annotation
		{"size=4096", 0, "", true},                            // missing @layout
		{"@layout size=abc", 0, "", true},                     // non-numeric size
		{"@layout size=-1", 0, "", true},                      // negative size
		{"@layout size=0", 0, "", true},                       // zero size (explicit 0 is invalid)
		{"@layout size=4096 endian=foo", 0, "", true},         // invalid endian
		{"@layout size=4096 unknown=bar", 0, "", true},        // unknown param
	}

	for _, tt := range tests {
		t.Run(tt.comment, func(t *testing.T) {
			got, err := ParseAnnotation(tt.comment)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseAnnotation(%q) expected error, got nil", tt.comment)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseAnnotation(%q) unexpected error: %v", tt.comment, err)
			}

			if got.Size != tt.wantSize {
				t.Errorf("ParseAnnotation(%q).Size = %d, want %d", tt.comment, got.Size, tt.wantSize)
			}

			if got.Endian != tt.wantEndian {
				t.Errorf("ParseAnnotation(%q).Endian = %q, want %q", tt.comment, got.Endian, tt.wantEndian)
			}
		})
	}
}

func TestCleanComment(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"// @layout size=4096", "@layout size=4096"},
		{"  //   @layout size=4096  ", "@layout size=4096"},
		{"/* @layout size=4096 */", "@layout size=4096"},
		{"  /*  @layout size=4096  */  ", "@layout size=4096"},
		{"@layout size=4096", "@layout size=4096"}, // no markers
		{"", ""},
	}

	for _, tt := range tests {
		got := CleanComment(tt.input)
		if got != tt.want {
			t.Errorf("CleanComment(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFindAnnotation(t *testing.T) {
	tests := []struct {
		name       string
		comments   []string
		wantSize   int
		wantEndian string
		wantFound  bool
	}{
		{
			name: "found in first line",
			comments: []string{
				"@layout size=4096",
				"other comment",
			},
			wantSize:   4096,
			wantEndian: "little",
			wantFound:  true,
		},
		{
			name: "found in second line",
			comments: []string{
				"Package comment",
				"@layout size=8192 endian=big",
			},
			wantSize:   8192,
			wantEndian: "big",
			wantFound:  true,
		},
		{
			name: "not found",
			comments: []string{
				"Just a comment",
				"Another comment",
			},
			wantFound: false,
		},
		{
			name:      "empty comments",
			comments:  []string{},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := FindAnnotation(tt.comments)

			if found != tt.wantFound {
				t.Errorf("FindAnnotation() found = %v, want %v", found, tt.wantFound)
				return
			}

			if !tt.wantFound {
				return
			}

			if got.Size != tt.wantSize {
				t.Errorf("FindAnnotation().Size = %d, want %d", got.Size, tt.wantSize)
			}

			if got.Endian != tt.wantEndian {
				t.Errorf("FindAnnotation().Endian = %q, want %q", got.Endian, tt.wantEndian)
			}
		})
	}
}