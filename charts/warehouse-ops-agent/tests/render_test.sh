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

echo "warehouse-ops-agent chart render assertions passed"
