# Detector scaling, shared detection state and operations console

Status: ready for implementation
Date: 2026-10-02

## Problem Statement

AgentShield watches the tool calls of AI agents and raises findings for dangerous behaviour. Its most valuable rule, exfiltration, is stateful: it fires when an agent that read a secret later makes a web request. That state lives in memory inside the single detector process.

This causes four problems for the people who run and use the pipeline:

1. The detector cannot be scaled. With two replicas, one sees the secret read and the other sees the web request, and the exfiltration is missed. The detector is therefore pinned to one replica and its throughput caps the whole pipeline.
2. The state is lost whenever the detector restarts.
3. There is no way to see what the pipeline is doing. Findings are only reachable through a JSON endpoint, and queue depth, throughput and the number of running detectors are not visible anywhere in the product.
4. The pipeline is hard to exercise. The sensor emits one event every two seconds per customer, so there is no load to scale against, no way to trigger a known attack on command, and the LLM triage stage cannot run without a provider key or without spending provider quota.

## Solution

The detector becomes horizontally scalable, and the pipeline becomes observable and exercisable.

- Exfiltration state moves to Redis and is shared by every detector replica. Detection is correct regardless of which replica handles which event and regardless of the order in which the two events are processed.
- The detector Deployment is autoscaled by KEDA on two signals from the events queue: the publish rate and the backlog. It runs between 1 and 5 replicas.
- The sensor becomes a configurable fleet simulator: many customers and agents, each agent alternating between bursts of work and idle periods, so the load is irregular in the way real agent activity is and autoscaling is exercised by rate changes, not by a constant stream.
- An attack simulation CLI publishes a known attack sequence so detection can be verified end to end, including the out-of-order case.
- The triage service gains a mock provider, which is the default on a deployed stack, so the full pipeline runs with no LLM key and spends no quota until the LLM provider is switched on. A finding records which provider handled it and when.
- The API gains a pipeline statistics endpoint and richer finding data.
- A web console shows live pipeline statistics, a chart of queue depth and detector count, and the findings table with the evidence behind each exfiltration finding.
- A single developer script builds, deploys, stops and operates the stack on a local Kubernetes cluster.

## User Stories

Scaling and state

1. As a platform operator, I want to run several detector replicas against one event stream, so that detection throughput is not capped by a single process.
2. As a platform operator, I want an exfiltration to be detected when the secret read and the web request are handled by different replicas, so that scaling out does not create blind spots.
3. As a platform operator, I want an exfiltration to be detected when the two events are processed in the opposite order to the one in which they happened, so that reordering between replicas does not hide an attack.
4. As a platform operator, I want the number of detectors to follow the publish rate automatically, so that I do not size the deployment by hand.
5. As a platform operator, I want extra replicas to burn down a backlog after a peak, so that detection latency recovers quickly.
6. As a platform operator, I want the replica count to stay steady under sustained load, so that the deployment does not oscillate between its minimum and maximum.
7. As a platform operator, I want detectors to scale back in about a minute after load drops, so that idle replicas do not linger.
8. As a platform operator, I want the replica count bounded between 1 and 5, so that a load spike cannot exhaust the cluster.
9. As a platform operator, I want events to wait in the queue while Redis is unreachable, so that no event is evaluated without the state it needs.
10. As a platform operator, I want a detector with an unreachable dependency to pause between retries, so that it does not spin and flood the logs.
11. As a platform operator, I want the removal of a replica to lose no events, so that scale-in is safe.
12. As a platform operator, I want replicas that start at the same moment to set up the schema without failing, so that scale-out on a fresh database works.
13. As a platform operator, I want an existing database to gain the new columns automatically, so that no migration has to be run by hand.
14. As a platform operator, I want the detector to refuse to start when Redis is unreachable, so that a misconfiguration is visible immediately.
15. As a platform operator, I want shared state to expire on its own, so that Redis memory stays bounded.
16. As a platform operator, I want a startup log line naming the detector's identity, which is also its broker connection name, so that I can match a pod's log to the connection it holds.
17. As a customer, I want the detection state of my agents kept separate from every other customer's, so that another tenant's activity can never raise a finding against my agents.

Detection

18. As a detection engineer, I want the exfiltration rule to fire only for requests to non-allowlisted domains after a secret read, so that routine traffic to trusted domains does not flood the findings.
19. As a detection engineer, I want the rule to consider only web requests within 10 minutes after the secret read, measured by event time, so that unrelated activity is not linked.
20. As a detection engineer, I want each exfiltration finding to reference the secret read that preceded it, so that the correlation can be audited.
21. As a detection engineer, I want every finding to record which detector replica stored it, so that I can trace a finding back to the process that produced it.
22. As a detection engineer, I want to simulate an attack on command, so that I can verify detection end to end without waiting for random traffic.
23. As a detection engineer, I want to simulate the attack with its events published out of order, so that I can verify the reordering path.
24. As a detection engineer, I want each simulation run to use fresh identifiers, so that repeated runs do not interfere with each other.
25. As a detection engineer, I want the simulation to report how many detectors are consuming, so that I know whether the cross-replica path can be exercised.
26. As a detection engineer, I want the stateful rule tested without Redis, so that rule tests stay fast and deterministic.
27. As a detection engineer, I want the same scenarios verified against a real Redis, so that the shared-state behaviour is proven and not assumed.

