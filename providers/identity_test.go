package providers

import "testing"

// Two profiles inventing "service" and "svc" for the same idea would make an
// overlay provider-specific, which is exactly what the vocabulary exists to
// prevent. The check is cheap and the drift it guards against is silent.
func TestIdentityKeysAreInTheClosedVocabulary(t *testing.T) {
	for _, p := range All() {
		for typ, keys := range p.Identities {
			for key := range keys {
				if !IsSelectorKey(key) {
					t.Errorf("%s: %s declares selector key %q, which is not in the vocabulary %v",
						p.Name, typ, key, SelectorKeys())
				}
			}
		}
	}
}

// An identity naming an attribute the parser does not carry can never match
// anything: the value simply is not in the IR. It would fail as silence rather
// than as an error, which is the failure mode this package was created to end.
func TestIdentityAttributesAreCarried(t *testing.T) {
	for _, p := range All() {
		for typ, keys := range p.Identities {
			carried := map[string]bool{}
			for _, a := range p.Attrs[typ] {
				carried[a] = true
			}
			for key, path := range keys {
				root := path
				for i := 0; i < len(path); i++ {
					if path[i] == '.' {
						root = path[:i]
						break
					}
				}
				if !carried[root] {
					t.Errorf("%s: %s maps %q to attribute %q, which is not in its Attrs — the value never reaches the IR",
						p.Name, typ, key, path)
				}
			}
		}
	}
}

func TestSelectorKeysAllHaveHelp(t *testing.T) {
	for _, k := range SelectorKeys() {
		if help, ok := SelectorKeyHelp(k); !ok || help == "" {
			t.Errorf("selector key %q has no description, so an error message about it cannot help anyone", k)
		}
	}
}

func TestIdentityOfReadsATopLevelAttribute(t *testing.T) {
	got, ok := IdentityOf("aws_ecs_service", "service", map[string]any{"name": "api"})
	if !ok || got != "api" {
		t.Errorf("IdentityOf = %q, %v; want \"api\", true", got, ok)
	}
}

// Terraform encodes an HCL block as a list of one object, so the same logical
// path arrives in two shapes. An overlay author should not have to know which.
func TestIdentityOfDescendsIntoBlocks(t *testing.T) {
	nested := map[string]any{"metadata": map[string]any{"name": "checkout", "namespace": "shop"}}
	listed := map[string]any{"metadata": []any{map[string]any{"name": "checkout", "namespace": "shop"}}}

	for _, attrs := range []map[string]any{nested, listed} {
		got, ok := IdentityOf("kubernetes_deployment", "workload", attrs)
		if !ok || got != "checkout" {
			t.Errorf("IdentityOf = %q, %v; want \"checkout\", true", got, ok)
		}
	}
}

// "This type has no service name" and "its service name is empty" are
// different answers, and a resolver that cannot tell them apart would treat a
// missing attribute as a match on the empty string.
func TestIdentityOfReportsAbsence(t *testing.T) {
	if _, ok := IdentityOf("aws_ecs_service", "bucket", map[string]any{"name": "api"}); ok {
		t.Error("a key the type does not answer to was reported as found")
	}
	if _, ok := IdentityOf("aws_ecs_service", "service", map[string]any{}); ok {
		t.Error("a missing attribute was reported as found")
	}
	if _, ok := IdentityOf("aws_ecs_service", "service", map[string]any{"name": ""}); ok {
		t.Error("an empty value was reported as found")
	}
}

// More than one block element is genuinely ambiguous, and guessing which one
// to read is how a resolver starts inventing joins.
func TestIdentityOfRefusesAmbiguousBlocks(t *testing.T) {
	attrs := map[string]any{"metadata": []any{
		map[string]any{"name": "a"},
		map[string]any{"name": "b"},
	}}
	if _, ok := IdentityOf("kubernetes_deployment", "workload", attrs); ok {
		t.Error("a two-element block was read as if it were one")
	}
}
