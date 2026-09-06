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
//
// Where it goes cannot be counted from the top: the header is one line or two
// depending on whether the name and the type would say the same thing. And a
// text's y is its baseline, so a rule measured against one crosses it — the
// only place it does not is the gap under the last header line.
func TestAClassBoxHasACompartmentRule(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	for _, want := range []string{
		"const members = Math.min(Number(st.members) || 0, lines.length);",
		"const header = lines.length - members;",
		"if (members > 0 && header > 0) {",
		"at(x, top + LINE * header + 2)",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so the rule is not in the gap under the header", want)
		}
	}
	// The same count decides which lines are dimmed, so a one-line header does
	// not dim its own first member.
	if !strings.Contains(app, "opacity: i >= header ? 0.85 : 1,") {
		t.Error("the members are dimmed by an absolute line number rather than by where they start")
	}
}

// The glyph and the chevron belong to the thing, not to its members. Centred
// on the whole box they float in the middle of the list.
func TestTheGlyphAndTheDoorStayWithTheName(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "const headMiddle = members ? top + (header * LINE) / 2 : y + h / 2;") {
		t.Fatal("nothing measures the first compartment")
	}
	if !strings.Contains(app, "at(x + PAD_X, headMiddle - ICON / 2)") {
		t.Error("the glyph is still centred on the whole box")
	}
	if !strings.Contains(app, "at(x + w - PAD_X, headMiddle)") {
		t.Error("the chevron is still centred on the whole box")
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
