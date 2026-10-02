// Package rules is the detection engine: it decides whether an agent
// tool-call event is suspicious, and how severe it is.
package rules

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/leetgitpete/agent-shield/internal/event"
)

// Finding is one rule match; the detector stores these in Postgres.
type Finding struct {
	// Event is the event the finding belongs to. For an exfiltration raised
	// while evaluating the secret read, that is the earlier web request, not
	// the event being evaluated.
	Event    event.Event
	Rule     string    // machine-readable rule name, e.g. "pipe_to_shell"
	Severity string    // MEDIUM, HIGH or CRITICAL
	Detail   string    // human-readable explanation for the analyst
	Evidence *Evidence // nil for rules without evidence
}

// Evidence is what an exfiltration finding was correlated from. It is stored
// with the finding and returned by the API under these names.
type Evidence struct {
	ReadEventID       string    `json:"read_event_id"`
	ReadTs            time.Time `json:"read_ts"`
	ReadPath          string    `json:"read_path"`
	ReadDetectorID    string    `json:"read_detector_id"`    // the detector that processed the read
	RequestDetectorID string    `json:"request_detector_id"` // the detector that processed the request
	RaisedBy          string    `json:"raised_by"`           // which of the two events was processed second
}

// Values of Evidence.RaisedBy.
const (
	RaisedByRead    = "read"
	RaisedByRequest = "request"
)

// Rule names, as stored on findings and accepted by the API's rule filter.
const (
	RuleSecretFileRead = "secret_file_read"
	RulePipeToShell    = "pipe_to_shell"
	RuleUnknownDomain  = "unknown_domain"
	RuleExfiltration   = "exfiltration"
)

// Names lists every rule the engine can raise.
var Names = []string{RuleSecretFileRead, RulePipeToShell, RuleUnknownDomain, RuleExfiltration}

// A web request is linked to a secret read made at most this long before it,
// measured by event time.
const exfiltrationWindow = 10 * time.Minute

// Reading any path containing one of these fragments counts as touching a secret.
var secretPathFragments = []string{".env", "id_rsa", "id_ed25519", ".aws/credentials", ".ssh/"}

// Domains an agent may fetch without raising a finding.
var allowedDomains = map[string]bool{
	"github.com":        true,
	"pkg.go.dev":        true,
	"go.dev":            true,
	"golang.org":        true,
	"stackoverflow.com": true,
}

// Engine runs every rule against each incoming event. The exfiltration rule
// is stateful: it links a web request to an earlier secret read by the same
// agent. That state lives in the store, so engines that share a store detect
// an exfiltration whichever of them evaluates which event, in either order.
type Engine struct {
	store      Store
	detectorID string
}

// NewEngine returns an engine that keeps its state in store. detectorID
// names the detector the engine runs in; it is recorded with the state and
// in the evidence.
func NewEngine(store Store, detectorID string) *Engine {
	return &Engine{store: store, detectorID: detectorID}
}

