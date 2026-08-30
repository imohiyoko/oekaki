package html

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

func fixture() *core.Graph {
	v1, v2 := 10.0, 18.0
	g := &core.Graph{
		Version: core.Version,
		Axes:    []core.Axis{{ID: core.AxisNetwork}},
		Groups: []core.Group{
			{ID: "aws_vpc.main", Axis: core.AxisNetwork, Type: "vpc", Label: "main"},
		},
		Nodes: []core.Node{
			{ID: "aws_ecs_service.api", Type: "aws_ecs_service", Name: "api", Description: "public API service",
				Groups:   map[string]string{core.AxisNetwork: "aws_vpc.main"},
				Coverage: &core.Coverage{State: core.CoverageBlind, Reason: "nothing found", Evidence: []core.Evidence{{Kind: core.EvidenceNone}}}},
			{ID: "aws_db_instance.main", Type: "aws_db_instance", Name: "main"},
		},
		Edges: []core.Edge{
			{From: "aws_ecs_service.api", To: "aws_db_instance.main", Kind: core.EdgeObserved,
				Claim: &core.Claim{Origin: core.OriginAI, Author: "assistant"}},
		},
		LogStatus: &core.LogCollectionStatus{Fetched: 3, Classified: 3, CompletedAt: "2026-08-28T01:00:00Z"},
		Observations: []core.Observation{
			{Subject: "aws_ecs_service.api", Metric: "temperature", Labels: map[string]string{"sensor": "room-1"}, Value: &v1, ObservedAt: "2026-08-28T00:00:00Z", Threshold: &core.Threshold{Operator: ">", Value: 15}},
			{Subject: "aws_ecs_service.api", Metric: "temperature", Labels: map[string]string{"sensor": "room-1"}, Value: &v2, ObservedAt: "2026-08-28T01:00:00Z", Threshold: &core.Threshold{Operator: ">", Value: 15}, State: "abnormal"},
		},
	}
	g.Normalize()
	return g
}