Load generation

28. As a platform operator, I want a simulated fleet of customers and agents sized by configuration, so that I can produce realistic volumes.
29. As a platform operator, I want simulated agents to work in bursts and idle periods of random length, so that the load is irregular in the way real agent activity is.
30. As a platform operator, I want to set the average load level with one command, so that I can load test the pipeline at a known rate.
31. As a platform operator, I want to switch the load off, so that the backlog drains and the detectors return to the minimum replica count.
32. As a platform operator, I want to replay a load pattern from a seed, so that two runs can be compared.
33. As a platform operator, I want to control the share of suspicious events, so that the volume of findings can be kept within what triage can process.
34. As a platform operator, I want a configurable synthetic per-event evaluation cost, so that autoscaling can be load tested on small hardware.
35. As a security analyst, I want simulated customers and agents to have predictable identifiers, so that I can tell which tenant and agent a finding belongs to.

Triage

36. As a developer, I want the pipeline to run without an LLM key, so that I can work on it without a provider account.
37. As a platform operator, I want to switch the triage provider while the system is running, so that I can control when provider quota is spent.
38. As a security analyst, I want the mock provider never to produce a verdict, so that canned output is never mistaken for a model's judgment.
39. As a security analyst, I want to see which provider handled each finding and when, so that I know how much weight a verdict carries.
40. As a security analyst, I want every finding to show one of four triage states (pending, verdict present, processed by Gemini without a verdict, processed by the mock provider), so that an empty verdict is never ambiguous.
41. As a platform operator, I want the triage service to log which provider is active at startup, so that the configuration is never a guess.
42. As a platform operator, I want the LLM model pinned to a fixed identifier, so that the provider cannot change the model underneath a running system.

Console

43. As a security analyst, I want a web console that lists findings newest first, so that I see new detections without calling an API by hand.
44. As a security analyst, I want to filter findings by severity, customer and rule, so that I can narrow a long list to one tenant, rule or severity.
45. As a security analyst, I want to expand an exfiltration finding and see the secret read, the web request and the replicas involved, so that I can check the correlation against its two source events without querying the database.
46. As a security analyst, I want findings whose two events were handled by different replicas to be marked, so that when I audit a correlation I know it depended on shared state.
47. As a platform operator, I want to see queue depth, detector count and events per second in and out, so that I know the state of the pipeline at a glance.
48. As a platform operator, I want a chart of queue depth and detector count over the last five minutes, so that I can tell whether the replica count kept pace with a backlog and how long recovery took.
49. As a platform operator, I want the console to keep sampling while its browser tab is not focused, so that the five-minute history, which exists only in the browser, keeps filling for as long as the browser allows.
50. As a platform operator, I want the console to show clearly when pipeline statistics are unavailable, so that I do not read stale numbers as current.
51. As a security analyst, I want a high-contrast dark console with consistent severity colours, so that critical findings stand out.

API

52. As an API consumer, I want an endpoint returning pipeline statistics as JSON, so that tools other than the console can use them.
53. As an API consumer, I want an endpoint listing the customers that have findings, so that I can build a filter without guessing identifiers.
54. As an API consumer, I want to filter findings by rule, so that I can retrieve one rule's findings.
55. As an API consumer, I want findings returned in a stable order, so that two findings with the same event time do not swap between requests.
56. As a platform operator, I want the health endpoint to stay independent of the message broker, so that a broker outage does not take the API out of rotation.

Developer workflow

57. As a developer, I want one script that lists every command with a one-line description, so that I do not have to remember the tooling.
58. As a developer, I want to build and deploy the whole stack to a local cluster with one command, including the autoscaler, so that setup is repeatable.
59. As a developer, I want the deploy command to stop with a clear message when the cluster cannot see locally built images, so that the cause is not hidden behind failing pods.
60. As a developer, I want a status command, so that I can see what is running and whether the autoscaler is healthy.
61. As a developer, I want a clean command that empties findings, shared state and queues, so that I can start from a known state.
62. As a developer, I want to run every test with one command, so that I can check a change quickly.
63. As a developer, I want to run the console locally with hot reload against the API, so that UI work is fast.
64. As a developer, I want docker compose to keep bringing up the whole stack, so that I can work without a cluster.
65. As a developer, I want CI to run the Redis-backed test and the frontend checks, so that regressions are caught on every push.
66. As a developer, I want the README and architecture diagram to match the system, so that a new reader gets an accurate picture.
67. As a developer, I want one command that stops the whole stack, so that no part of the pipeline keeps running or spending provider quota when I am not using it.
68. As a platform operator, I want a deployed stack to use the mock provider until I switch the LLM provider on, so that provider quota is never spent by accident.

