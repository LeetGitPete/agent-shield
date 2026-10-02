package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/leetgitpete/agent-shield/internal/event"
)

// The attack each simulated agent carries out: it reads a secret and then
// sends a web request to a domain outside the allowlist.
const (
	secretPath = "/home/dev/.env"
	requestURL = "http://exfil-node.xyz/upload"
)

// The read's event time is this long before the request's. The times are set
// explicitly, so detection does not depend on when the events are published.
const readBeforeRequest = 2 * time.Second

// scenario is one run of the simulation.
type scenario struct {
	Run     string // short random suffix that makes the agent ids unique to this run
	Attacks []attack
}

// attack is the two events of one simulated agent.
type attack struct {
	AgentID string
	Read    event.Event
	Request event.Event
}

// newScenario builds a run with fresh identifiers. Agent ids have the form
// attack_<run>_<n>, which neither a sensor nor another run produces.
// requestTime is the event time of every request.
func newScenario(customerID string, agents int, requestTime time.Time) scenario {
	run := randomHex(3)
	simulation := scenario{Run: run}
	for n := 1; n <= agents; n++ {
		agentID := fmt.Sprintf("attack_%s_%d", run, n)
		simulation.Attacks = append(simulation.Attacks, attack{
			AgentID: agentID,
			Read: event.Event{
				ID:         randomHex(16),
				Ts:         requestTime.Add(-readBeforeRequest),
				CustomerID: customerID,
				AgentID:    agentID,
				Tool:       event.ToolFileRead,
				Args:       map[string]string{"path": secretPath},
			},
			Request: event.Event{
				ID:         randomHex(16),
				Ts:         requestTime,
				CustomerID: customerID,
				AgentID:    agentID,
				Tool:       event.ToolWebFetch,
				Args:       map[string]string{"url": requestURL},
			},
		})
	}
	return simulation
}

// batches returns the events in publishing order: one kind for every agent,
// then, after the gap, the other kind. Normally the reads go first. Out of
// order the requests go first, while the reads keep their earlier event time.
func (simulation scenario) batches(outOfOrder bool) (first, second []event.Event) {
	var reads, requests []event.Event
	for _, attack := range simulation.Attacks {
		reads = append(reads, attack.Read)
		requests = append(requests, attack.Request)
	}
	if outOfOrder {
		return requests, reads
	}
	return reads, requests
}

func randomHex(byteCount int) string {
	buffer := make([]byte, byteCount)
	rand.Read(buffer)
	return hex.EncodeToString(buffer)
}
