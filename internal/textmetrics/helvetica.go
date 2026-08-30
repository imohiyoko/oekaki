// Package textmetrics measures text the way a font does.
//
// It exists because Graphviz's WebAssembly build ships no font files. It sizes
// every box from a metric table looked up by font name, and the browser then
// draws the result with whatever real font it resolves `font-family` to. So
// "does this label fit its box" cannot be answered by reading the DOT, or by
// reading the SVG's numbers alone — it needs an independent measurement, which
// is what this package is.
//
// It is not test scaffolding. The HTML renderer has to hand ELK a width and a
// height for every node, and ELK does no text measurement of its own.
package textmetrics

// helvetica holds Adobe's Helvetica advance widths for printable ASCII, in
// units of 1/1000 em — the AFM convention. Arial, Liberation Sans and Nimbus
// Sans are metric-compatible with Helvetica by design, so these are also the
// advances of every font a browser is likely to substitute for it.
var helvetica = [95]uint16{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, // space ! " # $ % & ' ( )
	389, 584, 278, 333, 278, 278, 556, 556, 556, 556, // * + , - . / 0 1 2 3
	556, 556, 556, 556, 556, 556, 278, 278, 584, 584, // 4 5 6 7 8 9 : ; < =
	584, 556, 1015, 667, 667, 722, 722, 667, 611, 778, // > ? @ A B C D E F G
	722, 278, 500, 667, 556, 833, 722, 778, 667, 778, // H I J K L M N O P Q
	722, 667, 611, 722, 667, 944, 667, 667, 611, 278, // R S T U V W X Y Z [
	278, 278, 469, 556, 333, 556, 556, 500, 556, 556, // \ ] ^ _ ` a b c d e
	278, 556, 556, 222, 222, 500, 222, 833, 556, 556, // f g h i j k l m n o
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, // p q r s t u v w x y
	500, 334, 260, 334, 584, // z { | } ~
}

// wideWidth is the advance of a full-width character: exactly one em. CJK
// ideographs, kana and Hangul are designed on an em square, so this is a
// measurement rather than an estimate — which is also why the substitution
// tolerance does not apply to them.
const wideWidth = 1000

// narrowWidth is used for non-ASCII that is not full-width — accented Latin,
// Greek, Cyrillic. Those scripts sit close to Helvetica's lowercase advances,
// which cluster around this value.
//
// Being wrong here is not symmetric. A width that is too small produces a box
// the text spills out of, which is the defect this package was written to
// catch; a width that is too large only wastes space. So where the two are in
// tension, round up.
const narrowWidth = 556

// SubstitutionTolerance is how much wider than Helvetica a substituted font may
// be before a box that happens to fit should be considered lucky rather than
// correct.
//
// A document asking for a generic sans-serif commonly resolves to DejaVu Sans,
// which measures about 1.34x Helvetica on lowercase Latin. 1.20 sits between
// the metric-compatible clones and that worst case: wide enough to catch boxes
// with no real slack, not so wide that every label demands a huge box.
//
// It applies only to the parts of a string whose width is an estimate. A
// full-width character is one em in every font that has it, so multiplying it
// by a substitution factor would be counting a risk that does not exist.
const SubstitutionTolerance = 1.20

// isWide reports whether r is drawn on a full em square.
//
// These are the East Asian Wide and Fullwidth ranges, to the accuracy that
// matters for sizing a box: the boundaries between wide and narrow within CJK
// punctuation are not worth chasing, because being one character out changes a
// label's width by less than the slack any box needs anyway.
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F: // Hangul Jamo
		return true
	case r >= 0x2E80 && r <= 0x303E: // CJK radicals, Kangxi, CJK punctuation
		return true
	case r >= 0x3041 && r <= 0x33FF: // kana, Bopomofo, Hangul compatibility, CJK compatibility
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK extension A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs
		return true
	case r >= 0xA000 && r <= 0xA4CF: // Yi
		return true
	case r >= 0xAC00 && r <= 0xD7A3: // Hangul syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility ideographs
		return true
	case r >= 0xFE30 && r <= 0xFE6F: // CJK compatibility forms, small form variants
		return true
	case r >= 0xFF00 && r <= 0xFF60: // fullwidth forms (halfwidth kana starts at FF61)
		return true
	case r >= 0xFFE0 && r <= 0xFFE6: // fullwidth signs
		return true
	case r >= 0x20000 && r <= 0x3FFFD: // CJK extensions B and beyond
		return true
	}
	return false
}

// advance returns r's width in AFM units, and whether that width is an
// estimate that font substitution could invalidate.
func advance(r rune) (units int, estimated bool) {
	switch {
	case r >= ' ' && r <= '~':
		return int(helvetica[r-' ']), true
	case isWide(r):
		return wideWidth, false
	default:
		return narrowWidth, true
	}
}

// HelveticaWidth returns the advance width of s in points at the given font
// size: Helvetica's own metrics for ASCII, one em for full-width characters,
// and a lowercase-sized estimate for everything else.
func HelveticaWidth(s string, sizePt float64) float64 {
	var units int
	for _, r := range s {
		w, _ := advance(r)
		units += w
	}
	return float64(units) * sizePt / 1000
}

// FitWidth returns the width a box must accommodate to hold s safely: the
// measured width, with SubstitutionTolerance applied to the part of it that is
// an estimate.
//
// This is the number to size a box against. HelveticaWidth says how wide the
// text is if the browser cooperates; FitWidth says how wide it might be when
// the browser picks a font we did not ask for.
func FitWidth(s string, sizePt float64) float64 {
	var exact, estimated int
	for _, r := range s {
		w, isEstimate := advance(r)
		if isEstimate {
			estimated += w
			continue
		}
		exact += w
	}
	units := float64(exact) + SubstitutionTolerance*float64(estimated)
	return units * sizePt / 1000
}
