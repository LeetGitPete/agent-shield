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
		f, err := parseFilters(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		findings, err := queryFindings(db, f)
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

func queryFindings(db *sql.DB, f Filters) ([]Finding, error) {
	// $1='' disables a filter — keeps this a single static, injection-safe query.
	rows, err := db.Query(`
		SELECT id, event_id, customer_id, agent_id, rule, severity, ts, detail, llm_verdict
		FROM findings
		WHERE ($1 = '' OR severity = $1)
		  AND ($2 = '' OR customer_id = $2)
		ORDER BY ts DESC
		LIMIT $3`,
		f.Severity, f.Customer, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	findings := []Finding{}
	for rows.Next() {
		var x Finding
		if err := rows.Scan(&x.ID, &x.EventID, &x.CustomerID, &x.AgentID,
			&x.Rule, &x.Severity, &x.Ts, &x.Detail, &x.LLMVerdict); err != nil {
			return nil, err
		}
		findings = append(findings, x)
	}
	return findings, rows.Err()
}
