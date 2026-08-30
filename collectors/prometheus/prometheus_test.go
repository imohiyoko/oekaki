package prometheus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func TestParseExposition(t *testing.T) {
	got, err := Parse("# HELP latency latency\nlatency{service=\"checkout\",unit=\"ms\"} 820\n", Options{ObservedAt: "2026-08-28T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subject != "checkout" || *got[0].Value != 820 || got[0].Labels["service"] != "checkout" || got[0].Labels["unit"] != "ms" {
		t.Fatalf("got %+v", got)
	}
}

func TestParsePreservesLabelsAndAppliesThreshold(t *testing.T) {
	threshold := map[string]core.Threshold{"request_latency": {Operator: ">", Value: 100}}
	obs, err := Parse(`request_latency{service="checkout",sensor="p95"} 123`, Options{Thresholds: threshold})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || obs[0].Labels["sensor"] != "p95" || obs[0].Threshold == nil {
		t.Fatalf("labels or threshold lost: %#v", obs)
	}
}

func TestParseHandlesQuotedCommasEscapesAndTimestamps(t *testing.T) {
	obs, err := Parse(`request_latency{service="checkout",message="a,b",quote="a\"b"} 123 1700000000000`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 || obs[0].Value == nil || *obs[0].Value != 123 || obs[0].Labels["message"] != "a,b" || obs[0].Labels["quote"] != `a"b` {
		t.Fatalf("sample was not parsed correctly: %#v", obs)
	}
}

func TestJSONSinkRetainsMetricHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "observations.json")
	value1, value2 := 1.0, 2.0
	sink := JSONSink{Path: path}
	if err := sink.Write(context.Background(), Document{Kind: "oekaki.observations", Version: "1", Observations: []core.Observation{{Subject: "s", Metric: "m", ObservedAt: "2026-01-01T00:00:00Z", Value: &value1}}}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Write(context.Background(), Document{Kind: "oekaki.observations", Version: "1", Observations: []core.Observation{{Subject: "s", Metric: "m", ObservedAt: "2026-01-01T00:01:00Z", Value: &value2}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Document
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 2 {
		t.Fatalf("history was overwritten: %#v", got.Observations)
	}
}

func TestJSONSinkDoesNotCollideEscapedLabelTuples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.json")
	value1, value2 := 1.0, 2.0
	doc := Document{Kind: "oekaki.observations", Version: "1", Observations: []core.Observation{
		{Subject: "s", Metric: "m", ObservedAt: "2026-01-01T00:00:00Z", Labels: map[string]string{"a": "b,c=d"}, Value: &value1},
		{Subject: "s", Metric: "m", ObservedAt: "2026-01-01T00:00:00Z", Labels: map[string]string{"a": "b", "c": "d"}, Value: &value2},
	}}
	if err := (JSONSink{Path: path}).Write(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Document
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 2 {
		t.Fatalf("label tuples collided: %#v", got.Observations)
	}
}

func TestJSONSinkCanonicalizesNilAndEmptyLabelsAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.json")
	value1, value2 := 1.0, 2.0
	sink := JSONSink{Path: path}
	base := core.Observation{Subject: "s", Metric: "m", ObservedAt: "2026-01-01T00:00:00Z"}
	first := base
	first.Value = &value1
	first.Labels = nil
	if err := sink.Write(context.Background(), Document{Kind: "oekaki.observations", Version: "1", Observations: []core.Observation{first}}); err != nil {
		t.Fatal(err)
	}
	second := base
	second.Value = &value2
	second.Labels = map[string]string{}
	if err := sink.Write(context.Background(), Document{Kind: "oekaki.observations", Version: "1", Observations: []core.Observation{second}}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Document
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Observations) != 1 || got.Observations[0].Value == nil || *got.Observations[0].Value != value2 {
		t.Fatalf("nil and empty labels were treated as different identities: %#v", got.Observations)
	}
}
