#!/usr/bin/env bash
# Chart render assertions for warehouse-ops-agent, run in CI (chart-tests).
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release=warehouse-ops-agent

fail() {
  echo "$1" >&2
  exit 1
}

configmap_default="$(helm template "$release" "$chart_dir" --show-only templates/configmap.yaml)"
deployment="$(helm template "$release" "$chart_dir" --show-only templates/deployment.yaml)"

# The Deployment mounts the ConfigMap, so every config.* value reaches the
# binary (an unmounted ConfigMap silently does nothing).
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap_default")"
grep -A3 'envFrom:' <<<"$deployment" | grep -q 'configMapRef:' || fail "Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $configmap_name" || fail "Deployment envFrom does not reference ConfigMap $configmap_name"

# config.corsAllowedOrigins: rendered as CORS_ALLOWED_ORIGINS when set, absent
# when empty (the binary then keeps its http://localhost:5173 default).
if grep -q 'CORS_ALLOWED_ORIGINS' <<<"$configmap_default"; then
  fail "CORS_ALLOWED_ORIGINS rendered although config.corsAllowedOrigins is empty"
fi
cors_set="$(helm template "$release" "$chart_dir" --set-string 'config.corsAllowedOrigins=http://console.local\,http://localhost:5173' --show-only templates/configmap.yaml)"
grep -q 'CORS_ALLOWED_ORIGINS: "http://console.local,http://localhost:5173"' <<<"$cors_set" || fail "config.corsAllowedOrigins is not rendered into the ConfigMap as CORS_ALLOWED_ORIGINS"

# LOG_LEVEL=warning is passed through unchanged (the binary accepts it like warn).
warning_level="$(helm template "$release" "$chart_dir" --set config.logLevel=warning --show-only templates/configmap.yaml)"
grep -q 'LOG_LEVEL: "warning"' <<<"$warning_level" || fail "config.logLevel=warning is not rendered as LOG_LEVEL"

# config.slotPlanStaleAge: rendered as SLOT_PLAN_STALE_AGE when set, absent
# when empty (the binary then uses its own 24h default, ADR 0024).
if grep -q 'SLOT_PLAN_STALE_AGE' <<<"$configmap_default"; then
  fail "SLOT_PLAN_STALE_AGE rendered although config.slotPlanStaleAge is empty"
fi
stale_set="$(helm template "$release" "$chart_dir" --set-string 'config.slotPlanStaleAge=12h' --show-only templates/configmap.yaml)"
grep -q 'SLOT_PLAN_STALE_AGE: "12h"' <<<"$stale_set" || fail "config.slotPlanStaleAge is not rendered into the ConfigMap as SLOT_PLAN_STALE_AGE"

# SLOTTING_OPTIMIZATION_MCP_ENDPOINT is always rendered in the Deployment
# (empty = client disabled, fail-open), and the removed
# process-path-management client's env var never comes back (ADR 0024,
# gap A9).
grep -q 'SLOTTING_OPTIMIZATION_MCP_ENDPOINT' <<<"$deployment" || fail "upstreams.slottingOptimization.endpoint is not rendered into the Deployment"
if grep -q 'PROCESS_PATH_MANAGEMENT' <<<"$deployment"; then
  fail "PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT still rendered: the client was removed (ADR 0024)"
fi

# Every one of the twelve ADR-0025 upstream endpoints is rendered into
# BOTH the Deployment env and the ConfigMap (envFrom mounts it), from
# its upstreams.<name>.endpoint value, with exactly the env name the
# binary reads; and a set value reaches the Deployment verbatim.
adr0025_envs="CUSTOMER_ORDERING_MCP_ENDPOINT DELIVERY_PROMISE_MCP_ENDPOINT LAST_MILE_DELIVERY_MCP_ENDPOINT LINEHAUL_TRANSPORTATION_MCP_ENDPOINT NODE_PERFORMANCE_MCP_ENDPOINT NODE_TOPOLOGY_MCP_ENDPOINT OFFER_AVAILABILITY_MCP_ENDPOINT ORDER_SOURCING_MCP_ENDPOINT SHIPPING_AND_TRACKING_MCP_ENDPOINT VENDOR_CONNECTIVITY_MCP_ENDPOINT TASK_INTERLEAVING_MCP_ENDPOINT PICK_FACE_REPLENISHMENT_MCP_ENDPOINT"
for env_name in $adr0025_envs; do
  grep -q "$env_name" <<<"$deployment" || fail "upstreams.<adr0025>.endpoint is not rendered into the Deployment as $env_name"
  grep -q "$env_name" <<<"$configmap_default" || fail "upstreams.<adr0025>.endpoint is not rendered into the ConfigMap as $env_name"
done
set_vendor_endpoint="$(helm template "$release" "$chart_dir" --set-string 'upstreams.vendorConnectivity.endpoint=http://vendor-connectivity-mcp.warehouse-systems.svc.cluster.local:8090/mcp' --show-only templates/deployment.yaml)"
grep -q 'VENDOR_CONNECTIVITY_MCP_ENDPOINT' <<<"$set_vendor_endpoint" || fail "upstreams.vendorConnectivity.endpoint is not rendered into the Deployment"
grep -q 'value: "http://vendor-connectivity-mcp.warehouse-systems.svc.cluster.local:8090/mcp"' <<<"$set_vendor_endpoint" ||
  fail "upstreams.vendorConnectivity.endpoint does not reach the Deployment as VENDOR_CONNECTIVITY_MCP_ENDPOINT verbatim"
# Nothing the binary does not read is rendered: the removed
# process-path-management name must stay absent from the ConfigMap too.
if grep -q 'PROCESS_PATH_MANAGEMENT' <<<"$configmap_default"; then
  fail "PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT still rendered into the ConfigMap: the client was removed (ADR 0024)"
fi

echo "warehouse-ops-agent chart render assertions passed"
