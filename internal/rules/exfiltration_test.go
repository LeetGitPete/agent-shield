package rules

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/leetgitpete/agent-shield/internal/event"
)

const (
	secretPath = "/home/dev/.env"
	unknownURL = "http://exfil-node.xyz/upload"
)

// testClock is the clock the stores under test count expiry from.
type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time          { return clock.now }
func (clock *testClock) Advance(d time.Duration) { clock.now = clock.now.Add(d) }

// fixture is what one scenario works with: two engines that share one store,
// standing in for two detector replicas, and the clock that store runs on.
type fixture struct {
	t        *testing.T
	clock    *testClock
	first    *Engine // "detector-a"
	second   *Engine // "detector-b"
	customer string  // unique per scenario, so runs against a shared Redis never meet
	t0       time.Time
	events   int
}

func newFixture(t *testing.T, newStore func(now func() time.Time) Store) *fixture {
	suffix := make([]byte, 6)
	rand.Read(suffix)

	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: t0}
	store := newStore(clock.Now)
	return &fixture{
		t:        t,
		clock:    clock,
		first:    NewEngine(store, "detector-a"),
		second:   NewEngine(store, "detector-b"),
		customer: "customer-" + hex.EncodeToString(suffix),
		t0:       t0,
	}
}

func (f *fixture) event(agent string, at time.Time, tool event.Tool, key, value string) event.Event {
	f.events++
	return event.Event{
		ID:         fmt.Sprintf("%s-event-%d", f.customer, f.events),
		Ts:         at,
		CustomerID: f.customer,
		AgentID:    agent,
		Tool:       tool,
		Args:       map[string]string{key: value},
	}
}

// read is a secret read by the agent at t0 plus offset.
func (f *fixture) read(agent string, offset time.Duration) event.Event {
	return f.event(agent, f.t0.Add(offset), event.ToolFileRead, "path", secretPath)
}

// request is a non-allowlisted web request by the agent at t0 plus offset.
func (f *fixture) request(agent string, offset time.Duration) event.Event {
	return f.event(agent, f.t0.Add(offset), event.ToolWebFetch, "url", unknownURL)
}

// evaluate returns only the exfiltration findings of one evaluation.
func (f *fixture) evaluate(engine *Engine, evt event.Event) []Finding {
	f.t.Helper()
	findings, err := engine.Evaluate(context.Background(), evt)
	if err != nil {
		f.t.Fatalf("evaluate %s: %v", evt.ID, err)
	}
	var exfiltrations []Finding
	for _, finding := range findings {
		if finding.Rule == RuleExfiltration {
			exfiltrations = append(exfiltrations, finding)
		}
	}
	return exfiltrations
}

