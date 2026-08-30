package terraform

import "testing"

// TestReferenceToAForeignContainerIsNotRepresented pins a known gap so it is a
// recorded limitation rather than a surprise.
//
// Schema v0.1 requires both ends of an edge to be node ids, and containers are
// groups rather than nodes. A reference to a container is therefore expressed
// by containment and nothing else. That is fine while the container is in the
// same provider, because containment does say "this lives in that".
//
// Across a provider boundary containment is refused — an on-premises machine
// does not live in a VPC — and there is no other way in v0.1 to say that the
// reference happened. So it is dropped.
//
// Making containers addressable is part of the schema v0.2 work. Until then
// this test documents what actually happens, so that a future change to the
// edge filter is recognised as the fix rather than mistaken for a regression.
func TestReferenceToAForeignContainerIsNotRepresented(t *testing.T) {
	g := parseFile(t, "testdata/mixed-estate.json", Options{})

	// aws_subnet.private_a is a group, so nothing may point at it.
	for _, e := range g.Edges {
		if e.To == "aws_subnet.private_a" {
			t.Fatalf("edge %s -> %s: v0.1 edges must land on nodes, not groups", e.From, e.To)
		}
	}

	// The cross-provider reference to a *node* is the case that must survive,
	// and it is the common one: systems depend on databases, not on subnets.
	var found bool
	for _, e := range g.Edges {
		if e.From == "vsphere_virtual_machine.legacy_erp" && e.To == "aws_db_instance.orders" {
			found = true
		}
	}
	if !found {
		t.Error("a cross-provider dependency on a node was dropped")
	}
}
