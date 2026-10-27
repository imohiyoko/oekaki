package datadog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse([]byte(`{"series":[{"metric":"latency","scope":"service:checkout,env:prod","pointlist":[[1724842800,820]]}]}`), Options{})
	if err != nil || len(got) != 1 || got[0].Subject != "checkout" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestFetchRejectsNilRequest(t *testing.T) {
	if _, err := Fetch(context.Background(), nil, nil, Options{}); err == nil {
		t.Fatal("nil request was accepted")
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	old := maxResponseBytes
	maxResponseBytes = 16
	defer func() { maxResponseBytes = old }()
	body := `{"series":[{"metric":"latency","scope":"service:checkout","pointlist":[[1724842800,820]]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), server.Client(), req, Options{}); err == nil {
		t.Fatal("oversized body was accepted")
	}
	maxResponseBytes = old
	req, _ = http.NewRequest(http.MethodGet, server.URL, nil)
	if got, err := Fetch(context.Background(), server.Client(), req, Options{}); err != nil || len(got) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
