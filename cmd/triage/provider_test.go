package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

const unset = "<unset>"

// setEnv gives one variable a value for the length of the test, or removes
// it when the value is unset. Either way the test never sees the value the
// variable has outside it.
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value) // registers the restore of the outside value
	if value == unset {
		os.Unsetenv(key)
	}
}

// providerFor runs the selection with TRIAGE_PROVIDER and the key as given
// and every other triage setting unset.
func providerFor(t *testing.T, setting, key string) (Provider, error) {
	t.Helper()
	setEnv(t, "TRIAGE_PROVIDER", setting)
	setEnv(t, "GEMINI_API_KEY", key)
	setEnv(t, "GEMINI_MODEL", unset)
	setEnv(t, "MOCK_TRIAGE_DELAY_MS", unset)
	return providerFromEnv()
}

func TestProviderSelection(t *testing.T) {
	cases := []struct {
		setting string
		key     string
		want    string // provider name; "" = startup error
	}{
		{unset, unset, "mock"},
		{unset, "", "mock"}, // an empty key is no key: compose passes one when the environment file has none
		{unset, "k", "gemini"},
		{"", unset, "mock"},
		{"", "", "mock"},
		{"", "k", "gemini"},
		{"gemini", unset, ""}, // Gemini cannot run without a key
		{"gemini", "", ""},
		{"gemini", "k", "gemini"},
		{"mock", unset, "mock"},
		{"mock", "k", "mock"}, // set to mock, a key does not switch the LLM provider on
		{"openai", unset, ""},
		{"openai", "k", ""},
		{"Mock", "k", ""}, // matched as written
	}

	for _, c := range cases {
		provider, err := providerFor(t, c.setting, c.key)

		if c.want == "" {
			if err == nil {
				t.Errorf("TRIAGE_PROVIDER=%q key=%q: got provider %q, want an error", c.setting, c.key, provider.Name())
			}
			continue
		}
		if err != nil {
			t.Errorf("TRIAGE_PROVIDER=%q key=%q: %v", c.setting, c.key, err)
			continue
		}
		if provider.Name() != c.want {
			t.Errorf("TRIAGE_PROVIDER=%q key=%q: provider = %q, want %q", c.setting, c.key, provider.Name(), c.want)
		}
	}
}

func TestUnknownProviderErrorNamesTheAllowedValues(t *testing.T) {
	_, err := providerFor(t, "openai", unset)
	if err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if !strings.Contains(err.Error(), "gemini") || !strings.Contains(err.Error(), "mock") {
		t.Errorf("error %q does not name both allowed values", err)
	}
}

func TestInvalidMockDelayIsAStartupError(t *testing.T) {
	for _, delay := range []string{"soon", "-1", "1.5"} {
		setEnv(t, "TRIAGE_PROVIDER", "mock")
		setEnv(t, "GEMINI_API_KEY", unset)
		setEnv(t, "MOCK_TRIAGE_DELAY_MS", delay)
		if provider, err := providerFromEnv(); err == nil {
			t.Errorf("MOCK_TRIAGE_DELAY_MS=%q: got provider %v, want an error", delay, provider)
		}
	}
}

func TestMockProviderReturnsNoVerdict(t *testing.T) {
	started := time.Now()
	verdict, err := newMockProvider(0).Judge(TriageRequest{FindingID: 1, Rule: "pipe_to_shell"})
	if err != nil {
		t.Fatal(err)
	}
	if verdict != nil {
		t.Errorf("mock provider returned verdict %+v, want none", verdict)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("mock provider with zero delay took %v", elapsed)
	}
}
