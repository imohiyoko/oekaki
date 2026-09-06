package html

import (
	"strings"
	"testing"
)

// A class box lists what the type declares, which is UML's own answer and the
// right one: a class with nine methods drawn as nine boxes is a picture of
// nine things, when it is a picture of one thing with nine methods.
func TestAClassBoxListsWhatItDeclares(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"function declaredBy(n) {",
		"const members = n.attrs && n.attrs.declares;",
		"return (first ? [first, second] : [second]).concat(declaredBy(n));",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a type's members are carried and drawn nowhere", want)
		}
	}
	// A type with forty methods is a box the length of the page, so the box
	// says what it left out rather than growing without end.
	if !strings.Contains(app, "const MEMBERS = 8;") || !strings.Contains(app, "他 ${members.length - MEMBERS} 件") {
		t.Error("a long member list has no bound, or does not say it was cut")
	}
}

// The line between the name and the members is what makes the box read as a
// class rather than as a name that ran on.
func TestAClassBoxHasACompartmentRule(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "if (lines.length > 2) {") {
		t.Error("nothing separates the members from the name")
	}
}

// An interface and a struct are one word apart in the document, and a class
// diagram that draws them the same way has lost what it was drawn for. The
// word is read only for a type: `kind` means something else on a Kubernetes
// object, whose box already says it.
func TestAClassBoxSaysWhichKindOfTypeItIs(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "if (n.type === 'code_type' && n.attrs && typeof n.attrs.kind === 'string' && n.attrs.kind) {") {
		t.Error("the box does not say whether it is a struct or an interface, or says it for everything")
	}
}