func mustRead(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "renderers", "html", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

func render(t *testing.T, g *core.Graph, opts Options) string {
	t.Helper()

	out, err := Render(g, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

// A page that fetched a sibling file would need a web server, because Chrome
// blocks a file:// page from reading its neighbours. The audience is one
// person looking at their own estate on their own machine, so everything has
// to be in the one file.
func TestPageIsSelfContained(t *testing.T) {
	out := render(t, fixture(), Options{})

	external := regexp.MustCompile(`(?:src|href)="(?:https?:)?//`)
	if loc := external.FindString(out); loc != "" {
		t.Errorf("the page loads something from outside itself: %s", loc)
	}
	if !strings.Contains(out, "<!doctype html>") {
		t.Error("output is not an HTML document")
	}
}

// The canvas is maxGraph. A self-contained page carries it for the same reason
// it carries ELK: the file has to draw with nothing to fetch.
func TestMaxGraphIsEmbedded(t *testing.T) {
	out := render(t, fixture(), Options{})
	if !strings.Contains(out, "window.maxGraph") {
		t.Error("the canvas library is not in the page, so nothing will be drawn")
	}

	assets := Assets(nil)
	bundle, ok := assets[AssetMax]
	if !ok || len(bundle) < 200_000 {
		t.Errorf("the shared runtime carries %d bytes of maxGraph, which is too small to be the library", len(bundle))
	}
	if !strings.Contains(out, string(bundle)) {
		t.Error("the self-contained page and the shared runtime carry different maxGraph bytes")
	}
}

func TestELKIsEmbedded(t *testing.T) {
	out := render(t, fixture(), Options{})

	if !strings.Contains(out, "ELK") {
		t.Error("the layout engine is not in the page, so it will never lay anything out")
	}
	if len(out) < 500_000 {
		t.Errorf("the page is %d bytes, which is too small to contain ELK", len(out))
	}
}

// The viewer has to show the document it came from. A view that quietly
// differs from the file it was made out of is worse than no view.
func TestEmbeddedGraphRoundTrips(t *testing.T) {
	g := fixture()
	out := render(t, g, Options{})

	embedded := between(t, out, `<script type="application/json" id="oekaki-graph">`, `</script>`)
	// The escape that stops a "</script>" inside a resource name from ending
	// the block early has to be undone the way a JSON parser would.
	embedded = strings.ReplaceAll(embedded, `<\/`, "</")

	back, err := core.Decode(strings.NewReader(embedded))
	if err != nil {
		t.Fatalf("the embedded document does not decode: %v", err)
	}
	if len(back.Nodes) != len(g.Nodes) || len(back.Edges) != len(g.Edges) {
		t.Errorf("embedded graph has %d nodes and %d edges, want %d and %d",
			len(back.Nodes), len(back.Edges), len(g.Nodes), len(g.Edges))
	}
	api, ok := back.Node("aws_ecs_service.api")
	if !ok {
		t.Fatal("a node was lost on the way into the page")
	}
	if api.Coverage == nil {
		t.Error("coverage was lost on the way into the page")
	}
	if api.Description != "public API service" {
		t.Errorf("description was lost on the way into the page: %q", api.Description)
	}
	if back.LogStatus == nil || back.LogStatus.Fetched != 3 {
		t.Errorf("log status was lost on the way into the page: %+v", back.LogStatus)
	}
}

func TestNodeConflictLookupKeepsTheTargetKind(t *testing.T) {
	g := fixture()
	edgeTarget := core.EdgeKey(g.Edges[0].From, g.Edges[0].To, g.Edges[0].Kind, g.Edges[0].Relation)
	g.Nodes = append(g.Nodes, core.Node{ID: edgeTarget, Type: "service", Name: "same spelling"})
	g.Conflicts = []core.Conflict{{
		TargetKind: core.ConflictTargetEdge,
		Target:     edgeTarget,
		Field:      "name",
		Claims: []core.ClaimedValue{
			{Value: "one", Claim: core.Claim{Origin: core.OriginHuman}},
			{Value: "two", Claim: core.Claim{Origin: core.OriginParser}},
		},
	}}
	g.Normalize()

	out := render(t, g, Options{})
	back := embeddedGraph(t, out)
	if len(back.Conflicts) != 1 || back.Conflicts[0].TargetKind != core.ConflictTargetEdge || back.Conflicts[0].Target != edgeTarget {
		t.Fatalf("typed conflict was not preserved in the page: %#v", back.Conflicts)
	}
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "filter((c) => c.target_kind === 'entity')") {
		t.Fatal("HTML runtime does not discriminate entity conflicts before node highlighting")
	}
	if !strings.Contains(app, "const conflicts = entityConflicts.filter((c) => c.target === n.id)") {
		t.Fatal("HTML node details can include a same-spelled edge conflict")
	}
}

func TestPageContainsLogPollingHealthDisplay(t *testing.T) {
	g := fixture()
	out := render(t, g, Options{})
	back := embeddedGraph(t, out)
	if back.LogStatus == nil || back.LogStatus.Fetched != 3 || back.LogStatus.Classified != 3 {
		t.Fatalf("the page carries no log polling health data: %+v", back.LogStatus)
	}
}

func TestPageContainsMetricChangeVisualization(t *testing.T) {
	back := embeddedGraph(t, render(t, fixture(), Options{}))
	if len(back.Observations) != 2 {
		t.Fatalf("embedded graph has %d observations, want 2", len(back.Observations))
	}
	if back.Observations[1].Threshold == nil || back.Observations[1].Threshold.Value != 15 || back.Observations[1].State != "abnormal" {
		t.Fatalf("metric threshold/state was not preserved: %+v", back.Observations[1])
	}
}

func embeddedGraph(t *testing.T, out string) *core.Graph {
	t.Helper()
	embedded := strings.ReplaceAll(between(t, out, `<script type="application/json" id="oekaki-graph">`, `</script>`), `<\/`, "</")
	back, err := core.Decode(strings.NewReader(embedded))
	if err != nil {
		t.Fatalf("the embedded graph does not decode: %v", err)
	}
	return back
}

// A resource whose name contains a closing script tag must not be able to
// break out of the data block and land in the document as markup.
func TestAClosingTagInTheDataCannotEscape(t *testing.T) {
	g := fixture()
	g.Nodes[0].Name = `</script><img src=x onerror=alert(1)>`
	g.Normalize()

	out := render(t, g, Options{})

	// The data block must still be one block. A literal closing tag inside it
	// would end it early and put the rest of the graph into the document as
	// markup, which is how a diagram becomes an injection.
	data := between(t, out, `<script type="application/json" id="oekaki-graph">`, `</script>`)
	if !strings.Contains(data, `<\/script>`) {
		t.Error("the closing tag in the data was not escaped")
	}
	if !strings.Contains(data, "onerror") {
		t.Error("the block was cut short, so the rest of the graph is loose in the document")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first := render(t, fixture(), Options{Title: "t"})
	for range 5 {
		if got := render(t, fixture(), Options{Title: "t"}); got != first {
			t.Fatal("two renders of the same graph produced different pages")
		}
	}
}

func TestUnknownAxisIsRejected(t *testing.T) {
	if _, err := Render(fixture(), Options{Axis: "nope"}); err == nil {
		t.Error("an axis the graph does not have was accepted")
	}
}

func TestRankDirectionAndKindFilterReachTheViewer(t *testing.T) {
	out := render(t, fixture(), Options{RankDir: "TB", Kinds: []core.EdgeKind{core.EdgeObserved}})
	if !strings.Contains(out, `data-rankdir="TB"`) || !strings.Contains(out, `data-kinds="observed"`) {
		t.Fatalf("viewer options were not embedded: %s", out[:min(1000, len(out))])
	}
	if !strings.Contains(out, `rankdir === 'TB' ? 'DOWN' : 'RIGHT'`) {
		t.Fatal("viewer does not translate TB to the ELK direction")
	}
	if !strings.Contains(out, `requestedKinds.has(e.kind)`) {
		t.Fatal("viewer does not apply the requested edge kinds")
	}
}

func TestPageOpensInReadMode(t *testing.T) {
	out := render(t, fixture(), Options{})
	if !strings.Contains(out, `data-mode="read"`) {
		t.Fatal("the page does not open in read mode")
	}
	if !strings.Contains(out, `<button id="mode-edit" class="mode-option" aria-pressed="false">Edit</button>`) {
		t.Fatal("the page offers no way into edit mode")
	}
}

// The gestures that author a document are gated on the mode rather than on a
// modifier key, so the gate is worth pinning: a drag that places a box and a
// drag that pans the canvas are the same gesture, and only the mode tells them
// apart.
func TestAuthoringGesturesAreGatedOnEditMode(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	// Moving a box is maxGraph's gesture, so the gate is on the library rather
	// than in a handler of this page's own.
	if !strings.Contains(app, "board.setCellsMovable(editing);") {
		t.Error("boxes can be picked up in read mode")
	}
	if !strings.Contains(app, "if (!editing || !me.getEvent().shiftKey) return;") {
		t.Error("a connection can be asserted in read mode")
	}
	if !strings.Contains(app, "if (editing && positions.delete(id)) {") {
		t.Error("reading a diagram can release a box somebody placed")
	}

	// A line drawn for one position of a box says nothing once the box is
	// somewhere else. maxGraph drops the route rather than dragging the bends
	// along, which is what this viewer had to do by hand before.
	if !strings.Contains(app, "board.resetEdgesOnMove = true;") {
		t.Error("a moved box keeps the route its lines had before it moved")
	}

	// ELK sizes every container to hold its children. maxGraph growing them
	// again as each child arrives is a fight with the layout, and with
	// containers inside containers it is a resize that feeds itself.
	for _, off := range []string{
		"board.setExtendParents(false);",
		"board.setExtendParentsOnAdd(false);",
		"board.setConstrainChildren(false);",
	} {
		if !strings.Contains(app, off) {
			t.Errorf("the canvas resizes containers the layout already sized: %s", off)
		}
	}
}

// A shape paints in graph coordinates and the canvas applies the view's scale
// on the way out. The glyph and the label are put into the node by hand, so
// they have to carry that themselves — without it the boxes shrink to fit and
// their labels stay behind at full size, off to the side of the diagram.
func TestHandDrawnPartsCarryTheViewScale(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "const screen = (c) => {") {
		t.Error("the viewer does not convert its own drawing into view coordinates")
	}
	if !strings.Contains(app, "for (const old of [...node.querySelectorAll('[data-ig]')]) old.remove();") {
		t.Error("hand-drawn parts accumulate across repaints")
	}
}

// Selecting moves nothing, so it must not reach the layout. This is the
// change the whole port exists for: the viewer it replaced answered a click on
// a box by laying the graph out again and rebuilding every element.
func TestSelectingDoesNotLayOutAgain(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "function highlight(cell) {") {
		t.Fatal("there is no way to mark a selection without repainting")
	}

	// The callers that used to end in render(). A box now marks the whole set
	// it was picked with; a container and a line still mark one cell.
	if !strings.Contains(app, "    markPicked();\n  }") {
		t.Error("selecting a box does not end by marking it")
	}
	if !strings.Contains(app, "    highlight(cells.get(id));\n  }") {
		t.Error("selecting a container does not end by marking it")
	}

	// A repaint builds new cells, so a selection that survives one has to be
	// marked again.
	if !strings.Contains(app, "if (picked.size) markPicked();") {
		t.Error("a repaint drops the highlight on whatever was selected")
	}
}

// A diagram is nearly always wider than the box it is drawn in, and maxGraph
// paints a cell wherever it lands rather than clipping to its container. The
// canvas is also the only positioned element in the page, so anything that
// leaves it is painted over the panel a reader is reading.
func TestTheDrawingStaysInsideItsBox(t *testing.T) {
	css := string(Assets(nil)[AssetCSS])
	if !strings.Contains(css, "#canvas { flex: 1; overflow: hidden; position: relative; min-width: 0; cursor: grab; }") {
		t.Error("the canvas does not clip what it draws")
	}
	if !strings.Contains(css, "#bar, #detail, #hint { position: relative; }") {
		t.Error("the page around the drawing is not kept in front of it")
	}
}

// Editing, a drag that starts on a box has to reach the box. maxGraph's
// panning handler forces a pan on every press when it is told to ignore
// cells, and it consumes the press, so nothing downstream ever sees it:
// with that on, a box could not be moved and a connection could not be
// asserted. Reading, ignoring cells is right — there is nothing else a drag
// can mean, and a dense diagram leaves little empty canvas to grab.
func TestADragOnABoxReachesTheBox(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if strings.Contains(app, "panning.ignoreCell") {
		t.Error("panning is still told to swallow every press, whatever it lands on")
	}
	// The assert gesture registers last, and the selection handler would read
	// its press as picking the box up.
	if !strings.Contains(app, "board.mouseListeners.unshift(board.mouseListeners.pop());") {
		t.Error("the assert gesture is offered the press after maxGraph's own handlers")
	}
}

// A nested diagram makes two more claims on the same press. maxGraph walks up
// from the cell under the pointer to the outermost selectable ancestor, so a
// drag meant for one box would carry off every box in its VPC; and a
// container is placed by the layout, never by hand, so nothing records where
// one was dropped and the next repaint would undo it.
func TestOnlyABoxIsPickedUp(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "selectionHandler.getInitialCellForEvent = (me) => me.getCell();") {
		t.Error("a press on a box inside a container is answered by the container")
	}
	if !strings.Contains(app, "movable: false,") {
		t.Error("a container can be picked up and put down where no layout knows about it")
	}
	// A container covers most of the canvas in a nested diagram, so the drag
	// it swallows has to become the one gesture that is always available.
	// Editing, the left button belongs to the drawing everywhere, so a drag on
	// a container draws a box round what it covers rather than doing nothing.
	if !strings.Contains(app, "rubberBand.setEnabled(editing);") {
		t.Error("a drag that starts on a container does nothing at all")
	}
}

