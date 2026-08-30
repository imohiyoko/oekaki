package views

import (
	"encoding/csv"
	"strings"
	"testing"
)

func rows(t *testing.T, table string) [][]string {
	t.Helper()
	var b strings.Builder
	if err := WriteCSV(&b, estate(), table); err != nil {
		t.Fatal(err)
	}
	got, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatalf("what was written is not readable as a table: %v", err)
	}
	return got
}

// Two columns with one name is a table that spreadsheets and dataframe
// libraries quietly mangle — one of them is renamed or dropped and nothing
// says so. An axis can be called the same thing as a field, so this is
// reachable from ordinary input rather than being hypothetical.
func TestNoTwoColumnsShareAName(t *testing.T) {
	for _, table := range Tables() {
		head := rows(t, table)[0]
		seen := map[string]bool{}
		for _, c := range head {
			if seen[c] {
				t.Fatalf("%s has two columns called %q: %v", table, c, head)
			}
			seen[c] = true
		}
	}
}

func TestEveryNodeAndEdgeGetsARow(t *testing.T) {
	g := estate()
	if got := len(rows(t, TableNodes)) - 1; got != len(g.Nodes) {
		t.Fatalf("%d rows for %d nodes", got, len(g.Nodes))
	}
	if got := len(rows(t, TableEdges)) - 1; got != len(g.Edges) {
		t.Fatalf("%d rows for %d edges", got, len(g.Edges))
	}
}

// Whether an edge leaves its container is the question these tables get built
// to answer, so it is a column rather than something the reader recomputes.
func TestAnEdgeSaysWhetherItLeavesItsGroup(t *testing.T) {
	got := rows(t, TableEdges)
	at := index(got[0])
	var crossing, staying int
	for _, r := range got[1:] {
		switch r[at["crosses_account"]] {
		case "true":
			crossing++
		case "false":
			staying++
		}
	}
	if crossing == 0 || staying == 0 {
		t.Fatalf("crossing=%d staying=%d — one of the two is not being reported", crossing, staying)
	}
}

// A column per attribute would change shape whenever the input did, which is
// what a spreadsheet cannot cope with.
func TestFreeFormAttributesGoInOneColumnInAStableOrder(t *testing.T) {
	g := estate()
	g.Nodes[0].Attrs = map[string]any{"z": 1, "a": 2, "m": 3}
	var b strings.Builder
	if err := WriteCSV(&b, g, TableNodes); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "a=2; m=3; z=1") {
		t.Fatalf("attributes are not sorted into one column:\n%s", b.String())
	}
}

// Two runs over the same graph have to write the same bytes, or a table
// checked into a repository churns for reasons nobody chose.
func TestTheSameGraphWritesTheSameBytes(t *testing.T) {
	g := estate()
	g.Nodes[0].Attrs = map[string]any{"z": 1, "a": 2, "m": 3}
	var first strings.Builder
	if err := WriteCSV(&first, g, TableNodes); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		var again strings.Builder
		if err := WriteCSV(&again, g, TableNodes); err != nil {
			t.Fatal(err)
		}
		if again.String() != first.String() {
			t.Fatalf("run %d differed", i)
		}
	}
}

func TestATableNobodyHasIsNamedInTheComplaint(t *testing.T) {
	var b strings.Builder
	err := WriteCSV(&b, estate(), "sideways")
	if err == nil {
		t.Fatal("an invented table was written")
	}
	if !strings.Contains(err.Error(), "sideways") {
		t.Fatalf("%v", err)
	}
}

func index(head []string) map[string]int {
	out := make(map[string]int, len(head))
	for i, c := range head {
		out[c] = i
	}
	return out
}

// Everything written here came out of somebody's infrastructure — a resource
// name, an attribute, a label somebody typed. csv.Writer quotes what CSV needs
// quoting and stops there, which is a different question from what a
// spreadsheet will evaluate when the file is opened in one, which is roughly
// always.
func TestAValueASpreadsheetWouldRunIsNeutralised(t *testing.T) {
	g := estate()
	g.Nodes[0].Name = `=1+1`
	g.Nodes[1].Name = `@SUM(A1:A9)`
	g.Nodes[2].Name = `  +danger`
	var b strings.Builder
	if err := WriteCSV(&b, g, TableNodes); err != nil {
		t.Fatal(err)
	}
	got, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	at := index(got[0])["name"]
	for _, r := range got[1:] {
		v := strings.TrimLeft(r[at], " ")
		if v == "" {
			continue
		}
		switch v[0] {
		case '=', '+', '-', '@':
			t.Fatalf("a formula reached the file: %q", r[at])
		}
	}
	// The value is evidence about somebody's estate, so it is prefixed rather
	// than having the character dropped.
	if !strings.Contains(b.String(), "'=1+1") {
		t.Fatalf("the value was changed rather than quoted:\n%s", b.String())
	}
}

// A minus sign at the front of an ordinary number is not an attack, and losing
// it would change what the row says.
func TestANegativeNumberKeepsItsSign(t *testing.T) {
	g := estate()
	g.Nodes[0].Attrs = map[string]any{"delta": -3}
	var b strings.Builder
	if err := WriteCSV(&b, g, TableNodes); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "delta=-3") {
		t.Fatalf("%s", b.String())
	}
}
