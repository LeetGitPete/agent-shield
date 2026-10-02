// attacksim publishes a known attack to the events queue, so detection can
// be verified end to end: for each simulated agent, a secret read followed
// by a web request to a domain outside the allowlist. Every agent should end
// up with exactly one exfiltration finding.
//
//	attacksim [-agents 3] [-gap 300ms] [-out-of-order] [-customer customer_1]
//
// It publishes one kind of event for all agents, waits for the gap, then
// publishes the other kind. With -out-of-order the web requests go first,
// which exercises the path where a detector processes the read second.
//
// The broker address comes from AMQP_URL, like the services'.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/mq"
)

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	agents := flag.Int("agents", 3, "number of simulated agents")
	gap := flag.Duration("gap", 300*time.Millisecond, "wait between publishing the first and the second kind of event")
	outOfOrder := flag.Bool("out-of-order", false, "publish the web requests before the secret reads")
	customerID := flag.String("customer", "customer_1", "customer id the events are published under")
	flag.Parse()

	if flag.NArg() > 0 || *agents < 1 || *gap < 0 {
		fmt.Fprintln(os.Stderr, "attacksim: agents must be at least 1, gap must not be negative, and there are no other arguments")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*customerID, *agents, *gap, *outOfOrder); err != nil {
		fmt.Fprintln(os.Stderr, "attacksim:", err)
		os.Exit(1)
	}
}

func run(customerID string, agents int, gap time.Duration, outOfOrder bool) error {
	fmt.Printf("attack simulation: customer=%s agents=%d gap=%s out-of-order=%t\n", customerID, agents, gap, outOfOrder)

	connection, err := amqp.Dial(envOr("AMQP_URL", "amqp://guest:guest@localhost:5672/"))
	if err != nil {
		return fmt.Errorf("connect to rabbitmq: %w", err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("open channel: %w", err)
	}
	if err := mq.Declare(channel); err != nil {
		return fmt.Errorf("declare queues: %w", err)
	}

	// A passive declare changes nothing and reports the queue's consumers,
	// which are the running detectors.
	eventsQueue, err := channel.QueueDeclarePassive(mq.EventsQueue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("inspect the events queue: %w", err)
	}
	fmt.Printf("detectors consuming the events queue: %d\n", eventsQueue.Consumers)
	if eventsQueue.Consumers < 2 {
		fmt.Println("note: fewer than two detectors are consuming, so the read and the request cannot land on different detectors")
	}

	simulation := newScenario(customerID, agents, time.Now().UTC())
	first, second := simulation.batches(outOfOrder)
	if err := publish(channel, first); err != nil {
		return err
	}
	time.Sleep(gap)
	if err := publish(channel, second); err != nil {
		return err
	}
	// Closing waits for the broker to confirm the close, by which time it
	// has taken every message sent before it.
	if err := channel.Close(); err != nil {
		return fmt.Errorf("close channel: %w", err)
	}

	firstKind, secondKind := "secret reads", "web requests"
	if outOfOrder {
		firstKind, secondKind = secondKind, firstKind
	}
	fmt.Printf("published %d %s, waited %s, published %d %s\n", len(first), firstKind, gap, len(second), secondKind)
	fmt.Printf("run %s:\n", simulation.Run)
	for _, attack := range simulation.Attacks {
		fmt.Printf("  agent %s  read %s  request %s\n", attack.AgentID, attack.Read.ID, attack.Request.ID)
	}
	fmt.Printf("expected: one exfiltration finding per agent, on the request's event (GET /findings?customer=%s&rule=exfiltration)\n", customerID)
	return nil
}

func publish(channel *amqp.Channel, events []event.Event) error {
	for _, evt := range events {
		payload, err := json.Marshal(evt)
		if err != nil {
			return err
		}
		if err := mq.PublishJSON(channel, mq.EventsQueue, payload); err != nil {
			return fmt.Errorf("publish: %w", err)
		}
	}
	return nil
}
