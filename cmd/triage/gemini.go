package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// A fixed model identifier, not a floating alias, so the provider cannot
	// change the model underneath a running system. GEMINI_MODEL overrides it.
	defaultGeminiModel = "gemini-3.5-flash-lite"

	geminiBaseURL = "https://generativelanguage.googleapis.com"
)

// geminiProvider asks Gemini whether a finding is really malicious.
type geminiProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// newGeminiProvider takes the base URL as a parameter so a test can point the
// provider at a stand-in server; empty means the provider's public address.
func newGeminiProvider(apiKey, model, baseURL string) *geminiProvider {
	if baseURL == "" {
		baseURL = geminiBaseURL
	}
	return &geminiProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (provider *geminiProvider) Name() string { return providerGemini }

func (provider *geminiProvider) String() string {
	return fmt.Sprintf("%s (model %s)", providerGemini, provider.model)
}

// Gemini REST API request/response shapes (only the fields we use).
type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}
type geminiPart struct {
	Text string `json:"text"`
}
type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
}

// Judge asks Gemini for a verdict on one finding.
func (provider *geminiProvider) Judge(request TriageRequest) (*Verdict, error) {
	eventJSON, _ := json.Marshal(request.Event)
	prompt := fmt.Sprintf(`You review security findings about AI agents' tool calls.
Rule %q (severity %s) flagged this event: %s
Event: %s

Is this genuinely malicious or a false positive? Reply with ONLY this JSON, no other text:
{"verdict": "malicious" or "benign", "reason": "<one short sentence>"}`,
		request.Rule, request.Severity, request.Detail, eventJSON)

	body, _ := json.Marshal(geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
	})

	url := provider.baseURL + "/v1beta/models/" + provider.model + ":generateContent"
	httpRequest, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("x-goog-api-key", provider.apiKey) // in a header, so the key never appears in a URL or an error text
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := provider.client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK { // includes 429 rate limits; caller retries
		return nil, fmt.Errorf("gemini returned %d: %.200s", response.StatusCode, responseBody)
	}

	var parsed geminiResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini returned no candidates")
	}
	verdict, err := parseVerdict(parsed.Candidates[0].Content.Parts[0].Text)
	if err != nil {
		return nil, err
	}
	return &verdict, nil
}
