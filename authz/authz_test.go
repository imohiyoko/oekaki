package authz

import (
	"strings"
	"testing"
)

func policy() Policy {
	return Policy{
		Roles: map[string][]Rule{
			"viewer": {{Read, Allow}},
			"editor": {{Read, Allow}, {Write, Allow}},
			"boss":   {{Read, Allow}, {Write, Allow}, {Admin, Allow}},
			"muted":  {{Read, Deny}},
		},
		Grants: map[string][]string{
			"github:reader": {"viewer"},
			"github:writer": {"editor"},
			"github:chief":  {"boss"},
		},
		Anonymous: []string{"viewer"},
		Enforce:   true,
	}
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// The catalog is walked by following Parent until it runs out. A cycle there
// would hang every call, and the loop guard that prevents it would hide the
// mistake, so prove the shape instead of relying on the guard.
func TestTheCatalogIsATree(t *testing.T) {
	for _, p := range Catalog() {
		if p.Parent == "" {
			continue
		}
		if !Known(p.Parent) {
			t.Fatalf("%q has parent %q, which is not in the catalog", p.Name, p.Parent)
		}
		seen := map[string]bool{p.Name: true}
		for at := p.Parent; at != ""; at = parentOf(at) {
			if seen[at] {
				t.Fatalf("%q reaches itself through %q", p.Name, at)
			}
			seen[at] = true
		}
	}
}

// Running without authorization has to be visibly that, not an accident that
// happens to allow everything. The sentence is the only thing distinguishing
// the two for whoever reads it.
func TestNothingIsRefusedWhenNobodyIsAuthorizing(t *testing.T) {
	p := policy()
	p.Enforce = false
	got := Can(p, Request{Subject: "", Permission: Admin, RepoRead: no()})
	if !got.Allowed {
		t.Fatalf("refused with enforcement off: %#v", got)
	}
	if !strings.Contains(got.Because, "does not authorize") {
		t.Fatalf("the reason does not say why it passed: %q", got.Because)
	}
}

// The identity provider's answer is the floor. Someone who cannot read the
// repository must not be shown a picture of it, however many roles they hold,
// because that is a way around the repository's own access control.
func TestNoRoleGetsPastTheRepositorySayingNo(t *testing.T) {
	got := Can(policy(), Request{Subject: "github:chief", Permission: Read, RepoRead: no()})
	if got.Allowed {
		t.Fatalf("an admin was let past a repository refusal: %q", got.Because)
	}
}

// Wanting that anyway is legitimate, but it must be asked for rather than
// being what happens when nobody chose.
func TestTheRepositoryFloorCanBeLiftedOnPurpose(t *testing.T) {
	p := policy()
	p.AllowWithoutRepoRead = true
	if got := Can(p, Request{Subject: "github:reader", Permission: Read, RepoRead: no()}); !got.Allowed {
		t.Fatalf("the explicit setting had no effect: %q", got.Because)
	}
}

// Not having asked is not the same as having been told no.
func TestNotHavingAskedTheProviderIsNotARefusal(t *testing.T) {
	if got := Can(policy(), Request{Subject: "github:reader", Permission: Read}); !got.Allowed {
		t.Fatalf("refused without anyone having asked: %q", got.Because)
	}
	if got := Can(policy(), Request{Subject: "github:reader", Permission: Read, RepoRead: yes()}); !got.Allowed {
		t.Fatalf("refused after a yes: %q", got.Because)
	}
}

// An admin can delete the generation and read the files off disk, so hiding an
// item from them protects nothing. Letting it through is right; letting it
// through silently is not, because the person who wrote the limit would go on
// believing it was one.
func TestAnAdminPassesAnItemLimitAndTheReasonSaysSo(t *testing.T) {
	item := &Item{ReadRoles: []string{"viewer"}}
	got := Can(policy(), Request{Subject: "github:chief", Permission: Read, Item: item})
	if !got.Allowed {
		t.Fatalf("the admin was refused: %q", got.Because)
	}
	if !strings.Contains(got.Because, "as admin") {
		t.Fatalf("the bypass is invisible in the reason: %q", got.Because)
	}
}

// Everyone else is held to the limit, and told what would lift it.
func TestAnItemLimitNamesWhatWouldSatisfyIt(t *testing.T) {
	item := &Item{ReadRoles: []string{"editor"}}
	got := Can(policy(), Request{Subject: "github:reader", Permission: Read, Item: item})
	if got.Allowed {
		t.Fatalf("the limit did not hold: %q", got.Because)
	}
	if !strings.Contains(got.Because, "editor") {
		t.Fatalf("the reason does not say what is wanted: %q", got.Because)
	}
}

// An item nobody limited is readable by anyone who may read at all.
func TestAnUnlimitedItemIsNotALimit(t *testing.T) {
	if got := Can(policy(), Request{Subject: "github:reader", Permission: Read, Item: &Item{}}); !got.Allowed {
		t.Fatalf("an empty limit refused: %q", got.Because)
	}
}

// Taking a permission back from a broad role must not depend on which role the
// evaluator happened to look at first.
func TestARefusalBeatsAGrantHoweverTheRolesAreOrdered(t *testing.T) {
	p := policy()
	p.Grants["github:awkward"] = []string{"viewer", "muted"}
	for i := range 10 {
		if got := Can(p, Request{Subject: "github:awkward", Permission: Read}); got.Allowed {
			t.Fatalf("run %d let a denied read through: %q", i, got.Because)
		}
	}
}

// A permission is only in force if the one above it is. Revoking the root has
// to revoke what hangs off it, or every revocation is a checklist somebody
// will get wrong.
func TestRefusingTheRootRefusesWhatHangsBelowIt(t *testing.T) {
	p := policy()
	p.Roles["hobbled"] = []Rule{{Read, Deny}, {Write, Allow}, {Admin, Allow}}
	p.Grants["github:hobbled"] = []string{"hobbled"}
	for _, permission := range []string{Read, Write, Admin} {
		if got := Can(p, Request{Subject: "github:hobbled", Permission: permission}); got.Allowed {
			t.Fatalf("%s survived read being denied: %q", permission, got.Because)
		}
	}
}

// Holding a permission without the one above it is a policy that says nothing
// coherent, and the safe reading of it is no.
func TestAPermissionWithoutTheOneAboveItDoesNotCount(t *testing.T) {
	p := policy()
	p.Roles["odd"] = []Rule{{Write, Allow}}
	p.Grants["github:odd"] = []string{"odd"}
	if got := Can(p, Request{Subject: "github:odd", Permission: Write}); got.Allowed {
		t.Fatalf("write counted without read: %q", got.Because)
	}
}

// Nobody says anything about most subjects. That has to deny rather than
// crash, and rather than allow.
func TestASubjectNobodyMentionedIsRefusedQuietly(t *testing.T) {
	for _, subject := range []string{"github:stranger", "gitlab:someone", "nonsense"} {
		got := Can(policy(), Request{Subject: subject, Permission: Read})
		if got.Allowed {
			t.Fatalf("%q was allowed: %q", subject, got.Because)
		}
		if got.Because == "" {
			t.Fatalf("%q was refused without a reason", subject)
		}
	}
}

// A grant naming a role that was deleted must not take the evaluator with it.
func TestARoleThatIsNotThereIsJustNoPermissions(t *testing.T) {
	p := policy()
	p.Grants["github:ghost"] = []string{"deleted"}
	if got := Can(p, Request{Subject: "github:ghost", Permission: Read}); got.Allowed {
		t.Fatalf("a missing role granted something: %q", got.Because)
	}
}

// Whoever has not signed in still gets whatever anonymous was given, so that
// turning enforcement on does not hide what was public a moment earlier.
func TestAnUnnamedCallerGetsWhatAnonymousHolds(t *testing.T) {
	if got := Can(policy(), Request{Permission: Read}); !got.Allowed {
		t.Fatalf("anonymous could not read despite holding viewer: %q", got.Because)
	}
	if got := Can(policy(), Request{Permission: Write}); got.Allowed {
		t.Fatalf("anonymous could write: %q", got.Because)
	}
}

// An empty policy denies everything and does not panic doing it.
func TestAnEmptyPolicyRefusesEverything(t *testing.T) {
	p := Policy{Enforce: true}
	for _, permission := range []string{Read, Write, Admin} {
		if got := Can(p, Request{Subject: "github:someone", Permission: permission}); got.Allowed {
			t.Fatalf("%s allowed by an empty policy: %q", permission, got.Because)
		}
	}
}

// A permission this program never checks for would be a word in a file that
// protects nothing, so it is refused where it is written.
func TestAPolicyCannotGrantAPermissionNothingChecks(t *testing.T) {
	p := policy()
	p.Roles["odd"] = []Rule{{"deploy", Allow}}
	err := Check(p)
	if err == nil {
		t.Fatal("an invented permission was accepted")
	}
	if !strings.Contains(err.Error(), "deploy") {
		t.Fatalf("the complaint does not name it: %v", err)
	}
}

// Effect has three values and only two of them may be written down. An empty
// one in a hand-written file is a typo, not a statement.
func TestAnEffectMustBeOneOfTheTwoThatCanBeWritten(t *testing.T) {
	p := policy()
	p.Roles["odd"] = []Rule{{Read, Unset}}
	if err := Check(p); err == nil {
		t.Fatal("an unwritten effect was accepted")
	}
}

// A bare login is ambiguous as soon as there are two identity providers, and
// by then the grants are written and two different people share a name.
func TestASubjectHasToSayWhichProviderNamedIt(t *testing.T) {
	p := policy()
	p.Grants["plainlogin"] = []string{"viewer"}
	err := Check(p)
	if err == nil {
		t.Fatal("a subject without a provider was accepted")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("the complaint does not explain: %v", err)
	}
}

// A grant to a role that does not exist is silently no permissions at
// evaluation time, which is safe but looks like the grant worked. Say so when
// it is written.
func TestAGrantToANonexistentRoleIsRefusedWhenWritten(t *testing.T) {
	p := policy()
	p.Grants["github:typo"] = []string{"viewr"}
	if err := Check(p); err == nil {
		t.Fatal("a misspelt role was accepted")
	}
}

func TestAnonymousIsCheckedLikeAnyOtherHolder(t *testing.T) {
	p := policy()
	p.Anonymous = []string{"nosuchrole"}
	if err := Check(p); err == nil {
		t.Fatal("anonymous was allowed to hold a role that is not there")
	}
}

func TestAGoodPolicyHasNothingWrongWithIt(t *testing.T) {
	if err := Check(policy()); err != nil {
		t.Fatalf("a valid policy was refused: %v", err)
	}
}

// Turning enforcement on without having looked either hides everything or
// protects nothing, and both are found out by the people affected rather than
// by whoever flipped it.
func TestWhatWouldBeHiddenCanBeSeenBeforeEnforcing(t *testing.T) {
	p := policy()
	p.Enforce = false // the preview must not depend on the current setting
	items := map[string]Item{
		"open":   {},
		"closed": {ReadRoles: []string{"editor"}},
	}
	rows := Explain(p, items, nil)
	if len(rows) != 4 {
		t.Fatalf("expected the unnamed caller and three subjects, got %d: %#v", len(rows), rows)
	}

	by := map[string]Row{}
	for _, r := range rows {
		by[r.Subject] = r
	}
	if got := by["github:reader"]; got.Visible != 1 || len(got.Hidden) != 1 || got.Hidden[0] != "closed" {
		t.Fatalf("a viewer should lose exactly the limited item: %#v", got)
	}
	if got := by["github:writer"]; got.Visible != 2 || len(got.Hidden) != 0 {
		t.Fatalf("an editor should keep both: %#v", got)
	}
	if got := by["github:chief"]; got.Visible != 2 {
		t.Fatalf("an admin should keep both: %#v", got)
	}
}

// The preview is a question about a hypothetical, and asking it must not
// change what the server is doing now.
func TestAskingWhatWouldHappenDoesNotTurnItOn(t *testing.T) {
	p := policy()
	p.Enforce = false
	Explain(p, map[string]Item{"a": {ReadRoles: []string{"editor"}}}, nil)
	if p.Enforce {
		t.Fatal("the preview switched enforcement on")
	}
	if got := Can(p, Request{Subject: "github:reader", Permission: Admin}); !got.Allowed {
		t.Fatalf("the preview left enforcement on: %q", got.Because)
	}
}

// Whoever did not say how they were running it gets the answer that fails
// safe, and a typo must not be the thing that opens a server up.
func TestAnUnnamedModeAsksForAuthentication(t *testing.T) {
	for _, name := range []string{"", "Local", "prod", "loca"} {
		got := ModeOf(name)
		if !got.Auth || !got.Enforce {
			t.Fatalf("mode %q came out permissive: %#v", name, got)
		}
	}
}

func TestLocalIsTheOneModeThatAsksForNothing(t *testing.T) {
	if got := ModeOf("local"); got.Auth || got.Enforce {
		t.Fatalf("local should neither authenticate nor authorize: %#v", got)
	}
	for _, name := range []string{"saas", "enterprise"} {
		if got := ModeOf(name); !got.Auth || !got.Enforce {
			t.Fatalf("%s should do both: %#v", name, got)
		}
	}
}

// Roles come back sorted so that a reason sentence reads the same twice.
func TestTheRolesSomeoneHoldsComeBackInAStableOrder(t *testing.T) {
	p := policy()
	p.Grants["github:many"] = []string{"viewer", "editor", "viewer"}
	first := strings.Join(Roles(p, "github:many", nil), ",")
	if first != "editor,viewer" {
		t.Fatalf("expected sorted and deduplicated, got %q", first)
	}
	for i := range 10 {
		if got := strings.Join(Roles(p, "github:many", nil), ","); got != first {
			t.Fatalf("run %d differed: %q vs %q", i, got, first)
		}
	}
}

// Groups are what an identity provider says the caller also is. Nothing passes
// them yet, but the plumbing has to work the day something does.
func TestARoleCanArriveThroughAGroupRatherThanTheSubject(t *testing.T) {
	p := policy()
	p.Grants["github-team:acme/ops"] = []string{"editor"}
	got := Can(p, Request{Subject: "github:stranger", Groups: []string{"github-team:acme/ops"}, Permission: Write})
	if !got.Allowed {
		t.Fatalf("a group grant did not reach the caller: %q", got.Because)
	}
}

// The chain is walked by climbing parents, and a name with no ancestors ends
// the walk having found nothing to object to. For a name that is not a
// permission at all — an empty field a caller forgot to fill in, most of all —
// that reasoning arrives at yes, which is the wrong direction to fail in.
func TestAPermissionNameThisProgramDoesNotHaveIsNotAPass(t *testing.T) {
	for _, permission := range []string{"", "deploy", "READ", "read "} {
		got := Can(policy(), Request{Subject: "github:chief", Permission: permission})
		if got.Allowed {
			t.Fatalf("%q was allowed: %q", permission, got.Because)
		}
		if p := (policy()).Effect([]string{"boss"}, permission); p != Unset {
			t.Fatalf("%q resolved to %q, expected it to be unset", permission, p)
		}
	}
}

// Read is the root of the chain, so anything that implies read has to be held
// to whatever holds read back. Checking only the literal name lets somebody
// refused a drawing save a layout onto it, which is a stranger thing to be
// allowed than simply reading it.
func TestWhatStopsAReadStopsEverythingThatImpliesOne(t *testing.T) {
	p := policy()
	for _, permission := range []string{Read, Write, Admin} {
		got := Can(p, Request{Subject: "github:chief", Permission: permission, RepoRead: no()})
		if got.Allowed {
			t.Fatalf("%s was allowed past a repository refusal: %q", permission, got.Because)
		}
	}
}

func TestAnItemLimitHoldsForWritingAsWellAsReading(t *testing.T) {
	p := policy()
	p.Grants["github:outsider"] = []string{"editor"}
	item := &Item{ReadRoles: []string{"boss"}}
	for _, permission := range []string{Read, Write} {
		got := Can(p, Request{Subject: "github:outsider", Permission: permission, Item: item})
		if got.Allowed {
			t.Fatalf("%s got past a limit that excludes this caller: %q", permission, got.Because)
		}
	}
}