## Implementation Decisions

### Settings

New settings are environment variables, following the existing convention:

- Detector: REDIS_ADDR (host and port; also read by the Redis-backed test) and RULE_EVAL_DELAY_MS.
- Triage: TRIAGE_PROVIDER and MOCK_TRIAGE_DELAY_MS.
- Sensor: SENSOR_CUSTOMERS, SENSOR_AGENTS_PER_CUSTOMER, SENSOR_EVENTS_PER_SEC, SENSOR_SUSPICIOUS_PROB and SENSOR_SEED. The existing CUSTOMER_ID and AGENT_ID are removed with the sensor rewrite.
- API: RABBITMQ_MGMT_URL.

### Rules engine and shared state

- The exfiltration rule is narrowed: it fires for a web request to a non-allowlisted domain made by an agent that read a secret at most 10 minutes earlier, measured by event time. A request at the same instant as the read counts.
- A request is non-allowlisted under the same test the unknown-domain rule uses: its URL has a host and the host is not on the allowlist. A URL with no parseable host raises neither finding and does not touch the state store.
- Only secret reads and non-allowlisted web requests touch the state store. All other events are evaluated statelessly.
- The state store is a small interface with two operations: record a secret read and return the live web-request records of that agent; record a web request and return the live secret-read records of that agent. Each operation writes its own record first and then reads the other kind. This ordering guarantees that at least one of two concurrent workers sees the other's record.
- All comparison logic stays in the rules engine. The store only stores and returns records, so the in-memory and Redis implementations cannot diverge in rule behaviour.
- Records are keyed by customer and agent, so tenants never mix.
- A secret-read record holds the event id, event time, the path read and the identity of the detector that processed it. It lives 10 minutes.
- A web-request record holds the full event and the identity of the detector that processed it. It lives 60 seconds. This is enough because reordering between replicas is normally well under a second; the full event is needed so that the worker that processes the read second can raise a finding on behalf of the request.
- Expiry is counted from processing time. The engine additionally checks the 10-minute bound on event times, so a late-processed read cannot link to a much later request.
- The Redis implementation keeps two sorted sets per agent (reads and requests). Members are the JSON records and scores are expiry times. One ordered pipeline adds the record, trims expired members, refreshes the key's expiry and reads the live members of the other set.
- Writing the same record twice is idempotent: it is the same member with a refreshed score. A redelivery handled by another detector writes a second member for the same event, because the record contains the detector identity. Record identity is therefore the event id: the engine collapses returned records that share an event id, keeping the first in score order, before applying the rule, so one event never yields two findings. Neither store implementation de-duplicates on its own.
- Both implementations take an injectable clock. In the Redis implementation it supplies the scores and the trim and read bounds, so tests advance the clock instead of waiting. The key-level expiry uses the server clock and is not asserted by tests.
- A finding carries the event it belongs to and optional evidence. On the request side the engine uses the latest qualifying read. On the read side it raises one finding per qualifying live request, each belonging to that request's event. Both sides build the same detail text, naming the secret path, the URL and the time gap.
- Evaluation takes a context and can return an error, so a store failure propagates to the detector.
- The engine is constructed with a store and the detector's identity.

### Detector

- The detector's identity is its hostname, which is the pod name in Kubernetes. It is logged at startup and used as the broker connection name.
- Findings are stored and triage requests are built from the event the finding belongs to, not from the message being processed. The triage request format is unchanged.
- Duplicate exfiltration findings raised by both sides are absorbed by the existing unique constraint on event and rule, and only the first insert requests triage.
- On an infrastructure error (Redis or Postgres) the detector logs, pauses about one second and requeues the message. The pause is deliberate back-pressure.
- The detector pings Redis at startup and exits if it is unreachable, as it already does for the broker and the database. The Redis client uses short timeouts so one stuck call cannot stall the consumer.
- RULE_EVAL_DELAY_MS gives a synthetic per-event evaluation cost in milliseconds, default 0. It stands in for heavier rule evaluation during load testing and is applied as a minimum time per event. The Kubernetes manifest sets it to 5 with a comment explaining its purpose; code and compose leave it at 0.
- There is no graceful shutdown. When a replica is removed, the broker redelivers its unacknowledged messages; the unique constraint and the collapse by event id absorb the repeats.
- From delivery step 3 the detector manifest sets two replicas and drops the note that pins it to one, so the cross-replica path runs in the cluster. Step 4 removes the count when the scaled object takes over.

### Schema

