// The sensor mocks the component that would sit on customers' machines
// watching AI agents' tool calls (in production: an MCP proxy in each
// agent's execution path). One process simulates a whole fleet: several
// customers, each with several agents that alternate between bursts of work
// and idle periods, so the load is irregular in the way real agent activity
// is. It publishes the fleet's events to RabbitMQ.
//
// Settings: SENSOR_CUSTOMERS, SENSOR_AGENTS_PER_CUSTOMER,
// SENSOR_EVENTS_PER_SEC (the fleet's average rate), SENSOR_SUSPICIOUS_PROB
// and SENSOR_SEED. With a seed the sequence is reproducible, event ids
// included. Findings are unique per event id, so the findings of an earlier
// run with the same seed have to be removed before a replay can store its own.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	mathrand "math/rand/v2"
	"os"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/mq"
)

// The sensor logs one summary per interval and no line per event.
const summaryInterval = 10 * time.Second

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// configFromEnv reads the load settings. The defaults are the starting fleet
// size and the low load level.
func configFromEnv() (Config, error) {
	var config Config
	var err error
	if config.Customers, err = strconv.Atoi(envOr("SENSOR_CUSTOMERS", "4")); err != nil || config.Customers < 1 {
		return config, fmt.Errorf("SENSOR_CUSTOMERS must be a whole number, one or more")
	}
	if config.AgentsPerCustomer, err = strconv.Atoi(envOr("SENSOR_AGENTS_PER_CUSTOMER", "10")); err != nil || config.AgentsPerCustomer < 1 {
		return config, fmt.Errorf("SENSOR_AGENTS_PER_CUSTOMER must be a whole number, one or more")
	}
	// The comparisons are written so that NaN fails them.
	if config.EventsPerSec, err = strconv.ParseFloat(envOr("SENSOR_EVENTS_PER_SEC", "10"), 64); err != nil || !(config.EventsPerSec > 0 && config.EventsPerSec <= 100000) {
		return config, fmt.Errorf("SENSOR_EVENTS_PER_SEC must be a number above 0 and at most 100000")
	}
	if config.SuspiciousProb, err = strconv.ParseFloat(envOr("SENSOR_SUSPICIOUS_PROB", "0.005"), 64); err != nil || !(config.SuspiciousProb >= 0 && config.SuspiciousProb <= 1) {
		return config, fmt.Errorf("SENSOR_SUSPICIOUS_PROB must be a number from 0 to 1")
	}
	return config, nil
}

// randomFromEnv returns the generator's random source: seeded from
// SENSOR_SEED when it is set, randomly seeded otherwise.
func randomFromEnv() (random *mathrand.Rand, seedText string, err error) {
	seedText = os.Getenv("SENSOR_SEED")
	if seedText == "" {
		return mathrand.New(mathrand.NewPCG(mathrand.Uint64(), mathrand.Uint64())), "random", nil
	}
	seed, err := strconv.ParseUint(seedText, 10, 64)
	if err != nil {
		return nil, "", fmt.Errorf("SENSOR_SEED must be a whole number, zero or more")
	}
	return mathrand.New(mathrand.NewPCG(seed, 0)), seedText, nil
}

func main() {
	amqpURL := envOr("AMQP_URL", "amqp://guest:guest@localhost:5672/")
	config, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	random, seed, err := randomFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("sensor starting: customers=%d agents_per_customer=%d events_per_sec=%g suspicious_prob=%g seed=%s",
		config.Customers, config.AgentsPerCustomer, config.EventsPerSec, config.SuspiciousProb, seed)

	connection, err := amqp.Dial(amqpURL)
	if err != nil {
		log.Fatal("connect to rabbitmq: ", err) // the sensor is useless without the broker
	}
	defer connection.Close()

	channel, err := connection.Channel()
	if err != nil {
		log.Fatal("open channel: ", err)
	}
	defer channel.Close()

	if err := mq.Declare(channel); err != nil { // the queues must exist before the first publish
		log.Fatal("declare queues: ", err)
	}

	// The generator decides when each event is due; this loop waits for that
	// time and publishes. The schedule is absolute, so a late publish does
	// not push the events after it back.
	generator := NewGenerator(config, random, time.Now().UTC())
	nextSummary := time.Now().Add(summaryInterval)
	published, suspiciousPublished, total := 0, 0, 0
	for {
		evt, suspicious := generator.Next()

		// Summaries keep their own schedule, so they also appear while every agent is idle.
		for evt.Ts.After(nextSummary) {
			time.Sleep(time.Until(nextSummary))
			total += published
			log.Printf("published %d events in the last %s (%.1f per second), %d suspicious; %d since start",
				published, summaryInterval, float64(published)/summaryInterval.Seconds(), suspiciousPublished, total)
			published, suspiciousPublished = 0, 0
			nextSummary = nextSummary.Add(summaryInterval)
		}
		time.Sleep(time.Until(evt.Ts))

		payload, err := json.Marshal(evt)
		if err != nil {
			log.Fatal("marshal event: ", err)
		}
		if err := mq.PublishJSON(channel, mq.EventsQueue, payload); err != nil {
			log.Fatal("publish: ", err)
		}
		published++
		if suspicious {
			suspiciousPublished++
		}
	}
}
