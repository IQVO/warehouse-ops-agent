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
			name: "newline is stripped",
			in:   "a\nb",
			want: "ab",
		},
		{
			name: "carriage return is stripped",
			in:   "a\rb",
			want: "ab",
		},
		{
			name: "tab passes through (cannot forge a line)",
			in:   "a\tb",
			want: "a\tb",
		},
		{
			name: "CRLF line-forging attempt collapses",
			in:   "INFO released ok\r\nERROR forged entry",
			want: "INFO released okERROR forged entry",
		},
		{
			name: "mixed control runes: only CR/LF are stripped",
			in:   "x\n\ry\tz",
			want: "xy\tz",
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
			name: "only newlines yields the empty string",
			in:   "\n\r",
			want: "",
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

// TestSanitizeNoLineSplittingRunesSurvive is the property the
// log-injection query relies on: for any input built from the forge
// alphabet, the output contains neither of the line-splitting runes.
func TestSanitizeNoLineSplittingRunesSurvive(t *testing.T) {
	pieces := []string{"", "ok", "\n", "\r", "\t", "\r\n", "unit-7", "\n\t\r"}
	for _, a := range pieces {
		for _, b := range pieces {
			for _, c := range pieces {
				out := Sanitize(a + b + c)
				for _, r := range out {
					if r == '\n' || r == '\r' {
						t.Fatalf("Sanitize(%q) = %q still contains %q", a+b+c, out, r)
					}
				}
			}
		}
	}
}
