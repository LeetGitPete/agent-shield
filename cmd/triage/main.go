// The triage service is the second-tier reviewer: it consumes triage
// requests published by the detector, asks its provider whether the finding
// is really malicious, and writes the result onto the finding in Postgres.
// Rules are cheap and run on everything; the LLM is slow and rate-limited,
// so it only sees events that already matched a rule.
//
// The provider is Gemini or mock, chosen by TRIAGE_PROVIDER. The mock
// provider produces no verdict; it lets the pipeline run without a provider
// key or quota.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
	"github.com/leetgitpete/agent-shield/internal/schema"
)

// Mirrors the detector's TriageRequest.
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

	provider, err := providerFromEnv()
	if err != nil {
		log.Fatal("triage provider: ", err)
	}
	// Logged before anything is consumed, so the configuration is never a guess.
	log.Println("triage starting, provider:", provider)

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

	if err := channel.Qos(1, 0, false); err != nil { // one at a time: the LLM is the bottleneck anyway
		log.Fatal("set qos: ", err)
	}
	deliveries, err := channel.Consume(mq.TriageQueue, "triage", false, false, false, false, nil)
	if err != nil {
		log.Fatal("consume: ", err)
	}

	log.Println("triage running")

	for delivery := range deliveries {
		var request TriageRequest
		if err := json.Unmarshal(delivery.Body, &request); err != nil {
			log.Printf("malformed triage request, dropping: %.100s", delivery.Body)
			delivery.Ack(false) // triage is best-effort: a lost verdict is acceptable, a stuck queue is not
			continue
		}

		verdict, err := provider.Judge(request)
		result := decide(provider.Name(), verdict, err, delivery.Redelivered, time.Now().UTC())
		if result.Retry {
			log.Printf("finding %d: triage failed, will retry: %v", request.FindingID, err)
			time.Sleep(10 * time.Second) // crude rate-limit backoff before retrying
			delivery.Nack(false, true)
			continue
		}
		if result.GaveUp { // second failure: give up rather than loop forever
			log.Printf("finding %d: triage failed twice, giving up: %v", request.FindingID, err)
		}

		// An outcome without a verdict never replaces a verdict: a request can be
		// redelivered after its verdict was written but before it was acknowledged.
		if _, err := db.Exec(`
			UPDATE findings SET llm_verdict = $1, verdict_source = $2, triaged_at = $3
			WHERE id = $4 AND ($1::text IS NOT NULL OR llm_verdict IS NULL)`,
			result.Verdict, result.Source, result.TriagedAt, request.FindingID); err != nil {
			log.Println("update finding: ", err)
			if result.GaveUp {
				delivery.Ack(false) // a requeue would call the provider a third time; the finding stays pending instead
			} else {
				delivery.Nack(false, true)
			}
			continue
		}
		if result.Verdict != nil {
			log.Printf("finding %d (%s): %s", request.FindingID, request.Rule, *result.Verdict)
		} else {
			log.Printf("finding %d (%s): processed by %s, no verdict", request.FindingID, request.Rule, result.Source)
		}
		delivery.Ack(false)
	}
}
