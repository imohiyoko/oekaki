// Package authz decides who may see and change what, and says why.
//
// Three different questions are kept apart here, because they have three
// different answers and mixing them produces a system nobody can reason about:
//
//	who you are      an identity provider says so
//	what you may read   this package, from roles somebody wrote down
//	what you may write  the repository's own owners file says so
//
// The host asks its identity provider whether the caller can read the
// repository a diagram was drawn from, and passes the answer in. That answer
// is the floor. Roles only carve within it, because showing someone a picture
// of a repository they cannot read is a way around that repository's own
// access control, and it must not be the thing that happens when nobody chose.
//
// # Why the permission names are in code and the roles are not
//
// A permission is the name of something this program does. Code has to test
// for it by name, so a permission nobody wrote a check for would be a word
// that does nothing — a configuration file promising a protection that does
// not exist. The catalog is therefore fixed here and small.
//
// Role names, what each role may do, and who holds which role are the
// opposite: they are one deployment's vocabulary, they differ everywhere, and
// nothing in this package should have an opinion about them. They arrive in a
// Policy. Never test a role's name to make a decision — ask whether the
// subject has a permission. A name is a label somebody may rename tomorrow.
//
// # Why every answer carries a sentence
//
// "Why can't I see this?" is asked every single time. A bare false can only be
// answered by someone willing to read the source, so Can returns the reason
// with the verdict and the caller shows it.
//
// This package does no I/O and holds no state. Reading policy from disk,
// asking an identity provider, and enforcing a decision all happen elsewhere.
package authz

import (
	"fmt"
	"sort"
	"strings"
)

// Effect is what the rules say about one permission. Three values, not two:
// "nobody said anything" has to be distinguishable from "somebody said no",
// or a policy cannot express taking a permission back from a broad role
// without also being unable to tell an intentional silence from a refusal.
//
// Unset is never written down. It is what the evaluator returns when it found
// no rule, and it denies, because default-deny is the only default that fails
// in the safe direction.
type Effect string

