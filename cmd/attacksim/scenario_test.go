package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/leetgitpete/agent-shield/internal/event"
	"github.com/leetgitpete/agent-shield/internal/rules"
)

var requestTime = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func tools(events []event.Event) []event.Tool {
	var tools []event.Tool
	for _, evt := range events {
		tools = append(tools, evt.Tool)
	}
	return tools
}

func TestBatchesPublishReadsFirstNormallyAndRequestsFirstOutOfOrder(t *testing.T) {
	simulation := newScenario("customer_1", 3, requestTime)

	cases := []struct {
		name          string
		outOfOrder    bool
		first, second event.Tool
	}{
		{"normal", false, event.ToolFileRead, event.ToolWebFetch},
		{"out of order", true, event.ToolWebFetch, event.ToolFileRead},
	}
	for _, c := range cases {
		first, second := simulation.batches(c.outOfOrder)

		if len(first) != 3 || len(second) != 3 {
			t.Fatalf("%s: batches of %d and %d events, want 3 and 3", c.name, len(first), len(second))
		}
		for i := range first {
			if first[i].Tool != c.first || second[i].Tool != c.second {
				t.Errorf("%s: tools are %v then %v, want every %s before every %s",
					c.name, tools(first), tools(second), c.first, c.second)
				break
			}
			// Agents keep their order within a batch.
			wantAgent := fmt.Sprintf("attack_%s_%d", simulation.Run, i+1)
			if first[i].AgentID != wantAgent || second[i].AgentID != wantAgent {
				t.Errorf("%s: position %d holds agents %s and %s, want %s", c.name, i, first[i].AgentID, second[i].AgentID, wantAgent)
			}
		}

		// Event times do not follow the publishing order: the read is always the earlier event.
		for _, evt := range append(first, second...) {
			want := requestTime
			if evt.Tool == event.ToolFileRead {
				want = requestTime.Add(-2 * time.Second)
			}
			if !evt.Ts.Equal(want) {
				t.Errorf("%s: %s of %s has event time %v, want %v", c.name, evt.Tool, evt.AgentID, evt.Ts, want)
			}
			if evt.CustomerID != "customer_1" {
				t.Errorf("%s: customer = %s, want customer_1", c.name, evt.CustomerID)
			}
		}
	}
}

func TestRunsUseDistinctIdentifiers(t *testing.T) {
	seenAgents, seenEvents := map[string]bool{}, map[string]bool{}
	seenRuns := map[string]bool{}

	for run := 0; run < 2; run++ {
		simulation := newScenario("customer_1", 3, requestTime)
		if seenRuns[simulation.Run] {
			t.Errorf("run suffix %s used twice", simulation.Run)
		}
		seenRuns[simulation.Run] = true

		for _, attack := range simulation.Attacks {
			if seenAgents[attack.AgentID] {
				t.Errorf("agent id %s used twice", attack.AgentID)
			}
			seenAgents[attack.AgentID] = true
			for _, evt := range []event.Event{attack.Read, attack.Request} {
				if evt.ID == "" || seenEvents[evt.ID] {
					t.Errorf("event id %q is empty or used twice", evt.ID)
				}
				seenEvents[evt.ID] = true
			}
		}
	}
	if len(seenAgents) != 6 || len(seenEvents) != 12 {
		t.Errorf("two runs of 3 agents gave %d agent ids and %d event ids, want 6 and 12", len(seenAgents), len(seenEvents))
	}
}

// The scenario is only useful if the rules engine sees an exfiltration in
// it, whichever order the events arrive in.
func TestEngineRaisesOneExfiltrationPerAgentInBothOrders(t *testing.T) {
	for _, outOfOrder := range []bool{false, true} {
		simulation := newScenario("customer_1", 3, requestTime)
		engine := rules.NewEngine(rules.NewMemoryStore(time.Now), "detector-test")

		raisedFor := map[string]int{}
		first, second := simulation.batches(outOfOrder)
		for _, evt := range append(first, second...) {
			findings, err := engine.Evaluate(context.Background(), evt)
			if err != nil {
				t.Fatal(err)
			}
			for _, finding := range findings {
				if finding.Rule == rules.RuleExfiltration {
					raisedFor[finding.Event.AgentID]++
				}
			}
		}

		for _, attack := range simulation.Attacks {
			if raisedFor[attack.AgentID] != 1 {
				t.Errorf("out of order %v: agent %s has %d exfiltration finding(s), want 1", outOfOrder, attack.AgentID, raisedFor[attack.AgentID])
			}
		}
	}
}
