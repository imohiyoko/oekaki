package textmetrics

import (
	"math"
	"testing"
)

func TestKnownAdvances(t *testing.T) {
	// Straight from Adobe's Helvetica AFM, at 1000pt so the numbers are the
	// table's own units and a transcription error is obvious.
	for _, c := range []struct {
		s    string
		want float64
	}{
		{" ", 278},
		{"i", 222},
		{"M", 833},
		{"W", 944},
		{"0", 556},
		{"_", 556},
	} {
		if got := HelveticaWidth(c.s, 1000); got != c.want {
			t.Errorf("HelveticaWidth(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestWidthScalesWithSize(t *testing.T) {
	ten := HelveticaWidth("db_subnet_group", 10)
	twenty := HelveticaWidth("db_subnet_group", 20)

	if math.Abs(twenty-2*ten) > 1e-9 {
		t.Errorf("doubling the point size gave %v, want %v", twenty, 2*ten)
	}
}

// Helvetica is proportional. A measurement that misses this is measuring
// character counts, which is the mistake this package exists to avoid.
func TestNarrowLettersMeasureNarrower(t *testing.T) {
	if HelveticaWidth("iiii", 10) >= HelveticaWidth("MMMM", 10) {
		t.Error("iiii is not narrower than MMMM, so the metrics are not proportional")
	}
}

func TestEmptyStringIsZero(t *testing.T) {
	if got := HelveticaWidth("", 10); got != 0 {
		t.Errorf("HelveticaWidth(\"\") = %v, want 0", got)
	}
}

// A full-width character occupies exactly one em. Measuring it as anything
// less produces a box the label spills out of, and a fit check that passes
// while the picture is visibly wrong is worse than no fit check at all.
func TestFullWidthCharactersAreOneEm(t *testing.T) {
	for _, s := range []string{"本", "ア", "あ", "한", "Ａ", "、"} {
		if got := HelveticaWidth(s, 10); got != 10 {
			t.Errorf("HelveticaWidth(%q, 10) = %v, want 10 (one em)", s, got)
		}
	}
}

// Halfwidth katakana is not full-width, and neither is accented Latin. Giving
// them a full em would inflate every box that contains them.
func TestNarrowNonASCIIIsNotOneEm(t *testing.T) {
	for _, s := range []string{"ｱ", "é", "ж"} {
		if got := HelveticaWidth(s, 10); got >= 10 {
			t.Errorf("HelveticaWidth(%q, 10) = %v, want less than one em", s, got)
		}
	}
}

// The substitution tolerance models a browser picking a wider Latin face. A
// full-width character is one em in every font that has it, so applying the
// tolerance to it would demand space for a risk that does not exist.
func TestToleranceAppliesOnlyToEstimatedWidths(t *testing.T) {
	const cjk = "本番環境"
	if got, want := FitWidth(cjk, 10), HelveticaWidth(cjk, 10); got != want {
		t.Errorf("FitWidth(%q) = %v, want %v: full-width text needs no tolerance", cjk, got, want)
	}

	const latin = "database"
	if FitWidth(latin, 10) <= HelveticaWidth(latin, 10) {
		t.Error("FitWidth did not add the substitution tolerance to Latin text")
	}
}

// A mixed label must take the tolerance on its Latin half only.
func TestMixedScriptTakesToleranceOnTheLatinPartOnly(t *testing.T) {
	const s = "本番-db"

	want := HelveticaWidth("本番", 10) + SubstitutionTolerance*HelveticaWidth("-db", 10)
	if got := FitWidth(s, 10); math.Abs(got-want) > 1e-9 {
		t.Errorf("FitWidth(%q) = %v, want %v", s, got, want)
	}
}
