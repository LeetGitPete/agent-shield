package main

import "testing"

func TestParsesPlainJSON(t *testing.T) {
	v, err := parseVerdict(`{"verdict": "malicious", "reason": "reads keys then exfiltrates"}`)
	if err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "malicious" || v.Reason == "" {
		t.Errorf("got %+v", v)
	}
}

func TestParsesJSONInMarkdownFence(t *testing.T) {
	v, err := parseVerdict("Here is my analysis:\n```json\n{\"verdict\": \"benign\", \"reason\": \"normal dev activity\"}\n```\nHope this helps!")
	if err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "benign" {
		t.Errorf("got %+v", v)
	}
}

func TestRejectsMissingJSON(t *testing.T) {
	if _, err := parseVerdict("I think this looks suspicious."); err == nil {
		t.Fatal("expected error when no JSON object present")
	}
}

func TestRejectsUnknownVerdictValue(t *testing.T) {
	if _, err := parseVerdict(`{"verdict": "maybe", "reason": "unsure"}`); err == nil {
		t.Fatal("expected error for verdict outside malicious|benign")
	}
}
