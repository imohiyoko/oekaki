package style

// CoverageStyle is how a coverage state is drawn.
//
// It touches the border and the label, never the fill. The fill says what a
// thing is, and coverage must not overwrite that: a database with a broken log
// pipeline is still a database, and a reader who loses the category to learn
// the coverage has been given one fact in exchange for another.
//
// The four states worth acting on differ in dash pattern and carry a word in
// the label as well as a colour, so the map survives a black-and-white print
// and a red-green colour deficiency. That is the same reasoning that gave the
// three edge kinds three dash patterns.
type CoverageStyle struct {
	// Stroke is empty when the category's own stroke should stand.
	Stroke string
	// Dashes is "" or "dashed".
	Dashes string
	// PenWidth is 0 when the default width should stand.
	PenWidth float64
	// Badge is appended to the type line, so the state survives being printed
	// in one colour.
	Badge string
	// Label is the legend text.
	Label string
}

// Decorated reports whether this state changes how a node is drawn at all.
type Decorated func() bool

// coverageStyles is deliberately short on decoration for the healthy state.
//
// Colours are reused from the existing palettes rather than invented. Amber is
// the "reachable" colour — configured but not used, which is the same idea as
// declared but silent. Red is Storage's stroke, purple is Database's. Five new
// hex values would each have needed a justification these already have.
var coverageStyles = map[string]CoverageStyle{
	// The normal case must look completely normal. Decorating it would make a
	// healthy map unreadable, and a map that is exhausting to read is a map
	// nobody checks.
	"flowing": {Label: "logs flowing"},

	"silent":     {Stroke: "#c97b1e", PenWidth: 2.4, Badge: "silent", Label: "declared, nothing seen"},
	"blind":      {Stroke: "#c74f63", Dashes: "dashed", PenWidth: 2.4, Badge: "no logs", Label: "no log destination"},
	"undeclared": {Stroke: "#7a52c7", Dashes: "dashed", PenWidth: 2.4, Badge: "unmodelled", Label: "logs from nothing declared"},

	// Grey, quiet and honest. Not assessed is not a fault, and drawing it as
	// one would push people to assert something rather than to go and look.
	"unknown": {Stroke: "#8a9099", Dashes: "dashed", Badge: "?", Label: "not assessed"},
}

// ForCoverage returns how a coverage state is drawn. An unknown state draws
// like "unknown" rather than erroring, for the same reason an unknown resource
// type draws as a generic box.
func ForCoverage(state string) CoverageStyle {
	if s, ok := coverageStyles[state]; ok {
		return s
	}
	return coverageStyles["unknown"]
}

// CoverageStates lists the states in the order a legend should show them:
// the ones worth acting on first, and "not assessed" last because it measures
// what has not been looked at rather than what is wrong.
func CoverageStates() []string {
	return []string{"blind", "silent", "undeclared", "flowing", "unknown"}
}

// OriginStyle distinguishes a claim from a derivation.
//
// A dashed border and a hollow arrowhead say "somebody said so" without
// competing for the colour channels, which are already carrying what a thing
// is and whether it is logging. Provenance is a real distinction and it has to
// be visible, but it is not the thing a reader came for.
type OriginStyle struct {
	Dashed    bool
	ArrowHead string
	Label     string
}

var origins = map[string]OriginStyle{
	"parser": {ArrowHead: "normal", Label: "found in the configuration"},
	"human":  {Dashed: true, ArrowHead: "onormal", Label: "asserted by a person"},
	"ai":     {Dashed: true, ArrowHead: "onormal", Label: "asserted by a model"},
}

// ForOrigin returns how a claim of a given origin is drawn.
func ForOrigin(origin string) OriginStyle {
	if s, ok := origins[origin]; ok {
		return s
	}
	return origins["parser"]
}

// Suppressed is how an edge somebody asserted is not real is drawn: present,
// faint, and clearly not load-bearing. It stays in the picture by default
// because a reader who cannot see the edge cannot judge the claim about it.
var Suppressed = EdgeStyle{Color: "#c0c5cb", Dashes: "dotted", Label: "asserted not to exist"}

// Contested is the pen width for something two claims disagree about. Weight
// rather than colour, because the colour channels are taken and because a
// disagreement is about how much attention to pay, not about what kind of
// thing it is.
const Contested = 2.6
