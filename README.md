# AgentShield

Runtime security monitoring for AI agents — a miniature [CNAPP](https://en.wikipedia.org/wiki/Cloud-native_application_protection_platform)-style
event pipeline that watches what AI agents *do* (their tool calls) and flags dangerous
behavior in real time.

Cloud runtime-security products (CrowdStrike, Sysdig, Upwind) monitor workloads at the
kernel level with eBPF sensors. AgentShield applies the same architecture one layer up,
to a newer problem: an AI agent that reads your `.env` and then posts to an unknown
domain is exfiltrating secrets, and no kernel-level tool has the semantic context to
know it. Monitoring the agent's tool-call stream does.

## Architecture

```
sensor (acme)  ──┐
                 ├─ JSON events ──▶ RabbitMQ ──▶ detector ── findings ──▶ Postgres ◀── api ◀── curl
sensor (globex) ─┘                    │              │                       ▲
                                      ▼              └── triage requests ──▶ RabbitMQ ──▶ triage ──▶ Gemini
                                 dead-letter                                                  │
                                    queue                                     verdict UPDATE ─┘
```

Six services, one shared contract ([internal/event](internal/event/event.go)):

| Service | Role |
|---|---|
| `sensor` | Mocks the on-host agent monitor (in production: an MCP proxy in the agent's tool path). Emits benign + occasionally suspicious tool-call events. Two instances run as different customers. |
| `detector` | Consumes events, runs 4 detection rules, stores findings, requests LLM triage. At-least-once delivery: manual acks, idempotent writes, malformed messages dead-lettered. |
| `triage` | Second-tier review: asks Gemini whether each finding is genuinely malicious; verdict lands on the finding row. Queue-based and best-effort — LLMs are slow and rate-limited, so they only see rule hits, never the raw stream. |
| `api` | Read-only REST: `GET /findings?severity=&customer=&limit=`, `GET /healthz`. |

### Detection rules ([internal/rules](internal/rules/rules.go))

1. `secret_file_read` (HIGH) — agent reads `.env`, SSH keys, cloud credentials
2. `pipe_to_shell` (CRITICAL) — `curl … | sh` patterns
3. `unknown_domain` (MEDIUM) — web fetch outside an allowlist
4. `exfiltration` (CRITICAL) — **stateful**: any web request by an agent that previously read a secret

## Run it

```bash
cp .env.example .env   # add your free Gemini API key (optional; triage skips gracefully without traffic)
docker compose up --build
```

Then:

```bash
curl "localhost:8080/findings?severity=CRITICAL"          # findings, newest first
curl "localhost:8080/findings?customer=globex"            # per-tenant view
# RabbitMQ management UI: http://localhost:15672 (guest/guest)
```

Tests: `go test ./...` (rules engine and API input validation are TDD'd; CI runs vet + tests + gofmt on every push).

## Design decisions

- **Queue between sensors and detector** — buffers bursts, survives detector downtime, lets N detector replicas share one stream. Events are transient; only findings persist.
- **At-least-once + idempotency, not exactly-once** — the detector acks after the DB write; redeliveries are absorbed by a `UNIQUE (event_id, rule)` constraint. Cheaper and more honest than pretending exactly-once exists.
- **Tiered detection** — deterministic rules on 100% of events (cheap, fast), LLM only on rule hits (slow, costly, smarter). The triage queue absorbs the rate mismatch.
- **Multi-tenancy in the data, not the infrastructure** — one shared pipeline; `customer_id` travels with every event, finding, and API query.

## Known limits / next steps

- Exfiltration state is in-process — scaling the detector to N replicas needs Redis or partition-by-agent routing
- Sensor is a mock; the real thing would be an MCP proxy wrapping the agent's tools (which could also *block* on verdict)
- Findings deserve an events-forensics store (ElasticSearch) and a real dashboard
- Kubernetes manifests for the full stack