- The findings table gains four columns: detector_id (text, the identity of the detector that stored the finding), evidence (JSON, null for rules without evidence), verdict_source (text, gemini or mock, null while pending) and triaged_at (timestamp, null while pending). The existing verdict column, llm_verdict, and its JSON field of the same name are unchanged. Rows stored before a column existed keep null in it.
- When the triage columns are added, rows that already hold a verdict were written by Gemini: schema setup gives them verdict_source gemini and triaged_at equal to the event time.
- Evidence for an exfiltration finding has the keys read_event_id, read_ts, read_path, read_detector_id (the detector that processed the read), request_detector_id (the detector that processed the request) and raised_by (read or request: which of the two events was processed second and so raised the finding).
- Schema setup runs in one transaction under a database advisory lock: create the table if missing, then add each new column if missing. This makes concurrent startup safe and upgrades an existing table in place.
- Schema setup lives in one shared place and is run at startup by both services that write to the table (detector and triage). Delivery step 2 introduces it with the two triage columns; step 3 adds detector_id and evidence to it.

### Triage

- The LLM call sits behind a provider interface with two implementations: Gemini and mock.
- Provider selection comes from TRIAGE_PROVIDER, with the values gemini and mock. When it is unset, Gemini is used if a key is present and mock otherwise. An empty value counts as unset; any value other than gemini or mock is a startup error. The active provider is logged at startup. Selecting Gemini without a key is a startup error.
- The mock provider is the default wherever the stack is deployed: the Kubernetes manifest sets TRIAGE_PROVIDER to mock explicitly, and compose sets it to mock unless the environment file sets it to a non-empty value. A deployed stack therefore never calls the LLM provider until it is switched on, with the developer script's triage command on the cluster or with TRIAGE_PROVIDER in the environment file under compose.
- The mock provider waits MOCK_TRIAGE_DELAY_MS (default 1000) and produces no verdict. It sets verdict_source to mock and triaged_at to the processing time and leaves the verdict empty.
- Gemini verdicts set verdict_source to gemini and triaged_at in addition to the verdict text. When the service gives up on a finding after its retry, it sets the same two columns and leaves the verdict empty.
- The four resulting states are: pending (triaged_at is null), verdict present, processed by Gemini without a verdict, processed by the mock provider.
- The Gemini model is pinned to a fixed model identifier as the code default, in place of the floating alias used today, so compose and Kubernetes run the same model. Delivery step 2 owns this: the identifier is the stable id of the current flash-lite model from the provider's published model list. If it cannot be determined when the step is implemented, the alias stays and the pin remains listed under values fixed during implementation.
- In Kubernetes the Gemini key is an optional secret reference, so the service starts when the secret is absent. The example environment file ships with an empty key and with TRIAGE_PROVIDER set to mock, with a comment naming the two values.
- The Gemini provider takes its base URL as a parameter, defaulting to the provider's public address, so tests can point it at a stand-in server. No test and no delivery step calls the real provider.
- Retry behaviour, concurrency and prompt are unchanged.

### Sensor

- One sensor Deployment replaces the two per-customer sensors. It simulates SENSOR_CUSTOMERS customers, each with SENSOR_AGENTS_PER_CUSTOMER agents. Identifiers have the form customer_1 and agent_1_3 (customer 1, agent 3).
- Each agent alternates between working and idle sessions whose lengths are random. While working it emits tool-call events at random intervals. Total load therefore rises and falls on its own over tens of seconds.
- SENSOR_EVENTS_PER_SEC is the target average rate of the whole fleet. The rate of one working agent is the target rate times (mean working + mean idle) divided by (agents times mean working); with equal means that is twice the agent's share. Each agent starts in a random phase, working with probability equal to the duty cycle, so start-up is not a synchronized burst above the target.
- SENSOR_SUSPICIOUS_PROB is the probability that an event carries a suspicious argument (a secret path, a download piped to a shell, a non-allowlisted URL).
- With SENSOR_SEED set the generated sequence is reproducible; without it the sequence is random.
- The generator is a pure component driven by an injected random source and clock, separate from the publishing loop. The publishing loop keeps to the schedule the generator produces.
- Starting values: 4 customers with 10 agents each and mean session lengths of about 30 seconds. Low level: about 10 events per second with a suspicious probability of 0.5 percent. High level: about 500 events per second on average, with peaks near 650, and a suspicious probability of 0. The high level is fixed by the calibration described under Autoscaling.
- The sensor logs a periodic summary and does not print one line per event.
- The Deployment uses the recreate strategy, so a change of settings never runs two sensors at once. Its manifest declares its replica count and the four load settings (customers, agents per customer, rate, suspicious probability) explicitly; SENSOR_SEED is left unset in the manifest and in compose.
- The code defaults are the starting fleet size and the low level.

### Attack simulation CLI

