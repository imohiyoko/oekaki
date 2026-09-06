package overlay

import (
	"strings"
	"testing"
)

// A note is written about a subject the same way anything else is asserted
// about one, so it is resolved through the same selectors and carries the same
// claim as the rest of the document.
func TestANoteIsAppliedToItsSubject(t *testing.T) {
	g, _ := apply(t, doc(`
	  {"assert":"note","subject":{"name":"checkout","type":"aws_ecs_service"},
	   "text":"**決済の入り口。** リトライは 3 回まで。"}`), Options{})

	notes := g.NotesAbout("aws_ecs_service.checkout")
	if len(notes) != 1 {
		t.Fatalf("got %d notes about checkout, want one: %#v", len(notes), g.Notes)
	}
	if !strings.Contains(notes[0].Text, "リトライ") {
		t.Errorf("the text did not survive: %q", notes[0].Text)
	}
	if notes[0].Claim == nil || notes[0].Claim.Author != "operator" {
		t.Errorf("the note is not signed by the document's author: %#v", notes[0].Claim)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("the enriched document does not hold together: %v", err)
	}
}

// A subject that names two things is not written about at all — the rule every
// other assertion follows, because guessing which one was meant is worse than
// saying nothing.
func TestANoteAboutAnAmbiguousSubjectIsNotApplied(t *testing.T) {
	g, r := apply(t, doc(`
	  {"assert":"note","subject":{"name":"checkout"},"text":"どちらの話か分からない"}`), Options{})

	if len(g.Notes) != 0 {
		t.Fatalf("an ambiguous note was applied: %#v", g.Notes)
	}
	if r.r.Clean() {
		t.Error("the report says nothing went wrong")
	}
}

// The text is the note; a note without it is a signature on an empty page.
func TestANoteNeedsText(t *testing.T) {
	body := doc(`{"assert":"note","subject":{"node":"aws_lb.api"}}`)
	if _, err := Parse([]byte(body), "test.json"); err == nil {
		t.Fatal("a note with no text was accepted")
	}
}

// What a note says is not a field of the resource, so the fields meaningful
// for other assertions are refused here.
func TestANoteDoesNotCarryAResourceField(t *testing.T) {
	body := doc(`{"assert":"note","subject":{"node":"aws_lb.api"},"text":"x","name":"renamed"}`)
	_, err := Parse([]byte(body), "test.json")
	if err == nil {
		t.Fatal("a note carrying a rename was accepted")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("the error does not say which field: %v", err)
	}
}
