package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindingsRouteRejectsUnknownRule(t *testing.T) {
	// No database: the request must be rejected before any query is made.
	api := httptest.NewServer(newMux(nil, "", http.DefaultClient))
	defer api.Close()

	response, err := http.Get(api.URL + "/findings?rule=bananas")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.StatusCode)
	}
}