- A new command-line tool, attacksim, publishes a known attack to the events queue: for each of 3 agents (configurable), a secret read followed by a web request to a non-allowlisted domain.
- Its flags are agents (default 3), gap (default 300ms), out-of-order and customer (default customer_1). The broker address comes from AMQP_URL with the same localhost default as the services.
- It sets event times explicitly, the read two seconds before the request, so the result does not depend on clock resolution.
- It publishes the first kind of event for all agents, waits a configurable gap (default 300 ms), then publishes the second kind. With the out-of-order flag the requests are published first while the reads keep the earlier event time.
- Event ids are random per run. Events are published under customer_1 by default, with a flag to override; the customer id is only a string and need not be produced by the sensor. Agent ids have the form attack_<run>_<n>, where run is a short random suffix and n counts from 1, so they cannot collide with the sensor's identifiers or with another run.
- It declares the queues exactly as the other services do, prints the ids it used and the number of consumers on the events queue, and notes when fewer than two detectors are consuming.
- The tool is not built into an image. The developer script runs it on the host with the Go toolchain against the broker on localhost, which is why the broker's load-balancer service is added in the same delivery step. Under compose it is run the same way against the same port.

### Autoscaling

- KEDA 2.21.0 is installed from its release manifest with a server-side apply. The install waits for KEDA's deployments and its metrics API to be available before the scaled object is applied.
- A scaled object, kept in its own manifest file, targets the detector Deployment with a minimum of 1 and a maximum of 5 replicas.
- Two triggers read the events queue through the broker's management API (the scaler's http protocol): a publish-rate trigger for steady state and a queue-length trigger for backlog. The autoscaler uses the larger result.
- The queue-length trigger sets excludeUnacknowledged to true so it counts ready messages only; the scaler's default counts ready plus unacknowledged, which would include each replica's prefetch window.
- Starting targets: 150 events per second per replica for the rate trigger and 100 ready messages per replica for the length trigger.
- Calibration (delivery step 4): with one detector replica, RULE_EVAL_DELAY_MS at 5 and no scaled object applied, the sensor publishes about 300 events per second with a suspicious probability of 0 for two minutes. Per-replica throughput is the events queue's acknowledge rate reported by the broker once it is steady. The rate target is 0.8 of that figure rounded to the nearest 10. The high load level is 3.3 times the rate target, so the average needs four replicas and the peaks, about 1.3 times the average, reach five. The length target stays at 100. The rate target replaces the starting value in the scaled object, the high level (rounded to the nearest 10) replaces it in the developer script's load command, and both, with the measured throughput, replace the starting values in this document in the same commit. The sensor manifest holds the low level and does not change. If no cluster is reachable when the step is implemented, the starting values are committed unchanged and the calibration stays listed under values fixed during implementation.
- The scale-down stabilization window is 60 seconds, set through the scaled object's autoscaler behaviour. Scale-up uses the defaults, so a large backlog can take the detector from 1 to 5 in one step.
- KEDA reaches the broker through its full in-cluster service name in the default namespace, because the scaler runs in KEDA's own namespace.
- The detector Deployment manifest no longer sets a replica count once the scaled object exists, so re-applying it does not reset the autoscaler's decision.

### API

- The API stays read-only and keeps its existing routes without a prefix.
- GET /stats returns one JSON object for the events queue with the fields ready, unacked, publish_rate, ack_rate and consumers. The consumer count is the number of running detectors. Events per second in is the publish rate; events per second out is the acknowledge rate.
- The endpoint reads the broker's management API with a two-second timeout. RABBITMQ_MGMT_URL is the management base URL including credentials, in the style of AMQP_URL; the code default is the guest account on localhost. The endpoint requests the events queue of the default virtual host and maps the ready count, the unacknowledged count, the consumer count and the publish and acknowledge rates. Fields the broker omits are reported as zero. Any outcome other than a successful response (unreachable, timeout, missing queue, rejected credentials) is a 503 with a JSON object holding one field, error. The error field and the log line hold a short fixed description plus the broker's status code when there is one, never the HTTP client's error text, which embeds the URL.
- The broker refreshes these numbers every five seconds. This granularity is accepted.
- Findings gain four JSON fields named exactly as the columns: detector_id (null for rows stored before the column existed), evidence (the stored object, or null), verdict_source and triaged_at. Findings are ordered by id, newest first.
- A rule filter (query parameter rule) is added next to the severity, customer and limit filters. It accepts exactly the four rule names; any other value is a 400, as for severity.
- GET /customers returns a JSON array of the distinct customer ids that have findings, sorted ascending.
- The health endpoint is unchanged and does not depend on the broker.

### Web console

