package opensearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSearchResponse(t *testing.T) {
	got, err := Parse([]byte(`{"hits":{"hits":[{"_id":"abc","_source":{"service":"checkout","observed_at":"2026-08-28T10:00:00Z","message":"secret must not be persisted"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "abc" || got[0].Source != "checkout" || got[0].Body == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestFetchUsesCallerRequestAndClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("caller header was not preserved")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"hits":[{"_id":"id-1","_source":{"service":"api","message":"event"}}]}}`))
	}))
	defer server.Close()
	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"query":{"match_all":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	got, err := Fetch(context.Background(), server.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "id-1" {
		t.Fatalf("got %+v", got)
	}
}
