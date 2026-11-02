package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/claudioed/warehouse-ops-agent/internal/config"
	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type siteStaffingStub struct{}

func (siteStaffingStub) GetStaffingGapForSite(context.Context, string, string, string) (ports.StaffingGap, error) {
	return ports.StaffingGap{}, nil
}

// The site-scoped staffing reader is the same upstream as wfm: built only
// when the workforce-management MCP endpoint is set, and a genuinely nil
// interface otherwise (a typed nil would pass != nil and panic on first use).
func TestNewOutboundClients_SiteStaffingFollowsTheWorkforceEndpoint(t *testing.T) {
	if c := newOutboundClients(config.Config{}); c.wfmSite != nil {
		t.Fatalf("an empty endpoint must build no site reader, got %T", c.wfmSite)
	}
	c := newOutboundClients(config.Config{WorkforceManagement: config.UpstreamConfig{Endpoint: "http://workforce-management-mcp:8090/mcp"}})
	if c.wfmSite == nil || c.wfm == nil {
		t.Fatalf("a set endpoint must build both readers: site=%v wfm=%v", c.wfmSite, c.wfm)
	}
}

func TestNewLaborReallocation_FeatureMatrix(t *testing.T) {
	const a2a = "http://workforce-management-a2a:8093/"
	cases := []struct {
		name       string
		a2aURL     string
		staffing   ports.StaffingGapBySiteClient
		wantErr    bool
		wantRead   bool
		wantAct    bool
		signingKey string
	}{
		{"nothing configured: all off", "", nil, false, false, false, ""},
		{"MCP only: read-only outlook, act off", "", siteStaffingStub{}, false, true, false, ""},
		{"A2A without the staffing reads is a startup error", a2a, nil, true, false, false, ""},
		{"both set: outlook and act", a2a, siteStaffingStub{}, false, true, true, ""},
		{"both set with a pinned signing key", a2a, siteStaffingStub{}, false, true, true, "0123456789abcdef0123456789abcdef"},
		{"a weak signing key is a startup error", a2a, siteStaffingStub{}, true, false, false, "short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{WorkforceManagementA2AURL: tc.a2aURL}
			cfg.Act.SigningKey = tc.signingKey
			cfg.Act.TokenTTL = 5 * 60 * 1e9
			outlook, act, err := newLaborReallocation(cfg, quietLogger(), tc.staffing)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tc.wantErr)
			}
			if (outlook != nil) != tc.wantRead || (act != nil) != tc.wantAct {
				t.Fatalf("outlook=%v act=%v, want read=%t act=%t", outlook != nil, act != nil, tc.wantRead, tc.wantAct)
			}
		})
	}
}