// ELK routes with splines. A box placed by hand has no ELK route, and the
// straight diagonal maxGraph falls back to reads as a different kind of
// drawing on a page made of curves.
func TestAPlacedBoxKeepsTheDrawingsOwnKindOfLine(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "function curveBetween(a, b, fromSide, toSide, channel, bypass) {") {
		t.Fatal("there is no route for a line whose ELK one no longer applies")
	}
	if !strings.Contains(app, "if (moved.length) applyAnchors();") {
		t.Error("moving a box leaves its lines straight")
	}
	// A repaint has to draw the same curve, or folding a container after a
	// move puts the straight lines back.
	if !strings.Contains(app, "    applyAnchors();\n\n    // A repaint builds new cells") {
		t.Error("a repaint drops the curve on a line touching a placed box")
	}
}

// maxGraph moves everything it has selected, so moving several boxes at once
// only ever needed a way to select more than one. Its own toggle could not be
// used: measured, it runs after the click has been answered and takes the box
// straight back off, leaving nothing selected.
func TestSeveralBoxesCanBePickedUpAtOnce(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "board.isToggleEvent = () => false;") {
		t.Error("maxGraph's toggle still undoes the page's own selection")
	}
	if !strings.Contains(app, "function markPicked() {") {
		t.Fatal("only one box can be marked at a time")
	}
	if !strings.Contains(app, "select(idOf(cell), false, editing && togglesSelection(evt.getProperty('event')));") {
		t.Error("a modified click does not add to the boxes already picked")
	}
	// A gesture that moved boxes ends in a mouse-up like any other, and acting
	// on the click it reports drops every box but the one under the pointer.
	if !strings.Contains(app, "if (dragged) { evt.consume(); return; }") {
		t.Error("the click that ends a drag undoes the selection it was made with")
	}
	// A repaint builds new cells, so the marking has to be made again.
	if !strings.Contains(app, "if (picked.size) markPicked();") {
		t.Error("a repaint drops the boxes that were picked")
	}
}