- A single-page application in a top-level web directory, built with Vite, React and TypeScript, using TanStack Query for data fetching, Tailwind for styling with hand-built components, Recharts for the chart and Vitest for tests.
- One page, no routing, no login.
- Top strip: tiles for queue depth (ready messages), detector count and events per second in and out. When statistics are unavailable the tiles keep their last values and are visibly dimmed.
- Chart: queue depth and detector count on a shared time axis. History holds the samples of the last five minutes by timestamp, capped at about 150, in browser memory; it is lost on reload. Gaps in sampling show as gaps.
- Findings table: time, severity, customer, agent, rule, detail, detector and triage status, with filters for severity, customer and rule. The filters are applied by the API through its query parameters, and the customer options come from the customers endpoint. Rows with a verdict expand to show its text; exfiltration rows also show the evidence. Rows whose read and request were handled by different detectors are marked.
- Triage status renders the four states defined above, derived in this order: llm_verdict present; else verdict_source is mock; else verdict_source is gemini; else pending. The verdict text is the word malicious or benign, a colon, then the reason. The four cells read malicious or benign (verdict present), mock, no verdict (processed by Gemini without a verdict) and pending. Fields that are null on rows stored before an upgrade render as a dash.
- Statistics and findings are polled every two seconds. Background refetching is enabled so the query library does not pause on a hidden tab, because the chart history exists only in the browser. Browsers still throttle timers of hidden pages, so a long-hidden console samples more slowly and the chart shows the resulting gaps.
- Polled queries disable the library's retries; the next poll is the retry, so a failed statistics request dims the tiles within one polling interval. One history sample is appended per successful statistics response, keyed on the response's update time and not on a change of the data, because equal consecutive responses keep the same object.
- Visual style: dark terminal look, Courier New throughout. Green for healthy and benign, red for CRITICAL, HIGH and malicious, amber for MEDIUM and pending, dim grey for mock and no verdict.
- The console calls the API under an api path prefix. In production an nginx image serves the built files and proxies that prefix to the API, stripping it. In development the Vite dev server does the same against the API on localhost.
- The console has its own image, Deployment and load-balancer service.

### Runtime and manifests

- The target cluster is the Kubernetes cluster built into Docker Desktop, which publishes load-balancer services on localhost. Its node keeps its own image store and gets locally built images by pulling them through Docker Desktop's local registry mirror.
- Redis runs as a single replica without persistence in both compose and Kubernetes. State is lost when it restarts.
- On localhost the cluster exposes: console 3000, API 8080, broker 5672 and broker management 15672. The broker's external ports are a separate load-balancer service. Postgres and Redis stay internal and are reached through cluster exec.
- Compose and the cluster use the same host ports and are alternatives, not run together. The developer script's down command frees the ports for compose.
- Compose gains Redis, the single sensor, the management URL for the API and the console service, each in the delivery step that introduces the component. It passes TRIAGE_PROVIDER (mock when the environment file does not set it or sets it empty) and MOCK_TRIAGE_DELAY_MS through from the environment file and starts without a local environment file.
- The stack is deployed to the default namespace.
- Images are built locally with a fixed local tag. The application Deployments use the pull policy Always: with Never the node cannot see a locally built image at all, and with IfNotPresent it keeps running its cached copy after a rebuild. Deploy rebuilds, applies, restarts the application Deployments and waits for them, because a fixed tag does not trigger a rollout on its own; each restarted pod then pulls the current build.

### Developer script

- One PowerShell script, dev.ps1, compatible with Windows PowerShell 5.1, targeting Kubernetes only. Run with no arguments it prints every command with a one-line description.
- Commands, in their final form:
  - help: list all commands.
  - build: build all images with the local tag.
  - deploy: check compose is down, build, install KEDA if missing, apply infrastructure, wait for it, apply applications and the scaled object, remove the two legacy sensor Deployments, restart and wait, then make the Gemini secret match the local environment file (recreated when a key is present, removed when there is none). While waiting, if a pod reports that its image cannot be pulled, deploy stops and says the cluster cannot get the locally built image. Applying the manifests puts the triage provider back on mock. The secret is written last, after the rollout has finished, so a pod left on gemini never receives a key during a deploy; the triage command's own rollout picks the secret up.
  - down: remove the stack from the cluster. It deletes the scaled object first, so the autoscaler does not recreate detector replicas, then the applications and the infrastructure including their services, waits until no pod of the stack is left and prints what was removed. The Gemini secret and any legacy sensor Deployment are removed as well. It is safe to repeat. KEDA stays installed; down all removes KEDA as well. When KEDA's resource type is not installed, down skips the scaled-object step: it probes for the type first, so down and down all succeed on a cluster that never had KEDA. The scaled object's deletion is not waited on for more than 30 seconds: if it is still present because KEDA is not running, down says so, continues with the rest and ends with a failure message naming the leftover object. Findings and shared state are lost, as on any restart of their pods. deploy brings the stack back.
  - status: pods, autoscaler state and the current load and triage settings; on a stopped stack it says that the stack is down.
  - test: Go tests and frontend tests.
  - web: install the console's dependencies if missing and run its dev server on the tool's default port.
  - load low, high or off: low and high set the sensor's average rate and suspicious probability together and make sure the sensor runs one replica; off scales the sensor to zero; each waits for the rollout and prints the applied values.
  - simulate-attack: run the attack simulation on the host, passing through its flags.
  - triage mock or gemini: set TRIAGE_PROVIDER on the triage Deployment and wait for its rollout; gemini stops with a clear message when the Gemini secret does not exist.
  - clean: scale the sensor to zero and wait until its pod is gone, purge the events and dead-letter queues, wait until the events queue has no unacknowledged messages, purge the triage queue and wait until it has none, flush Redis, truncate findings without restarting the id sequence, then restore the sensor's previous replica count, which stays zero after load off. Queue counts are read with the broker's own command-line tool in its pod, which is not subject to the five-second sampling.
