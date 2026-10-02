package rules

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/leetgitpete/agent-shield/internal/event"
)

// How long each kind of record stays live, counted from the time it was
// recorded. A secret read lives as long as the rule's window. A web request
// only has to outlive the reordering between two detectors, which is
// normally well under a second.
const (
	SecretReadTTL = exfiltrationWindow
	WebRequestTTL = 60 * time.Second
)

// SecretRead is what the store keeps about a secret read.
type SecretRead struct {
	EventID    string    `json:"event_id"`
	Ts         time.Time `json:"ts"`
	Path       string    `json:"path"`
	DetectorID string    `json:"detector_id"` // the detector that processed the read
}

// WebRequest is what the store keeps about a non-allowlisted web request. It
// holds the whole event, so the detector that processes the read second can
// raise the finding on behalf of the request.
type WebRequest struct {
	Event      event.Event `json:"event"`
	DetectorID string      `json:"detector_id"` // the detector that processed the request
}

// Store holds the exfiltration rule's state for every detector. Records are
// kept per customer and agent, so tenants never mix.
//
// Each operation writes its own record first and then reads the other kind:
// of two detectors working on an agent's read and request at the same moment,
// at least one sees the other's record.
//
// A store only stores and returns records, oldest expiry first. Recording an
// identical record again refreshes its expiry. The same event recorded by
// another detector is a second record; the engine collapses those.
type Store interface {
	// RecordSecretRead records the read and returns the agent's live web requests.
	RecordSecretRead(ctx context.Context, customerID, agentID string, read SecretRead) ([]WebRequest, error)
	// RecordWebRequest records the request and returns the agent's live secret reads.
	RecordWebRequest(ctx context.Context, customerID, agentID string, request WebRequest) ([]SecretRead, error)
}

// encodeRecord is the form a record is stored in. Encoding is deterministic,
// so an identical record always becomes the same member of its set.
func encodeRecord(record any) (string, error) {
	encoded, err := json.Marshal(record)
	return string(encoded), err
}

func decodeRecords[Record any](members []string) ([]Record, error) {
	records := make([]Record, 0, len(members))
	for _, member := range members {
		var record Record
		if err := json.Unmarshal([]byte(member), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// MemoryStore keeps the state in process, for the rule tests. It follows the
// Redis store's set semantics, so the engine behaves the same on both.
type MemoryStore struct {
	now func() time.Time

	mu       sync.Mutex
	reads    map[agentKey]map[string]time.Time // encoded record -> expiry
	requests map[agentKey]map[string]time.Time
}

type agentKey struct{ customerID, agentID string }

// NewMemoryStore returns an empty store. now is the clock expiry is counted
// from; tests pass their own.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	return &MemoryStore{
		now:      now,
		reads:    make(map[agentKey]map[string]time.Time),
		requests: make(map[agentKey]map[string]time.Time),
	}
}

func (store *MemoryStore) RecordSecretRead(_ context.Context, customerID, agentID string, read SecretRead) ([]WebRequest, error) {
	members, err := store.record(store.reads, store.requests, agentKey{customerID, agentID}, read, SecretReadTTL)
	if err != nil {
		return nil, err
	}
	return decodeRecords[WebRequest](members)
}

func (store *MemoryStore) RecordWebRequest(_ context.Context, customerID, agentID string, request WebRequest) ([]SecretRead, error) {
	members, err := store.record(store.requests, store.reads, agentKey{customerID, agentID}, request, WebRequestTTL)
	if err != nil {
		return nil, err
	}
	return decodeRecords[SecretRead](members)
}

// record adds the record to the agent's set in own, drops that set's expired
// members and returns the live members of the agent's set in other.
func (store *MemoryStore) record(own, other map[agentKey]map[string]time.Time, key agentKey, record any, ttl time.Duration) ([]string, error) {
	member, err := encodeRecord(record)
	if err != nil {
		return nil, err
	}
	now := store.now()

	store.mu.Lock()
	defer store.mu.Unlock()

	if own[key] == nil {
		own[key] = make(map[string]time.Time)
	}
	own[key][member] = now.Add(ttl)
	for existing, expiry := range own[key] {
		if !expiry.After(now) {
			delete(own[key], existing)
		}
	}

	var live []string
	for existing, expiry := range other[key] {
		if expiry.After(now) {
			live = append(live, existing)
		}
	}
	// A sorted set's order: by score, members with equal scores by their bytes.
	sort.Slice(live, func(i, j int) bool {
		first, second := other[key][live[i]], other[key][live[j]]
		if !first.Equal(second) {
			return first.Before(second)
		}
		return live[i] < live[j]
	})
	return live, nil
}
