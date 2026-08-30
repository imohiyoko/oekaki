package layout

import "testing"

// Two entries for one line contradict each other rather than add up, and which
// one wins would come down to the order they happen to be written in.
func TestOneLineIsOnlyDescribedOnce(t *testing.T) {
	doc := []byte(`{"kind":"oekaki.layout","version":"0.2","nodes":[],
		"edges":[{"from":"a","to":"b","kind":"iac_ref","source":"right"},
		         {"from":"a","to":"b","kind":"iac_ref","target":"left"}],
		"claim":{"origin":"human"}}`)
	if _, err := Parse(doc, "test"); err == nil {
		t.Fatal("a line described twice was accepted")
	}
}

// Relation is part of a line's name, so two kinds of reference between the
// same pair are two lines and may be described differently.
func TestRelationTellsTwoLinesApart(t *testing.T) {
	doc := []byte(`{"kind":"oekaki.layout","version":"0.2","nodes":[],
		"edges":[{"from":"a","to":"b","kind":"iac_ref","relation":"remote_state","source":"right"},
		         {"from":"a","to":"b","kind":"iac_ref","relation":"module","source":"top"}],
		"claim":{"origin":"human"}}`)
	parsed, err := Parse(doc, "test")
	if err != nil {
		t.Fatalf("two different lines were read as one: %v", err)
	}
	if len(parsed.Edges) != 2 {
		t.Errorf("read %d lines, want 2", len(parsed.Edges))
	}
}

// A document written before sides existed still applies.
func TestAnEarlierDocumentStillReads(t *testing.T) {
	doc := []byte(`{"kind":"oekaki.layout","version":"0.1",
		"nodes":[{"id":"a","x":1,"y":2}],"claim":{"origin":"human"}}`)
	parsed, err := Parse(doc, "test")
	if err != nil {
		t.Fatalf("a 0.1 layout was rejected: %v", err)
	}
	if len(parsed.Nodes) != 1 || len(parsed.Edges) != 0 {
		t.Errorf("read %d nodes and %d lines, want 1 and 0", len(parsed.Nodes), len(parsed.Edges))
	}
}

// A layout applies by id and leaves what it does not recognise alone. Both the
// CLI and the server need the same answer to "how much of this lands", and two
// copies of that answer would eventually disagree.
func TestADocumentSaysHowMuchOfItLands(t *testing.T) {
	doc, err := Parse([]byte(`{"kind":"oekaki.layout","version":"0.2","nodes":[
		{"id":"here","x":1,"y":2},{"id":"gone","x":3,"y":4},{"id":"group","x":5,"y":6}],
		"claim":{"origin":"human"}}`), "t")
	if err != nil {
		t.Fatal(err)
	}

	at := doc.Against(map[string]struct{}{"here": {}, "group": {}})

	if at.Placed != 2 || at.Total() != 3 {
		t.Fatalf("counted %d of %d", at.Placed, at.Total())
	}
	if len(at.Missing) != 1 || at.Missing[0] != "gone" {
		t.Fatalf("missing ids wrong: %v", at.Missing)
	}
}

// Nothing known means nothing placed, and every position is worth naming: a
// document pointed at the wrong graph is the case the count exists to catch.
func TestADocumentThatLandsNowhereSaysSoForEveryPosition(t *testing.T) {
	doc, err := Parse([]byte(`{"kind":"oekaki.layout","version":"0.2","nodes":[
		{"id":"a","x":1,"y":2},{"id":"b","x":3,"y":4}],"claim":{"origin":"human"}}`), "t")
	if err != nil {
		t.Fatal(err)
	}

	at := doc.Against(nil)

	if at.Placed != 0 || len(at.Missing) != 2 {
		t.Fatalf("expected nothing placed and both named, got %#v", at)
	}
}
