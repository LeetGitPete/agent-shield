package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
)

// newMux registers every route. It takes its dependencies as arguments so a
// test can serve the routes without the real database or broker.
func newMux(db *sql.DB, mgmtURL string, client *http.Client) *http.ServeMux {
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

	mux.HandleFunc("GET /customers", func(w http.ResponseWriter, r *http.Request) {
		customers, err := queryCustomers(db)
		if err != nil {
			log.Println("query customers: ", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(customers)
	})

	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats, err := fetchStats(client, mgmtURL)
		if err != nil {
			log.Println("stats: ", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(stats)
	})

	return mux
}
