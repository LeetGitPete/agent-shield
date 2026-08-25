package event

import "time"

type Tool string

const (
	ToolBashExec Tool = "bash_exec"
	ToolFileRead Tool = "file_read"
	ToolWebFetch Tool = "web_fetch"
)

type Event struct {
	ID         string            `json:"id"`
	Ts         time.Time         `json:"ts"`
	CustomerID string            `json:"customer_id"`
	AgentID    string            `json:"agent_id"`
	Tool       Tool              `json:"tool"`
	Args       map[string]string `json:"args"`
}
