package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// managementServer stands in for the broker's management API. It answers
// only on the real queue path, compared while still percent-encoded, and
// only for the expected credentials.
func managementServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.RequestURI != "/api/queues/%2F/events" {
			t.Errorf("unexpected request %s %s", r.Method, r.RequestURI)
			http.NotFound(w, r)
			return
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "monitor" || password != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// withCredentials turns http://host:port into http://monitor:<password>@host:port.
func withCredentials(serverURL, password string) string {
	return strings.Replace(serverURL, "http://", "http://monitor:"+password+"@", 1)
}

func assertFields(t *testing.T, body, want map[string]any) {
	t.Helper()
	for key, wantValue := range want {
		if body[key] != wantValue {
			t.Errorf("%s = %v, want %v", key, body[key], wantValue)
		}
	}
	if len(body) != len(want) {
		t.Errorf("body has %d fields, want exactly %d: %v", len(body), len(want), body)
	}
}

// getStats serves the api routes against the given management URL and returns
// the status and decoded body of GET /stats.
func getStats(t *testing.T, mgmtURL string) (int, map[string]any) {
	t.Helper()
	api := httptest.NewServer(newMux(nil, mgmtURL, http.DefaultClient))
	defer api.Close()

	response, err := http.Get(api.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("stats body is not a JSON object: %v", err)
	}
	return response.StatusCode, body
}

func TestStatsMapsAllFiveFields(t *testing.T) {
	management := managementServer(t, http.StatusOK, `{
		"name": "events",
		"messages": 49,
		"messages_ready": 42,
		"messages_unacknowledged": 7,
		"consumers": 3,
		"message_stats": {
			"publish": 1000, "publish_details": {"rate": 12.5},
			"ack": 990, "ack_details": {"rate": 11.25}
		}
	}`)

	status, body := getStats(t, withCredentials(management.URL, "s3cret"))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
	want := map[string]any{"ready": 42.0, "unacked": 7.0, "publish_rate": 12.5, "ack_rate": 11.25, "consumers": 3.0}
	assertFields(t, body, want)
}

func TestStatsReportsZeroRatesWithoutMessageStatistics(t *testing.T) {
	// The broker omits message_stats for a queue it has not sampled yet.
	management := managementServer(t, http.StatusOK,
		`{"name": "events", "messages_ready": 5, "messages_unacknowledged": 1, "consumers": 1}`)

	status, body := getStats(t, withCredentials(management.URL, "s3cret"))

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", status, body)
	}
	want := map[string]any{"ready": 5.0, "unacked": 1.0, "publish_rate": 0.0, "ack_rate": 0.0, "consumers": 1.0}
	assertFields(t, body, want)
}

func TestStatsIsUnavailableWhenQueueIsMissing(t *testing.T) {
	management := managementServer(t, http.StatusNotFound, `{"error": "Object Not Found", "reason": "Not Found"}`)

	status, body := getStats(t, withCredentials(management.URL, "s3cret"))

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "404") {
		t.Errorf("error = %q, want it to name the broker's status code 404", message)
	}
}

func TestStatsIsUnavailableWhenCredentialsAreRejected(t *testing.T) {
	management := managementServer(t, http.StatusOK, `{}`)

	status, body := getStats(t, withCredentials(management.URL, "wrong"))

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "401") {
		t.Errorf("error = %q, want it to name the broker's status code 401", message)
	}
}

func TestStatsIsUnavailableWhenServerIsUnreachable(t *testing.T) {
	management := managementServer(t, http.StatusOK, `{}`)
	mgmtURL := withCredentials(management.URL, "s3cret")
	address := strings.TrimPrefix(management.URL, "http://")
	management.Close() // nothing listens on the address any more

	status, body := getStats(t, mgmtURL)

	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	message, _ := body["error"].(string)
	if message == "" {
		t.Errorf("body %v has no error field", body)
	}
	if strings.Contains(message, address) || strings.Contains(message, "s3cret") {
		t.Errorf("error %q leaks the management address or credentials", message)
	}
}
