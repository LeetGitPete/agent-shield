// The detector consumes events from RabbitMQ, runs the rules engine,
// stores findings in Postgres and requests LLM triage for new ones.
//
// Delivery semantics: at-least-once. A message is acked only after its
// findings are safely in Postgres; if we crash mid-way RabbitMQ redelivers,
// and the (event_id, rule) unique constraint makes the redelivery harmless.
package main

import (
	"database/sql" // Go's standard SQL interface
	"encoding/json"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // blank import: only runs its init(), registering the "pgx" driver
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
	"github.com/leetgitpete/agent-shield/internal/rules"
)

// Run on startup; IF NOT EXISTS makes it safe to run every time.
const findingsSchema = `
CREATE TABLE IF NOT EXISTS findings (
	id          BIGSERIAL PRIMARY KEY,       -- auto-incrementing finding id
	event_id    TEXT NOT NULL,               -- which event triggered it
	customer_id TEXT NOT NULL,
	agent_id    TEXT NOT NULL,
	rule        TEXT NOT NULL,               -- which rule fired
	severity    TEXT NOT NULL,
	ts          TIMESTAMPTZ NOT NULL,        -- event timestamp
	detail      TEXT NOT NULL,
	llm_verdict TEXT,                        -- filled in later by the triage service
	UNIQUE (event_id, rule)                  -- idempotency: same event+rule can only exist once
)`

// TriageRequest is the message published for the LLM triage service.
type TriageRequest struct {
	FindingID int64       `json:"finding_id"`
	Event     event.Event `json:"event"`
	Rule      string      `json:"rule"`
	Severity  string      `json:"severity"`
	Detail    string      `json:"detail"`
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	amqpURL := envOr("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	postgresURL := envOr("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/agentshield")

	db, err := sql.Open("pgx", postgresURL) // connection pool, lazily connected
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()
	if _, err := db.Exec(findingsSchema); err != nil { // create the table if this is the first run
		log.Fatal("create schema: ", err)
	}

	connection, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Fatal("connect to rabbitmq: ", err)
	}
	defer connection.Close()

	channel, err := connection.Channel()
	if err != nil {
		log.Fatal("open channel: ", err)
	}
	defer channel.Close()

	if err := mq.Declare(channel); err != nil {
		log.Fatal("declare queues: ", err)
	}

	if err := channel.Qos(16, 0, false); err != nil { // prefetch=16: at most 16 unacked messages in flight (backpressure)
		log.Fatal("set qos: ", err)
	}

	deliveries, err := channel.Consume(mq.EventsQueue, "detector", false, false, false, false, nil) // autoAck=false: we ack manually
	if err != nil {
		log.Fatal("consume: ", err)
	}

	engine := rules.NewEngine()
	log.Println("detector running")

	for delivery := range deliveries { // blocks until the next message arrives
		var evt event.Event
		if err := json.Unmarshal(delivery.Body, &evt); err != nil || evt.ID == "" { // parse JSON into evt; &evt = write into it
			log.Printf("malformed event -> DLQ: %.100s", delivery.Body)
			delivery.Nack(false, false) // reject, requeue=false -> dead-lettered to DLQ (retrying can't fix bad JSON)
			continue
		}

		findings := engine.Evaluate(evt) // run all rules

		storedAll := true
		for _, finding := range findings {
			if err := storeAndRequestTriage(db, channel, evt, finding); err != nil {
				log.Println("store finding: ", err)
				storedAll = false
				break
			}
		}
		if !storedAll {
			delivery.Nack(false, true) // infra hiccup (e.g. DB down): requeue=true so the event is retried
			continue
		}

		if len(findings) > 0 {
			log.Printf("event %s: %d finding(s)", evt.ID, len(findings))
		}
		delivery.Ack(false) // done: only now does RabbitMQ forget the message
	}
}

// storeAndRequestTriage inserts one finding and, if it is new, publishes a triage request.
func storeAndRequestTriage(db *sql.DB, channel *amqp.Channel, evt event.Event, finding rules.Finding) error {
	var findingID int64
	err := db.QueryRow(`
		INSERT INTO findings (event_id, customer_id, agent_id, rule, severity, ts, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id, rule) DO NOTHING
		RETURNING id`, // returns the new row's id — or no row at all on conflict
		evt.ID, evt.CustomerID, evt.AgentID, finding.Rule, finding.Severity, evt.Ts, finding.Detail,
	).Scan(&findingID) // copy the returned id into findingID

	if err == sql.ErrNoRows {
		return nil // duplicate delivery: finding already exists, no second triage request
	}
	if err != nil {
		return err
	}

	triageMessage, err := json.Marshal(TriageRequest{
		FindingID: findingID,
		Event:     evt,
		Rule:      finding.Rule,
		Severity:  finding.Severity,
		Detail:    finding.Detail,
	})
	if err != nil {
		return err
	}
	return mq.PublishJSON(channel, mq.TriageQueue, triageMessage)
}
