package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// geminiServer stands in for the LLM provider: it records the request it
// received and answers with the given status and body.
func geminiServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	received := new(http.Request)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*received = *r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, received
}

var sampleRequest = TriageRequest{FindingID: 7, Rule: "pipe_to_shell", Severity: "CRITICAL", Detail: "agent piped a download into a shell"}

func TestGeminiProviderReturnsTheVerdict(t *testing.T) {
	server, received := geminiServer(t, http.StatusOK,
		`{"candidates": [{"content": {"parts": [{"text": "{\"verdict\": \"malicious\", \"reason\": \"runs a remote script\"}"}]}}]}`)
	provider := newGeminiProvider("test-key", defaultGeminiModel, server.URL)

	verdict, err := provider.Judge(sampleRequest)

	if err != nil {
		t.Fatal(err)
	}
	if verdict == nil || verdict.Verdict != "malicious" || verdict.Reason != "runs a remote script" {
		t.Errorf("verdict = %+v, want malicious / runs a remote script", verdict)
	}
	if received.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", received.Method)
	}
	// The model is pinned: a fixed identifier, never a floating alias.
	if want := "/v1beta/models/gemini-3.5-flash-lite:generateContent"; received.URL.Path != want {
		t.Errorf("request path = %s, want %s", received.URL.Path, want)
	}
	if got := received.Header.Get("x-goog-api-key"); got != "test-key" {
		t.Errorf("key header = %q, want the configured key", got)
	}
	if received.URL.RawQuery != "" {
		t.Errorf("query = %q, want none: the key travels in the header only", received.URL.RawQuery)
	}
}

func TestGeminiProviderFailsOnAnErrorResponse(t *testing.T) {
	server, _ := geminiServer(t, http.StatusTooManyRequests, `{"error": {"message": "quota exceeded"}}`)
	provider := newGeminiProvider("test-key", defaultGeminiModel, server.URL)

	verdict, err := provider.Judge(sampleRequest)

	if err == nil {
		t.Fatalf("expected an error, got verdict %+v", verdict)
	}
}

func TestGeminiProviderFailsWithoutCandidates(t *testing.T) {
	server, _ := geminiServer(t, http.StatusOK, `{"candidates": []}`)
	provider := newGeminiProvider("test-key", defaultGeminiModel, server.URL)

	verdict, err := provider.Judge(sampleRequest)

	if err == nil {
		t.Fatalf("expected an error, got verdict %+v", verdict)
	}
}
