// The api service is the read-only window into findings — the backend a
// security-analyst dashboard would call. It only SELECTs from Postgres.
//
//	GET /healthz                              -> "ok"
//	GET /findings?severity=&customer=&limit=  -> JSON list, newest first
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Finding is the JSON response shape (mirrors the findings table).
type Finding struct {
	ID         int64     `json:"id"`
	EventID    string    `json:"event_id"`
	CustomerID string    `json:"customer_id"`
	AgentID    string    `json:"agent_id"`
	Rule       string    `json:"rule"`
	Severity   string    `json:"severity"`
	Ts         time.Time `json:"ts"`
	Detail     string    `json:"detail"`
	LLMVerdict *string   `json:"llm_verdict"` // null until triage has written a verdict
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	pgURL := envOr("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/agentshield")
	addr := envOr("ADDR", ":8080")

	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(); err != nil { // healthy means the database is reachable; nothing else is checked
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /findings", func(w http.ResponseWriter, r *http.Request) {
		filters, err := parseFilters(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		findings, err := queryFindings(db, filters)
		if err != nil {
			log.Println("query findings: ", err)
			http.Error(w, "internal error", http.StatusInternalServerError) // the cause goes to the log, not to the caller
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(findings)
	})

	log.Println("api listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func queryFindings(db *sql.DB, filters Filters) ([]Finding, error) {
	// One static query: an empty filter value disables its condition, so no
	// SQL is ever assembled from request input.
	rows, err := db.Query(`
		SELECT id, event_id, customer_id, agent_id, rule, severity, ts, detail, llm_verdict
		FROM findings
		WHERE ($1 = '' OR severity = $1)
		  AND ($2 = '' OR customer_id = $2)
		ORDER BY ts DESC
		LIMIT $3`,
		filters.Severity, filters.Customer, filters.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	findings := []Finding{} // non-nil, so zero rows encode as [] and not null
	for rows.Next() {
		var finding Finding
		if err := rows.Scan(&finding.ID, &finding.EventID, &finding.CustomerID, &finding.AgentID,
			&finding.Rule, &finding.Severity, &finding.Ts, &finding.Detail, &finding.LLMVerdict); err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, rows.Err()
}
