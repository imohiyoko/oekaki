package datadog

import (
	"context"
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
