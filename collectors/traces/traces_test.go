package traces

import "testing"

func TestParseEdgesAndObservation(t *testing.T) {
	d, err := Parse([]byte(`{"version":"1","spans":[{"trace_id":"t1","service":"checkout","parent_service":"frontend","operation":"GET /checkout","duration_ms":82,"status":"ok"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Edges()) != 1 || d.Edges()[0].Relation != "calls" {
		t.Fatal("missing calls edge")
	}
	if d.Spans[0].Observation().Metric != "request_duration" {
		t.Fatal("missing observation")
	}
}
