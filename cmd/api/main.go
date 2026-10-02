// The api service is the read-only window into the pipeline — the backend a
// security-analyst dashboard would call. It only SELECTs from Postgres and
// reads queue statistics from the broker's management API.
//
//	GET /healthz                                    -> "ok"
//	GET /findings?severity=&customer=&rule=&limit=  -> JSON list, newest first
//	GET /customers                                  -> JSON list of customer ids that have findings
//	GET /stats                                      -> JSON statistics of the events queue
//
// The api does not create the findings table: the detector and the triage
// service do, so its queries fail until one of them has started once.
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
	ID            int64      `json:"id"`
	EventID       string     `json:"event_id"`
	CustomerID    string     `json:"customer_id"`
	AgentID       string     `json:"agent_id"`
	Rule          string     `json:"rule"`
	Severity      string     `json:"severity"`
	Ts            time.Time  `json:"ts"`
	Detail        string     `json:"detail"`
	LLMVerdict    *string    `json:"llm_verdict"`    // null when triage wrote no verdict
	VerdictSource *string    `json:"verdict_source"` // gemini or mock; null while triage is pending
	TriagedAt     *time.Time `json:"triaged_at"`     // null while triage is pending

	// Both are null on rows stored before the columns existed.
	DetectorID *string         `json:"detector_id"` // the detector that stored the finding
	Evidence   json.RawMessage `json:"evidence"`    // the stored object; null for rules without evidence
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
	mgmtURL := envOr("RABBITMQ_MGMT_URL", "http://guest:guest@localhost:15672")

	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		log.Fatal("open postgres: ", err)
	}
	defer db.Close()

	// The timeout bounds GET /stats when the broker does not answer.
	mux := newMux(db, mgmtURL, &http.Client{Timeout: 2 * time.Second})

	log.Println("api listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func queryFindings(db *sql.DB, filters Filters) ([]Finding, error) {
	// One static query: an empty filter value disables its condition, so no
	// SQL is ever assembled from request input. Ordering by id is stable:
	// two findings with the same event time never swap between requests.
	rows, err := db.Query(`
		SELECT id, event_id, customer_id, agent_id, rule, severity, ts, detail,
		       llm_verdict, verdict_source, triaged_at, detector_id, evidence
		FROM findings
		WHERE ($1 = '' OR severity = $1)
		  AND ($2 = '' OR customer_id = $2)
		  AND ($3 = '' OR rule = $3)
		ORDER BY id DESC
		LIMIT $4`,
		filters.Severity, filters.Customer, filters.Rule, filters.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	findings := []Finding{} // non-nil, so zero rows encode as [] and not null
	for rows.Next() {
		var finding Finding
		var evidence []byte // nil for a NULL column, which then encodes as JSON null
		if err := rows.Scan(&finding.ID, &finding.EventID, &finding.CustomerID, &finding.AgentID,
			&finding.Rule, &finding.Severity, &finding.Ts, &finding.Detail,
			&finding.LLMVerdict, &finding.VerdictSource, &finding.TriagedAt,
			&finding.DetectorID, &evidence); err != nil {
			return nil, err
		}
		finding.Evidence = evidence
		findings = append(findings, finding)
	}
	return findings, rows.Err()
}

// queryCustomers returns the distinct customer ids that have findings, sorted ascending.
func queryCustomers(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT customer_id FROM findings ORDER BY customer_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	customers := []string{} // non-nil, so zero rows encode as [] and not null
	for rows.Next() {
		var customer string
		if err := rows.Scan(&customer); err != nil {
			return nil, err
		}
		customers = append(customers, customer)
	}
	return customers, rows.Err()
}
