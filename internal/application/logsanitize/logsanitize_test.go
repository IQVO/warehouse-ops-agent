package logsanitize

import "testing"

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain identifier is unchanged",
			in:   "path-14-north-conveyor",
			want: "path-14-north-conveyor",
		},
		{
			name: "newline becomes a space",
			in:   "a\nb",
			want: "a b",
		},
		{
			name: "carriage return becomes a space",
			in:   "a\rb",
			want: "a b",
		},
		{
			name: "tab becomes a space",
			in:   "a\tb",
			want: "a b",
		},
		{
			name: "CRLF line-forging attempt collapses to spaces",
			in:   "INFO released ok\r\nERROR forged entry",
			want: "INFO released ok  ERROR forged entry",
		},
		{
			name: "mixed control runes are each mapped",
			in:   "x\n\ry\tz",
			want: "x  y z",
		},
		{
			name: "empty string stays empty",
			in:   "",
			want: "",
		},
		{
			name: "multi-byte UTF-8 is preserved",
			in:   "långrend-strøk-Øst",
			want: "långrend-strøk-Øst",
		},
		{
			name: "only control runes yields spaces",
			in:   "\n\r\t",
			want: "   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.in); got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitizeNoControlRunesSurvive is the property the log-injection query
// relies on: for any input built from the forge alphabet, the output contains
// none of the line-splitting runes.
func TestSanitizeNoControlRunesSurvive(t *testing.T) {
	pieces := []string{"", "ok", "\n", "\r", "\t", "\r\n", "unit-7", "\n\t\r"}
	for _, a := range pieces {
		for _, b := range pieces {
			for _, c := range pieces {
				out := Sanitize(a + b + c)
				for _, r := range out {
					if r == '\n' || r == '\r' || r == '\t' {
						t.Fatalf("Sanitize(%q) = %q still contains %q", a+b+c, out, r)
					}
				}
			}
		}
	}
}