// expectOne fails unless there is exactly one finding and it belongs to the request's event.
func (f *fixture) expectOne(findings []Finding, request event.Event) Finding {
	f.t.Helper()
	if len(findings) != 1 {
		f.t.Fatalf("got %d exfiltration finding(s), want exactly 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Event.ID != request.ID {
		f.t.Fatalf("finding belongs to event %s, want the request's event %s", finding.Event.ID, request.ID)
	}
	if finding.Evidence == nil {
		f.t.Fatalf("exfiltration finding without evidence: %+v", finding)
	}
	return finding
}

func (f *fixture) expectNone(findings []Finding) {
	f.t.Helper()
	if len(findings) != 0 {
		f.t.Fatalf("got %d exfiltration finding(s), want none: %+v", len(findings), findings)
	}
}

// exfiltrationScenarios are run against every store implementation. Each
// gets its own fixture.
var exfiltrationScenarios = []struct {
	name string
	run  func(f *fixture)
}{
	{"an allowlisted request after a secret read raises nothing", func(f *fixture) {
		f.evaluate(f.first, f.read("a1", 0))
		allowlisted := f.event("a1", f.t0.Add(2*time.Second), event.ToolWebFetch, "url", "https://github.com/peteler")

		findings, err := f.second.Evaluate(context.Background(), allowlisted)
		if err != nil || len(findings) != 0 {
			f.t.Fatalf("got %+v, %v; want no finding of any rule", findings, err)
		}
	}},

	{"read then request, on different engines", func(f *fixture) {
		read, request := f.read("a1", 0), f.request("a1", 2*time.Second)

		f.expectNone(f.evaluate(f.first, read))
		finding := f.expectOne(f.evaluate(f.second, request), request)

		if finding.Severity != "CRITICAL" {
			f.t.Errorf("severity = %s, want CRITICAL", finding.Severity)
		}
		if finding.Evidence.RaisedBy != RaisedByRequest {
			f.t.Errorf("raised_by = %s, want %s", finding.Evidence.RaisedBy, RaisedByRequest)
		}
	}},

	{"request then read: the finding belongs to the request's event", func(f *fixture) {
		read, request := f.read("a1", 0), f.request("a1", 2*time.Second)

		f.expectNone(f.evaluate(f.first, request))
		finding := f.expectOne(f.evaluate(f.second, read), request)

		if finding.Event.Tool != event.ToolWebFetch || finding.Event.Args["url"] != unknownURL || !finding.Event.Ts.Equal(request.Ts) {
			f.t.Errorf("finding must carry the whole request event, got %+v", finding.Event)
		}
		if finding.Evidence.RaisedBy != RaisedByRead {
			f.t.Errorf("raised_by = %s, want %s", finding.Evidence.RaisedBy, RaisedByRead)
		}
	}},

	{"the 10-minute bound is on event time and includes both ends", func(f *fixture) {
		f.evaluate(f.first, f.read("a1", 0))

		sameInstant := f.request("a1", 0)
		f.expectOne(f.evaluate(f.second, sameInstant), sameInstant)
		atTheBound := f.request("a1", 10*time.Minute)
		f.expectOne(f.evaluate(f.second, atTheBound), atTheBound)

		f.expectNone(f.evaluate(f.second, f.request("a1", 10*time.Minute+time.Millisecond)))
		f.expectNone(f.evaluate(f.second, f.request("a1", -time.Second))) // made before the read
	}},

	{"the 10-minute bound holds when the read is processed second", func(f *fixture) {
		late := f.request("a2", 10*time.Minute+time.Millisecond)
		early := f.request("a2", -time.Second)
		inside := f.request("a2", 10*time.Minute)
		for _, request := range []event.Event{late, early, inside} {
			f.expectNone(f.evaluate(f.first, request))
		}

		f.expectOne(f.evaluate(f.second, f.read("a2", 0)), inside)
	}},

	{"several reads: a request is linked to the latest read before it", func(f *fixture) {
		older, newer := f.read("a1", 0), f.read("a1", time.Minute)
		afterBoth := f.read("a1", 5*time.Minute)
		for _, read := range []event.Event{newer, afterBoth, older} { // processing order differs from event order
			f.evaluate(f.first, read)
		}

		for _, request := range []event.Event{f.request("a1", 2*time.Minute), f.request("a1", 3*time.Minute)} {
			finding := f.expectOne(f.evaluate(f.second, request), request)
			if finding.Evidence.ReadEventID != newer.ID {
				f.t.Errorf("request %s linked to read %s, want the latest earlier read %s", request.ID, finding.Evidence.ReadEventID, newer.ID)
			}
		}
	}},

	{"several requests: a read processed after them raises one finding per request", func(f *fixture) {
		requests := []event.Event{f.request("a1", 2*time.Second), f.request("a1", 3*time.Second), f.request("a1", 4*time.Second)}
		for _, request := range requests {
			f.expectNone(f.evaluate(f.first, request))
		}
		read := f.read("a1", 0)

		findings := f.evaluate(f.second, read)

		if len(findings) != len(requests) {
			f.t.Fatalf("got %d exfiltration finding(s), want one per request (%d): %+v", len(findings), len(requests), findings)
		}
		raisedFor := map[string]int{}
		for _, finding := range findings {
			raisedFor[finding.Event.ID]++
			if finding.Evidence == nil || finding.Evidence.ReadEventID != read.ID {
				f.t.Errorf("finding for %s must reference read %s, got %+v", finding.Event.ID, read.ID, finding.Evidence)
			}
		}
		for _, request := range requests {
			if raisedFor[request.ID] != 1 {
				f.t.Errorf("request %s has %d finding(s), want 1", request.ID, raisedFor[request.ID])
			}
		}
	}},

	{"tenants and agents are kept apart", func(f *fixture) {
		f.evaluate(f.first, f.read("a1", 0))

		otherAgent := f.request("a2", 2*time.Second)
		f.expectNone(f.evaluate(f.second, otherAgent))

		otherTenant := f.request("a1", 2*time.Second) // same agent id under another customer
		otherTenant.CustomerID = f.customer + "-other"
		f.expectNone(f.evaluate(f.second, otherTenant))

		// And in the other direction: the other tenant's read is not linked to this tenant's request.
		otherTenantRead := f.read("a3", 0)
		otherTenantRead.CustomerID = f.customer + "-other"
		f.evaluate(f.first, otherTenantRead)
		f.expectNone(f.evaluate(f.second, f.request("a3", 2*time.Second)))
	}},

	{"evidence names the read and both detectors, in either order", func(f *fixture) {
		read, request := f.read("a1", 0), f.request("a1", 2*time.Second)
		f.evaluate(f.first, read)
		raisedByRequest := f.expectOne(f.evaluate(f.second, request), request)

		laterRead, laterRequest := f.read("a2", 0), f.request("a2", 2*time.Second)
		f.evaluate(f.second, laterRequest)
		raisedByRead := f.expectOne(f.evaluate(f.first, laterRead), laterRequest)

		cases := []struct {
			finding  Finding
			read     event.Event
			raisedBy string
		}{
			{raisedByRequest, read, RaisedByRequest},
			{raisedByRead, laterRead, RaisedByRead},
		}
		for _, c := range cases {
			got := *c.finding.Evidence
			if !got.ReadTs.Equal(c.read.Ts) {
				f.t.Errorf("raised by %s: read_ts = %v, want %v", c.raisedBy, got.ReadTs, c.read.Ts)
			}
			got.ReadTs = time.Time{} // compared above; two equal instants need not be identical values
			want := Evidence{
				ReadEventID:       c.read.ID,
				ReadPath:          secretPath,
				ReadDetectorID:    "detector-a",
				RequestDetectorID: "detector-b",
				RaisedBy:          c.raisedBy,
			}
			if got != want {
				f.t.Errorf("raised by %s: evidence = %+v, want %+v", c.raisedBy, got, want)
			}
		}

		// Both sides describe the pair in the same words.
		if raisedByRequest.Detail != raisedByRead.Detail {
			f.t.Errorf("detail differs by side:\n  request: %s\n  read:    %s", raisedByRequest.Detail, raisedByRead.Detail)
		}
		for _, part := range []string{secretPath, unknownURL, "2s"} {
			if !strings.Contains(raisedByRequest.Detail, part) {
				f.t.Errorf("detail %q does not name %q", raisedByRequest.Detail, part)
			}
		}
	}},

	{"a request recorded again by the same and by another engine yields one finding", func(f *fixture) {
		read, request := f.read("a1", 0), f.request("a1", 2*time.Second)

		f.expectNone(f.evaluate(f.first, request))
		f.clock.Advance(time.Second)
		f.expectNone(f.evaluate(f.first, request)) // redelivered to the same detector
		f.clock.Advance(time.Second)
		f.expectNone(f.evaluate(f.second, request)) // redelivered to the other detector

		finding := f.expectOne(f.evaluate(f.second, read), request)
		if finding.Evidence.RequestDetectorID != "detector-a" {
			f.t.Errorf("request_detector_id = %s, want the first record's detector-a", finding.Evidence.RequestDetectorID)
		}
	}},

	{"a read recorded by two engines yields one finding", func(f *fixture) {
		read, request := f.read("a1", 0), f.request("a1", 2*time.Second)

		f.expectNone(f.evaluate(f.first, read))
		f.clock.Advance(time.Second)
		f.expectNone(f.evaluate(f.second, read)) // redelivered to the other detector

		finding := f.expectOne(f.evaluate(f.first, request), request)
		if finding.Evidence.ReadDetectorID != "detector-a" {
			f.t.Errorf("read_detector_id = %s, want the first record's detector-a", finding.Evidence.ReadDetectorID)
		}
	}},

	{"a read record expires 10 minutes after it was processed", func(f *fixture) {
		// Event times stay inside the window throughout: only processing time moves.
		f.evaluate(f.first, f.read("a1", 0))

		f.clock.Advance(SecretReadTTL - time.Second)
		stillLive := f.request("a1", 2*time.Second)
		f.expectOne(f.evaluate(f.second, stillLive), stillLive)

		f.clock.Advance(time.Second)
		f.expectNone(f.evaluate(f.second, f.request("a1", 3*time.Second)))
	}},

	{"a request record expires 60 seconds after it was processed", func(f *fixture) {
		expired := f.request("a1", 2*time.Second)
		f.evaluate(f.first, expired)
		f.clock.Advance(WebRequestTTL - time.Second)
		stillLive := f.request("a1", 3*time.Second)
		f.evaluate(f.first, stillLive)
		f.clock.Advance(time.Second) // the first request is now 60 seconds old, the second one second

		f.expectOne(f.evaluate(f.second, f.read("a1", 0)), stillLive)
	}},

	{"recording a request again keeps it live", func(f *fixture) {
		request := f.request("a1", 2*time.Second)
		f.evaluate(f.first, request)
		f.clock.Advance(WebRequestTTL - time.Second)
		f.evaluate(f.first, request) // the same record: its expiry starts over
		f.clock.Advance(WebRequestTTL - time.Second)

		f.expectOne(f.evaluate(f.second, f.read("a1", 0)), request)
	}},
}

func runExfiltrationScenarios(t *testing.T, newStore func(now func() time.Time) Store) {
	for _, scenario := range exfiltrationScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			scenario.run(newFixture(t, newStore))
		})
	}
}

func TestExfiltrationWithMemoryStore(t *testing.T) {
	runExfiltrationScenarios(t, func(now func() time.Time) Store {
		return NewMemoryStore(now)
	})
}

// TestExfiltrationWithRedisStore runs the same scenarios against the Redis
// at REDIS_ADDR. CI must run it; elsewhere it is skipped without the address.
func TestExfiltrationWithRedisStore(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("REDIS_ADDR is not set: CI must run this test against a real Redis")
		}
		t.Skip("REDIS_ADDR is not set")
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis at %s: %v", addr, err)
	}

	runExfiltrationScenarios(t, func(now func() time.Time) Store {
		return NewRedisStore(client, now)
	})
}
