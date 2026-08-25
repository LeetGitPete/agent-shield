// Package rules is the detection engine: it decides whether a single
// agent tool-call event is suspicious, and how severe it is.
package rules

import (
	"net/url"
	"strings"

	"github.com/leetgitpete/agent-shield/internal/event"
)

// Finding is one rule match on one event. The detector stores these in
// Postgres; everything else (event id, customer, agent) comes from the
// event the finding was produced for.
type Finding struct {
	Rule     string // machine-readable rule name, e.g. "pipe_to_shell"
	Severity string // MEDIUM, HIGH or CRITICAL
	Detail   string // human-readable explanation for the analyst
}

// Reading any path containing one of these fragments counts as touching
// a secret (credentials, private keys, env files).
var secretPathFragments = []string{".env", "id_rsa", "id_ed25519", ".aws/credentials", ".ssh/"}

// Domains an agent may fetch without raising a finding.
var allowedDomains = map[string]bool{
	"github.com":        true,
	"pkg.go.dev":        true,
	"go.dev":            true,
	"golang.org":        true,
	"stackoverflow.com": true,
}

// Engine runs every rule against each incoming event.
//
// It is STATEFUL: the exfiltration rule fires when an agent makes a web
// request *after* having read a secret earlier, so the engine remembers
// which agents have read secrets. The map key is "customerID/agentID"
// so two customers' agents with the same name never mix.
type Engine struct {
	agentsThatReadSecrets map[string]bool
}

func NewEngine() *Engine {
	return &Engine{agentsThatReadSecrets: make(map[string]bool)}
}

// Evaluate returns all findings for one event (usually zero or one,
// but a single event can match several rules).
func (engine *Engine) Evaluate(evt event.Event) []Finding {
	var findings []Finding
	agentKey := evt.CustomerID + "/" + evt.AgentID

	switch evt.Tool {

	case event.ToolFileRead:
		if isSecretPath(evt.Args["path"]) {
			findings = append(findings, Finding{
				Rule:     "secret_file_read",
				Severity: "HIGH",
				Detail:   "agent read secret file " + evt.Args["path"],
			})
			engine.agentsThatReadSecrets[agentKey] = true
		}

	case event.ToolBashExec:
		if isPipeToShell(evt.Args["command"]) {
			findings = append(findings, Finding{
				Rule:     "pipe_to_shell",
				Severity: "CRITICAL",
				Detail:   "agent piped a download into a shell: " + evt.Args["command"],
			})
		}

	case event.ToolWebFetch:
		domain := domainOf(evt.Args["url"])
		if domain != "" && !allowedDomains[domain] {
			findings = append(findings, Finding{
				Rule:     "unknown_domain",
				Severity: "MEDIUM",
				Detail:   "agent fetched non-allowlisted domain " + domain,
			})
		}
		// Any web request after a secret read could carry the secret out.
		if engine.agentsThatReadSecrets[agentKey] {
			findings = append(findings, Finding{
				Rule:     "exfiltration",
				Severity: "CRITICAL",
				Detail:   "agent made a web request after reading a secret file (url: " + evt.Args["url"] + ")",
			})
		}
	}
	return findings
}

func isSecretPath(path string) bool {
	for _, fragment := range secretPathFragments {
		if strings.Contains(path, fragment) {
			return true
		}
	}
	return false
}

// isPipeToShell catches the classic "curl http://... | sh" pattern:
// something piped into a shell interpreter.
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
		return ""
	}
	return parsed.Hostname()
}
