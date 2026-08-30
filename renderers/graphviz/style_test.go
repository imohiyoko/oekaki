package graphviz

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// parses reports whether the whole document is well-formed XML, which is what
// a browser demands of an .svg file — not the tolerance it extends to HTML.
func parses(t *testing.T, doc []byte) error {
	t.Helper()

	dec := xml.NewDecoder(strings.NewReader(string(doc)))
	dec.Strict = true
	for {
		if _, err := dec.Token(); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
	}
}

// The stylesheet has to be inside the root element to apply to anything, and
// after it rather than before, because content outside the root is not part
// of the picture.
func TestAStylesheetGoesInsideTheSvgRoot(t *testing.T) {
	out, err := Render(context.Background(), fitFixture(), Options{
		CSS: []byte("g.edge path { stroke: #7c8b99; }"),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	root := strings.Index(string(out), "<svg")
	style := strings.Index(string(out), "<style")
	if style < 0 {
		t.Fatal("the picture carries no stylesheet")
	}
	if root < 0 || style < root {
		t.Error("the stylesheet is outside the root element, where it styles nothing")
	}
	if !strings.Contains(string(out), "g.edge path { stroke: #7c8b99; }") {
		t.Error("the rules did not survive into the picture")
	}
	if err := parses(t, out); err != nil {
		t.Errorf("the picture is no longer well-formed XML: %v", err)
	}
}

// An SVG is XML, where a bare & or < ends the parse rather than the rule.
// Both are ordinary CSS now — nesting spells the parent &, and a container
// query compares with <. Without the CDATA section this file would produce a
// picture that no browser will open.
func TestOrdinaryCssCharactersDoNotBreakThePicture(t *testing.T) {
	out, err := Render(context.Background(), fitFixture(), Options{
		CSS: []byte("g.edge { & path { stroke: red; } }\n@container (width < 40rem) { g.edge { stroke-width: 2; } }"),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if err := parses(t, out); err != nil {
		t.Errorf("the picture is no longer well-formed XML: %v", err)
	}
}

func TestAStylesheetCannotEndTheCdataSection(t *testing.T) {
	_, err := Render(context.Background(), fitFixture(), Options{
		CSS: []byte("a{content:\"]]>\"}"),
	})
	if err == nil {
		t.Fatal("a stylesheet that closes the CDATA section was accepted")
	}
	if !strings.Contains(err.Error(), "]]>") {
		t.Errorf("the error does not say what is wrong with the file: %v", err)
	}
}

// Nothing is added when nothing was asked for, so every picture this project
// has already committed stays byte-identical.
func TestAPictureWithoutAStylesheetIsUnchanged(t *testing.T) {
	out, err := Render(context.Background(), fitFixture(), Options{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(out), "<style") || strings.Contains(string(out), "CDATA") {
		t.Error("a picture nobody themed came out with a stylesheet in it")
	}
}

// CDATA settles what the characters mean, not whether they are characters.
// XML 1.0 admits no control character but tab, newline and carriage return,
// and no byte sequence that is not UTF-8 — so these produce a picture that no
// parser will open, which is worse than a rule that fails to apply.
func TestBytesXmlCannotCarryAreRefused(t *testing.T) {
	for _, c := range []struct {
		about string
		css   []byte
		says  string
	}{
		{"a stray NUL", []byte("a{}\x00b{}"), "U+0000"},
		{"a form feed", []byte("a{}\x0cb{}"), "U+000C"},
		{"a sheet saved in Latin-1", []byte("a{}/* caf\xe9 */"), "UTF-8"},
	} {
		out, err := Render(context.Background(), fitFixture(), Options{CSS: c.css})
		if err == nil {
			t.Errorf("%s was accepted; the picture it made %s", c.about,
				map[bool]string{true: "does not open", false: "opens"}[parses(t, out) != nil])
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: the error does not say what is wrong: %v", c.about, err)
		}
	}
}

// Tab, newline and carriage return are the control characters XML does allow,
// and CSS is full of the first two. Refusing them would refuse every file.
func TestWhitespaceIsStillAllowed(t *testing.T) {
	out, err := Render(context.Background(), fitFixture(), Options{
		CSS: []byte("g.edge path {\n\tstroke: red;\r\n}"),
	})
	if err != nil {
		t.Fatalf("ordinary whitespace was refused: %v", err)
	}
	if err := parses(t, out); err != nil {
		t.Errorf("the picture is no longer well-formed XML: %v", err)
	}
}
