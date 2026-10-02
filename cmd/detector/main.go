// The detector consumes events from RabbitMQ, runs the rules engine,
// stores findings in Postgres and requests LLM triage for new ones.
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

	_ "github.com/jackc/pgx/v5/stdlib"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
	"github.com/leetgitpete/agent-shield/internal/rules"
	"github.com/leetgitpete/agent-shield/internal/schema"
)

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

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()
	if err := schema.Setup(db); err != nil {
		log.Fatal("set up schema: ", err)
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

	// Prefetch 16: bounds the unacknowledged messages in flight (back-pressure).
	if err := channel.Qos(16, 0, false); err != nil {
		log.Fatal("set qos: ", err)
	}

	// autoAck is off: each message is acknowledged manually, below.
	deliveries, err := channel.Consume(mq.EventsQueue, "detector", false, false, false, false, nil)
	if err != nil {
		log.Fatal("consume: ", err)
	}

	engine := rules.NewEngine()
	log.Println("detector running")

	for delivery := range deliveries {
		var evt event.Event
		if err := json.Unmarshal(delivery.Body, &evt); err != nil || evt.ID == "" {
			log.Printf("malformed event -> DLQ: %.100s", delivery.Body)
			delivery.Nack(false, false) // no requeue: the broker dead-letters it, since a retry cannot fix a malformed event
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
			delivery.Nack(false, true) // infrastructure failure (e.g. database down): requeue so the event is retried
			continue
		}

		if len(findings) > 0 {
			log.Printf("event %s: %d finding(s)", evt.ID, len(findings))
		}
		delivery.Ack(false) // only now may the broker drop the message
	}
}

// storeAndRequestTriage inserts one finding and, if it is new, publishes a triage request.
func storeAndRequestTriage(db *sql.DB, channel *amqp.Channel, evt event.Event, finding rules.Finding) error {
	var findingID int64
	err := db.QueryRow(`
		INSERT INTO findings (event_id, customer_id, agent_id, rule, severity, ts, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_id, rule) DO NOTHING
		RETURNING id`, // on conflict no row comes back
		evt.ID, evt.CustomerID, evt.AgentID, finding.Rule, finding.Severity, evt.Ts, finding.Detail,
	).Scan(&findingID)

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
