package traces

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func reading(t *testing.T, about []core.Observation, metric string) *core.Observation {
	t.Helper()
	for i := range about {
		if about[i].Metric == metric {
			return &about[i]
		}
	}
	return nil
}

// A trace is one request; a session is the reason there were four. One person
// clicking four times and four people finding the same route are different
// things, and the count of walks cannot tell them apart.
func TestARouteSaysHowManySessionsWalkedIt(t *testing.T) {
	paths, readings, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","session_id":"s1","service":"gateway"},
		{"trace_id":"t1","session_id":"s1","service":"checkout","parent_service":"gateway"},
		{"trace_id":"t2","session_id":"s1","service":"gateway"},
		{"trace_id":"t2","session_id":"s1","service":"checkout","parent_service":"gateway"},
		{"trace_id":"t3","session_id":"s2","service":"gateway"},
		{"trace_id":"t3","session_id":"s2","service":"checkout","parent_service":"gateway"}
	]}`)
	if len(paths) != 1 {
		t.Fatalf("got %v", routes(paths))
	}

	walks := reading(t, readings[0], "path_requests")
	if walks == nil || *walks.Value != 3 {
		t.Fatalf("three walks: %#v", walks)
	}
	sessions := reading(t, readings[0], "path_sessions")
	if sessions == nil || *sessions.Value != 2 {
		t.Fatalf("two sessions walked it: %#v", sessions)
	}
	if sessions.Subject != walks.Subject {
		t.Error("the two readings are about different things")
	}
}

// Nothing derived from a session id reaches the graph except a number. Whether
// it identifies a person is the caller's business; this side only ever counts
// distinct values.
func TestASessionIdIsCountedAndNotKept(t *testing.T) {
	_, readings, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","session_id":"user-4139-secret","service":"a"},
		{"trace_id":"t1","session_id":"user-4139-secret","service":"b","parent_service":"a"}
	]}`)

	for _, o := range readings[0] {
		if o.Subject == "user-4139-secret" || o.Unit == "user-4139-secret" {
			t.Fatal("the session id reached the graph")
		}
		for _, v := range o.Labels {
			if v == "user-4139-secret" {
				t.Fatal("the session id reached the graph as a label")
			}
		}
	}
}

// Traces that carry no session say nothing about sessions, rather than
// claiming there was one.
func TestNoSessionMeansNoReading(t *testing.T) {
	_, readings, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","service":"a"},
		{"trace_id":"t1","service":"b","parent_service":"a"}
	]}`)

	if reading(t, readings[0], "path_sessions") != nil {
		t.Fatal("a session was counted where nothing said there was one")
	}
	if reading(t, readings[0], "path_requests") == nil {
		t.Fatal("the walk was not counted")
	}
}
