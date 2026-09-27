package html

import (
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A page told what was folded carries the record, and the graph it carries is
// the unfolded one — which is what lets a fold open where it stands instead of
// sending the reader back to the command line.
func TestFoldRecordReachesThePage(t *testing.T) {
	plain := render(t, fixture(), Options{})
	if strings.Contains(plain, `id="oekaki-folds"`) {
		t.Error("a page with nothing folded carries a fold record")
	}

	record := `[{"kind":"twins","stands":"fold:twins:aws_ecs_service.api",` +
		`"members":["aws_ecs_service.api","aws_db_instance.main"],"label":"api ×2"}]`
	out := render(t, fixture(), Options{Folds: []byte(record)})

	data := between(t, out, `<script type="application/json" id="oekaki-folds">`, "</script>")
	if !strings.Contains(data, "fold:twins:aws_ecs_service.api") {
		t.Errorf("the record did not arrive intact: %q", data)
	}
	// The page still carries the boxes the record says were folded, or there
	// would be nothing to put back.
	if !strings.Contains(out, `"id": "aws_db_instance.main"`) {
		t.Error("the page does not carry the boxes the record stands for")
	}
	// And it says so, because a gesture nobody has been told about is one
	// nobody makes.
	if !strings.Contains(out, "A stacked box stands for several") {
		t.Error("the page does not say how to put a fold back")
	}
}

// The same escaping the graph and the atlas get, for the same reason.
func TestAClosingTagInTheFoldRecordCannotEscape(t *testing.T) {
	out := render(t, fixture(), Options{Folds: []byte(`[{"label":"</script><img src=x onerror=alert(1)>"}]`)})
	data := between(t, out, `<script type="application/json" id="oekaki-folds">`, "</script>")
	if !strings.Contains(data, `<\/script>`) {
		t.Error("the closing tag in the record was not escaped")
	}
	if !strings.Contains(data, "onerror") {
		t.Error("the block was cut short, so the rest of the record is loose in the document")
	}
}

// The page folds edges of its own — a collapsed container puts several
// references onto one line, after the document was normalized and where
// nothing will normalize it again — so it has to settle the denial's
// sentence itself, and it needs the sentence to do that. Two copies of one
// string, in two languages: this is what keeps them the same one.
func TestThePageKnowsTheSentenceCoreWrites(t *testing.T) {
	if !strings.Contains(appJS, "'"+core.DeniedNote+"'") {
		t.Errorf("app.js does not carry %q, so its fold cannot tell that sentence from an author's",
			core.DeniedNote)
	}

	// And settles it on the same terms core does. settledClaim takes the
	// sentence off a line only where it could have put it there — one
	// somebody denied — because on any other line those words are an
	// author's own. The page had the rule before core narrowed it, and
	// deleted an author's note the document had kept.
	//
	// This reads the condition rather than running it. No Go test can run
	// this file: the page's fold is inside the browser, and node is not
	// part of the test toolchain. So it catches the two ways the rule has
	// actually been got wrong — dropping the guard, and negating it — and
	// nothing else. A behavioural test of app.js needs a JavaScript
	// harness, which this repository does not have.
	if !strings.Contains(appJS, "if (edge.suppressed && edge.claim && edge.claim.note === DENIED_NOTE)") {
		t.Error("app.js does not take the sentence off exactly where core does")
	}
	if strings.Contains(appJS, "!edge.suppressed && edge.claim && edge.claim.note") {
		t.Error("app.js takes the sentence off a line precisely because nobody denied it")
	}
}
