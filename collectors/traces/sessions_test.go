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

// A trace id is what says two spans belong to the same request. Without one,
// the only thing left that groups them is the session, so that is what groups
// them — three sessions walking the same route are three, not one.
func TestEverySessionInATraceIsCounted(t *testing.T) {
	_, readings, _ := folded(t, `{"version":"1","spans":[
		{"session_id":"s1","service":"gateway"},
		{"session_id":"s1","service":"checkout","parent_service":"gateway"},
		{"session_id":"s2","service":"gateway"},
		{"session_id":"s2","service":"checkout","parent_service":"gateway"},
		{"session_id":"s3","service":"gateway"},
		{"session_id":"s3","service":"checkout","parent_service":"gateway"}
	]}`)

	sessions := reading(t, readings[0], "path_sessions")
	if sessions == nil || *sessions.Value != 3 {
		t.Fatalf("three sessions walked it: %#v", sessions)
	}
}

// And the other half of it: unrelated walks in a file of trace-less spans are
// unrelated. Reading the whole file as one request would credit every session
// in it to every route in it, so two walks by two people become two routes
// each walked by two people — twice the callers, and none of them real.
func TestTraceLessWalksDoNotShareEachOthersSessions(t *testing.T) {
	paths, readings, _ := folded(t, `{"version":"1","spans":[
		{"session_id":"s1","service":"gateway"},
		{"session_id":"s1","service":"checkout","parent_service":"gateway"},
		{"session_id":"s2","service":"api"},
		{"session_id":"s2","service":"ledger","parent_service":"api"}
	]}`)

	if len(paths) != 2 {
		t.Fatalf("got %v, want the two walks kept apart", routes(paths))
	}
	for i, about := range readings {
		sessions := reading(t, about, "path_sessions")
		if sessions == nil || *sessions.Value != 1 {
			t.Errorf("%s: %#v, want one session", routes(paths)[i], sessions)
		}
	}
}

// The same person walking two routes is one person on each of them, which is
// the case that must not be fixed by splitting everything apart.
func TestOneSessionWalkingTwoRoutesCountsOnceOnEach(t *testing.T) {
	paths, readings, _ := folded(t, `{"version":"1","spans":[
		{"session_id":"s1","service":"gateway"},
		{"session_id":"s1","service":"checkout","parent_service":"gateway"},
		{"session_id":"s1","service":"api"},
		{"session_id":"s1","service":"ledger","parent_service":"api"}
	]}`)

	if len(paths) != 2 {
		t.Fatalf("got %v, want both walks", routes(paths))
	}
	for i, about := range readings {
		sessions := reading(t, about, "path_sessions")
		if sessions == nil || *sessions.Value != 1 {
			t.Errorf("%s: %#v, want the one session", routes(paths)[i], sessions)
		}
	}
}

// A trace id groups spans, and a session id does not override it: two sessions
// named inside one trace are two, because nothing in the input promises a
// trace names only one and reporting them as one is the failure this counts
// against.
func TestATraceIdStillDoesTheGrouping(t *testing.T) {
	_, readings, _ := folded(t, `{"version":"1","spans":[
		{"trace_id":"t1","session_id":"s1","service":"gateway"},
		{"trace_id":"t1","session_id":"s2","service":"checkout","parent_service":"gateway"}
	]}`)

	sessions := reading(t, readings[0], "path_sessions")
	if sessions == nil || *sessions.Value != 2 {
		t.Fatalf("both sessions named in the trace: %#v", sessions)
	}
}