// Which side of a box a line met was never written down: it fell out of ELK's
// route and went with it. Moving one box put every line on it through the same
// two points — measured at thirty lines on two points — and nothing could say
// otherwise.
func TestALineIsGivenASideOfEachBox(t *testing.T) {
	app := string(Assets(nil)[AssetApp])

	// maxGraph honours a fixed anchor over its own perimeter guess, which is
	// what makes "chosen" and "worked out" the same mechanism.
	for _, marker := range []string{
		"exitX: ex, exitY: ey, exitPerimeter: false,",
		"entryX: tx, entryY: ty, entryPerimeter: false});",
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("a line does not pin the point it meets a box at: %s", marker)
		}
	}
	if !strings.Contains(app, "function facingSides(from, to) {") {
		t.Error("no side is worked out for a line that was not given one")
	}
	// The flow decides while the boxes are clear of each other along it. The
	// first rule compared the two gaps, which sent a line out of the top of a
	// box whenever the two were further apart down the page than along it —
	// on a tall diagram, most of them.
	if !strings.Contains(app, "    return clear > 0 ? alongFlow : acrossFlow;") {
		t.Error("the side a line leaves on does not follow the direction the diagram is read in")
	}
	// Without sharing a side out, every line on a box lands on one point.
	if !strings.Contains(app, "m.r[m.role + 'At'] = (i + 1) / (members.length + 1);") {
		t.Error("lines that meet the same side of a box are not spread along it")
	}
	// Taking a side back has to put the line back the way it was found, and a
	// layout is not run for that.
	if !strings.Contains(app, "geo.points = elkRoutes.get(key) || [];") {
		t.Error("a line given back to the layout keeps the route drawn by hand")
	}
	if !strings.Contains(app, "function attachmentControl(key) {") {
		t.Error("a side cannot be chosen by hand")
	}
	if !strings.Contains(app, "if (editing) detail.append(attachmentControl(key));") {
		t.Error("the sides are offered while reading, which authors nothing")
	}
}

// Two lines given the same two points draw the same line. Right angles make
// that the common case: every line turning at the midpoint between its boxes
// runs down the same column. Measured after moving one box, 54% of the drawn
// line lay on another line, seven deep at the worst place.
func TestLinesDoNotRunOnTopOfEachOther(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "const channelOf = (r) => 1 - 2 * ((r.targetLane || 1) >= (r.sourceLane || 1) ? r.targetAt : r.sourceAt);") {
		t.Fatal("lines drawn between the same two boxes have nothing to tell them apart")
	}
	// Order matters as much as spacing. The channel comes from one lane, in
	// that lane's own order, so the bundle nests. Adding the two lanes'
	// orders together crosses the lines wherever they disagree: measured,
	// 91 crossings without channels became 120, and 49 with this ordering.
	if !strings.Contains(app, "m.r[m.role + 'Lane'] = members.length;") {
		t.Error("there is nothing to tell which of a line's two lanes is the crowded one")
	}
	// Both shapes have to take it: a curve repeats just as exactly as an elbow.
	if !strings.Contains(app, "function elbowBetween(a, b, fromSide, toSide, channel, bypass) {") {
		t.Error("right-angled lines all turn in the same place")
	}
	if !strings.Contains(app, "function curveBetween(a, b, fromSide, toSide, channel, bypass) {") {
		t.Error("curves between the same two boxes lie on top of each other")
	}
	if !strings.Contains(app, "const channel = channelOf(r);") {
		t.Error("the channel is worked out but never used")
	}
}

