// Package architecture contains fitness tests that encode
// warehouse-ops-agent's hexagonal dependency rule as executable checks (the
// Go equivalent of ArchUnit), using github.com/arch-go/arch-go — the same
// tool and pattern every sibling bounded-context repo already uses (see
// fulfillment-execution/internal/architecture/architecture_test.go, the
// pilot).
//
// This module is NOT a new bounded context: it owns no aggregate and
// persists no domain state (see internal/domain/policy/doc.go). Its
// dependency rule is correspondingly narrower than a full DDD context's:
// domain (decision policy) depends on nothing, application depends on
// domain/ports, adapters depend on application/domain/ports, and only cmd
// wires every layer together — plus the NON-NEGOTIABLE guardrail from
// PROPOSAL-agentic-warehouse-ops.md §5: no import of any of the five
// bounded contexts' Go packages, ever. This module is their Customer via
// published MCP tool contracts only.
package architecture

import (
	"fmt"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

const modulePath = "github.com/claudioed/warehouse-ops-agent"

// boundedContextModules are the five sibling repos' Go module paths.
// warehouse-ops-agent must never import a package under any of these — it is
// their read-side Customer via published MCP tool contracts, not a Go
// dependent. This is the single most important guardrail carried from the
// proposal and every T-card body into executable form.
var boundedContextModules = []string{
	"github.com/claudioed/fulfillment-execution",
	"github.com/claudioed/wes-work-planning",
	"github.com/claudioed/workforce-management",
	"github.com/claudioed/inventory-storage",
	"github.com/claudioed/facility-layout",
}

// TestNoDirectDependencyOnBoundedContexts asserts that no package in this
// module imports any Go package from the five bounded contexts it reads
// from via MCP. This is a static, compile-time-cheap proof of the
// "published contracts only" guardrail: even if none of those modules are a
// dependency in go.mod today, this test fails loudly the moment one is
// added, rather than relying on code review to catch it.
func TestNoDirectDependencyOnBoundedContexts(t *testing.T) {
	for _, bc := range boundedContextModules {
		t.Run(bc, func(t *testing.T) {
			if hasModuleDependency(t, bc) {
				t.Fatalf(
					"go.mod/go.sum reference %q: warehouse-ops-agent must depend on the five "+
						"bounded contexts' PUBLISHED MCP TOOL CONTRACTS ONLY, never their Go packages "+
						"(PROPOSAL-agentic-warehouse-ops.md §5, ADR-warehouse-ops-agent). Remove the "+
						"import and call the context's MCP tool via internal/adapters/outbound/mcpclient instead.",
					bc,
				)
			}
		})
	}
}

// TestHexagonalDependencyRules enforces this module's inward-pointing
// dependency rule: domain (policy) is pure, application depends only on
// domain and ports, adapters never depend on each other across the
// inbound/outbound boundary, and only cmd wires every layer together.
func TestHexagonalDependencyRules(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("domain (policy) has no internal dependencies except domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.domain.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{"**.domain.**"},
					},
				},
			},
		})

		assertPasses(t, result)
	})

	t.Run("application depends only on domain and ports", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.application.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{"**.domain.**", "**.application.**", "**.ports.**"},
					},
				},
			},
		})

		assertPasses(t, result)
	})

	t.Run("ports depend on nothing internal", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.ports.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{"**.ports.**"},
					},
				},
			},
		})

		assertPasses(t, result)
	})

	t.Run("inbound adapters do not depend on outbound adapters", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.inbound.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.outbound.**"},
					},
				},
			},
		})

		assertPasses(t, result)
	})

	t.Run("outbound adapters do not depend on inbound adapters", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.outbound.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.inbound.**"},
					},
				},
			},
		})

		assertPasses(t, result)
	})

	t.Run("only cmd wires every layer together", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					// Every package under internal/** (domain, application,
					// ports, adapters) must never reach back into cmd/**;
					// only cmd is allowed to import from every layer to wire
					// them together.
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.cmd.**"},
					},
				},
			},
		})

		assertPasses(t, result)
	})
}

// hasModuleDependency reports whether the given module path appears in this
// module's go.mod. It is a lightweight static check (no go/packages load
// needed) matched against the require directives that dependency-graph tools
// like arch-go/go list would otherwise surface after the fact.
func hasModuleDependency(t *testing.T, modulePath string) bool {
	t.Helper()

	data, err := goModContent()
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	return strings.Contains(data, modulePath)
}

func assertPasses(t *testing.T, result *archgo.Result) {
	t.Helper()

	if !result.Pass {
		t.Fatalf("%s", archViolation("dependency", "architecture", describeViolations(result)))
	}
}

func describeViolations(result *archgo.Result) string {
	var b strings.Builder

	if result.DependenciesRuleResult != nil {
		for _, r := range result.DependenciesRuleResult.Results {
			if r.Passes {
				continue
			}

			fmt.Fprintf(&b, "%s\n", r.Description)

			for _, v := range r.Verifications {
				if v.Passes {
					continue
				}

				fmt.Fprintf(&b, "  package %s:\n", v.Package)

				for _, d := range v.Details {
					fmt.Fprintf(&b, "    - %s\n", d)
				}
			}
		}
	}

	return b.String()
}
