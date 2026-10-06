---
id: 0017-empty-path-targets-is-a-config-error
title: "0017 — An empty DAILY_BRIEF_PATH_TARGETS list is a startup config error"
sidebar_label: "0017 · Empty path targets fail startup"
description: A DAILY_BRIEF_PATH_TARGETS that is set to an empty JSON array (or null) now fails startup with a config error naming the variable; unset still selects the built-in default target.
---

# ADR 0017: An empty `DAILY_BRIEF_PATH_TARGETS` list is a startup config error

## Status

Accepted (decided 2026-10-06 on the 2026-10-05 audit findings). Refines
[ADR 0016](./0016-rest-error-mapping-cors-and-strict-path-target-config.md)
decision 3, which left an explicitly empty array selecting the built-in default.
ADR 0016 is not edited; this record supersedes only that one sentence.

## Context

ADR 0016 made a set-but-unparseable `DAILY_BRIEF_PATH_TARGETS` a startup
error but kept `[]` (and JSON `null`) silent: `config.loadPathTargets` returned
the built-in default target (`WH1` / `pick-zone-a`). That leaves one
misconfiguration invisible. An operator who writes `[]` either made a mistake
(a templating step produced an empty list) or means "monitor nothing" — an
intent the daily brief cannot honour, because a brief that monitors no path
is useless, and quietly substituting the demo path would brief the wrong
building under a healthy-looking 200.

## Decision

1. **Unset (or the empty string) uses the default target** — unchanged. The
   Helm chart's empty `config.dailyBriefPathTargets` default renders no
   variable at all, so it is unaffected.
2. **Set to unparseable JSON** is a config error naming the variable —
   unchanged (ADR 0016).
3. **Set to an empty array `[]` (or `null`)** is now **also** a config error.
   `config.Load` returns it, `cmd/agent` exits non-zero before serving. The
   message names `DAILY_BRIEF_PATH_TARGETS` and says how to get the default:
   *unset the variable*, or list at least one target.

No new variable, no new default, no change to the `PathTarget` shape.

## Consequences

**Positive** — fail fast: the mistake surfaces in the rollout, not in a brief
about the wrong path. The rule is now uniform: the default is reachable only
by not setting the variable.

**Negative / contract notes** — a deployment that currently carries
`DAILY_BRIEF_PATH_TARGETS='[]'` will crash on start until the variable is unset
or filled. This is intended and visible in the rollout. REST, MCP and event
contracts are unchanged (this service has no Kafka I/O).
