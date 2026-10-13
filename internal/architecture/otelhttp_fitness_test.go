package architecture

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// Outbound-HTTP tracing sensor. Every outbound http.Client (REST, MCP,
// Prometheus, Loki, Anthropic) must come from
// internal/adapters/outbound/httpclient.New, whose transport is otelhttp:
// a hand-built http.Client or http.DefaultClient silently drops the trace
// at this agent's boundary.
// ---------------------------------------------------------------------

// rawHTTPClientRE matches the shapes that build an un-instrumented
// client: an http.Client composite literal, new(http.Client), or the
// package-level http.DefaultClient / http.Get-style helpers.
var rawHTTPClientRE = regexp.MustCompile(`http\.Client\{|new\(http\.Client\)|http\.DefaultClient|http\.(Get|Head|Post|PostForm)\(`)

// httpClientFactoryDir is the one package allowed to build an
// http.Client, because it installs the otelhttp transport.
const httpClientFactoryDir = "adapters/outbound/httpclient"

// rawHTTPClientViolations returns every non-comment line of one source
// file that builds an http.Client without httpclient.New.
func rawHTTPClientViolations(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	lineNo := 0
	scanner := bufio.NewScanner(strings.NewReader(string(src)))
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if rawHTTPClientRE.MatchString(line) {
			out = append(out, fmt.Sprintf("%s:%d: builds an http.Client without the otelhttp transport (%q); use httpclient.New from internal/adapters/outbound/httpclient", path, lineNo, strings.TrimSpace(line)))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

// TestOutboundHTTPClientsUseOtelhttp scans every non-test Go file under
// internal/ and cmd/ (except the httpclient factory itself).
func TestOutboundHTTPClientsUseOtelhttp(t *testing.T) {
	var paths []string
	for _, root := range []string{"..", "../../cmd"} {
		paths = append(paths, goFilesUnder(t, root)...)
	}
	for _, path := range paths {
		if strings.Contains(filepath.ToSlash(path), httpClientFactoryDir+"/") {
			continue
		}
		for _, v := range rawHTTPClientViolations(t, path) {
			t.Error(v)
		}
	}
}

// TestOutboundHTTPClientSensorFailsOnBadFixtures proves the sensor can
// fail: raw clients are reported; httpclient.New, an http.Server and a
// comment mention are not.
func TestOutboundHTTPClientSensorFailsOnBadFixtures(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"client literal", "package x\n\nvar c = &http.Client{Timeout: 5}\n", true},
		{"new client", "package x\n\nvar c = new(http.Client)\n", true},
		{"default client", "package x\n\nvar c = http.DefaultClient\n", true},
		{"package get", "package x\n\nfunc f() { _, _ = http.Get(\"u\") }\n", true},
		{"factory", "package x\n\nvar c = httpclient.New(5)\n", false},
		{"server", "package x\n\nvar s = &http.Server{Addr: \":1\"}\n", false},
		{"comment", "package x\n\n// HTTPClient used to default to http.DefaultClient.\nvar c = httpclient.New(0)\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(p, []byte(tc.src), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := rawHTTPClientViolations(t, p)
			if tc.wantErr && len(got) == 0 {
				t.Fatal("sensor did not flag the bad fixture")
			}
			if !tc.wantErr && len(got) != 0 {
				t.Fatalf("compliant fixture reported violations: %v", got)
			}
		})
	}
}