- load, triage, clean and simulate-attack first check that the stack is deployed and otherwise stop with the message that the stack is down and that deploy brings it up.
- Because the manifests declare the sensor's replica count and load settings and the triage provider, a deploy resets the load to the low level and the provider to mock. The Gemini provider runs only between a triage gemini command and the next triage mock, deploy or down; status shows the active provider.
- The script is ASCII only, checks the exit code of every external command and stops on failure, does not redirect error output, and passes no inline JSON to external commands. Existence probes and removals use the tool's ignore-not-found form and test the output, so a missing object is not a failed command.

### Repository and CI

- The Go CI job gains a Redis service and runs the Redis-backed test. CI is recognised by the CI environment variable, which GitHub Actions sets to true: with it set, a missing REDIS_ADDR is a failure; without it the test is skipped when REDIS_ADDR is not set.
- A frontend CI job runs typecheck, lint, a few Vitest tests and build.
- CI fails if dev.ps1 contains a non-ASCII byte.
- Go tooling ignores the frontend's dependency directory, and the format check is limited to Go source directories.
- A Docker ignore file keeps version control data, local secrets, documentation assets and local working notes out of the Go image builds; frontend dependencies are added to the ignore files with the console.
- The Go module file is tidied so direct dependencies are listed as direct.
- Code comments explain intent and trade-offs rather than language mechanics. Existing source files and manifests are brought to that convention before feature work begins, so feature diffs stay focused.
- The README and the architecture diagram are updated to describe the system as built. The earlier design document is rewritten in the form of this one, as the design record of the first version, and points to this document where it is superseded.

### Delivery sequence

Each step is one commit and leaves the repository building and passing its tests. Step 1 changes no behaviour and is verified by build and tests only; the stack is not started before step 2, because until then the triage service calls the LLM provider whenever a key is present. Step 2 is run under compose and keeps the manifests valid; from step 3 each step is run under compose and on the cluster.

1. Housekeeping: this document committed, the earlier design document rewritten, comment convention pass over source files and manifests, module tidy, Docker ignore file.
2. Triage providers and API: provider interface, mock provider, the mock default in the manifest and in compose, shared schema setup with the verdict_source and triaged_at columns (run by detector and triage) and their fields in the findings response, optional secret reference, pinned model, example environment file; statistics endpoint, customers endpoint, rule filter, ordering, the management URL setting in compose and manifests. The developer script does not exist yet, so this step is exercised under compose.
3. Shared state: state store with both implementations, narrowed rule, finding and evidence shape, the detector_id and evidence columns added to the shared schema setup, their fields in the findings response, detector changes including two replicas in the manifest, Redis in compose, manifests and CI, the broker's load-balancer service, the attack simulation CLI, dev.ps1 with its first commands (help, build, deploy, down, test, status, simulate-attack, triage) and the CI ASCII check for it.
4. Load and autoscaling: sensor rewrite, the single sensor in compose and manifests in place of the two per-customer ones, evaluation cost setting, detector manifest without a replica count, KEDA install and scaled object, the load and clean commands, calibration and the resulting numbers.
5. Web console and documentation: the application, its image, Deployment and load-balancer service, the console service in compose, the web command, the frontend CI job, frontend entries in the ignore files, the Go tooling exclusion and the narrowed format check; README including the known limits, architecture diagram, final help text.

The steps form one line: each depends on the one before. The mock provider comes in step 2, before any step starts the stack, so that no step calls the LLM provider.

dev.ps1 starts in step 3 and grows with the steps; at each commit a command covers only what exists at that commit:

- Step 3: build covers the Go images. deploy performs every listed action except the KEDA install, the scaled object and the removal of the legacy sensors. down removes the applications, the infrastructure and the Gemini secret. status shows pods and the triage provider. test runs the Go tests.
- Step 4: deploy gains the KEDA install, the scaled object and the removal of the legacy sensors. down removes the scaled object first, removes legacy sensor Deployments when present and gains down all. The load and clean commands arrive. status adds the load settings and autoscaler state.
- Step 5: build gains the console image, test gains the frontend tests, deploy applies the console and includes it in its restart and wait, and down removes the console too.

If an implementation deviates from this document, the document is updated in the same commit.

## Testing Decisions

