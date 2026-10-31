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
# (empty = client disabled, fail-open). PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT
# is rendered in BOTH the Deployment and the ConfigMap from
# upstreams.processPathManagement.endpoint (ADR 0026 re-added the client
# after ADR 0024's removal; a set value reaches the Deployment verbatim).
grep -q 'SLOTTING_OPTIMIZATION_MCP_ENDPOINT' <<<"$deployment" || fail "upstreams.slottingOptimization.endpoint is not rendered into the Deployment"
grep -q 'PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT' <<<"$deployment" || fail "upstreams.processPathManagement.endpoint is not rendered into the Deployment as PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT"
grep -q 'PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT' <<<"$configmap_default" || fail "upstreams.processPathManagement.endpoint is not rendered into the ConfigMap as PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT"
set_ppm_endpoint="$(helm template "$release" "$chart_dir" --set-string 'upstreams.processPathManagement.endpoint=http://process-path-management.warehouse-systems.svc.cluster.local:8090/mcp' --show-only templates/deployment.yaml)"
grep -q 'PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT' <<<"$set_ppm_endpoint" || fail "upstreams.processPathManagement.endpoint is not rendered into the Deployment"
grep -q 'value: "http://process-path-management.warehouse-systems.svc.cluster.local:8090/mcp"' <<<"$set_ppm_endpoint" ||
  fail "upstreams.processPathManagement.endpoint does not reach the Deployment as PROCESS_PATH_MANAGEMENT_MCP_ENDPOINT verbatim"

# Every one of the twelve ADR-0025 upstream endpoints is rendered into
# BOTH the Deployment env and the ConfigMap (envFrom mounts it), from
# its upstreams.<name>.endpoint value, with exactly the env name the
# binary reads; and a set value reaches the Deployment verbatim.
adr0025_envs="CUSTOMER_ORDERING_MCP_ENDPOINT DELIVERY_PROMISE_MCP_ENDPOINT LAST_MILE_DELIVERY_MCP_ENDPOINT LINEHAUL_TRANSPORTATION_MCP_ENDPOINT NODE_PERFORMANCE_MCP_ENDPOINT NODE_TOPOLOGY_MCP_ENDPOINT OFFER_AVAILABILITY_MCP_ENDPOINT ORDER_SOURCING_MCP_ENDPOINT SHIPPING_AND_TRACKING_MCP_ENDPOINT VENDOR_CONNECTIVITY_MCP_ENDPOINT TASK_INTERLEAVING_MCP_ENDPOINT PICK_FACE_REPLENISHMENT_MCP_ENDPOINT SELLER_INBOUND_MCP_ENDPOINT"
for env_name in $adr0025_envs; do
  grep -q "$env_name" <<<"$deployment" || fail "upstreams.<adr0025>.endpoint is not rendered into the Deployment as $env_name"
  grep -q "$env_name" <<<"$configmap_default" || fail "upstreams.<adr0025>.endpoint is not rendered into the ConfigMap as $env_name"
done
set_vendor_endpoint="$(helm template "$release" "$chart_dir" --set-string 'upstreams.vendorConnectivity.endpoint=http://vendor-connectivity-mcp.warehouse-systems.svc.cluster.local:8090/mcp' --show-only templates/deployment.yaml)"
grep -q 'VENDOR_CONNECTIVITY_MCP_ENDPOINT' <<<"$set_vendor_endpoint" || fail "upstreams.vendorConnectivity.endpoint is not rendered into the Deployment"
grep -q 'value: "http://vendor-connectivity-mcp.warehouse-systems.svc.cluster.local:8090/mcp"' <<<"$set_vendor_endpoint" ||
  fail "upstreams.vendorConnectivity.endpoint does not reach the Deployment as VENDOR_CONNECTIVITY_MCP_ENDPOINT verbatim"
