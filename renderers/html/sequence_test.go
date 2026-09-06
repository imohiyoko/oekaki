package html

import (
	"strings"
	"testing"
)

// A sequence is laid out by arithmetic, not by ELK. The participants are a row
// and the messages are an order, and a graph engine asked to draw one produces
// a picture of the same edges — which is what the reader already had one level
// up.
func TestASequenceIsDrawnAsLifelines(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"ShapeRegistry.add('oekaki-lifeline', LifelineShape);",
		"function sequenceLayout() {",
		"if (isSequence()) {",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a sequence is drawn as an ordinary graph", want)
		}
	}

	// Lifeline to lifeline, at the height of the step. Without turning the
	// perimeter off, maxGraph pulls each end back to the edge of the box and
	// every message stops short of the thing it arrives at.
	if !strings.Contains(app, "exitPerimeter: false") || !strings.Contains(app, "entryPerimeter: false") {
		t.Error("messages are anchored to the box perimeter rather than to the lifeline")
	}
}

// A message can be put aside, and the band left in its place says how many and
// puts them back. The step is still in the document: hiding is not filtering.
func TestAStepCanBePutAsideAndPutBack(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"const hiddenSteps = new Set();",
		"kind: 'band'",
		"for (const step of cell.infra.steps) hiddenSteps.delete(hiddenKey(step));",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so the middle of a long sequence cannot be put aside", want)
		}
	}

	// The columns are a property of the sequence. Taking a message away must
	// not move the participants it named to the end of the row.
	if !strings.Contains(app, "Worked out from every step, not from the ones on screen") {
		t.Error("hiding a step rearranges the participants")
	}
}

// "A request went this way" and "the references say a request could go this
// way" are different claims, and a reader four pages down has no other way to
// tell which one they are looking at.
func TestThePageSaysWhereASequencesOrderCameFrom(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "page.order === 'observed'") {
		t.Error("the page does not say whether a sequence was observed or derived")
	}
	// After .kind and at higher specificity, or the shorthand border in .kind
	// resets it and the two badges look the same in the browser while the
	// selector is right there in the file.
	css := string(Assets(nil)[AssetCSS])
	kind := strings.Index(css, "#breadcrumbs .kind {")
	observed := strings.Index(css, "#breadcrumbs .kind.order-observed")
	if kind < 0 || observed < 0 || observed < kind {
		t.Error("an observed order and a derived one are drawn as the same badge")
	}
}

// Both ends of the first message share a step number, so ranking the columns
// by step leaves them tied — and two columns that tie fall back to the order
// the document happens to list them in, which can put the one being called to
// the left of the one calling it and draw the first message right to left.
func TestTheColumnsAreRankedPerParticipant(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "first.set(id, appearance++)") {
		t.Error("the columns are ranked by step, so the first message can be drawn backwards")
	}
}

// A disagreement is about the edge, not about which of its steps was clicked,
// and the document names it the way the IR does.
func TestAConflictOnAStepIsStillFound(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "edgeConflicts.filter((c) => c.target === claimedKey(key))") {
		t.Error("a contested message in a sequence never shows its disagreement")
	}
}
