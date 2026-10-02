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
                 KEDA: reads the publish rate and the backlog, scales the detector
                   │
sensor fleet ──▶ events queue ──▶ detector (1 to 5) ──▶ Postgres ◀── api ◀── console ◀── browser
                   │               │      │                ▲          │
                   ▼               ▼      ▼                │          └──▶ broker management API
              events.dlq        Redis   triage queue ──▶ triage (mock or Gemini)
```

Interactive diagram: [docs/diagrams/agent-shield-architecture.html](docs/diagrams/agent-shield-architecture.html)
(open the file in a browser). Design: [detector scaling, shared detection state and operations console](docs/specs/2026-10-02-detector-scaling-and-console.md),
which builds on the [design record of the first version](docs/specs/2026-08-24-agent-shield-design.md).

Every service shares one event contract ([internal/event](internal/event/event.go)):

| Component | Role |
|---|---|
| `sensor` | Simulates a fleet of customers and agents (in production: an MCP proxy in the agent's tool path). Each agent alternates between bursts of work and idle periods, so the load rises and falls on its own. A configurable share of the events is suspicious. |
| RabbitMQ | The broker. Three durable queues: `events`, `events.dlq` for malformed events, and `triage`. |
| `detector` | Consumes events, runs the four detection rules, stores findings and requests triage. Runs as 1 to 5 replicas. At-least-once delivery: manual acks, idempotent writes, malformed messages dead-lettered. |
| Redis | The shared state of the exfiltration rule. Every detector replica reads and writes it, so detection does not depend on which replica handles which event. |
| KEDA | Autoscales the detector between 1 and 5 replicas on two signals from the `events` queue: the publish rate (140 events per second per replica) and the backlog (100 waiting messages per replica). Scale-in follows a minute after the load drops. |
| `triage` | Second-tier review of rule hits, behind a provider interface. The mock provider is the default wherever the stack is deployed: it produces no verdict and spends no quota. The Gemini provider asks the LLM whether a finding is malicious and is only used once it is switched on explicitly. Each finding records which provider handled it and when. |
| Postgres | Stores the findings. Events are transient; only findings persist. |
| `api` | Read-only REST API over the findings, plus the statistics of the `events` queue, which it reads from the broker's management API. |
| `console` | Web console: live pipeline statistics, a five-minute chart of queue depth and detector count, and the findings table. nginx serves the page and proxies its API calls. |

### Detection rules ([internal/rules](internal/rules/rules.go))

1. `secret_file_read` (HIGH): an agent reads `.env`, SSH keys or cloud credentials.
2. `pipe_to_shell` (CRITICAL): `curl … | sh` patterns.
3. `unknown_domain` (MEDIUM): a web request to a domain outside the allowlist.
4. `exfiltration` (CRITICAL), **stateful**: a web request to a non-allowlisted domain by an agent
   that read a secret at most 10 minutes earlier, measured by event time. The rule fires
   whichever replica handles the read and the request, and in whichever order the two are
   processed. Each finding carries its evidence: the secret read it was correlated with and
   the detectors that handled the two events.

### Triage states

Every finding is in one of four states, shown in the console and derivable from the API fields
`llm_verdict`, `verdict_source` and `triaged_at`:

| State | Meaning |
|---|---|
| pending | Triage has not processed the finding yet. |
| malicious / benign | The LLM returned a verdict; the finding holds its text. |
| no verdict | The Gemini provider processed the finding and gave up without a verdict. |
| mock | The mock provider processed the finding; it never produces a verdict. |

## Run it

Compose and the cluster publish these four ports on localhost and are alternatives, not run together:

| Address | What |
|---|---|
| http://localhost:3000 | console |
| http://localhost:8080 | API |
| localhost:5672 | broker (AMQP) |
| http://localhost:15672 | broker management UI (guest/guest) |

Compose also publishes Postgres on localhost:5432; on the cluster Postgres and Redis stay internal.

### Docker compose

```bash
docker compose up --build
```

A local `.env` is optional (`cp .env.example .env`). Triage runs the mock provider unless `.env`
sets `TRIAGE_PROVIDER=gemini` together with a `GEMINI_API_KEY`. `docker compose down` stops the stack.

### Kubernetes (Docker Desktop)

`dev.ps1` builds, deploys and operates the stack on the Kubernetes cluster built into Docker Desktop.
It runs under Windows PowerShell 5.1; with no arguments it lists its commands.

```powershell
.\dev.ps1 deploy            # build the images, install KEDA if missing, apply the manifests, wait for the rollout
.\dev.ps1 status            # pods, autoscaler state, load and triage provider
.\dev.ps1 load high         # raise the load and watch the detector scale out in the console
.\dev.ps1 simulate-attack   # publish a known attack and find it in the console
.\dev.ps1 down              # remove the stack
```

| Command | What it does |
|---|---|
| `help` | Lists all commands. |
| `build` | Builds all images, the console's included, with the local tag. |
| `deploy` | Checks that compose is down, builds, installs KEDA if missing, applies the manifests and the scaled object, restarts the applications and waits for them. A deploy puts the load back on the low level and triage back on the mock provider. |
| `down` | Removes the stack from the cluster; findings and shared state are lost. `deploy` brings it back. `down all` also removes KEDA. |
| `status` | Shows the pods, the autoscaler state, the load and the triage provider. |
| `test` | Runs the Go tests and the frontend tests. |
| `web` | Runs the console's dev server with hot reload on http://localhost:5173, against the API on localhost:8080. |
| `load low\|high\|off` | Sets the average load of the simulated fleet. `low`: about 10 events per second, 0.5 percent of them suspicious. `high`: about 460 events per second on average with peaks near 600 and no suspicious events, which takes the detector to 5 replicas. `off`: scales the sensor to zero, so the backlog drains and the detector returns to 1 replica. |
| `clean` | Empties the queues, the shared state and the findings. |
| `simulate-attack` | Publishes a known attack: for each of 3 agents a secret read followed by a web request to a non-allowlisted domain. Flags: `-agents N`, `-gap 300ms`, `-out-of-order` (requests published before the reads), `-customer ID`. |
| `triage mock\|gemini` | Sets the triage provider and waits for its rollout. `gemini` needs `GEMINI_API_KEY` in `.env` at the time of the last `deploy`, which creates the secret from it. |

`web` and `test` first install the console's dependencies from the lock file when they are missing.
Under compose the attack simulation runs with `go run ./cmd/attacksim` and the same flags.

## API

Read-only. The console reaches the same routes under the `/api` prefix on port 3000.

| Route | Returns |
|---|---|
| `GET /healthz` | `ok` when the database is reachable. Does not depend on the broker. |
| `GET /findings` | JSON list of findings, newest first. Filters: `severity` (`MEDIUM`, `HIGH` or `CRITICAL`), `customer`, `rule` (one of the four rule names) and `limit` (default 50, at most 500). An invalid value is a 400. |
| `GET /customers` | JSON list of the customer ids that have findings, sorted. |
| `GET /stats` | Statistics of the `events` queue: `ready`, `unacked`, `publish_rate`, `ack_rate` and `consumers` (the number of running detectors). A 503 with an `error` field when the broker's management API does not answer. |

```bash
curl "localhost:8080/findings?severity=CRITICAL&rule=exfiltration"   # findings, newest first
curl "localhost:8080/findings?customer=customer_1&limit=10"          # one tenant
curl "localhost:8080/stats"                                          # queue depth, rates, detector count
```

## Tests

`.\dev.ps1 test`, or `go test ./...` and, in `web`, `npm test`. The rules engine, the API input
validation, the triage providers, the load generator and the attack scenario are covered by Go
tests; the console is rendered in Vitest against a stubbed API. The Redis-backed rule test runs
when `REDIS_ADDR` is set. CI runs vet, the tests with Redis, the format check and, for the console,
typecheck, lint, tests and build on every push to main and on every pull request.

## Design decisions

- **Queue between sensor and detector**: buffers bursts, survives detector downtime and lets several detector replicas share one stream.
- **At-least-once and idempotency, not exactly-once**: the detector acks after the database write; redeliveries are absorbed by a `UNIQUE (event_id, rule)` constraint.
- **Shared state instead of pinned routing**: each detector writes its own record to Redis first and then reads the other kind, so of two replicas working on the read and the request at the same moment at least one sees the other's record.
- **Autoscaling on rate and backlog**: the publish rate sizes the detector for steady load, the backlog adds replicas after a peak, and the autoscaler follows whichever asks for more.
- **Tiered detection**: deterministic rules on every event (cheap, fast), the LLM only on rule hits (slow, costly, smarter). The triage queue absorbs the rate mismatch.
- **Multi-tenancy in the data, not the infrastructure**: one shared pipeline; the customer id travels with every event, finding and API query, and the detection state is keyed by customer and agent.

## Known limits

- Exfiltration through an allowlisted domain is not detected.
- Storing a finding and requesting its triage are not atomic. If a detector dies between the two, the finding is kept but never gets a triage request.
- Shared state is lost when Redis restarts, and expiry uses the detector's clock.
- Reordering between the secret read and the web request is tolerated up to 60 seconds of processing skew.
- If the database is recreated while detectors are running, they must be restarted to set up the schema again.
