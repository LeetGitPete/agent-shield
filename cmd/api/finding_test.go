package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/leetgitpete/agent-shield/internal/rules"
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

func TestFindingJSONDetectorAndEvidence(t *testing.T) {
	decode := func(finding Finding) map[string]any {
		t.Helper()
		encoded, err := json.Marshal(finding)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// A row stored before the columns existed: both keys present, both null.
	earlier := decode(Finding{})
	for _, key := range []string{"detector_id", "evidence"} {
		value, present := earlier[key]
		if !present || value != nil {
			t.Errorf("row without the columns: %s = %v (present: %v), want JSON null", key, value, present)
		}
	}

	// An exfiltration row: the evidence column holds what the detector stored.
	stored, err := json.Marshal(rules.Evidence{
		ReadEventID:       "read-1",
		ReadTs:            time.Date(2026, 10, 2, 12, 29, 58, 0, time.UTC),
		ReadPath:          "/home/dev/.env",
		ReadDetectorID:    "detector-7d9f-abcde",
		RequestDetectorID: "detector-7d9f-fghij",
		RaisedBy:          rules.RaisedByRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	detectorID := "detector-7d9f-fghij"
	exfiltration := decode(Finding{Rule: rules.RuleExfiltration, DetectorID: &detectorID, Evidence: stored})

	if got, isString := exfiltration["detector_id"].(string); !isString || got != detectorID {
		t.Errorf("detector_id = %v, want the string %q", exfiltration["detector_id"], detectorID)
	}
	evidence, isObject := exfiltration["evidence"].(map[string]any)
	if !isObject {
		t.Fatalf("evidence = %v, want a JSON object", exfiltration["evidence"])
	}
	want := map[string]any{
		"read_event_id":       "read-1",
		"read_ts":             "2026-10-02T12:29:58Z",
		"read_path":           "/home/dev/.env",
		"read_detector_id":    "detector-7d9f-abcde",
		"request_detector_id": "detector-7d9f-fghij",
		"raised_by":           "request",
	}
	if len(evidence) != len(want) {
		t.Errorf("evidence has %d key(s), want %d: %v", len(evidence), len(want), evidence)
	}
	for key, value := range want {
		if evidence[key] != value {
			t.Errorf("evidence.%s = %v, want %v", key, evidence[key], value)
		}
	}
}