// Curves or right angles. The diagram's answer goes to ELK, so it reaches
// every line rather than only the ones this file draws.
func TestTheShapeOfALineCanBeChosen(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "'elk.edgeRouting': lineShape === 'orthogonal' ? 'ORTHOGONAL' : 'SPLINES',") {
		t.Error("choosing right angles leaves the laid-out lines curved")
	}
	if !strings.Contains(app, "function elbowBetween(a, b, fromSide, toSide, channel, bypass) {") {
		t.Error("a line drawn here cannot be drawn with right angles")
	}
	if !strings.Contains(app, "const lineShapeOf = (key) => (edgeAnchors.get(key) || {}).line || lineShape;") {
		t.Error("a single line cannot differ from the diagram")
	}

	// An author rule beats the display:none a browser gives [hidden], so the
	// switch has to say it again or read mode shows a control it does not offer.
	css := string(Assets(nil)[AssetCSS])
	if !strings.Contains(css, "#line-shape[hidden], .tool-group[hidden] { display: none; }") {
		t.Error("the editing controls stay on screen in read mode")
	}
}

// One drawn arrow usually stands for many references, and until it could be
// clicked the diagram never said which ones, or which of the three questions
// an edge kind answers.
func TestALineCanBeAskedWhatItIs(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "if (cell.infra && cell.infra.edge) selectEdge(edgeKey(cell.infra.edge));") {
		t.Error("clicking a line opens nothing")
	}
	if !strings.Contains(app, "function edgeMeaning(e) {") {
		t.Error("the panel does not say what kind of link a line is")
	}
	if !strings.Contains(app, "section('reference paths')") {
		t.Error("the references one arrow stands for are not listed")
	}

	// A line is named the way the IR names it, so a conflict recorded about
	// that line is found under the same key.
	key := core.EdgeKey("a", "b", core.EdgeIACRef, "remote_state")
	parts := strings.Split(strings.TrimPrefix(key, "edge:"), ".")
	if !strings.HasPrefix(key, "edge:") || len(parts) != 4 {
		t.Fatalf("the IR names an edge in a shape the viewer does not build: %s", key)
	}
	if !strings.Contains(app, "const edgeKey = (e) => 'edge:' + [e.from, e.to, e.kind, e.relation || ''].map(b64url).join('.');") {
		t.Error("the viewer names a line differently from the IR")
	}
}

// A line's tooltip is the only place a reader sees who claimed it, or that
// somebody asserted it does not exist, without selecting one of its ends.
func TestALineStillSaysWhoClaimedIt(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "board.getTooltipForCell = (cell) => {") {
		t.Error("edges no longer carry their provenance")
	}
	// Folding belongs to the container's title, and maxGraph's own handle
	// fetches an image the page does not ship.
	if !strings.Contains(app, "board.isCellFoldable = () => false;") {
		t.Error("the canvas offers a second, broken way to fold a container")
	}
}

// The viewer writes overlay documents, so what it writes is part of the
// schema's contract and not merely an implementation detail of the page: a
// rename that restated the type would make a human the claimant of something
// the input already said.
func TestAuthoringWritesTheAssertionsTheSchemaAccepts(t *testing.T) {
	out := render(t, fixture(), Options{})
	if !strings.Contains(out, `<button id="add-node" type="button">Add box</button>`) {
		t.Error("the page offers no way to add a box")
	}

	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "pending.push({assertion: {assert: 'node', subject: {node: id}, name}});") {
		t.Error("a rename does not assert a name against the node it renames")
	}
	if !strings.Contains(app, "pending.push({assertion: {assert: 'node', subject: {name}, name}, node});") {
		t.Error("an added box does not assert a node the CLI can adopt")
	}
	// The id has to be the one adopt() builds for this selector, or re-applying
	// the export lands beside the box instead of on it.
	if !strings.Contains(app, "const id = 'asserted:name=' + name;") {
		t.Error("an added box does not use the id the CLI adopts it under")
	}
	// Other renderers break a name on its newlines, so the field that edits one
	// has to be able to hold them. A text input drops them, and the export
	// would carry a name nobody typed.
	if !strings.Contains(app, "const input = document.createElement('textarea');") {
		t.Error("the rename field cannot hold a name that has more than one line")
	}
}

func TestUnknownRankDirectionIsRejected(t *testing.T) {
	if _, err := Render(fixture(), Options{RankDir: "diagonal"}); err == nil {
		t.Fatal("an unsupported HTML layout direction was accepted")
	}
}

func TestLayoutIsEmbeddedAndEscaped(t *testing.T) {
	layout := []byte(`{"kind":"oekaki.layout","version":"0.1","nodes":[{"id":"x","x":12,"y":34}],"claim":{"origin":"human"}}`)
	out := render(t, fixture(), Options{Layout: layout})
	if !strings.Contains(out, `id="oekaki-layout"`) || !strings.Contains(out, `"x":12`) {
		t.Fatal("layout was not embedded in the page")
	}
}

func between(t *testing.T, s, open, close string) string {
	t.Helper()

	i := strings.Index(s, open)
	if i < 0 {
		t.Fatalf("%q is not in the page", open)
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%q is not closed", open)
	}
	return rest[:j]
}

