package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
	"github.com/leetgitpete/agent-shield/internal/rules"
)

const schema = `
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

// TriageRequest is what we hand the LLM triage service.
type TriageRequest struct {
	FindingID int64       `json:"finding_id"`
	Event     event.Event `json:"event"`
	Rule      string      `json:"rule"`
	Severity  string      `json:"severity"`
	Detail    string      `json:"detail"`
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	amqpURL := envOr("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	pgURL := envOr("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/agentshield")

	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		log.Fatal("create schema: ", err)
	}

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Fatal("connect to rabbitmq: ", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		log.Fatal("open channel: ", err)
	}
	defer ch.Close()
	if err := mq.Declare(ch); err != nil {
		log.Fatal("declare queues: ", err)
	}

	// Prefetch: hold at most 16 unacked messages — backpressure so a slow
	// detector never buffers the whole queue in memory.
	if err := ch.Qos(16, 0, false); err != nil {
		log.Fatal("set qos: ", err)
	}
	// autoAck=false: we ack only after the finding is safely in Postgres.
	msgs, err := ch.Consume(mq.EventsQueue, "detector", false, false, false, false, nil)
	if err != nil {
		log.Fatal("consume: ", err)
	}

	engine := rules.NewEngine()
	log.Println("detector running")

	for msg := range msgs {
		var ev event.Event
		if err := json.Unmarshal(msg.Body, &ev); err != nil || ev.ID == "" {
			// Malformed: no point retrying — dead-letter it.
			log.Printf("malformed event -> DLQ: %.100s", msg.Body)
			msg.Nack(false, false)
			continue
		}

		findings := engine.Evaluate(ev)
		ok := true
		for _, f := range findings {
			if err := storeAndRequestTriage(db, ch, ev, f); err != nil {
				log.Println("store finding: ", err)
				ok = false
				break
			}
		}
		if !ok {
			// Infra error (e.g. DB down): requeue for retry, don't dead-letter.
			msg.Nack(false, true)
			continue
		}
		if len(findings) > 0 {
			log.Printf("event %s: %d finding(s)", ev.ID, len(findings))
		}
		msg.Ack(false)
	}
}

func storeAndRequestTriage(db *sql.DB, ch *amqp.Channel, ev event.Event, f rules.Finding) error {
	var id int64
	err := db.QueryRow(`
		INSERT INTO findings (event_id, customer_id, agent_id, rule, severity, ts, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id, rule) DO NOTHING
		RETURNING id`,
		ev.ID, ev.CustomerID, ev.AgentID, f.Rule, f.Severity, ev.Ts, f.Detail,
	).Scan(&id)
	if err == sql.ErrNoRows {
		// Duplicate delivery: finding already stored, nothing to do.
		return nil
	}
	if err != nil {
		return err
	}

	req, err := json.Marshal(TriageRequest{
		FindingID: id, Event: ev, Rule: f.Rule, Severity: f.Severity, Detail: f.Detail,
	})
	if err != nil {
		return err
	}
	return mq.PublishJSON(ch, mq.TriageQueue, req)
}
