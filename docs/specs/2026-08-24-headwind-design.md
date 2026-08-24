# Headwind — AI-Agent Runtime Security Pipeline (Design Spec)

**Date:** 2026-08-24
**Purpose:** Interview-prep project for a Backend Engineer role at Upwind. Applies Upwind's
runtime-monitoring architecture (sensor → queue → detector → store → API) to a newer domain:
monitoring AI agents' tool use. Built in Go, guided mode (Itamar writes the code).

## Goal

A small, honest event pipeline demonstrating: message queues (RabbitMQ), microservices,
Docker, Kubernetes, CI/CD, and multi-tenancy — the Upwind JD requirements Itamar's CV
doesn't yet evidence. Public GitHub repo, defensible line-by-line in an interview.

## Architecture

```
sensors (mock AI agents, one per customer)
   │  publish JSON tool-use events
   ▼
RabbitMQ  ──rejected/malformed──▶ dead-letter queue
   │  consume
   ▼
detector (rules engine, idempotent)
   │  write findings          ┌─▶ Gemini LLM triage (stretch)
   ▼                          │
Postgres (findings only) ◀────┘
   ▲
   │  read
api (REST, stdlib only) ◀── curl / browser
```

Events are transient (queue only, never stored). Only findings persist.

## Components

### sensor (`cmd/sensor`)
Mock AI agent emitting tool-use events every ~2s. Configured via env vars:
`CUSTOMER_ID`, `AGENT_ID`. Compose/K8s runs 2–3 instances posing as different customers.
Mostly benign events (reading project files, fetching docs), occasionally planted
suspicious sequences (reading `.env` then posting to an unknown domain).

Event schema (JSON):
```json
{
  "id": "uuid",
  "ts": "RFC3339",
  "customer_id": "acme",
  "agent_id": "agent-1",
  "tool": "bash_exec | file_read | web_fetch",
  "args": { "command": "...", "path": "...", "url": "..." }
}
```

### detector (`cmd/detector`)
Consumes the queue. Manual acks; malformed messages → dead-letter queue.
Idempotency: unique constraint on event `id` in findings table — duplicate deliveries
don't duplicate findings.

Rules (pure Go functions in `internal/rules`, unit-tested, TDD):
1. **Stateless:** file_read of secret paths (`.env`, `id_rsa`, etc.) → HIGH
2. **Stateless:** bash_exec matching `curl … | sh` pattern → CRITICAL
3. **Stateless:** web_fetch to non-allowlisted domain → MEDIUM
4. **Stateful:** secret read followed later by any web_fetch (same agent) → CRITICAL
   (exfiltration). Requires per-agent in-memory history — bounded window.

### api (`cmd/api`)
Go stdlib HTTP. Endpoints:
- `GET /findings?severity=HIGH&customer=acme` — newest first
- `GET /healthz`

### Storage
Postgres, one `findings` table: id, event_id (unique), customer_id, agent_id, rule,
severity, ts, details (jsonb), llm_verdict (nullable).

### LLM triage (stretch, layer 4)
On rule hit, detector sends event + agent's recent history to Gemini (free-tier API key):
"malicious or benign? severity? why?" — verdict stored on finding. Tiered because LLM
calls are too slow/costly for the full stream.

## Repo layout

```
cmd/sensor/  cmd/detector/  cmd/api/
internal/event/  internal/rules/
docker-compose.yml  Dockerfile  k8s/
.github/workflows/ci.yml  README.md
```

## Delivery layers (build order; cut from the bottom if time runs out)

1. **Core pipeline** — sensor → RabbitMQ → detector (all 4 rules, idempotency, DLQ) → Postgres → api
2. **Docker Compose + CI** — one `docker compose up`; Actions runs `go vet` + `go test`
3. **Kubernetes** — manifests (Deployments, Services) on Docker Desktop's local cluster;
   demo: kill a pod / scale detectors
4. **LLM triage** — Gemini second-tier review

## Testing

- Unit tests: rules package (pure functions), TDD as the Go learning vehicle
- Acceptance: `docker compose up` + curl the API, see findings from planted sequences
- Integration tests: deliberately cut

## Deliberate cuts (interview "future work" answers)

Event forensics store (ElasticSearch), metrics/observability, API auth, config management,
real agent instrumentation (sensor is a mock — the role is the backend, not the sensor).

## Cost

Everything free: Go, RabbitMQ, Postgres, Docker Desktop (personal), GitHub public repo +
Actions, Docker Desktop K8s, Gemini free tier.