// Evaluate returns all findings for one event (a single event can match
// several rules). Only secret reads and non-allowlisted web requests touch
// the store; an error means the store failed and the event must be evaluated
// again.
func (engine *Engine) Evaluate(ctx context.Context, evt event.Event) ([]Finding, error) {
	var findings []Finding

	switch evt.Tool {

	case event.ToolFileRead:
		path := evt.Args["path"]
		if !isSecretPath(path) {
			break
		}
		findings = append(findings, Finding{ // rule 1: secret file access
			Event:    evt,
			Rule:     RuleSecretFileRead,
			Severity: "HIGH",
			Detail:   "agent read secret file " + path,
		})

		// Rule 4, read side: a request another detector has already
		// evaluated found no read, so the finding is raised here on its behalf.
		read := SecretRead{EventID: evt.ID, Ts: evt.Ts, Path: path, DetectorID: engine.detectorID}
		requests, err := engine.store.RecordSecretRead(ctx, evt.CustomerID, evt.AgentID, read)
		if err != nil {
			return nil, err
		}
		requests = firstPerEvent(requests, func(request WebRequest) string { return request.Event.ID })
		for _, request := range requests {
			if withinWindow(read, request.Event) {
				findings = append(findings, exfiltration(read, request, RaisedByRead))
			}
		}

	case event.ToolBashExec:
		if isPipeToShell(evt.Args["command"]) { // rule 2: downloaded code piped into a shell
			findings = append(findings, Finding{
				Event:    evt,
				Rule:     RulePipeToShell,
				Severity: "CRITICAL",
				Detail:   "agent piped a download into a shell: " + evt.Args["command"],
			})
		}

	case event.ToolWebFetch:
		domain := domainOf(evt.Args["url"])
		if domain == "" || allowedDomains[domain] {
			break
		}
		findings = append(findings, Finding{ // rule 3: fetch outside the allowlist
			Event:    evt,
			Rule:     RuleUnknownDomain,
			Severity: "MEDIUM",
			Detail:   "agent fetched non-allowlisted domain " + domain,
		})

		// Rule 4, request side: the request could carry out a secret the agent read shortly before.
		request := WebRequest{Event: evt, DetectorID: engine.detectorID}
		reads, err := engine.store.RecordWebRequest(ctx, evt.CustomerID, evt.AgentID, request)
		if err != nil {
			return nil, err
		}
		reads = firstPerEvent(reads, func(read SecretRead) string { return read.EventID })
		var latest *SecretRead
		for i := range reads {
			if withinWindow(reads[i], evt) && (latest == nil || reads[i].Ts.After(latest.Ts)) {
				latest = &reads[i]
			}
		}
		if latest != nil {
			findings = append(findings, exfiltration(*latest, request, RaisedByRequest))
		}
	}
	return findings, nil
}

// firstPerEvent keeps the first record of each event. A redelivery handled by
// another detector leaves a second record of the same event in the store;
// without this, one event could raise two findings.
func firstPerEvent[Record any](records []Record, eventID func(Record) string) []Record {
	seen := make(map[string]bool, len(records))
	kept := records[:0]
	for _, record := range records {
		if id := eventID(record); !seen[id] {
			seen[id] = true
			kept = append(kept, record)
		}
	}
	return kept
}

// withinWindow reports whether the request was made at or after the read and
// no later than the window allows. The store bounds a record's life by
// processing time; this bounds the link by event time, so a read processed
// late cannot be linked to a much later request.
func withinWindow(read SecretRead, request event.Event) bool {
	gap := request.Ts.Sub(read.Ts)
	return gap >= 0 && gap <= exfiltrationWindow
}

// exfiltration builds the finding for a read and request pair. It belongs to
// the request's event, and reads the same whichever side raises it.
func exfiltration(read SecretRead, request WebRequest, raisedBy string) Finding {
	evidence := Evidence{
		ReadEventID:       read.EventID,
		ReadTs:            read.Ts,
		ReadPath:          read.Path,
		ReadDetectorID:    read.DetectorID,
		RequestDetectorID: request.DetectorID,
		RaisedBy:          raisedBy,
	}
	return Finding{
		Event:    request.Event,
		Rule:     RuleExfiltration,
		Severity: "CRITICAL",
		Detail: fmt.Sprintf("agent made a web request to %s %s after reading secret file %s",
			request.Event.Args["url"], request.Event.Ts.Sub(read.Ts).Round(time.Millisecond), read.Path),
		Evidence: &evidence,
	}
}

func isSecretPath(path string) bool {
	for _, fragment := range secretPathFragments {
		if strings.Contains(path, fragment) {
			return true
		}
	}
	return false
}

// isPipeToShell reports whether the last pipe in the command feeds a shell,
// the classic "curl http://... | sh" pattern.
func isPipeToShell(command string) bool {
	if !strings.Contains(command, "|") {
		return false
	}
	afterLastPipe := command[strings.LastIndex(command, "|")+1:]
	words := strings.Fields(afterLastPipe)
	return len(words) > 0 && (words[0] == "sh" || words[0] == "bash" || words[0] == "zsh")
}

// domainOf extracts "example.com" from "https://example.com/path".
func domainOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "" // unparseable URL: treat as no domain
	}
	return parsed.Hostname()
}
