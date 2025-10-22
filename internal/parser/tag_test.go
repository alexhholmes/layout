package parser

import (
	"testing"
)

func TestParseTag(t *testing.T) {
	tests := []struct {
		tag       string
		wantOff   int
		wantDir   PackDirection
		wantStart int
		wantCount string
		wantErr   bool
	}{
		// Fixed offsets (no direction)
		{"@0", 0, Fixed, -1, "", false},
		{"@4", 4, Fixed, -1, "", false},
		{"@16", 16, Fixed, -1, "", false},
		{"@4096", 4096, Fixed, -1, "", false},

		// Pure directional packing (start calculated from context)
		{"start-end", -1, StartEnd, -1, "", false},
		{"end-start", -1, EndStart, -1, "", false},

		// With count field
		{"start-end,count=BodyLen", -1, StartEnd, -1, "BodyLen", false},
		{"end-start,count=NumElements", -1, EndStart, -1, "NumElements", false},

		// Combined: offset + direction (dynamic region starting at offset)
		{"@8,start-end", -1, StartEnd, 8, "", false},
		{"@16,end-start", -1, EndStart, 16, "", false},
		{"@1999,end-start", -1, EndStart, 1999, "", false},
		{"@2000,start-end", -1, StartEnd, 2000, "", false},

		// Combined with count
		{"@8,start-end,count=Len", -1, StartEnd, 8, "Len", false},
		{"@1999,end-start,count=N", -1, EndStart, 1999, "N", false},

		// Error cases
		{"", 0, 0, 0, "", true},                     // empty
		{"@", 0, 0, 0, "", true},                    // no offset number
		{"@abc", 0, 0, 0, "", true},                 // non-numeric offset
		{"invalid", 0, 0, 0, "", true},              // unknown direction
		{"@0,invalid", 0, 0, 0, "", true},           // bad direction after offset
		{"@8,@16", 0, 0, 0, "", true},               // double offset
		{"start-end,count=", 0, 0, 0, "", true},     // empty count
		{"start-end,unknown=foo", 0, 0, 0, "", true}, // unknown param
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got, err := ParseTag(tt.tag)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseTag(%q) expected error, got nil", tt.tag)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseTag(%q) unexpected error: %v", tt.tag, err)
			}

			if got.Offset != tt.wantOff {
				t.Errorf("ParseTag(%q).Offset = %d, want %d", tt.tag, got.Offset, tt.wantOff)
			}

			if got.Direction != tt.wantDir {
				t.Errorf("ParseTag(%q).Direction = %v, want %v", tt.tag, got.Direction, tt.wantDir)
			}

			if got.StartAt != tt.wantStart {
				t.Errorf("ParseTag(%q).StartAt = %d, want %d", tt.tag, got.StartAt, tt.wantStart)
			}

			if got.CountField != tt.wantCount {
				t.Errorf("ParseTag(%q).CountField = %q, want %q", tt.tag, got.CountField, tt.wantCount)
			}
		})
	}
}

func TestPackDirectionString(t *testing.T) {
	tests := []struct {
		dir  PackDirection
		want string
	}{
		{Fixed, "fixed"},
		{StartEnd, "start-end"},
		{EndStart, "end-start"},
		{PackDirection(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.dir.String(); got != tt.want {
			t.Errorf("PackDirection(%d).String() = %q, want %q", tt.dir, got, tt.want)
		}
	}
}