package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFindingJSONInEachTriageState(t *testing.T) {
	text := "malicious: reads keys then uploads them"
	gemini, mock := "gemini", "mock"
	triagedAt := time.Date(2026, 10, 2, 12, 30, 0, 0, time.UTC)
	const wantTime = "2026-10-02T12:30:00Z"

	cases := []struct {
		name    string
		finding Finding
		want    map[string]any // nil = JSON null
	}{
		{"pending", Finding{},
			map[string]any{"llm_verdict": nil, "verdict_source": nil, "triaged_at": nil}},
		{"verdict present", Finding{LLMVerdict: &text, VerdictSource: &gemini, TriagedAt: &triagedAt},
			map[string]any{"llm_verdict": text, "verdict_source": "gemini", "triaged_at": wantTime}},
		{"processed by Gemini without a verdict", Finding{VerdictSource: &gemini, TriagedAt: &triagedAt},
			map[string]any{"llm_verdict": nil, "verdict_source": "gemini", "triaged_at": wantTime}},
		{"processed by mock", Finding{VerdictSource: &mock, TriagedAt: &triagedAt},
			map[string]any{"llm_verdict": nil, "verdict_source": "mock", "triaged_at": wantTime}},
	}

	for _, c := range cases {
		encoded, err := json.Marshal(c.finding)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for key, want := range c.want {
			value, present := got[key]
			if !present {
				t.Errorf("%s: key %q missing from %s", c.name, key, encoded)
				continue
			}
			if value != want {
				t.Errorf("%s: %s = %v, want %v", c.name, key, value, want)
			}
		}
	}
}
