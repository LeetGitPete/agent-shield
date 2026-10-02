package main

import (
	"fmt"
	"strconv"
	"time"
)

// Provider names: the values of TRIAGE_PROVIDER, also stored as a finding's verdict_source.
const (
	providerGemini = "gemini"
	providerMock   = "mock"
)

// Provider reviews one finding. A nil verdict without an error means the
// finding was processed and no judgment was made.
type Provider interface {
	fmt.Stringer // the provider and its settings, for the startup log line
	Name() string
	Judge(request TriageRequest) (*Verdict, error)
}

// providerFromEnv builds the provider the environment selects. TRIAGE_PROVIDER
// is matched as written. Unset or empty, it means Gemini when a key is present
// and mock otherwise, so the service runs without a provider account.
func providerFromEnv() (Provider, error) {
	apiKey := envOr("GEMINI_API_KEY", "")

	name := envOr("TRIAGE_PROVIDER", "")
	if name == "" {
		name = providerMock
		if apiKey != "" {
			name = providerGemini
		}
	}

	switch name {
	case providerMock:
		delayMs, err := strconv.Atoi(envOr("MOCK_TRIAGE_DELAY_MS", "1000"))
		if err != nil || delayMs < 0 {
			return nil, fmt.Errorf("MOCK_TRIAGE_DELAY_MS must be a whole number of milliseconds, zero or more")
		}
		return newMockProvider(time.Duration(delayMs) * time.Millisecond), nil
	case providerGemini:
		if apiKey == "" {
			return nil, fmt.Errorf("TRIAGE_PROVIDER is %s but GEMINI_API_KEY is not set", providerGemini)
		}
		return newGeminiProvider(apiKey, envOr("GEMINI_MODEL", defaultGeminiModel), ""), nil
	default:
		return nil, fmt.Errorf("TRIAGE_PROVIDER is %q, want %s or %s", name, providerGemini, providerMock)
	}
}

// mockProvider lets the pipeline run without a provider key or quota. It
// takes about as long as a real review and never produces a verdict, so
// canned output cannot be mistaken for a model's judgment.
type mockProvider struct {
	delay time.Duration
}

func newMockProvider(delay time.Duration) *mockProvider {
	return &mockProvider{delay: delay}
}

func (provider *mockProvider) Name() string { return providerMock }

func (provider *mockProvider) String() string {
	return fmt.Sprintf("%s (delay %s)", providerMock, provider.delay)
}

func (provider *mockProvider) Judge(TriageRequest) (*Verdict, error) {
	time.Sleep(provider.delay)
	return nil, nil
}
