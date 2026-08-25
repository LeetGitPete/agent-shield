// The api service is the read-only window into findings — the backend a
// security-analyst dashboard would call. It never touches RabbitMQ; it
// only SELECTs from Postgres.
//
//	GET /healthz                       -> "ok" (used by k8s health checks)
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

type Finding struct {
	ID         int64     `json:"id"`
	EventID    string    `json:"event_id"`
	CustomerID string    `json:"customer_id"`
	AgentID    string    `json:"agent_id"`
	Rule       string    `json:"rule"`
	Severity   string    `json:"severity"`
	Ts         time.Time `json:"ts"`
	Detail     string    `json:"detail"`
	LLMVerdict *string   `json:"llm_verdict"`
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
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
		if err := db.Ping(); err != nil {
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
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(findings)
	})

	log.Println("api listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func queryFindings(db *sql.DB, filters Filters) ([]Finding, error) {
	// One static query; "$1 = '' OR ..." disables a filter when it's empty.
	// $N placeholders mean values are never spliced into the SQL string,
	// so injection is impossible by construction.
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

	// Start with an empty (not nil) slice so no rows serializes as [] not null.
	findings := []Finding{}
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