A good test here exercises behaviour through a public seam and asserts on what a caller can observe: the findings returned, the JSON produced, the text rendered. It does not assert on internal data structures, Redis key names or call sequences, so the implementation can change without rewriting tests.

Seams, existing ones first:

1. Rules engine evaluation (existing; prior art is the current rule tests). Covers the narrowed rule, including that an allowlisted request after a secret read raises nothing; read then request; request then read, where the finding belongs to the request's event; the 10-minute bound; several reads and several requests per agent; tenant isolation; evidence content; and that re-recording the same event from the same and from a different engine yields one finding. These run against the in-memory store with an injected clock. The existing tests that rely on an allowlisted request are changed to use a non-allowlisted one.
2. The same seam against a real Redis, with two engines sharing one store to stand in for two replicas: read on one and request on the other, in both orders, expiry through the injected clock, and re-recording from the other engine. This runs in CI.
3. API input parsing (existing; prior art is the current filter tests), extended for the rule filter and its validation.
4. Triage verdict parsing (existing), plus provider selection from the setting and key, the mock provider returning no verdict, what is written for each outcome (verdict, first failure, give-up, mock), and the Gemini provider over HTTP against a stand-in server: the pinned model in the request path, the key header, a normal response, a failed response and a response without candidates.
5. Statistics endpoint over HTTP against a stand-in management server: a normal response, a response with the statistics fields missing, and an unreachable server.
6. Sensor load generator as a pure function: the same seed gives the same sequence, identifiers have the documented form, a suspicious probability of zero yields no suspicious events, and the average rate over one simulated hour is within 10 percent of the target.
7. Attack simulation scenario builder: event order and event times for the normal and out-of-order modes, and distinct identifiers between runs.
8. Web console page rendered in Vitest against a stubbed API: the four triage states, the expandable evidence row, the cross-replica marker, the dimmed tiles on a statistics error, and the history keeping a bounded window of samples.

Not covered by automated tests: the detector's consume loop and schema setup, the Kubernetes manifests and KEDA configuration, and the developer script beyond the ASCII check. This matches the repository today, where the consume loops have no tests.

## Out of Scope

- Authentication and per-tenant authorization for the API and console.
- Graceful shutdown of the detector.
- Redis persistence, replication or high availability.
- Partition-by-agent routing as an alternative to shared state.
- Deduplication or aggregation of repeated alerts for the same agent.
- Detection of exfiltration through allowlisted domains, and payload inspection.
- A real sensor (an MCP proxy in the agent's tool path) and blocking of tool calls.
- Rate limiting or severity-based selection in triage, and any change to its retry behaviour.
- Pipeline statistics fresher than the broker's five-second sampling, and Prometheus-based metrics.
- Server-side storage of statistics history.
- Browser end-to-end tests.
- Developer-script support for docker compose beyond what compose already offers.
- Custom scale-up policies (rate-limited or stepped scale-up).

## Further Notes

Known limits to state in the README:

- Exfiltration through an allowlisted domain is not detected.
- Storing a finding and requesting its triage are not atomic. If a detector dies between the two, the finding is kept but never gets a triage request.
- Shared state is lost when Redis restarts, and expiry uses the detector's clock.
- Reordering between the two events is tolerated up to 60 seconds of processing skew.
- If the database is recreated while detectors are running, they must be restarted to set up the schema again.

Values fixed during implementation:

- One replica's real throughput with the evaluation cost setting, which fixes the high load level and the rate target. The expectation is 170 to 190 events per second at 5 ms.
- The pinned Gemini model identifier, taken from the provider's published model list.

Facts the design relies on:

- KEDA 2.21.0 is the release whose supported range covers Kubernetes 1.36. Its release manifest needs a server-side apply because of the size of one resource definition.
- With a minimum replica count of 1, KEDA's own polling and cooldown settings do not affect scaling between 1 and 5. The autoscaler evaluates every 15 seconds and its behaviour settings govern scale-down.
- The broker's management metrics are sampled every five seconds and are omitted entirely for a queue that has not been sampled yet, for example right after a broker restart.
- The broker dispatches messages round robin among consumers, so two consecutive messages usually go to different replicas, but which replica gets which event is not controllable.
- KEDA's images are pulled on every pod start, so the cluster needs registry access when KEDA starts.
- On the Docker Desktop cluster, observed on 2026-10-02: a pod with the pull policy Never fails for a locally built image; with IfNotPresent it starts but keeps the first copy it pulled even after the tag is rebuilt; with Always it runs the rebuilt image.
- The LLM provider's free tier allows in the order of 15 requests per minute and a few hundred per day; the limits are not guaranteed. This is why a deployed stack runs on the mock provider until the LLM provider is switched on. With it switched on, the low load level raises about 3 findings per minute, which is inside the per-minute limit and uses the daily allowance in about two hours of continuous running, after which findings are marked processed by Gemini without a verdict. The high level raises no findings.
