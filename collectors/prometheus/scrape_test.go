package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("latency{service=\"checkout\"} 820\n")); err != nil {
			panic(err)
		}
	}))
	defer srv.Close()
	got, err := Scrape(context.Background(), srv.Client(), srv.URL, Options{})
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestScrapeRejectsOversizedBody(t *testing.T) {
	old := maxResponseBytes
	maxResponseBytes = 16
	defer func() { maxResponseBytes = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("latency{service=\"checkout\"} 820\n"))
	}))
	defer srv.Close()
	if _, err := Scrape(context.Background(), srv.Client(), srv.URL, Options{}); err == nil {
		t.Fatal("oversized body was accepted")
	}
}
