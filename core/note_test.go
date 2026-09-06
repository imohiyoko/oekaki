package core

import "testing"

func notedGraph() *Graph {
	g := New()
	g.Nodes = []Node{
		{ID: "checkout", Type: "service", Name: "checkout"},
		{ID: "ledger", Type: "service", Name: "ledger"},
	}
	g.Edges = []Edge{{From: "checkout", To: "ledger", Kind: EdgeObserved, Relation: "calls"}}
	return g
}

// Two people writing about the same service is the ordinary case, and the
// later one does not replace the earlier one any more than a second opinion
// deletes the first.
func TestSeveralNotesAboutOneThingAreSeveralNotes(t *testing.T) {
	g := notedGraph()
	g.Notes = []Note{
		{Subject: "checkout", Text: "retries three times", Claim: &Claim{Origin: OriginHuman, Author: "one"}},
		{Subject: "checkout", Text: "times out since September", Claim: &Claim{Origin: OriginHuman, Author: "two"}},
	}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(g.NotesAbout("checkout")) != 2 {
		t.Fatalf("got %d notes, want both", len(g.NotesAbout("checkout")))
	}
}

// The same note twice is one note — an overlay applied twice, or two documents
// carrying the same sentence. Two that differ in a character are two, because
// nothing here can know which one the writer meant to keep.
func TestTheSameNoteTwiceIsOneNote(t *testing.T) {
	g := notedGraph()
	same := Claim{Origin: OriginHuman, Author: "one"}
	g.Notes = []Note{
		{Subject: "checkout", Text: "retries three times", Claim: &same},
		{Subject: "checkout", Text: "retries three times", Claim: &same},
		{Subject: "checkout", Text: "retries three times.", Claim: &same},
	}
	g.Normalize()
	if len(g.Notes) != 2 {
		t.Fatalf("got %d notes: %#v", len(g.Notes), g.Notes)
	}
}

// A note about a line is an ordinary case — one arrow usually stands for
// several references, and what somebody knows about it belongs on it.
func TestANoteMayBeAboutALine(t *testing.T) {
	g := notedGraph()
	g.Notes = []Note{{
		Subject: EdgeKey("checkout", "ledger", EdgeObserved, "calls"),
		Text:    "the slow one",
		Claim:   &Claim{Origin: OriginHuman},
	}}
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatalf("a note about a line was refused: %v", err)
	}
}

// A note about nothing is a sentence with no subject, and an empty one is a
// note nobody wrote.
func TestANoteIsCheckedAgainstWhatItIsAbout(t *testing.T) {
	g := notedGraph()
	g.Notes = []Note{
		{Subject: "nowhere", Text: "about something absent", Claim: &Claim{Origin: OriginHuman}},
		{Subject: "checkout", Text: "   ", Claim: &Claim{Origin: OriginHuman}},
	}
	err := g.Validate()
	if err == nil {
		t.Fatal("a note about nothing was accepted")
	}
	for _, want := range []string{"unknown subject", "empty text"} {
		if !contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// A scope renames every id in the document, and what a note is about is one.
func TestAScopeRenamesWhatANoteIsAbout(t *testing.T) {
	g := notedGraph()
	g.Notes = []Note{{Subject: "checkout", Text: "the entrance", Claim: &Claim{Origin: OriginHuman}}}
	g.Normalize()

	g.ApplyScope("shop")
	g.Normalize()
	if err := g.Validate(); err != nil {
		t.Fatalf("a scoped document does not hold together: %v", err)
	}
	if g.Notes[0].Subject != "shop:checkout" {
		t.Fatalf("the note is still about %q", g.Notes[0].Subject)
	}
}
