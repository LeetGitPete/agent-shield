# AgentShield: runtime security pipeline for AI agents

Status: design record of the first version
Date: 2026-08-24

Superseded in part by [Detector scaling, shared detection state and operations console](2026-10-02-detector-scaling-and-console.md), which redesigns the sensor, the exfiltration rule and its state, LLM triage, the API and the findings table. The decisions below are recorded as they were made on the date above and are not updated to follow the code.

## Problem Statement

AI agents act through tool calls: they run shell commands, read files and make web requests. An agent that reads a secret file and then posts to an unknown domain is exfiltrating secrets, and the people responsible for that agent have no place where its tool use is watched and judged.

Runtime security products solve the equivalent problem for cloud workloads with a fixed architecture: a sensor next to the workload, a queue, a detector, a store and an API. That architecture fits the tool use of AI agents as well.

## Solution

A small event pipeline that applies the runtime-monitoring architecture to AI agents' tool use, built in Go.

```
sensors (mock AI agents, one per customer)
   │  publish JSON tool-use events
   ▼
RabbitMQ  ──rejected/malformed──▶ dead-letter queue
   │  consume
   ▼
detector (rules engine, idempotent)
   │  write findings          ┌─▶ Gemini LLM triage (optional)
   ▼                          │
Postgres (findings only) ◀────┘
   ▲
   │  read
api (REST, stdlib only) ◀── curl / browser
```

Events are transient (queue only, never stored). Only findings persist.

The pipeline is built from a message queue (RabbitMQ), separate services, Docker, Kubernetes and a CI workflow, and it is multi-tenant: one shared pipeline serves several customers.

## User Stories

1. As a security analyst, I want every tool call of an agent published as an event, so that the agent's behaviour can be judged outside the agent.
2. As a security analyst, I want a finding when an agent reads a secret file, so that access to credentials is visible.
3. As a security analyst, I want a finding when an agent pipes a download into a shell, so that remote code execution is caught.
4. As a security analyst, I want a finding when an agent fetches a domain outside an allowlist, so that unexpected destinations are visible.
5. As a security analyst, I want a critical finding when an agent that read a secret later makes a web request, so that a likely exfiltration stands out from the single events.
6. As a security analyst, I want to list findings newest first and filter them by severity and customer, so that I can look at one tenant or one severity.
7. As a security analyst, I want an LLM verdict stored on a finding, so that I have a second opinion on whether it is malicious.
8. As a customer, I want my events and findings to carry my customer id, so that tenants stay separate in a shared pipeline.
9. As a platform operator, I want a redelivered event not to create a second finding, so that at-least-once delivery is safe.
10. As a platform operator, I want malformed messages moved to a dead-letter queue, so that they do not block the stream.
11. As a developer, I want the whole stack started by one compose command, so that it runs the same way on any machine.
12. As a developer, I want CI to run vet and the tests on every push, so that regressions are caught.
13. As a platform operator, I want Kubernetes manifests for the stack, so that it runs on a cluster.

## Implementation Decisions

### Sensor (`cmd/sensor`)

Mock AI agent emitting tool-use events every ~2s. Configured via env vars: `CUSTOMER_ID`, `AGENT_ID`. Compose and Kubernetes run 2 to 3 instances posing as different customers. Mostly benign events (reading project files, fetching docs), occasionally planted suspicious sequences (reading `.env` then posting to an unknown domain).

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

### Detector (`cmd/detector`)

Consumes the queue with manual acks. Malformed messages go to the dead-letter queue.

Idempotency: a unique constraint on the event `id` in the findings table, so duplicate deliveries do not duplicate findings.

Rules are pure Go functions in `internal/rules`:

1. Stateless: `file_read` of secret paths (`.env`, `id_rsa`, etc.) → HIGH
2. Stateless: `bash_exec` matching the `curl … | sh` pattern → CRITICAL
3. Stateless: `web_fetch` to a non-allowlisted domain → MEDIUM
4. Stateful: a secret read followed later by any `web_fetch` of the same agent → CRITICAL (exfiltration). Requires per-agent in-memory history with a bounded window.

### API (`cmd/api`)

Go stdlib HTTP. Endpoints:

- `GET /findings?severity=HIGH&customer=acme`, newest first
- `GET /healthz`

### Storage

Postgres, one `findings` table: id, event_id (unique), customer_id, agent_id, rule, severity, ts, details (jsonb), llm_verdict (nullable).

### LLM triage

On a rule hit, the detector sends the event and the agent's recent history to Gemini (free-tier API key) with the question "malicious or benign? severity? why?". The verdict is stored on the finding. Detection is tiered because LLM calls are too slow and costly for the full stream.

### Repository layout

```
cmd/sensor/  cmd/detector/  cmd/api/
internal/event/  internal/rules/
docker-compose.yml  Dockerfile  k8s/
.github/workflows/ci.yml  README.md
```

### Delivery sequence

Layers in build order:

1. Core pipeline: sensor → RabbitMQ → detector (all 4 rules, idempotency, dead-letter queue) → Postgres → api
2. Docker Compose and CI: one `docker compose up`; Actions runs `go vet` and `go test`
3. Kubernetes: manifests (Deployments, Services) on Docker Desktop's local cluster
4. LLM triage: Gemini second-tier review

## Testing Decisions

- Unit tests: the rules package (pure functions), written test-first.
- Acceptance: `docker compose up`, then curl the API and see findings from the planted sequences.
- Integration tests: deliberately left out.

## Out of Scope

- An event forensics store (ElasticSearch).
- Metrics and observability.
- API authentication.
- Configuration management.
- Real agent instrumentation: the sensor is a mock.
