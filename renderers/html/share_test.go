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
		"drawing.then(() => pointAt(location.hash))",
		"window.addEventListener('hashchange', () => {",
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

// Every state the selection can be in writes the link, not only a box being
// picked. A reader who selected a container and copied the address bar would
// otherwise hand somebody the box they had clicked before it.
func TestEveryKindOfSelectionWritesTheLink(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if strings.Count(app, "rememberSelection();") < 6 {
		t.Error("some way of changing the selection does not write the link")
	}
	// Including letting go of one: a link that still names it would reopen a
	// panel on a page nobody left open.
	if !strings.Contains(app, "// Clicking the canvas is how a reader puts something down.") {
		t.Error("clearing the selection leaves the link naming what was cleared")
	}
}

// An edge names itself "edge:..." already. Saying it twice is a second
// spelling of the same thing, and changing one of the two later breaks every
// link anybody kept.
func TestAnEdgeIsNamedOnce(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if strings.Contains(app, "return 'edge:' + selectedEdge;") {
		t.Error("an edge fragment says edge: twice")
	}
}

// A page has carried fragments since before there were links to a box. An
// anchor somebody kept is not a broken link to complain about.
func TestAFragmentThatIsNotOursIsLeftAlone(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "const mark = at.indexOf(':');") || !strings.Contains(app, "if (mark < 0) return;") {
		t.Error("a fragment with no kind in it is reported as a broken link")
	}
}

// Resolved against what is drawn, not against what the document has. A box
// inside a fold is in the graph and not on the canvas, and selecting it would
// point at something invisible and scroll nowhere.
func TestALinkResolvesAgainstWhatIsDrawn(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	for _, want := range []string{
		"case 'node': return cells.has(id) && (select(id), true);",
		"case 'edge': return edgeCells.has(at) && (selectEdge(at), true);",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the viewer has no %q, so a link to something folded away does nothing quietly", want)
		}
	}
}

// A fragment is text somebody sent, and a truncated escape is text.
// decodeURIComponent throws on it, and the throw used to travel into the catch
// that is there for a layout failure — so a link that could not even be read
// was the one kind that said nothing at all.
func TestALinkThatCannotBeReadIsStillAnswered(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "at = decodeURIComponent((fragment || '').replace(/^#/, ''));\n    } catch {") {
		t.Error("a malformed fragment throws instead of being answered")
	}
}
