// Package architecture contains fitness tests that encode
// warehouse-ops-agent's hexagonal dependency rule as executable checks (the
// Go equivalent of ArchUnit), using github.com/arch-go/arch-go.
package architecture

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestMCPAdapterDependencyRule encodes this agent's own governance charter
// (docs/mcp/governance-charter.md, ADR-0008 fleet-wide): the MCP inbound
// adapter is additive, never load-bearing for the composition root. It may
// depend only on the application layer (never outbound adapters, never
// cmd), and — the direction that actually matters for keeping it additive
// — nothing else in this codebase may depend on it. If some other package
// started importing internal/adapters/inbound/mcp, this agent's own MCP
// surface would have silently become a dependency other code relies on
// rather than a pure inbound entrypoint cmd/agent wires up and nothing
// else touches.
func TestMCPAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("mcp adapter depends only on application", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.inbound.mcp.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.inbound.mcp.**",
							"**.application.**",
							"**.domain.**",
							"**.ports.**",
						},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the mcp adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.inbound.mcp.**"},
					},
				},
			},
		})

		assertArchGoPasses(t, result)
	})
}

// assertArchGoPasses is a shared helper for the two-return-shape arch-go
// result, kept local to this file to avoid touching the existing
// assertPasses/describeViolations helpers in architecture_test.go.
func assertArchGoPasses(t *testing.T, result *archgo.Result) {
	t.Helper()

	if result.Pass {
		return
	}

	if result.DependenciesRuleResult != nil {
		for _, r := range result.DependenciesRuleResult.Results {
			if r.Passes {
				continue
			}
			for _, v := range r.Verifications {
				if v.Passes {
					continue
				}
				for _, d := range v.Details {
					t.Errorf("%s: %s", v.Package, d)
				}
			}
		}
	}

	t.FailNow()
}

// ---------------------------------------------------------------------
// Source-scanning fitness test below, same technique as
// internal/architecture/zerowrite's own static scan.
// ---------------------------------------------------------------------

// goFilesUnder returns every non-test .go file under root (relative to the
// module root).
func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// authPatternRE matches the literal shapes a reintroduced bearer/JWT/API-key
// auth middleware would contain in source. Deliberately narrow (case
// sensitive on "Bearer " and known JWT library import paths) so it doesn't
// false-positive on comment-only mentions of the fleet's auth revert — this
// test scans non-comment, non-test Go source only.
var authPatternRE = regexp.MustCompile(`"Bearer |golang-jwt/jwt|dgrijalva/jwt-go|lestrrat-go/jwx`)

// TestNoAuthMiddlewareReintroduced encodes ADR-0006 (superseding ADR-0005):
// this agent's inbound REST/MCP surfaces are deliberately unauthenticated,
// and its outbound MCP-client calls to the five upstreams carry no bearer
// key, as part of the fleet-wide static-bearer-auth revert on 2026-09-11.
// AGENTS.md explicitly says not to resurrect OIDC_ISSUER_URL/MCP_READ_KEY
// env vars or similar from old code/docs. An agent "helpfully" re-adding a
// bearer/JWT middleware to internal/adapters/inbound should fail CI, not
// ship silently.
func TestNoAuthMiddlewareReintroduced(t *testing.T) {
	for _, path := range goFilesUnder(t, "../adapters/inbound") {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		lineNo := 0
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if authPatternRE.MatchString(line) {
				t.Errorf("%s:%d: matches a reintroduced-auth pattern: %q — ADR-0006 (superseding ADR-0005) deliberately removed all auth fleet-wide 2026-09-11 (unauthenticated pending a fresh auth-model decision); if this is intentional, update this test alongside the ADR documenting the new decision", path, lineNo, trimmed)
			}
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
	}
}

// ---------------------------------------------------------------------
// Postgres integration-test sensor (fleet-wide rule; mirrors
// IQVO/wes-work-planning#141 and IQVO/inventory-storage#136).
// warehouse-ops-agent has no Postgres and no `_integration_test.go` today
// (it is an LLM agent over REST/MCP upstreams), so this passes trivially —
// it exists so the rule cannot silently regress if a database or an
// integration suite is ever added here.
// ---------------------------------------------------------------------

const tcPostgresImport = "testcontainers-go/modules/postgres"

// dbEnvGateRE matches the env lookups that gate a Postgres integration test
// on an externally provisioned database.
var dbEnvGateRE = regexp.MustCompile(`os\.(Getenv|LookupEnv)\("(ANALYTICS_)?DATABASE_URL"\)`)

