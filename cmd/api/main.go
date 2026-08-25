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
	"net/http" // stdlib HTTP server; no framework needed
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database driver
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
	LLMVerdict *string   `json:"llm_verdict"` // pointer: NULL in the DB -> null in JSON
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	pgURL := envOr("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/agentshield")
	addr := envOr("ADDR", ":8080") // port to listen on

	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()

	mux := http.NewServeMux() // router: maps "METHOD /path" patterns to handler functions

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { // handler runs per matching request
		if err := db.Ping(); err != nil { // healthy = we can reach the DB
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /findings", func(w http.ResponseWriter, r *http.Request) {
		filters, err := parseFilters(r.URL.Query()) // validate query params
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest) // bad input is the caller's fault: 400
			return
		}
		findings, err := queryFindings(db, filters)
		if err != nil {
			log.Println("query findings: ", err)
			http.Error(w, "internal error", http.StatusInternalServerError) // log details, hide them from the caller
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(findings) // serialize straight into the response
	})

	log.Println("api listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux)) // blocks forever: accept loop, one goroutine per request
}

func queryFindings(db *sql.DB, filters Filters) ([]Finding, error) {
	// One static query; "$1 = '' OR ..." disables a filter when it's empty.
	// $N placeholders keep values out of the SQL string — no injection possible.
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
	defer rows.Close() // release the DB cursor when done

	findings := []Finding{} // empty (not nil) so zero rows serializes as [] not null
	for rows.Next() {       // advance row by row
		var finding Finding
		if err := rows.Scan(&finding.ID, &finding.EventID, &finding.CustomerID, &finding.AgentID,
			&finding.Rule, &finding.Severity, &finding.Ts, &finding.Detail, &finding.LLMVerdict); err != nil { // copy columns into struct fields
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, rows.Err() // rows.Err reports any error that ended the loop early
}
