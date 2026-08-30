package reachability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeRecordsSuccessfulAndFailedHTTPPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	d, err := Probe(context.Background(), "service:checkout", []Target{
		{ID: "service:auth", Address: server.URL, Protocol: "http"},
		{ID: "service:missing", Address: "http://127.0.0.1:1", Protocol: "http"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Paths) != 2 || !d.Paths[0].Allowed || d.Paths[1].Allowed {
		t.Fatalf("unexpected paths: %#v", d.Paths)
	}
	if d.Kind != "oekaki.reachability" || d.Version == "" {
		t.Fatalf("unexpected document identity: %#v", d)
	}
	if !strings.Contains(d.Paths[0].Reason, "HTTP") {
		t.Fatalf("missing successful probe reason: %q", d.Paths[0].Reason)
	}
}

func TestProbeRejectsUnknownProtocol(t *testing.T) {
	_, err := Probe(context.Background(), "a", []Target{{ID: "b", Address: "b:1", Protocol: "udp"}})
	if err == nil {
		t.Fatal("unknown protocol was accepted")
	}
}

func TestProbeRejectsMismatchedHTTPScheme(t *testing.T) {
	d, err := Probe(context.Background(), "a", []Target{{ID: "b", Address: "http://127.0.0.1:1", Protocol: "https"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Paths) != 1 || d.Paths[0].Allowed {
		t.Fatalf("scheme mismatch was treated as reachable: %#v", d.Paths)
	}
}