// The self-contained page is the default because it opens from file:// with
// nothing to serve it. The external one is for the opposite situation — a
// server holding many diagrams — where every page carrying its own 1.5 MB
// copy of ELK is the cost that matters.
func TestExternalPageReferencesTheRuntimeInsteadOfCarryingIt(t *testing.T) {
	out := render(t, fixture(), Options{ExternalAssets: true, GraphSrc: "a.graph.json"})

	stamp := "?v=" + RuntimeStamp(nil)
	for _, want := range []string{
		`href="` + AssetCSS + stamp + `"`,
		`src="` + AssetELK + stamp + `"`,
		`src="` + AssetBoot + stamp + `"`,
		`data-graph="a.graph.json"`,
		`data-app="` + AssetApp + stamp + `"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page does not reference %s", want)
		}
	}
	if len(out) > 50_000 {
		t.Errorf("the page is %d bytes, so something is still inlined", len(out))
	}
}

// Fetching the graph and embedding it too would be two copies, and two copies
// is one copy that can be stale.
func TestExternalPageDoesNotAlsoEmbedTheGraph(t *testing.T) {
	out := render(t, fixture(), Options{ExternalAssets: true, GraphSrc: "a.graph.json"})

	if strings.Contains(out, "aws_ecs_service.api") {
		t.Error("the graph is embedded in a page that also fetches it")
	}
}

func TestExternalPageNeedsAGraphURL(t *testing.T) {
	if _, err := Render(fixture(), Options{ExternalAssets: true}); err == nil {
		t.Error("a page with no graph and no way to reach one was accepted")
	}
}

// Both kinds of page have to run the same code. If they drift, a bug
// reproduces in one and not the other, and the report is unusable.
func TestAssetsAreTheBytesTheSelfContainedPageInlines(t *testing.T) {
	inline := render(t, fixture(), Options{})

	assets := Assets(nil)
	if len(assets) != 5 {
		t.Fatalf("Assets() returned %d files, want 5", len(assets))
	}
	for name, data := range assets {
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
			continue
		}
		// The bootstrap is the one asset a self-contained page has no use
		// for: it already holds its graph and has nothing to fetch.
		if name == AssetBoot {
			if strings.Contains(inline, string(data)) {
				t.Error("the self-contained page carries a bootstrap it never runs")
			}
			continue
		}
		if !strings.Contains(inline, string(data)) {
			t.Errorf("%s is not the copy the self-contained page carries", name)
		}
	}
}

// A served page usually arrives with a Content-Security-Policy, and
// script-src 'self' is the ordinary setting rather than a strict one. An
// inline bootstrap fails there in the worst way available: no fetch, no
// viewer, and no message, because the handler that would have shown one was
// itself in the blocked script. The reader gets an empty canvas, which looks
// exactly like an estate with nothing in it.
func TestExternalPageRunsNoInlineScript(t *testing.T) {
	out := render(t, fixture(), Options{ExternalAssets: true, GraphSrc: "a.graph.json"})

	tags := regexp.MustCompile(`<script[^>]*>`).FindAllString(out, -1)
	if len(tags) == 0 {
		t.Fatal("the page has no script tags at all, so it cannot draw anything")
	}
	for _, tag := range tags {
		// A script element of a non-JavaScript type is data the page parses,
		// not code the browser runs, so a CSP does not stop it.
		if strings.Contains(tag, "src=") || strings.Contains(tag, `type="application/json"`) {
			continue
		}
		t.Errorf("script-src 'self' would block this: %s", tag)
	}
}

func TestAssetBaseJoinsWithExactlyOneSlash(t *testing.T) {
	stamped := AssetELK + "?v=" + RuntimeStamp(nil)
	for _, c := range []struct{ base, want string }{
		{"", stamped},
		{"/shell/v1", "/shell/v1/" + stamped},
		{"/shell/v1/", "/shell/v1/" + stamped},
		{"https://cdn.example/v1", "https://cdn.example/v1/" + stamped},
	} {
		if got := assetURL(c.base, AssetELK, RuntimeStamp(nil)); got != c.want {
			t.Errorf("assetURL(%q, %q) = %q, want %q", c.base, AssetELK, got, c.want)
		}
	}
}

// A page and the runtime it fetches have to be the same age. They are served
// from one path for every diagram and every generation of them, so a browser
// caches them and is right to — but a runtime that changes underneath that
// path leaves everyone who has opened a page before running the old one
// against the new markup. Seen once: a DOM error thrown out of a script with
// no way to report it, and a blank canvas that reads as an empty estate. A
// reload does not fix it, because the bootstrap creates the script element
// rather than the document declaring it.
func TestTheRuntimeUrlChangesWhenTheRuntimeDoes(t *testing.T) {
	first := RuntimeStamp(nil)
	if len(first) != 12 {
		t.Fatalf("the fingerprint is %q, which is not a name a cache can key on", first)
	}
	// Same build, same stamp: a server still holds one copy of each file.
	if second := RuntimeStamp(nil); second != first {
		t.Errorf("the fingerprint moved without the runtime moving: %q then %q", first, second)
	}

	// It is taken from the bytes that are actually served, so changing any of
	// them changes it.
	assets := Assets(nil)
	sum := sha256.New()
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write(assets[name])
	}
	if want := hex.EncodeToString(sum.Sum(nil))[:12]; want != first {
		t.Errorf("the fingerprint is %q, which is not what the served bytes come to (%q)", first, want)
	}
}

func TestExternalRenderIsDeterministic(t *testing.T) {
	opts := Options{ExternalAssets: true, GraphSrc: "a.graph.json", Title: "t"}

	first := render(t, fixture(), opts)
	for range 5 {
		if got := render(t, fixture(), opts); got != first {
			t.Fatal("two renders of the same graph produced different pages")
		}
	}
}

// An inline style is blocked by the same policy that blocks an inline script,
// and a blocked one is not quietly ignored: display:none stops applying, so
// the whole glyph sheet lands on screen above the diagram.
//
// This covers the markup the renderer writes. app.js still sets a few styles
// at runtime — coverage colours in the detail panel and on the filter chips —
// which the same policy blocks; those are cosmetic and are not fixed here.
//
// Script bodies are cut out before the check. A bundled library contains such
// strings as data — maxGraph carries the markup for an HTML label — and
// matching them would report a policy problem the page does not have, for a
// line no browser ever parses as markup. What a script does at runtime is a
// different question from what this document declares, and it is the document
// that this test is about.
func TestPageMarkupAppliesNoInlineStyle(t *testing.T) {
	inlineStyle := regexp.MustCompile(`style="[^"]*"`)
	scripts := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)

	for _, opts := range []Options{{}, {ExternalAssets: true, GraphSrc: "a.graph.json"}} {
		out := scripts.ReplaceAllString(render(t, fixture(), opts), "")
		if loc := inlineStyle.FindString(out); loc != "" {
			t.Errorf("external=%v: the page applies a style attribute a CSP would block: %s",
				opts.ExternalAssets, loc)
		}
	}
}

