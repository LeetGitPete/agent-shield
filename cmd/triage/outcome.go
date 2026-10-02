package main

import "time"

// outcome is what the service does with one triage request once the provider
// has answered: ask for a retry and write nothing, or write the three triage
// columns of the finding.
type outcome struct {
	Retry     bool
	GaveUp    bool      // the provider failed again on the retry; recorded as processed without a verdict
	Verdict   *string   // llm_verdict; nil when the finding was processed without a verdict
	Source    string    // verdict_source: the provider that processed the finding
	TriagedAt time.Time // triaged_at
}

// decide turns a provider's answer into an outcome. A failed request is
// retried once: a failure on a redelivered request is recorded as processed
// without a verdict, so the finding does not stay pending forever.
func decide(providerName string, verdict *Verdict, err error, redelivered bool, now time.Time) outcome {
	if err != nil && !redelivered {
		return outcome{Retry: true}
	}
	result := outcome{GaveUp: err != nil, Source: providerName, TriagedAt: now}
	if err == nil && verdict != nil {
		summary := verdict.Verdict + ": " + verdict.Reason
		result.Verdict = &summary
	}
	return result
}
