package main

import (
	"context"
	mathrand "math/rand/v2"
	"reflect"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/rules"
)

var start = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

var startingFleet = Config{Customers: 4, AgentsPerCustomer: 10, EventsPerSec: 10, SuspiciousProb: 0.005}

func seeded(config Config, seed uint64) *Generator {
	return NewGenerator(config, mathrand.New(mathrand.NewPCG(seed, 0)), start)
}

func take(generator *Generator, count int) []event.Event {
	events := make([]event.Event, 0, count)
	for range count {
		evt, _ := generator.Next()
		events = append(events, evt)
	}
	return events
}

// findingsFor counts the findings the detector's rules raise for the events.
func findingsFor(t *testing.T, events []event.Event) (eventsWithFindings int) {
	t.Helper()
	engine := rules.NewEngine(rules.NewMemoryStore(time.Now), "test")
	for _, evt := range events {
		findings, err := engine.Evaluate(context.Background(), evt)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) > 0 {
			eventsWithFindings++
		}
	}
	return eventsWithFindings
}

func TestTheSameSeedGivesTheSameSequence(t *testing.T) {
	first := take(seeded(startingFleet, 7), 2000)
	second := take(seeded(startingFleet, 7), 2000)
	if !reflect.DeepEqual(first, second) {
		t.Error("two generators with the same seed produced different sequences")
	}

	other := take(seeded(startingFleet, 8), 2000)
	if reflect.DeepEqual(first, other) {
		t.Error("two generators with different seeds produced the same sequence")
	}
}

func TestIdentifiersHaveTheDocumentedForm(t *testing.T) {
	config := Config{Customers: 3, AgentsPerCustomer: 4, EventsPerSec: 10, SuspiciousProb: 0.005}
	customerForm := regexp.MustCompile(`^customer_([1-3])$`)
	agentForm := regexp.MustCompile(`^agent_([1-3])_([1-4])$`)
	eventIDForm := regexp.MustCompile(`^[0-9a-f]{32}$`)

	agents := map[string]bool{}
	eventIDs := map[string]bool{}
	for _, evt := range take(seeded(config, 1), 5000) {
		customer := customerForm.FindStringSubmatch(evt.CustomerID)
		agent := agentForm.FindStringSubmatch(evt.AgentID)
		if customer == nil || agent == nil {
			t.Fatalf("event of customer %q, agent %q: want the forms customer_1 and agent_1_3", evt.CustomerID, evt.AgentID)
		}
		if agent[1] != customer[1] {
			t.Fatalf("agent %q belongs to %q: the agent id must name its customer", evt.AgentID, evt.CustomerID)
		}
		if !eventIDForm.MatchString(evt.ID) {
			t.Fatalf("event id %q: want 32 hex characters", evt.ID)
		}
		agents[evt.AgentID] = true
		eventIDs[evt.ID] = true
	}
	if len(agents) != 12 {
		t.Errorf("events came from %d agents, want all 12 of 3 customers with 4 agents each", len(agents))
	}
	if len(eventIDs) != 5000 {
		t.Errorf("%d distinct event ids among 5000 events", len(eventIDs))
	}
}

func TestSuspiciousProbabilityDecidesWhichEventsRaiseFindings(t *testing.T) {
	cases := []struct {
		probability float64
		wantRaising int // events the rules raise a finding for, out of 5000
	}{
		{0, 0},
		{1, 5000},
	}
	for _, c := range cases {
		config := startingFleet
		config.SuspiciousProb = c.probability
		generator := seeded(config, 3)

		var events []event.Event
		marked := 0
		for range 5000 {
			evt, suspicious := generator.Next()
			events = append(events, evt)
			if suspicious {
				marked++
			}
		}
		if raising := findingsFor(t, events); raising != c.wantRaising || marked != c.wantRaising {
			t.Errorf("suspicious probability %v: the rules raise findings for %d of 5000 events and %d are marked suspicious, want %d",
				c.probability, raising, marked, c.wantRaising)
		}
	}
}

func TestAverageRateOverOneHourIsWithinTenPercentOfTheTarget(t *testing.T) {
	for _, target := range []float64{10, 500} {
		for seed := uint64(1); seed <= 3; seed++ {
			config := startingFleet
			config.EventsPerSec = target
			generator := seeded(config, seed)

			count := 0
			previous := start
			for {
				evt, _ := generator.Next()
				if evt.Ts.Before(previous) {
					t.Fatalf("event due at %v came after one due at %v: events must come in time order", evt.Ts, previous)
				}
				previous = evt.Ts
				if !evt.Ts.Before(start.Add(time.Hour)) {
					break
				}
				count++
			}

			average := float64(count) / time.Hour.Seconds()
			if average < 0.9*target || average > 1.1*target {
				t.Errorf("target %v events per second, seed %d: averaged %s over one simulated hour",
					target, seed, strconv.FormatFloat(average, 'f', 2, 64))
			}
		}
	}
}
