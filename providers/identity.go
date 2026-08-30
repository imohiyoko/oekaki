package providers

import (
	"sort"
	"strings"
)

// selectorKeys is the closed vocabulary an overlay may name a resource by, and
// what each key means.
//
// Closed on purpose. If one profile could invent "service" and another "svc"
// for the same idea, an overlay would become provider-specific, which defeats
// the point of writing one: the person filling it in is reading an operations
// console and has never seen a Terraform address. Adding a key is a deliberate
// two-line change with a documented meaning.
//
// The descriptions are not decoration — they are what the CLI prints when an
// overlay uses a key that does not exist, and a generator that is wrong some
// of the time needs that error to be useful.
var selectorKeys = map[string]string{
	"log_group":     "a log destination named by the platform rather than by the IaC",
	"search_index":  "a search or SIEM index name",
	"service":       "a service name as the orchestrator knows it",
	"function":      "a serverless function name",
	"workload":      "a Kubernetes workload name",
	"namespace":     "a Kubernetes namespace name",
	"load_balancer": "a load balancer name",
	"bucket":        "an object storage bucket name",
}

// SelectorKeys lists the vocabulary, sorted.
func SelectorKeys() []string {
	out := make([]string, 0, len(selectorKeys))
	for k := range selectorKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsSelectorKey reports whether k is in the vocabulary.
func IsSelectorKey(k string) bool {
	_, ok := selectorKeys[k]
	return ok
}

// SelectorKeyHelp returns what a key means, for error messages.
func SelectorKeyHelp(k string) (string, bool) {
	d, ok := selectorKeys[k]
	return d, ok
}

// Identities returns the selector keys a resource type answers to, mapped to
// the node attribute holding each key's value.
func Identities(resourceType string) map[string]string {
	p := Lookup(resourceType)
	if p == nil {
		return nil
	}
	return p.Identities[resourceType]
}

// IdentityOf returns a node's value for a selector key, given its type and the
// attributes a parser carried over.
//
// It reports false rather than an empty string when the type does not answer
// to that key at all, so that a caller can tell "this resource has no service
// name" from "its service name is empty".
func IdentityOf(resourceType, key string, attrs map[string]any) (string, bool) {
	path, ok := Identities(resourceType)[key]
	if !ok {
		return "", false
	}
	v, ok := dig(attrs, path)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// dig walks a dotted attribute path.
//
// Nesting is normal rather than exotic: Terraform's Kubernetes provider puts a
// workload's name inside a `metadata` block, and that block arrives as a list
// of one object, which is how HCL blocks are encoded in JSON. Both shapes are
// followed, because an overlay author should not have to know which one their
// provider used.
func dig(attrs map[string]any, path string) (any, bool) {
	var cur any = attrs
	for _, part := range strings.Split(path, ".") {
		m, ok := asObject(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func asObject(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case []any:
		// A block with one element is the common encoding; more than one is
		// ambiguous and deliberately not guessed at.
		if len(t) == 1 {
			return asObject(t[0])
		}
	}
	return nil, false
}
