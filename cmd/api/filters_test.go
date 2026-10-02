package main

import (
	"net/url"
	"testing"
)

func TestDefaultsWhenNoParams(t *testing.T) {
	f, err := parseFilters(url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Severity != "" || f.Customer != "" || f.Limit != 50 {
		t.Errorf("got %+v, want empty filters with limit 50", f)
	}
}

func TestValidSeverityAccepted(t *testing.T) {
	f, err := parseFilters(url.Values{"severity": {"HIGH"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Severity != "HIGH" {
		t.Errorf("severity = %q, want HIGH", f.Severity)
	}
}

func TestSeverityIsCaseInsensitive(t *testing.T) {
	f, err := parseFilters(url.Values{"severity": {"critical"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Severity != "CRITICAL" {
		t.Errorf("severity = %q, want CRITICAL", f.Severity)
	}
}

func TestInvalidSeverityRejected(t *testing.T) {
	if _, err := parseFilters(url.Values{"severity": {"BANANAS"}}); err == nil {
		t.Fatal("expected error for invalid severity")
	}
}

func TestLimitParsedAndCapped(t *testing.T) {
	f, err := parseFilters(url.Values{"limit": {"10"}})
	if err != nil || f.Limit != 10 {
		t.Errorf("limit = %d err = %v, want 10", f.Limit, err)
	}
	f, err = parseFilters(url.Values{"limit": {"99999"}})
	if err != nil || f.Limit != 500 {
		t.Errorf("limit = %d err = %v, want cap 500", f.Limit, err)
	}
	if _, err := parseFilters(url.Values{"limit": {"abc"}}); err == nil {
		t.Fatal("expected error for non-numeric limit")
	}
}

func TestEachRuleNameAccepted(t *testing.T) {
	for _, rule := range []string{"secret_file_read", "pipe_to_shell", "unknown_domain", "exfiltration"} {
		f, err := parseFilters(url.Values{"rule": {rule}})
		if err != nil {
			t.Errorf("rule %q rejected: %v", rule, err)
			continue
		}
		if f.Rule != rule {
			t.Errorf("rule = %q, want %q", f.Rule, rule)
		}
	}
}

func TestUnknownRuleRejected(t *testing.T) {
	if _, err := parseFilters(url.Values{"rule": {"bananas"}}); err == nil {
		t.Fatal("expected error for unknown rule")
	}
}

func TestRuleIsCaseSensitive(t *testing.T) {
	if _, err := parseFilters(url.Values{"rule": {"EXFILTRATION"}}); err == nil {
		t.Fatal("expected error for a rule name in upper case")
	}
}
