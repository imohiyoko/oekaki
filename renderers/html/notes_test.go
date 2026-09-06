package html

import (
	"strings"
	"testing"
)

// What somebody wrote about a thing belongs next to everything else known
// about it, and every kind of thing that can be selected can be written about
// — a node, a group, and a line.
func TestWhatSomebodyWroteIsShownBesideTheThing(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"function notesControl(subject) {",
		"function noteBody(text) {",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so notes are carried in the document and shown nowhere", want)
		}
	}
	if n := strings.Count(app, "notesControl("); n < 4 {
		t.Errorf("notesControl appears %d times: some of what can be selected cannot be written about", n)
	}
}

// The pen is the way in. It sits in the panel rather than on the box, because
// a box is a few pixels tall on a drawing scaled to fit, and because a note is
// read next to the rest of what is known.
func TestThereIsAPenToWriteWith(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"function pen(subject) {",
		"button.className = 'pen';",
		"function noteForm(subject) {",
		"s.firstChild.append(pen(subject));",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a note can be read but not written", want)
		}
	}
	if !strings.Contains(string(Assets(nil)[AssetCSS]), ".pen") {
		t.Error("the pen has no style, so it does not look like a control")
	}
}

// Writing is asserting. A note typed here is a pending assertion like every
// other edit, so it leaves as an overlay and is never written into the graph
// the page was given.
func TestWritingANoteProducesAnAssertion(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"assertion: {assert: 'note', subject: subjectOf(subject), text}",
		"function subjectOf(subject) {",
		"return {edge: claimedKey(subject)};",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so what is written cannot leave the page", want)
		}
	}
	// Reading is the ordinary mode, and a note is written while reading, so
	// the way out has to be offered in it too.
	if !strings.Contains(app, "document.getElementById('export').hidden = !claims;") {
		t.Error("a note written while reading has no way out of the page")
	}
}

// A note is somebody else's text arriving in a document that gets drawn. The
// viewer builds elements and sets textContent; nothing in this path assembles
// markup, and no link is honoured — a destination in a note is a place this
// page would send a reader on somebody's say-so.
func TestANoteIsBuiltAsElementsAndNeverAsMarkup(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"parent.append(document.createTextNode(text.slice(at, match.index)));",
		"el.textContent = found.startsWith('**') ? found.slice(2, -2) : found.slice(1, -1);",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a note is not built out of text nodes", want)
		}
	}
	for _, forbidden := range []string{"innerHTML", "insertAdjacentHTML", "document.write("} {
		if strings.Contains(app, forbidden) {
			t.Errorf("the viewer uses %q somewhere, and a note is text somebody else wrote", forbidden)
		}
	}
	// What a note can turn into is decided in one place, and a link is not
	// among the three things it names.
	if !strings.Contains(app, "found.startsWith('**') ? 'strong' : found.startsWith('`') ? 'code' : 'em');") {
		t.Error("what a note can become is no longer decided in one place, so the list of it is no longer short")
	}
	if !strings.Contains(app, "const pattern = /(\\*\\*[^*]+\\*\\*|\\*[^*]+\\*|`[^`]+`)/g;") {
		t.Error("the inline pass understands something beyond emphasis and code; a link there is a destination chosen by whoever wrote the note")
	}
}