// integrationTestFilesUnder returns every *_integration_test.go under root
// (goFilesUnder deliberately skips _test.go files, so this walks separately).
func integrationTestFilesUnder(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, "_integration_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// touchesPostgres reports whether the test source talks to Postgres through
// pgx/pgxpool or database/sql (import-path check).
func touchesPostgres(content string) bool {
	return strings.Contains(content, `"github.com/jackc/pgx`) ||
		strings.Contains(content, `"database/sql"`)
}

// importsTCPostgres reports whether the file (or, via a shared helper, a
// sibling .go file in the same package directory) imports the
// testcontainers postgres module. A package's tests commonly share one
// helper, so the helper file satisfies the obligation for the directory.
func importsTCPostgres(t *testing.T, path string) bool {
	t.Helper()
	siblings, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.go"))
	if err != nil {
		t.Fatalf("glob siblings of %s: %v", path, err)
	}
	for _, s := range siblings {
		src, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("read %s: %v", s, err)
		}
		if strings.Contains(string(src), `"github.com/testcontainers/`+tcPostgresImport+`"`) {
			return true
		}
	}
	return false
}

// postgresIntegrationViolations returns every way one _integration_test.go
// file breaks the testcontainers-Postgres rule: gating on DATABASE_URL /
// ANALYTICS_DATABASE_URL (env lookup or a DB-env-tied t.Skip) in non-comment
// source, or using a Postgres driver with no testcontainers postgres import
// reachable in its package. Pure over (path) so the violation test can feed
// it a bad fixture.
func postgresIntegrationViolations(t *testing.T, path string) []string {
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
		switch {
		case dbEnvGateRE.MatchString(line):
			out = append(out, fmt.Sprintf("%s:%d: reads a DATABASE_URL env var (%q) — an env-gated Postgres test silently skips wherever the var is unset; start a real database via testcontainers-go/modules/postgres instead", path, lineNo, strings.TrimSpace(line)))
		case strings.Contains(line, "t.Skip") && strings.Contains(strings.ToUpper(line), "DATABASE_URL"):
			out = append(out, fmt.Sprintf("%s:%d: t.Skip tied to a missing DATABASE_URL (%q) — Postgres integration tests must boot their own database via testcontainers, never skip", path, lineNo, strings.TrimSpace(line)))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	if touchesPostgres(string(src)) && !importsTCPostgres(t, path) {
		out = append(out, fmt.Sprintf("%s: uses pgx/database/sql but neither this file nor a sibling .go file in its package imports github.com/testcontainers/testcontainers-go/modules/postgres — Postgres integration tests must start their own database via testcontainers (reuse the package's shared helper)", path))
	}
	return out
}

// TestPostgresIntegrationTestsUseTestcontainers: a `-tags=integration` test
// MUST boot its own database via testcontainers-go/modules/postgres, never
// gate on DATABASE_URL / ANALYTICS_DATABASE_URL + t.Skip (a skip-gated test
// silently proves nothing wherever the variable is unset), and a file that
// touches pgx/database/sql must have the testcontainers postgres import
// reachable in its package.
func TestPostgresIntegrationTestsUseTestcontainers(t *testing.T) {
	// internal/ and cmd/ — the only trees that hold Go sources.
	var paths []string
	for _, root := range []string{"..", "../../cmd"} {
		paths = append(paths, integrationTestFilesUnder(t, root)...)
	}
	for _, path := range paths {
		for _, v := range postgresIntegrationViolations(t, path) {
			t.Error(v)
		}
	}
}

// TestPostgresIntegrationSensorFailsOnBadFixtures proves the sensor above
// can actually fail: each fixture breaks the rule one way and must be
// reported, while a compliant fixture and a comment-only mention must not.
func TestPostgresIntegrationSensorFailsOnBadFixtures(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantErr string // substring of a violation; "" means no violation expected
	}{
		{
			name:    "env gate",
			files:   map[string]string{"bad_integration_test.go": "package x\n\nfunc f() {\n	dsn := os.Getenv(\"DATABASE_URL\")\n	_ = dsn\n}\n"},
			wantErr: "reads a DATABASE_URL env var",
		},
		{
			name:    "analytics env gate",
			files:   map[string]string{"bad_integration_test.go": "package x\n\nfunc f() {\n	_ = os.Getenv(\"ANALYTICS_DATABASE_URL\")\n}\n"},
			wantErr: "reads a DATABASE_URL env var",
		},
		{
			name:    "skip tied to missing db env",
			files:   map[string]string{"bad_integration_test.go": "package x\n\nfunc f(t *testing.T) {\n	t.Skip(\"DATABASE_URL not set\")\n}\n"},
			wantErr: "t.Skip tied to a missing DATABASE_URL",
		},
		{
			name:    "pgx without testcontainers",
			files:   map[string]string{"bad_integration_test.go": "package x\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n\nvar _ *pgxpool.Pool\n"},
			wantErr: "uses pgx/database/sql but neither this file nor a sibling",
		},
		{
			name: "pgx with helper in sibling file",
			files: map[string]string{
				"ok_integration_test.go":     "package x\n\nimport \"github.com/jackc/pgx/v5/pgxpool\"\n\nvar _ *pgxpool.Pool\n",
				"helper_integration_test.go": "package x\n\nimport tcpostgres \"github.com/testcontainers/testcontainers-go/modules/postgres\"\n\nvar _ = tcpostgres.Run\n",
			},
		},
		{
			name:  "comment mention is not a violation",
			files: map[string]string{"ok_integration_test.go": "package x\n\n// never reads os.Getenv(\"DATABASE_URL\") and never t.Skip(\"DATABASE_URL\")\nfunc f() {}\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fixtureViolations(t, tc.files)
			if tc.wantErr == "" {
				if len(got) != 0 {
					t.Fatalf("compliant fixture reported violations: %v", got)
				}
				return
			}
			if !anyContains(got, tc.wantErr) {
				t.Fatalf("sensor did not flag the bad fixture (want substring %q), got %v", tc.wantErr, got)
			}
		})
	}
}

