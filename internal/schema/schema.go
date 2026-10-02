// Package schema sets up the findings table. It is the one place that
// defines the table, run at startup by every service that writes to it.
package schema

import "database/sql"

// Any number shared by every caller works: it only names the lock.
const setupLockKey = 7461001

const createFindings = `
CREATE TABLE IF NOT EXISTS findings (
	id          BIGSERIAL PRIMARY KEY,       -- auto-incrementing finding id
	event_id    TEXT NOT NULL,               -- which event triggered it
	customer_id TEXT NOT NULL,
	agent_id    TEXT NOT NULL,
	rule        TEXT NOT NULL,               -- which rule fired
	severity    TEXT NOT NULL,
	ts          TIMESTAMPTZ NOT NULL,        -- event timestamp
	detail      TEXT NOT NULL,
	llm_verdict TEXT,                        -- filled in later by the triage service
	UNIQUE (event_id, rule)                  -- idempotency: same event+rule can only exist once
)`

const hasTriageColumns = `
SELECT EXISTS (
	SELECT 1 FROM information_schema.columns
	WHERE table_schema = current_schema() AND table_name = 'findings' AND column_name = 'verdict_source'
)`

// Columns added after the first version. Added if missing, so a table
// created by an earlier version is upgraded in place.
const addTriageColumns = `
ALTER TABLE findings
	ADD COLUMN IF NOT EXISTS verdict_source TEXT,       -- gemini or mock; null while triage is pending
	ADD COLUMN IF NOT EXISTS triaged_at     TIMESTAMPTZ -- null while triage is pending`

// Verdicts already in the table when the triage columns arrive were written
// when Gemini was the only provider. Their triage time was not kept, so the
// event time stands in.
const markEarlierVerdicts = `
UPDATE findings SET verdict_source = 'gemini', triaged_at = ts
WHERE llm_verdict IS NOT NULL`

// Setup creates the findings table if it is missing and adds the columns an
// older table lacks. It runs in one transaction under an advisory lock, so
// services that start at the same moment set the schema up one after another
// instead of failing on each other's half-finished work.
func Setup(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Held until the transaction ends.
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, setupLockKey); err != nil {
		return err
	}
	if _, err := tx.Exec(createFindings); err != nil {
		return err
	}

	// The earlier verdicts are marked only in the transaction that adds the
	// columns: a later start must not scan the table while it holds the lock
	// the ALTER takes.
	var upgraded bool
	if err := tx.QueryRow(hasTriageColumns).Scan(&upgraded); err != nil {
		return err
	}
	if _, err := tx.Exec(addTriageColumns); err != nil {
		return err
	}
	if !upgraded {
		if _, err := tx.Exec(markEarlierVerdicts); err != nil {
			return err
		}
	}
	return tx.Commit()
}
