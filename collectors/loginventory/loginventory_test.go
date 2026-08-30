package loginventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

type store struct {
	records []Record
	since   time.Time
}

func (s *store) Fetch(_ context.Context, since time.Time) ([]Record, error) {
	s.since = since
	return s.records, nil
}

type sink struct{ inv Inventory }

func (s *sink) Write(_ context.Context, inv Inventory) error { s.inv = inv; return nil }
func TestPollOnceClassifiesAndAdvancesWatermark(t *testing.T) {
	at := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	st := &store{records: []Record{{ID: "2", ObservedAt: at, Body: "ERROR timeout"}, {ID: "1", ObservedAt: at, Body: "ERROR timeout"}}}
	sk := &sink{}
	p := &Poller{Store: st, Classifier: RuleClassifier{Rules: []Rule{{Label: "error", Pattern: regexp.MustCompile("ERROR"), Characteristics: map[string]string{"severity": "error"}}}}, Sink: sk, Clock: func() time.Time { return at.Add(time.Minute) }}
	inv, err := p.PollOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Records) != 2 || inv.Records[0].ID != "1" || inv.Records[0].Labels[0] != "error" {
		t.Fatalf("inventory=%+v", inv)
	}
	if inv.Status == nil || inv.Status.Fetched != 2 || inv.Status.Classified != 2 || !inv.Status.CompletedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("poll status=%+v", inv.Status)
	}
	if _, err = p.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !st.since.Equal(at) {
		t.Fatalf("watermark=%v", st.since)
	}
}

func TestJSONSinkMergesByID(t *testing.T) {
	d := t.TempDir() + "/inventory.json"
	s := JSONSink{Path: d}
	at := time.Unix(10, 0)
	if err := s.Write(context.Background(), Inventory{Version: "1", Records: []ClassifiedRecord{{ID: "1", ObservedAt: at}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), Inventory{Version: "1", Records: []ClassifiedRecord{{ID: "2", ObservedAt: at.Add(time.Second)}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(d)
	if err != nil {
		t.Fatal(err)
	}
	var got Inventory
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 2 {
		t.Fatalf("got %d records", len(got.Records))
	}
}

type failingStore struct{}

func (failingStore) Fetch(context.Context, time.Time) ([]Record, error) {
	return nil, fmt.Errorf("backend unavailable")
}

func TestPollerPersistsFailureStatus(t *testing.T) {
	d := t.TempDir() + "/inventory.json"
	at := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	p := &Poller{Store: failingStore{}, Classifier: RuleClassifier{}, Sink: JSONSink{Path: d}, Clock: func() time.Time { return at }}
	if _, err := p.PollOnce(context.Background()); err == nil {
		t.Fatal("expected backend failure")
	}
	raw, err := os.ReadFile(d)
	if err != nil {
		t.Fatal(err)
	}
	var inv Inventory
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Status == nil || inv.Status.LastError != "backend unavailable" || !inv.Status.CompletedAt.Equal(at) {
		t.Fatalf("failure status was not persisted: %+v", inv.Status)
	}
}

func TestPollerDerivesStableIDWithoutPersistingBody(t *testing.T) {
	at := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	st := &store{records: []Record{{Source: "api", ObservedAt: at, Body: "private event"}}}
	sk := &sink{}
	p := &Poller{Store: st, Classifier: RuleClassifier{}, Sink: sk}
	if _, err := p.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sk.inv.Records) != 1 || sk.inv.Records[0].ID == "" || sk.inv.Records[0].ID[:8] != "derived:" {
		t.Fatalf("stable ID was not derived: %+v", sk.inv.Records)
	}
	if bytes.Contains(mustJSON(t, sk.inv), []byte("private event")) {
		t.Fatal("raw body leaked into inventory")
	}
}

func TestDirectoryStoreDecodesBodyWithoutSerializingIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api.jsonl")
	raw := []byte("{\"id\":\"event-1\",\"source\":\"api\",\"observed_at\":\"2026-08-28T01:00:00Z\",\"body\":\"ERROR private timeout\"}\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	records, err := (DirectoryStore{Root: root}).Fetch(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Body != "ERROR private timeout" {
		t.Fatalf("decoded records = %#v", records)
	}
	encoded, err := json.Marshal(records[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("private timeout")) || bytes.Contains(encoded, []byte(`"body"`)) {
		t.Fatalf("raw body was serialized: %s", encoded)
	}

	chars, labels, err := (RuleClassifier{Rules: []Rule{{
		Label: "error", Pattern: regexp.MustCompile("ERROR"), Characteristics: map[string]string{"severity": "error"},
	}}}).Classify(records[0])
	if err != nil || chars["severity"] != "error" || len(labels) != 1 || labels[0] != "error" {
		t.Fatalf("classification did not inspect decoded body: chars=%v labels=%v err=%v", chars, labels, err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
