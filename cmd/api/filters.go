package main

import (
	"fmt"
	"net/url" // url.Values = parsed query parameters
	"strconv" // string -> int
	"strings"
)

const (
	defaultLimit = 50  // rows returned when ?limit= is absent
	maxLimit     = 500 // hard cap so nobody can request the whole table
)

var validSeverities = map[string]bool{"MEDIUM": true, "HIGH": true, "CRITICAL": true}

// Filters are the validated query parameters for GET /findings.
type Filters struct {
	Severity string // "" = no filter
	Customer string // "" = no filter
	Limit    int
}

func parseFilters(q url.Values) (Filters, error) {
	f := Filters{Customer: q.Get("customer"), Limit: defaultLimit} // Get returns "" if the param is absent

	if s := q.Get("severity"); s != "" {
		s = strings.ToUpper(s) // accept ?severity=high too
		if !validSeverities[s] {
			return f, fmt.Errorf("invalid severity %q (want MEDIUM|HIGH|CRITICAL)", s)
		}
		f.Severity = s
	}

	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return f, fmt.Errorf("invalid limit %q", l)
		}
		f.Limit = min(n, maxLimit) // clamp to the cap
	}
	return f, nil
}