// fixtureViolations writes the fixture files into a temp dir and runs the
// sensor on the one that is not a "helper_" file (helpers exist only to
// satisfy the sibling-import check).
func fixtureViolations(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	var target string
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		if !strings.HasPrefix(name, "helper_") {
			target = p
		}
	}
	return postgresIntegrationViolations(t, target)
}

func anyContains(items []string, sub string) bool {
	for _, s := range items {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// No-struct-tags-in-domain sensor (Tier-2 item 1a). Serialisation (JSON
// wire shape, DB column mapping) is an adapter concern: the domain owns
// behaviour and invariants, adapters own DTOs and their tags. The wire
// shape of GET /runtime-signals lives in the inbound HTTP adapter's DTOs.
// ---------------------------------------------------------------------

// domainTagRE matches a `json:"..."` or `db:"..."` struct tag.
var domainTagRE = regexp.MustCompile("`[^`]*\\b(json|db):\"")

// domainTagWhitelist maps a domain file (slash path relative to the module
// root) to the reason it may legitimately carry such a tag. Empty today:
// the domain has no legitimate exception.
var domainTagWhitelist = map[string]string{}

// domainStructTagViolations returns every json:/db: struct tag in one
// non-test source file; comment-only lines are skipped.
func domainStructTagViolations(t *testing.T, path string) []string {
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
		if domainTagRE.MatchString(line) {
			out = append(out, fmt.Sprintf("%s:%d: struct tag in the domain layer (%q) — JSON/DB shape is an adapter concern; map through an adapter-owned DTO instead", path, lineNo, strings.TrimSpace(line)))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

// TestNoStructTagsInDomain: no json:"…" / db:"…" tags in non-test files
// under internal/domain/**.
func TestNoStructTagsInDomain(t *testing.T) {
	for _, path := range goFilesUnder(t, "../domain") {
		rel := filepath.ToSlash(strings.TrimPrefix(path, "../"))
		if _, ok := domainTagWhitelist["internal/"+rel]; ok {
			continue
		}
		for _, v := range domainStructTagViolations(t, path) {
			t.Error(v)
		}
	}
}

// TestNoStructTagsInDomainSensorFailsOnBadFixtures proves the sensor can
// fail: tagged structs are reported; clean structs, comment mentions and
// non-serialisation tags are not.
func TestNoStructTagsInDomainSensorFailsOnBadFixtures(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"json tag", "package d\n\ntype E struct {\n	ID string `json:\"id\"`\n}\n", true},
		{"json tag with omitempty and sibling tag", "package d\n\ntype E struct {\n	ID string `yaml:\"id\" json:\"id,omitempty\"`\n}\n", true},
		{"db tag", "package d\n\ntype E struct {\n	ID string `db:\"id\"`\n}\n", true},
		{"clean struct", "package d\n\ntype E struct {\n	ID string\n}\n", false},
		{"comment mention", "package d\n\n// E used to carry `json:\"id\"` tags.\ntype E struct {\n	ID string\n}\n", false},
		{"unrelated tag", "package d\n\ntype E struct {\n	ID string `validate:\"required\"`\n}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(p, []byte(tc.src), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := domainStructTagViolations(t, p)
			if tc.wantErr && len(got) == 0 {
				t.Fatalf("sensor did not flag the bad fixture")
			}
			if !tc.wantErr && len(got) != 0 {
				t.Fatalf("compliant fixture reported violations: %v", got)
			}
		})
	}
}