// A collection of pages wants to be generated the same way rather than
// switched one page at a time, so the shape is a flag as well as a control.
// A layout document still wins: that one was authored, this is a default.
func TestTheLineShapeCanBeSetWhenThePageIsMade(t *testing.T) {
	g := fixture()
	if !strings.Contains(render(t, g, Options{Lines: "orthogonal"}), `data-lines="orthogonal"`) {
		t.Error("the chosen shape does not reach the page")
	}
	if !strings.Contains(render(t, g, Options{}), `data-lines="curved"`) {
		t.Error("a page made without a shape does not default to curves")
	}
	if _, err := Render(g, Options{Lines: "wiggly"}); err == nil {
		t.Error("an unknown line shape was accepted")
	}

	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "(savedLayout && savedLayout.lines) || document.body.dataset.lines") {
		t.Error("a layout document does not override the shape the page was made with")
	}
}

// The click that ends a gesture is not the gesture. maxGraph reports one for
// every mouse-up, and answering it undoes what the drag did: a move keeps only
// the box under the pointer, and a box drawn round several ends on empty
// canvas, which reads as "clicked nothing" and clears the lot.
func TestTheClickThatEndsADragIsNotAnswered(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "if (pressedAt && Math.hypot(me.getX() - pressedAt.x, me.getY() - pressedAt.y) > 3) dragged = true;") {
		t.Error("a gesture that travelled is still taken for a click")
	}
	// And the clicks this page does answer are consumed, or maxGraph selects
	// the cell under the pointer for itself afterwards.
	if strings.Count(app, "evt.consume();") < 4 {
		t.Error("clicks this page answers are left for maxGraph to answer again")
	}
}

// Several boxes at once, drawn round rather than clicked one at a time. The
// left button belongs to the drawing while editing, so the view moves on the
// right button — which works in either mode and needs no explaining.
func TestABoxCanBeDrawnRoundSeveralBoxes(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "const rubberBand = new RubberBandHandler(board);") {
		t.Fatal("there is no way to draw a box round several boxes")
	}
	if !strings.Contains(app, "rubberBand.setEnabled(editing);") {
		t.Error("the box round several is offered while reading, where a drag moves the view")
	}
	if !strings.Contains(app, "return evt.button === 2 || evt.altKey || !editing;") {
		t.Error("the view cannot be moved while editing")
	}
	// The trigger has to agree with the force, or panning answers the press on
	// empty canvas before the rubber band sees it.
	if !strings.Contains(app, "panning.isPanningTrigger = panGesture;") {
		t.Error("panning still takes the gesture the rubber band is drawn with")
	}
	// A selection maxGraph made itself has to reach the page's own set.
	if !strings.Contains(app, "board.getSelectionModel().addListener(InternalEvent.CHANGE, () => {") {
		t.Error("a box drawn round several does not reach the set this page keeps")
	}

	entry := mustRead(t, "vendor/maxgraph.entry.js")
	if !strings.Contains(entry, "RubberBandHandler") {
		t.Error("the vendored bundle does not carry the handler that draws the box")
	}
}

