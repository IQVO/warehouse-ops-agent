package mcpclient

import (
	"context"

	"github.com/claudioed/warehouse-ops-agent/internal/ports"
)

// NodeTopology implements ports.NodeTopologyClient by calling
// node-topology's six published READ tools get_node, get_region,
// list_nodes, list_regions, list_lanes and resolve_service_area.
//
// node-topology's MCP server is read-only by its own governance test;
// this client is additionally held read-only from this side: the
// zero-write fitness test (internal/architecture/zerowrite) scans this
// package's tool-name literals, and
// TestNodeTopologyClientCallsOnlyPinnedTools allows exactly these six
// names. The argument keys and result shapes are pinned against
// node-topology's published registry in
// testdata/node_topology_tools.golden.json.
//
// Resilience matches every sibling in this package: a per-call timeout
// (Config.Timeout, 10s default), a fresh session per call, no retry and
// no circuit breaker. Every failure is returned as an ordinary error (a
// validation-slug tool error also matches ports.ErrUpstreamInvalidInput,
// a *-not-found one ports.ErrUpstreamNotFound, ADR 0018); the consuming
// use case decides what to do with it.
type NodeTopology struct {
	session *Session
}

// NewNodeTopology builds a NodeTopology client for the given connection
// config.
func NewNodeTopology(cfg Config) *NodeTopology {
	cfg.Name = "node-topology"
	return &NodeTopology{session: New(cfg)}
}

var _ ports.NodeTopologyClient = (*NodeTopology)(nil)

// GetNode calls get_node for one node id.
func (c *NodeTopology) GetNode(ctx context.Context, nodeID string) (ports.NetworkNode, error) {
	var out ports.NetworkNode
	err := c.session.callTool(ctx, "get_node", map[string]any{"nodeId": nodeID}, &out)
	return out, err
}

// GetRegion calls get_region for one region id.
func (c *NodeTopology) GetRegion(ctx context.Context, regionID string) (ports.NetworkRegion, error) {
	var out ports.NetworkRegion
	err := c.session.callTool(ctx, "get_region", map[string]any{"regionId": regionID}, &out)
	return out, err
}

// ListNodes calls list_nodes. Zero query fields are omitted so
// node-topology answers every node.
func (c *NodeTopology) ListNodes(ctx context.Context, query ports.NodeListQuery) (ports.NetworkNodeList, error) {
	args := map[string]any{}
	setIfNotEmpty(args, "type", query.Type)
	setIfNotEmpty(args, "regionId", query.RegionID)
	setIfNotEmpty(args, "status", query.Status)
	var out ports.NetworkNodeList
	err := c.session.callTool(ctx, "list_nodes", args, &out)
	return out, err
}

// ListRegions calls list_regions. It takes no arguments; node-topology
// answers every region (regions are few and not paginated).
func (c *NodeTopology) ListRegions(ctx context.Context) (ports.NetworkRegionList, error) {
	var out ports.NetworkRegionList
	err := c.session.callTool(ctx, "list_regions", map[string]any{}, &out)
	return out, err
}

// ListLanes calls list_lanes. Empty filters are omitted so
// node-topology answers every lane.
func (c *NodeTopology) ListLanes(ctx context.Context, origin, destination string) (ports.NetworkLaneList, error) {
	args := map[string]any{}
	setIfNotEmpty(args, "origin", origin)
	setIfNotEmpty(args, "destination", destination)
	var out ports.NetworkLaneList
	err := c.session.callTool(ctx, "list_lanes", args, &out)
	return out, err
}

// ResolveServiceArea calls resolve_service_area for one postal prefix
// (matched exactly).
func (c *NodeTopology) ResolveServiceArea(ctx context.Context, postalPrefix string) (ports.ServiceArea, error) {
	var out ports.ServiceArea
	err := c.session.callTool(ctx, "resolve_service_area", map[string]any{"postalPrefix": postalPrefix}, &out)
	return out, err
}
