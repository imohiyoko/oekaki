package providers

import (
	"strings"
	"testing"
)

// This file is mostly consistency checks. They exist because the tables they
// guard used to live in two packages and had already drifted: sixteen resource
// types appeared in both, eighteen in only one, so some resources had a colour
// but no attributes and others the reverse. Nothing detected it, because there
// was nothing that could.

// A type with curated attributes but no category draws as a generic grey box
// while carrying detail nobody sees — a sign the two tables have come apart.
func TestEveryTypeWithAttrsHasACategory(t *testing.T) {
	for _, p := range All() {
		for typ := range p.Attrs {
			if CategoryOf(typ) != Generic {
				continue
			}
			// Containers are drawn as borders, not boxes, so they legitimately
			// have no node category.
			if IsContainer(typ) {
				continue
			}
			t.Errorf("%s: %s has attributes but falls through to Generic", p.Name, typ)
		}
	}
}

// Everything a profile mentions must be claimed by that profile's prefixes,
// or Lookup will route it to the wrong provider — or to none.
func TestEveryMentionedTypeMatchesTheProfilePrefixes(t *testing.T) {
	for _, p := range All() {
		seen := map[string]bool{}
		for typ := range p.Attrs {
			seen[typ] = true
		}
		for typ := range p.Categories {
			seen[typ] = true
		}
		for typ := range p.Containers {
			seen[typ] = true
		}
		for typ := range p.Attachments {
			seen[typ] = true
		}
		for _, typ := range p.Highlights {
			seen[typ] = true
		}
		for typ := range p.Identities {
			seen[typ] = true
		}

		for typ := range seen {
			got := Lookup(typ)
			if got == nil {
				t.Errorf("%s lists %s, but no profile claims it", p.Name, typ)
				continue
			}
			if got.Name != p.Name {
				t.Errorf("%s lists %s, but %s claims it", p.Name, typ, got.Name)
			}
		}
	}
}

// Documentation promises these by name, so the code has to treat them as
// first-class rather than leaving them to the generic fallback.
func TestHighlightsAreReallySupported(t *testing.T) {
	if len(Highlights()) == 0 {
		t.Fatal("no highlighted types at all")
	}

	for _, typ := range Highlights() {
		if IsContainer(typ) {
			continue
		}
		if len(Attrs(typ)) == 0 {
			t.Errorf("%s is advertised as first-class but carries no attributes", typ)
		}
		if CategoryOf(typ) == Generic {
			t.Errorf("%s is advertised as first-class but draws as a generic box", typ)
		}
	}
}

// Two profiles claiming the same prefix would make Lookup depend on
// registration order, which is not something a contributor should have to know.
func TestPrefixesDoNotOverlap(t *testing.T) {
	type owner struct {
		prefix string
		name   string
	}
	var all []owner
	for _, p := range All() {
		for _, prefix := range p.Prefixes {
			if prefix == "" {
				t.Errorf("%s has an empty prefix, which would claim every type", p.Name)
			}
			all = append(all, owner{prefix, p.Name})
		}
	}

	for i, a := range all {
		for j, b := range all {
			if i == j || a.name == b.name {
				continue
			}
			if strings.HasPrefix(a.prefix, b.prefix) {
				t.Errorf("prefix %q (%s) is shadowed by %q (%s)", a.prefix, a.name, b.prefix, b.name)
			}
		}
	}
}

// An unknown type must render, not fail. Covering every provider resource is
// explicitly not a goal, so the fallback path is a feature and is tested.
func TestUnknownTypesFallBackRatherThanFail(t *testing.T) {
	const unknown = "acme_quantum_widget"

	if Lookup(unknown) != nil {
		t.Error("an unknown type was claimed by a profile")
	}
	if IsContainer(unknown) {
		t.Error("an unknown type became a container")
	}
	if IsAttachment(unknown) {
		t.Error("an unknown type became an attachment")
	}
	if Attrs(unknown) != nil {
		t.Error("an unknown type carried attributes")
	}
	if got := CategoryOf(unknown); got != Generic {
		t.Errorf("CategoryOf(%q) = %q, want %q", unknown, got, Generic)
	}
	if got := ShortType(unknown); got != unknown {
		t.Errorf("ShortType(%q) = %q, want it unchanged", unknown, got)
	}
}

// The substring rules are guesses and must never override an explicit entry.
func TestExplicitCategoriesBeatTheHeuristics(t *testing.T) {
	// aws_db_subnet_group contains "subnet", which the heuristics call
	// Network, but the table says Database and the table wins.
	if got := CategoryOf("aws_db_subnet_group"); got != Database {
		t.Errorf("CategoryOf(aws_db_subnet_group) = %q, want %q", got, Database)
	}
}

func TestShortTypeTrimsTheProviderPrefix(t *testing.T) {
	if got := ShortType("aws_ecs_service"); got != "ecs_service" {
		t.Errorf("ShortType = %q, want %q", got, "ecs_service")
	}
}
