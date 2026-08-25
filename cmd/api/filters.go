package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultLimit = 50
	maxLimit     = 500
)

var validSeverities = map[string]bool{"MEDIUM": true, "HIGH": true, "CRITICAL": true}

type Filters struct {
	Severity string
	Customer string
	Limit    int
}

func parseFilters(q url.Values) (Filters, error) {
	f := Filters{Customer: q.Get("customer"), Limit: defaultLimit}

	if s := q.Get("severity"); s != "" {
		s = strings.ToUpper(s)
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
		f.Limit = min(n, maxLimit)
	}
	return f, nil
}