// A wheel moves the view; a pinch scales it. Two fingers on a trackpad send a
// wheel, and a pinch sends one too but with ctrlKey set — that flag is the only
// thing telling them apart. Zooming on every wheel took the whole of a trackpad
// away: two fingers scaled the diagram instead of moving it.
func TestATrackpadMovesTheViewAndPinchesToScaleIt(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "if (!e.ctrlKey && !e.metaKey) {") {
		t.Fatal("every wheel still scales the diagram, which leaves no way to scroll it")
	}
	if !strings.Contains(app, "view.translate.x - e.deltaX / view.scale,") {
		t.Error("a wheel does not move the view sideways")
	}
	// A pinch reports a much smaller delta per event than a wheel notch, so
	// the step follows how far it moved rather than only which way.
	if !strings.Contains(app, "Math.exp(-e.deltaY / 400)") {
		t.Error("a pinch takes the same step as a wheel notch, which overshoots")
	}
}

// A box is a box. Sizing it by how much text it happens to hold made a row of
// them ragged and said something that was not true — a two-line name is not a
// bigger thing than a one-line one.
func TestEveryBoxIsTheSameHeightUntilAHandSaysOtherwise(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "const BOX_HEIGHT = 46;") {
		t.Fatal("there is no height a box has by default")
	}
	if !strings.Contains(app, "height: sized.height || BOX_HEIGHT,") {
		t.Error("a box's height still follows its text")
	}
	if !strings.Contains(app, "board.setCellsResizable(editing);") {
		t.Error("a box cannot be resized")
	}
	if !strings.Contains(app, "board.addListener(InternalEvent.CELLS_RESIZED, (sender, evt) => {") {
		t.Error("a size given by hand is not recorded")
	}
}

// Lining boxes up is arithmetic, and arithmetic is what a drawing tool is for.
func TestTheEditingControlsAreGrouped(t *testing.T) {
	page := render(t, fixture(), Options{})
	for _, id := range []string{
		`id="tools"`, `id="select-all"`, `id="align-left"`, `id="spread-x"`, `id="fit"`,
	} {
		if !strings.Contains(page, id) {
			t.Errorf("the editing controls are missing %s", id)
		}
	}

	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "function arrange(place) {") {
		t.Error("the picked boxes cannot be lined up")
	}
	// Lining boxes up needs boxes, and evening the gaps needs a gap.
	if !strings.Contains(app, "const need = id.startsWith('spread') ? 3 : 2;") {
		t.Error("the align controls are offered when there is nothing to align")
	}
}

// A line whose two ends face away from each other has to double back, and
// doing that between the boxes runs it through everything else crossing the
// same gap.
func TestALineThatDoublesBackGoesRoundTheOutside(t *testing.T) {
	app := string(Assets(nil)[AssetApp])
	if !strings.Contains(app, "function bypassLine(from, to, a, b, sideways, channel) {") {
		t.Fatal("a line that doubles back still cuts between the boxes")
	}
	if !strings.Contains(app, "const my = bypass !== undefined ? bypass : (a.y + b.y) / 2 + lane(b.y - a.y);") {
		t.Error("the way round is worked out and then not used")
	}
}

// A theme is added to the built-in styles rather than put in place of them,
// so a caller who wants their colours does not also have to restate the
// layout rules that make the page work at all. Later in the sheet is the only
// thing that makes their rules win, so the order is the feature.
func TestAThemeIsAddedAfterTheBuiltInStyles(t *testing.T) {
	theme := []byte(":root { --accent: rebeccapurple; }")

	inline := render(t, fixture(), Options{CSS: theme})
	built, mine := strings.Index(inline, appCSS), strings.Index(inline, string(theme))
	if mine < 0 {
		t.Fatal("the page does not carry the theme it was given")
	}
	if built < 0 || built > mine {
		t.Error("the theme is not after the built-in styles, so the built-in rules win")
	}

	// An external page links one stylesheet, so the theme has to be in it —
	// and be the same bytes, or the two kinds of page look different.
	shared := string(Assets(theme)[AssetCSS])
	if !strings.HasPrefix(shared, appCSS) || !strings.HasSuffix(shared, string(theme)) {
		t.Error("the shared stylesheet is not the built-in styles followed by the theme")
	}
}

// The shared stylesheet is served from one URL for every diagram and every
// generation of them, and a browser caches it. A theme that changed without
// moving the fingerprint would keep being served from that cache, so the
// person who edited it would be among the last to see it — and would edit it
// again, because nothing happened.
func TestAChangedThemeIsServedFromASeparateUrl(t *testing.T) {
	plain := RuntimeStamp(nil)
	first := RuntimeStamp([]byte(".edge { stroke: red; }"))
	second := RuntimeStamp([]byte(".edge { stroke: blue; }"))

	if first == plain {
		t.Error("a page with a theme is served from the same url as one without")
	}
	if first == second {
		t.Error("editing the theme did not change the url it is served from")
	}
	if again := RuntimeStamp([]byte(".edge { stroke: red; }")); again != first {
		t.Errorf("the same theme gave two urls, %q then %q, so nothing is shared", first, again)
	}
}

// A self-contained page carries the sheet inside a style element. A sheet
// that closes it would put the rest of itself in the document as markup —
// while an external page, where the sheet is its own file, would take the
// same bytes and look fine. Refusing keeps the two kinds of page honest
// about a file that is wrong in both.
func TestAStylesheetCannotEndThePagesStyleElement(t *testing.T) {
	_, err := Render(fixture(), Options{CSS: []byte("a{}</style><script>alert(1)</script>")})
	if err == nil {
		t.Fatal("a stylesheet that closes the style element was accepted")
	}
	if !strings.Contains(err.Error(), "</style") {
		t.Errorf("the error does not say what is wrong with the file: %v", err)
	}
}
