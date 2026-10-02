package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Verdict is the structured judgment we require from the LLM.
type Verdict struct {
	Verdict string `json:"verdict"` // "malicious" or "benign"
	Reason  string `json:"reason"`
}

// parseVerdict extracts a Verdict from raw LLM output, tolerating
// markdown code fences and surrounding prose.
func parseVerdict(llmText string) (Verdict, error) {
	var v Verdict
	start := strings.Index(llmText, "{")
	end := strings.LastIndex(llmText, "}")
	if start == -1 || end <= start {
		return v, fmt.Errorf("no JSON object in LLM output: %.80s", llmText)
	}
	if err := json.Unmarshal([]byte(llmText[start:end+1]), &v); err != nil {
		return v, fmt.Errorf("bad verdict JSON: %w", err)
	}
	if v.Verdict != "malicious" && v.Verdict != "benign" {
		return v, fmt.Errorf("verdict %q not in malicious|benign", v.Verdict)
	}
	return v, nil
}
