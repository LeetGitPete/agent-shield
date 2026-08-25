package event // shared event schema, imported by all services

import "time"

type Tool string // string-based "enum" of tool kinds

const (
	ToolBashExec Tool = "bash_exec" // agent ran a shell command
	ToolFileRead Tool = "file_read" // agent read a file
	ToolWebFetch Tool = "web_fetch" // agent made an HTTP request
)

// Event is one observed agent tool call; `json:"..."` tags set the field names used in JSON.
type Event struct {
	ID         string            `json:"id"`          // unique id, also the idempotency key
	Ts         time.Time         `json:"ts"`          // when the call happened
	CustomerID string            `json:"customer_id"` // which tenant this agent belongs to
	AgentID    string            `json:"agent_id"`    // which agent made the call
	Tool       Tool              `json:"tool"`        // which tool was used
	Args       map[string]string `json:"args"`        // tool arguments: command / path / url
}
