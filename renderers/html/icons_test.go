package html

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInGlyphsCoverEveryCategory(t *testing.T) {
	out := render(t, fixture(), Options{})

	for _, c := range categories {
		if !strings.Contains(out, `<symbol id="icon-`+c+`"`) {
			t.Errorf("no built-in glyph for %q, so those boxes draw a blank", c)
		}
	}
}

func writeIcon(t *testing.T, dir, name, body string) {
	t.Helper()

	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">` + body + `</svg>`
	if err := os.WriteFile(filepath.Join(dir, name+".svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A licensed set is per-service, so a file named after the resource type has
// to win over the category — otherwise pointing at a real icon set would flatten
// it back down to six pictures.
func TestIconDirPrefersTheResourceType(t *testing.T) {
	dir := t.TempDir()
	writeIcon(t, dir, "aws_ecs_service", `<circle cx="12" cy="12" r="9" id="from-the-set"/>`)

	out := render(t, fixture(), Options{IconDir: dir})

	if !strings.Contains(out, `<symbol id="icon-aws_ecs_service" viewBox="0 0 24 24">`) {
		t.Error("the per-type icon was not used, or its viewBox was lost")
	}
	if !strings.Contains(out, "from-the-set") {
		t.Error("the icon's contents did not make it into the page")
	}
}

// A set that does not cover somebody's estate should degrade, not fail: the
// categories it omits keep their built-in glyphs.
func TestIconDirFallsBackToTheBuiltInGlyphs(t *testing.T) {
	dir := t.TempDir()
	writeIcon(t, dir, "compute", `<rect width="24" height="24" id="theirs"/>`)

	out := render(t, fixture(), Options{IconDir: dir})

	if !strings.Contains(out, "theirs") {
		t.Error("the supplied category icon was not used")
	}
	for _, c := range []string{"database", "network", "security", "storage", "generic"} {
		if !strings.Contains(out, `<symbol id="icon-`+c+`"`) {
			t.Errorf("category %q lost its glyph instead of keeping the built-in one", c)
		}
	}
}

// Silence would be the wrong answer here: somebody who passes --icon-dir and
// gets the built-in glyphs anyway would have no idea why.
func TestAnEmptyIconDirIsAnError(t *testing.T) {
	_, err := Render(fixture(), Options{IconDir: t.TempDir()})
	if err == nil {
		t.Fatal("a directory with no usable icons was accepted")
	}
	if !strings.Contains(err.Error(), ".svg") {
		t.Errorf("the error does not say what was expected: %v", err)
	}
}

func TestAMissingIconDirIsAnError(t *testing.T) {
	if _, err := Render(fixture(), Options{IconDir: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Error("a directory that does not exist was accepted")
	}
}

// A resource type is data out of an input file. One containing a path
// separator must not be able to read outside the directory it was pointed at.
func TestIconLookupCannotEscapeTheDirectory(t *testing.T) {
	outer := t.TempDir()
	writeIcon(t, outer, "secret", `<rect id="should-not-appear"/>`)

	dir := filepath.Join(outer, "icons")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIcon(t, dir, "compute", `<rect id="fine"/>`)

	g := fixture()
	g.Nodes[0].Type = "../secret"
	g.Normalize()

	out := render(t, g, Options{IconDir: dir})
	if strings.Contains(out, "should-not-appear") {
		t.Error("a resource type reached a file outside the icon directory")
	}
}

func TestIconDirRejectsActiveSVGContent(t *testing.T) {
	dir := t.TempDir()
	writeIcon(t, dir, "compute", `<path d="M0 0"/><script>alert(1)</script>`)
	if _, err := Render(fixture(), Options{IconDir: dir}); err == nil {
		t.Fatal("an SVG containing script content was accepted")
	}
}