const (
	Unset Effect = ""
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

// Valid reports whether e is a value a policy may contain. Unset is not: it is
// produced by evaluation and refused on input, so that "" in a hand-written
// file is caught as the typo it is rather than silently meaning nothing.
func (e Effect) Valid() bool { return e == Allow || e == Deny }

// The permissions this program checks for.
//
// Parent is an implication: a permission is only in force if its parent is
// too. Denying Read therefore denies Write and Admin in one move, without
// anybody having to remember to list all three. The chain is deliberately
// shallow — one line of descent, no lattice — because a permission model
// people cannot hold in their head is one they will misconfigure.
const (
	Read  = "read"
	Write = "write"
	Admin = "admin"
)

// Permission is one entry in the catalog.
type Permission struct {
	Name   string
	Parent string // "" for a root permission
	About  string
}

var catalog = []Permission{
	{Name: Read, About: "see a diagram and what is saved for it"},
	{Name: Write, Parent: Read, About: "save a layout and make one the default"},
	{Name: Admin, Parent: Write, About: "change roles and delete generations"},
}

// Catalog lists the permissions, roots first. Callers render it; they do not
// extend it.
func Catalog() []Permission { return append([]Permission(nil), catalog...) }

// Known reports whether name is a permission this program checks.
func Known(name string) bool {
	for _, p := range catalog {
		if p.Name == name {
			return true
		}
	}
	return false
}

// under reports whether a permission is the named one or descends from it.
//
// Read is the root of the chain, so anything that implies read has to be held
// to whatever holds read back. Checking only the literal name lets somebody
// refused a drawing save a layout onto it, which is a stranger thing to be
// allowed than simply reading it.
func under(name, ancestor string) bool {
	seen := map[string]bool{}
	for at := name; at != ""; at = parentOf(at) {
		if at == ancestor {
			return true
		}
		if seen[at] {
			return false
		}
		seen[at] = true
	}
	return false
}

func parentOf(name string) string {
	for _, p := range catalog {
		if p.Name == name {
			return p.Parent
		}
	}
	return ""
}

// Mode is how this deployment runs. Authentication and authorization are one
// setting rather than two, because the two combinations you get by separating
// them are both useless: authenticating without authorizing makes the login
// screen decoration, and authorizing without authenticating hides everything
// from everyone. One choice decides both.
type Mode struct {
	Auth    bool
	Enforce bool
}

var modes = map[string]Mode{
	"local":      {Auth: false, Enforce: false},
	"saas":       {Auth: true, Enforce: true},
	"enterprise": {Auth: true, Enforce: true},
}

// unspecified is what an unnamed mode means. It requires authentication.
//
// The other way round, whoever did not think about how they were running it
// ends up serving without a login. Forgetting is common and its cost should
// land on the safe side, so the unsafe side — local — has to be asked for.
//
// It is left unnamed on purpose. saas and enterprise behave identically today,
// so calling the default either one would assert a difference that does not
// exist.
var unspecified = Mode{Auth: true, Enforce: true}

// ModeNames lists the modes that can be named, in a stable order.
func ModeNames() []string {
	out := make([]string, 0, len(modes))
	for name := range modes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ModeOf resolves a mode name. An unknown or empty name is the safe default
// rather than an error, so that a typo cannot open a server up.
func ModeOf(name string) Mode {
	if m, ok := modes[name]; ok {
		return m
	}
	return unspecified
}

// Rule is one thing a role says about one permission.
type Rule struct {
	Permission string `json:"permission"`
	Effect     Effect `json:"effect"`
}

// Item is a thing being looked at, in the only respect this package cares
// about: which roles its owner limited it to. Empty means no limit.
type Item struct {
	ReadRoles []string `json:"read_roles,omitempty"`
}

// Policy is everything about roles that came from outside this package.
//
// Enforce is not part of what gets written to a file anywhere, and callers
// must set it from the mode. A policy file that could carry it would let a
// deployment ship a configuration with enforcement quietly switched off.
type Policy struct {
	Roles                map[string][]Rule   `json:"roles"`
	Grants               map[string][]string `json:"grants"`
	Anonymous            []string            `json:"anonymous"`
	AllowWithoutRepoRead bool                `json:"allow_without_repo_read"`

	Enforce bool `json:"-"`
}

// Check reports what is wrong with a policy, so that it can be refused when it
// is written rather than behaving strangely when it is used.
func Check(p Policy) error {
	var problems []string
	for _, role := range sortedKeys(p.Roles) {
		for _, r := range p.Roles[role] {
			if !Known(r.Permission) {
				problems = append(problems, fmt.Sprintf("role %q: no such permission %q", role, r.Permission))
			}
			if !r.Effect.Valid() {
				problems = append(problems, fmt.Sprintf("role %q: %q must be %s or %s", role, r.Permission, Allow, Deny))
			}
		}
	}
	for _, subject := range sortedKeys(p.Grants) {
		// A bare login is ambiguous the moment there are two identity
		// providers, and by then the grants are written and the two people
		// called the same thing are the same person.
		if !strings.Contains(subject, ":") {
			problems = append(problems, fmt.Sprintf("subject %q needs the provider that named it, as provider:name", subject))
		}
		for _, role := range p.Grants[subject] {
			if _, ok := p.Roles[role]; !ok {
				problems = append(problems, fmt.Sprintf("subject %q holds %q, which is not a role here", subject, role))
			}
		}
	}
	for _, role := range p.Anonymous {
		if _, ok := p.Roles[role]; !ok {
			problems = append(problems, fmt.Sprintf("anonymous holds %q, which is not a role here", role))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%s", strings.Join(problems, "; "))
}

// Roles are the roles a subject holds, sorted and deduplicated.
//
// groups are the other names an identity provider gave for the same caller —
// team memberships and the like. Nothing passes them yet. The parameter is
// here from the start so that adding them later is not a change to every
// caller.
func Roles(p Policy, subject string, groups []string) []string {
	if subject == "" {
		return dedup(p.Anonymous)
	}
	got := append([]string(nil), p.Grants[subject]...)
	for _, g := range groups {
		got = append(got, p.Grants[g]...)
	}
	return dedup(got)
}

// Effect resolves one permission for a set of roles.
//
// Two rules decide it. Within a permission, deny wins over allow, so that
// taking something back from a broad role does not depend on which role was
// looked at first. Between a permission and its parent, the parent has to
// allow as well, so that revoking the root revokes everything under it.
func (p Policy) Effect(roles []string, permission string) Effect {
	// A name the catalog does not have is not a permission, and the walk below
	// would return Allow for it because a loop over no ancestors ends having
	// found nothing to object to. That is the wrong direction to fail in, and
	// the empty string reaches here whenever a caller forgets to fill the
	// field in.
	if !Known(permission) {
		return Unset
	}
	seen := map[string]bool{}
	for name := permission; name != ""; name = parentOf(name) {
		if seen[name] {
			break // a catalog cycle would otherwise hang; TestTheCatalogIsATree proves there is none
		}
		seen[name] = true
		switch p.direct(roles, name) {
		case Deny:
			return Deny
		case Unset:
			return Unset
		}
	}
	return Allow
}

// direct is what the roles say about exactly this permission, ignoring the
// chain above it.
func (p Policy) direct(roles []string, permission string) Effect {
	out := Unset
	for _, role := range roles {
		for _, r := range p.Roles[role] {
			if r.Permission != permission {
				continue
			}
			if r.Effect == Deny {
				return Deny
			}
			if r.Effect == Allow {
				out = Allow
			}
		}
	}
	return out
}

// Request is one question about one caller.
type Request struct {
	Subject    string
	Groups     []string
	Permission string

	// Item is the particular thing being asked about, or nil when the question
	// is not about one.
	Item *Item

	// RepoRead is what the identity provider answered when asked whether this
	// caller can read the repository the item came from. nil means nobody
	// asked — before sign-in, or for something with no repository behind it.
	RepoRead *bool
}

// Decision is a verdict and the sentence to show the person it applies to.
type Decision struct {
	Allowed bool
	Because string
}

// Can decides, and says why.
//
// The order is: whether this deployment enforces at all, then whether the
// identity provider already said no, then whether the item was limited, then
// whether the roles carry the permission. The provider comes before the roles
// because it is the floor and the roles only carve inside it.
func Can(p Policy, req Request) Decision {
	roles := Roles(p, req.Subject, req.Groups)
	who := req.Subject
	if who == "" {
		who = "an unnamed caller"
	}
	held := "no roles"
	if len(roles) > 0 {
		held = strings.Join(roles, ", ")
	}
	mine := fmt.Sprintf("%s holds %s", who, held)

	if !p.Enforce {
		return Decision{true, "this deployment does not authorize anyone"}
	}

	reading := under(req.Permission, Read)

	if reading && req.RepoRead != nil && !*req.RepoRead && !p.AllowWithoutRepoRead {
		return Decision{false, who + " cannot read the repository this was drawn from"}
	}

	if reading && req.Item != nil && len(req.Item.ReadRoles) > 0 {
		if !overlap(req.Item.ReadRoles, roles) {
			want := strings.Join(req.Item.ReadRoles, ", ")
			// An admin can delete whole generations and read the files off
			// disk, so hiding this from them protects nothing. Let it through
			// — but never quietly. Whoever wrote "this one is for the SRE
			// team" has to be able to see that it is not in fact a limit.
			if p.Effect(roles, Admin) == Allow {
				return Decision{true, fmt.Sprintf("%s. limited to %s, passing as admin", mine, want)}
			}
			return Decision{false, fmt.Sprintf("%s. this one wants %s", mine, want)}
		}
	}

	if p.Effect(roles, req.Permission) != Allow {
		return Decision{false, fmt.Sprintf("%s. no role here carries %s", mine, req.Permission)}
	}
	return Decision{true, mine}
}

// Row is what one subject would see, if enforcement were switched on.
type Row struct {
	Subject string
	Roles   []string
	Visible int
	Hidden  []string
}

// Explain answers "what happens if we turn this on", for every subject the
// policy names plus the unnamed one.
//
// Switching enforcement on without having looked at this either hides
// everything from everyone or protects nothing, and both are discovered by
// the people affected rather than by the person who flipped it.
func Explain(p Policy, items map[string]Item, repoRead *bool) []Row {
	p.Enforce = true

	subjects := append([]string{""}, sortedKeys(p.Grants)...)
	names := sortedKeys(items)

	rows := make([]Row, 0, len(subjects))
	for _, s := range subjects {
		var hidden []string
		for _, name := range names {
			item := items[name]
			if !Can(p, Request{Subject: s, Permission: Read, Item: &item, RepoRead: repoRead}).Allowed {
				hidden = append(hidden, name)
			}
		}
		shown := s
		if shown == "" {
			shown = "an unnamed caller"
		}
		rows = append(rows, Row{
			Subject: shown,
			Roles:   Roles(p, s, nil),
			Visible: len(names) - len(hidden),
			Hidden:  hidden,
		})
	}
	return rows
}

func overlap(a, b []string) bool {
	in := make(map[string]bool, len(a))
	for _, x := range a {
		in[x] = true
	}
	for _, y := range b {
		if in[y] {
			return true
		}
	}
	return false
}

func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
