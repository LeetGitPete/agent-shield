// The detector is the heart of the pipeline. It:
//  1. consumes agent tool-call events from RabbitMQ,
//  2. runs the rules engine over each one,
//  3. stores any findings in Postgres (skipping duplicates),
//  4. publishes a triage request so the LLM service can review the finding.
//
// Delivery semantics: at-least-once. A message is acked only after its
// findings are safely in Postgres; if we crash mid-way RabbitMQ redelivers,
// and the (event_id, rule) unique constraint makes the redelivery harmless.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver with database/sql
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
	"github.com/leetgitpete/agent-shield/internal/rules"
)

// Run on startup; IF NOT EXISTS makes it safe to run every time.
const findingsSchema = `
CREATE TABLE IF NOT EXISTS findings (
	id          BIGSERIAL PRIMARY KEY,
	event_id    TEXT NOT NULL,
	customer_id TEXT NOT NULL,
	agent_id    TEXT NOT NULL,
	rule        TEXT NOT NULL,
	severity    TEXT NOT NULL,
	ts          TIMESTAMPTZ NOT NULL,
	detail      TEXT NOT NULL,
	llm_verdict TEXT,
	UNIQUE (event_id, rule)
)`

// TriageRequest is the message we publish for the LLM triage service.
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

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()
	if _, err := db.Exec(findingsSchema); err != nil {
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

	// Prefetch = backpressure: RabbitMQ hands us at most 16 unacked
	// messages at a time, so a slow detector never gets flooded.
	if err := channel.Qos(16, 0, false); err != nil {
		log.Fatal("set qos: ", err)
	}

	// autoAck=false: messages are acked only after findings are persisted.
	deliveries, err := channel.Consume(mq.EventsQueue, "detector", false, false, false, false, nil)
	if err != nil {
		log.Fatal("consume: ", err)
	}

	engine := rules.NewEngine()
	log.Println("detector running")

	for delivery := range deliveries {
		var evt event.Event
		if err := json.Unmarshal(delivery.Body, &evt); err != nil || evt.ID == "" {
			// Malformed JSON will never parse no matter how often we retry,
			// so reject WITHOUT requeue -> RabbitMQ dead-letters it to the DLQ.
			log.Printf("malformed event -> DLQ: %.100s", delivery.Body)
			delivery.Nack(false, false)
			continue
		}

		findings := engine.Evaluate(evt)

		storedAll := true
		for _, finding := range findings {
			if err := storeAndRequestTriage(db, channel, evt, finding); err != nil {
				log.Println("store finding: ", err)
				storedAll = false
				break
			}
		}
		if !storedAll {
			// Infrastructure hiccup (e.g. Postgres briefly down): requeue
			// so the event is retried, unlike the malformed case above.
			delivery.Nack(false, true)
			continue
		}

		if len(findings) > 0 {
			log.Printf("event %s: %d finding(s)", evt.ID, len(findings))
		}
		delivery.Ack(false)
	}
}

// storeAndRequestTriage inserts one finding and, if it is new, asks the
// triage service to review it. Duplicate findings (same event + rule,
// e.g. after a redelivery) are silently skipped.
func storeAndRequestTriage(db *sql.DB, channel *amqp.Channel, evt event.Event, finding rules.Finding) error {
	var findingID int64
	err := db.QueryRow(`
		INSERT INTO findings (event_id, customer_id, agent_id, rule, severity, ts, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id, rule) DO NOTHING
		RETURNING id`,
		evt.ID, evt.CustomerID, evt.AgentID, finding.Rule, finding.Severity, evt.Ts, finding.Detail,
	).Scan(&findingID)

	if err == sql.ErrNoRows {
		// ON CONFLICT ... DO NOTHING returned no row: this finding already
		// exists (duplicate delivery). Done — and no second triage request.
		return nil
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