set_seller_endpoint="$(helm template "$release" "$chart_dir" --set-string 'upstreams.sellerInbound.endpoint=http://seller-inbound-mcp.warehouse-systems.svc.cluster.local:8090/mcp' --show-only templates/deployment.yaml)"
grep -q 'SELLER_INBOUND_MCP_ENDPOINT' <<<"$set_seller_endpoint" || fail "upstreams.sellerInbound.endpoint is not rendered into the Deployment"
grep -q 'value: "http://seller-inbound-mcp.warehouse-systems.svc.cluster.local:8090/mcp"' <<<"$set_seller_endpoint" ||
  fail "upstreams.sellerInbound.endpoint does not reach the Deployment as SELLER_INBOUND_MCP_ENDPOINT verbatim"

# The act slice (ADR 0027) is the only write route. Behind Kong it gets its OWN
# route with a GET/OPTIONS-only cors plugin, so a browser's preflight for POST
# is refused at the edge, while the main route's cors plugin (all methods)
# stays for /mcp. Rendered only when the Gateway API route AND cors are on.
edge_args=(--set gatewayApi.enabled=true --set kongPlugins.cors.enabled=true --set kongPlugins.prometheus.enabled=true
  --set 'gatewayApi.hosts[0].path=/api/warehouse-ops-agent' --set gatewayApi.stripPath=true
  --set 'gatewayApi.parentRefs[0].name=kong' --set 'gatewayApi.parentRefs[0].namespace=kong')
act_route="$(helm template "$release" "$chart_dir" "${edge_args[@]}" --show-only templates/httproute-act.yaml)"
grep -q 'value: /api/warehouse-ops-agent/act$' <<<"$act_route" || fail "the act route does not match <host path>/act"
grep -q 'replacePrefixMatch: /act$' <<<"$act_route" || fail "the act route does not rewrite the stripped prefix back to /act"
grep -q 'konghq.com/plugins: ".*-cors-readonly' <<<"$act_route" || fail "the act route is not attached to the read-only cors plugin"
if grep -E 'konghq.com/plugins' <<<"$act_route" | grep -qE -- '-cors([",])'; then
  fail "the act route is attached to the all-methods cors plugin"
fi
grep -q 'prometheus' <<<"$act_route" || fail "the act route lost the prometheus plugin"

plugins="$(helm template "$release" "$chart_dir" "${edge_args[@]}" --show-only templates/kong-plugins.yaml)"
# Bounded slices after each plugin's name line (each block is far shorter than
# the gap to the next one), portable across awk implementations.
readonly_cors="$(grep -A 20 'name: [^ ]*-cors-readonly$' <<<"$plugins" || true)"
[ -n "$readonly_cors" ] || fail "the cors-readonly KongPlugin is not rendered"
grep -q '^    - GET$' <<<"$readonly_cors" && grep -q '^    - OPTIONS$' <<<"$readonly_cors" || fail "cors-readonly does not allow GET and OPTIONS"
for m in POST PUT PATCH DELETE; do
  if grep -q "^    - $m\$" <<<"$readonly_cors"; then fail "cors-readonly allows $m — the whole point is that it must not"; fi
done
grep -q 'cors-readonly' <<<"$(helm template "$release" "$chart_dir" "${edge_args[@]}" --show-only templates/httproute.yaml)" &&
  fail "the MAIN route got the read-only cors plugin — /mcp would lose POST from a browser MCP client"
main_cors="$(grep -A 22 'name: [^ ]*-cors$' <<<"$plugins" || true)"
grep -q '^    - POST$' <<<"$main_cors" || fail "the main cors plugin lost POST — browser MCP clients need it"

# No cors plugin (or no Gateway API route) = no CORS at the edge to narrow = no act route.
for off in "--set gatewayApi.enabled=true" "--set kongPlugins.cors.enabled=true"; do
  # shellcheck disable=SC2086
  out="$(helm template "$release" "$chart_dir" $off --show-only templates/httproute-act.yaml 2>&1 || true)"
  grep -q 'kind: HTTPRoute' <<<"$out" && fail "the act route rendered with only: $off"
done
echo "warehouse-ops-agent chart render assertions passed"
