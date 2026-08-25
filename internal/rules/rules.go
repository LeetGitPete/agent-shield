package rules

import (
	"net/url"
	"strings"

	"github.com/leetgitpete/agent-shield/internal/event"
)

type Finding struct {
	Rule     string
	Severity string // MEDIUM, HIGH, CRITICAL
	Detail   string
}

var secretPathParts = []string{".env", "id_rsa", "id_ed25519", ".aws/credentials", ".ssh/"}

var allowedDomains = map[string]bool{
	"github.com":        true,
	"pkg.go.dev":        true,
	"go.dev":            true,
	"golang.org":        true,
	"stackoverflow.com": true,
}

// Engine evaluates events against all rules. It is stateful: the
// exfiltration rule needs to remember which agents read secrets.
// Key is customerID+agentID so agents never collide across tenants.
type Engine struct {
	readSecret map[string]bool
}

func NewEngine() *Engine {
	return &Engine{readSecret: make(map[string]bool)}
}

func (e *Engine) Evaluate(ev event.Event) []Finding {
	var out []Finding
	agentKey := ev.CustomerID + "/" + ev.AgentID

	switch ev.Tool {
	case event.ToolFileRead:
		if isSecretPath(ev.Args["path"]) {
			out = append(out, Finding{
				Rule:     "secret_file_read",
				Severity: "HIGH",
				Detail:   "agent read secret file " + ev.Args["path"],
			})
			e.readSecret[agentKey] = true
		}

	case event.ToolBashExec:
		if isPipeToShell(ev.Args["command"]) {
			out = append(out, Finding{
				Rule:     "pipe_to_shell",
				Severity: "CRITICAL",
				Detail:   "agent piped a download into a shell: " + ev.Args["command"],
			})
		}

	case event.ToolWebFetch:
		if d := domainOf(ev.Args["url"]); d != "" && !allowedDomains[d] {
			out = append(out, Finding{
				Rule:     "unknown_domain",
				Severity: "MEDIUM",
				Detail:   "agent fetched non-allowlisted domain " + d,
			})
		}
		if e.readSecret[agentKey] {
			out = append(out, Finding{
				Rule:     "exfiltration",
				Severity: "CRITICAL",
				Detail:   "agent made a web request after reading a secret file (url: " + ev.Args["url"] + ")",
			})
		}
	}
	return out
}

func isSecretPath(path string) bool {
	for _, part := range secretPathParts {
		if strings.Contains(path, part) {
			return true
		}
	}
	return false
}

func isPipeToShell(command string) bool {
	if !strings.Contains(command, "|") {
		return false
	}
	after := command[strings.LastIndex(command, "|")+1:]
	shell := strings.Fields(after)
	return len(shell) > 0 && (shell[0] == "sh" || shell[0] == "bash" || shell[0] == "zsh")
}

func domainOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
