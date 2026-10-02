package main

import (
	"errors"
	"testing"
	"time"
)

var triageTime = time.Date(2026, 10, 2, 9, 15, 0, 0, time.UTC)

func TestVerdictIsWrittenWithItsSourceAndTime(t *testing.T) {
	verdict := &Verdict{Verdict: "malicious", Reason: "reads keys then uploads them"}

	got := decide(providerGemini, verdict, nil, false, triageTime)

	if got.Retry || got.GaveUp {
		t.Fatalf("a verdict is neither a retry nor a give-up: %+v", got)
	}
	if got.Verdict == nil || *got.Verdict != "malicious: reads keys then uploads them" {
		t.Errorf("verdict text = %v, want %q", got.Verdict, "malicious: reads keys then uploads them")
	}
	if got.Source != "gemini" || !got.TriagedAt.Equal(triageTime) {
		t.Errorf("source = %q at %v, want gemini at %v", got.Source, got.TriagedAt, triageTime)
	}
}

func TestFirstFailureWritesNothingAndAsksForARetry(t *testing.T) {
	got := decide(providerGemini, nil, errors.New("gemini returned 429"), false, triageTime)

	if !got.Retry {
		t.Error("a first failure must ask for a retry")
	}
	if got.Verdict != nil || got.Source != "" || !got.TriagedAt.IsZero() {
		t.Errorf("a first failure must write nothing, got %+v", got)
	}
}

func TestFailureOnARedeliveredRequestIsRecordedWithoutAVerdict(t *testing.T) {
	got := decide(providerGemini, nil, errors.New("gemini returned 429"), true, triageTime)

	if got.Retry || !got.GaveUp {
		t.Errorf("a failure on a redelivered request must give up, not retry again: %+v", got)
	}
	if got.Verdict != nil {
		t.Errorf("verdict text = %q, want none", *got.Verdict)
	}
	if got.Source != "gemini" || !got.TriagedAt.Equal(triageTime) {
		t.Errorf("source = %q at %v, want gemini at %v", got.Source, got.TriagedAt, triageTime)
	}
}

func TestMockProviderIsRecordedWithoutAVerdict(t *testing.T) {
	got := decide(providerMock, nil, nil, false, triageTime)

	if got.Retry || got.GaveUp {
		t.Errorf("the mock provider is neither a retry nor a give-up: %+v", got)
	}
	if got.Verdict != nil {
		t.Errorf("verdict text = %q, want none", *got.Verdict)
	}
	if got.Source != "mock" || !got.TriagedAt.Equal(triageTime) {
		t.Errorf("source = %q at %v, want mock at %v", got.Source, got.TriagedAt, triageTime)
	}
}
