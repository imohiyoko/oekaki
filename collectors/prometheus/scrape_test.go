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
