package html

import (
	"strings"
	"testing"
)

// A diagram is a thing people talk about together, and the sentence that goes
// with it is "look at this one". The address bar could say which drawing and
// which page of it; everything after that was "scroll down, it is the box on
// the left".
func TestTheAddressBarSaysWhatIsSelected(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"function rememberSelection() {",
		"url.hash = at ? encodeURIComponent(at) : '';",
		// Replacing rather than pushing: picking a box is not somewhere you
		// navigated to, and Back should not walk your last six clicks.
		"history.replaceState(history.state, '', url.href);",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a link cannot name what is selected", want)
		}
	}
}

// And a link that names one arrives pointing at it — after a render, because
// the cell has to exist before the view can be moved to it.
func TestALinkPointsAtWhatItNames(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	for _, want := range []string{
		"function pointAt(fragment) {",
		"render().then(() => pointAt(location.hash))",
		"window.addEventListener('hashchange', () => pointAt(location.hash));",
		"board.scrollCellToVisible(cell, true)",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a shared link opens the page and points at nothing", want)
		}
	}

	// A link to something this page does not draw says so, rather than opening
	// the right page and quietly pointing at nothing.
	if !strings.Contains(app, "この図には、リンクが指しているものがありません") {
		t.Error("a link that names something absent fails silently")
	}
}

// The address bar is not where people look for a way to share, and a fragment
// somebody has to notice is a feature nobody uses.
func TestThereIsAControlThatHandsYouTheLink(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "function linkControl() {") || !strings.Contains(app, "navigator.clipboard.writeText(location.href)") {
		t.Error("nothing in the panel hands the reader the link")
	}
	// Every kind of thing a reader can point at.
	if strings.Count(app, "detail.append(linkControl());") < 3 {
		t.Error("only some of what can be selected can be linked to")
	}
}

// The element in the fragment belonged to the page being left. Carrying it to
// the next one hands somebody a link that points at nothing.
func TestTurningThePageDropsTheElement(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "url.hash = '';\n      history.pushState({diagram: id}, '', url);") {
		t.Error("opening another page keeps the old page's element in the link")
	}
}
