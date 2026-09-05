package html

import (
	"strings"
	"testing"
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
