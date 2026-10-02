// The detector consumes events from RabbitMQ, runs the rules engine,
// stores findings in Postgres and requests LLM triage for new ones.
//
// Any number of replicas can consume the same queue: the state of the
// exfiltration rule lives in Redis, shared by all of them.
//
// Delivery semantics: at-least-once. A message is acked only after its
// findings are safely in Postgres; if we crash mid-way RabbitMQ redelivers,
// and the (event_id, rule) unique constraint makes the redelivery harmless.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"

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

// A message that could not be processed because Redis or Postgres failed is
// requeued once this long has passed since the detector took it. Deliberate
// back-pressure: a failing dependency is retried about once a second, however
// long the failed call took, so the detector neither spins on the same
// messages nor floods the log.
const infrastructureRetryPause = time.Second

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	amqpURL := envOr("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	postgresURL := envOr("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/agentshield")
	redisAddr := envOr("REDIS_ADDR", "localhost:6379")

	// A synthetic per-event evaluation cost. It stands in for heavier rule
	// evaluation during load testing: with it one replica saturates at a rate
	// small hardware can produce, so scaling out can be exercised.
	evalDelayMs, err := strconv.Atoi(envOr("RULE_EVAL_DELAY_MS", "0"))
	if err != nil || evalDelayMs < 0 {
		log.Fatal("RULE_EVAL_DELAY_MS must be a whole number of milliseconds, zero or more")
	}
	evalDelay := time.Duration(evalDelayMs) * time.Millisecond

	// The hostname is the pod name in Kubernetes, so it tells replicas apart.
	detectorID, err := os.Hostname()
	if err != nil {
		log.Fatal("read hostname: ", err)
	}
	log.Println("detector starting, identity:", detectorID)
	if evalDelay > 0 {
		log.Println("synthetic evaluation cost: every event takes at least", evalDelay)
	}

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()
	if err := schema.Setup(db); err != nil {
		log.Fatal("set up schema: ", err)
	}

	// Short timeouts and a single attempt per call: a stuck call must not
	// stall the consumer, and the retry is the requeue in the loop below.
	redisClient := redis.NewClient(&redis.Options{
		Addr:          redisAddr,
		DialTimeout:   time.Second,
		ReadTimeout:   time.Second,
		WriteTimeout:  time.Second,
		MaxRetries:    -1, // commands are not retried
		DialerRetries: 1,  // one dial attempt
		PoolSize:      2,  // events are processed one at a time
	})
	defer redisClient.Close()
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatal("connect to redis: ", err)
	}

	// The connection carries the detector's identity, so the broker's
	// connection list shows which replica holds which connection.
	properties := amqp.NewConnectionProperties()
	properties.SetClientConnectionName(detectorID)
	connection, err := amqp.DialConfig(amqpURL, amqp.Config{Properties: properties})
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

	engine := rules.NewEngine(rules.NewRedisStore(redisClient, time.Now), detectorID)
	log.Println("detector running")

	for delivery := range deliveries {
		var evt event.Event
		if err := json.Unmarshal(delivery.Body, &evt); err != nil || evt.ID == "" {
			log.Printf("malformed event -> DLQ: %.100s", delivery.Body)
			delivery.Nack(false, false) // no requeue: the broker dead-letters it, since a retry cannot fix a malformed event
			continue
		}

		taken := time.Now()
		if err := process(db, channel, engine, detectorID, evt); err != nil {
			// Redis or Postgres failed: the event waits in the queue and is
			// retried, so it is never evaluated without the state it needs.
			log.Printf("event %s: %v; requeueing", evt.ID, err)
			time.Sleep(time.Until(taken.Add(infrastructureRetryPause)))
			delivery.Nack(false, true)
			continue
		}
		// The evaluation cost is a minimum time per event, not an addition
		// to the time the event really took.
		time.Sleep(time.Until(taken.Add(evalDelay)))
		delivery.Ack(false) // only now may the broker drop the message
	}
}

// process evaluates one event and stores its findings.
func process(db *sql.DB, channel *amqp.Channel, engine *rules.Engine, detectorID string, evt event.Event) error {
	findings, err := engine.Evaluate(context.Background(), evt)
	if err != nil {
		return err
	}
	for _, finding := range findings {
		if err := storeAndRequestTriage(db, channel, detectorID, finding); err != nil {
			return err
		}
	}
	if len(findings) > 0 {
		log.Printf("event %s: %d finding(s)", evt.ID, len(findings))
	}
	return nil
}

// storeAndRequestTriage inserts one finding and, if it is new, publishes a
// triage request. Both are built from the event the finding belongs to,
// which for an exfiltration can be an earlier event than the one being
// processed. An exfiltration can be raised twice, once from each of its two
// events; the unique constraint keeps the first.
func storeAndRequestTriage(db *sql.DB, channel *amqp.Channel, detectorID string, finding rules.Finding) error {
	var evidence any // stays nil, and NULL in the table, for rules without evidence
	if finding.Evidence != nil {
		encoded, err := json.Marshal(finding.Evidence)
		if err != nil {
			return err
		}
		evidence = string(encoded)
	}

	evt := finding.Event
	var findingID int64
	err := db.QueryRow(`
		INSERT INTO findings (event_id, customer_id, agent_id, rule, severity, ts, detail, detector_id, evidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		ON CONFLICT (event_id, rule) DO NOTHING
		RETURNING id`, // on conflict no row comes back
		evt.ID, evt.CustomerID, evt.AgentID, finding.Rule, finding.Severity, evt.Ts, finding.Detail, detectorID, evidence,
	).Scan(&findingID)

	if err == sql.ErrNoRows {
		return nil // already stored: a redelivery, or the other side of an exfiltration; no second triage request
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
