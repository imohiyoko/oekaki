package terraform

import (
	"sort"
	"strings"
)

// inferDependencies recovers references from attribute values rather than from
// configuration expressions.
//
// A plan carries a `configuration` block, so references are known exactly. A
// state file does not: `terraform show -json terraform.tfstate` gives concrete
// attribute values and nothing about how they were wired. But the values
// themselves give it away — a subnet whose `vpc_id` is "vpc-0abc" plainly
// depends on the VPC whose `id` is "vpc-0abc" — so the identifiers are indexed
// and the values scanned for them.
//
// This is a fallback, not an equal. It can only see dependencies that left a
// concrete identifier behind, and a plan for resources that do not yet exist
// has unknown ids and yields nothing. Prefer a plan when you have one.
// identityKeys are the attributes whose values identify a resource well enough
// that finding one inside another resource means a real dependency.
var identityKeys = []string{"id", "arn", "name"}

// skipWhenScanning are attributes not worth searching for references.
//
// The identity keys are skipped so that a resource does not depend on itself.
// Tags are skipped because they are free text that frequently repeats a
// resource name: an instance tagged Name="bastion" would otherwise appear to
// depend on every resource named "bastion", which is an edge drawn for the
// wrong reason even when it happens to point somewhere sensible.
var skipWhenScanning = map[string]bool{
	"id": true, "arn": true, "name": true,
	"tags": true, "tags_all": true,
}

func inferDependencies(resources []resource) map[string][]dependency {
	// Any value claimed by two resources is dropped rather than guessed at: a
	// db_subnet_group called "main" and a cluster called "main" would otherwise
	// produce a plausible-looking edge that is simply wrong. The minimum length
	// below does the same job for short, generic names.
	owner := map[string]string{}
	ambiguous := map[string]bool{}

	for _, r := range resources {
		for _, key := range identityKeys {
			v, ok := r.values[key].(string)
			if !ok || len(v) < 6 {
				continue
			}
			if existing, taken := owner[v]; taken && existing != r.address {
				ambiguous[v] = true
				continue
			}
			owner[v] = r.address
		}
	}

	deps := map[string][]dependency{}
	for _, r := range resources {
		seen := map[string]bool{}

		for key, value := range r.values {
			if skipWhenScanning[key] {
				continue
			}
			for _, found := range stringsIn(value) {
				if ambiguous[found] {
					continue
				}
				target, ok := owner[found]
				if !ok || target == r.address {
					continue
				}
				if seen[target+"\x00"+key] {
					continue
				}
				seen[target+"\x00"+key] = true
				deps[r.address] = append(deps[r.address], dependency{target: target, attribute: key})
			}
		}

		sort.Slice(deps[r.address], func(i, j int) bool {
			a, b := deps[r.address][i], deps[r.address][j]
			if a.target != b.target {
				return a.target < b.target
			}
			return a.attribute < b.attribute
		})
	}
	return deps
}

// stringsIn collects every string inside an arbitrary decoded JSON value, so a
// reference buried in a nested block or a list is still found.
func stringsIn(v any) []string {
	var out []string

	var walk func(any, int)
	walk = func(v any, depth int) {
		// Terraform attribute values are shallow in practice; the bound is
		// only here so that a pathological document cannot blow the stack.
		if depth > 8 {
			return
		}
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); len(s) >= 6 {
				out = append(out, s)
			}
		case []any:
			for _, item := range t {
				walk(item, depth+1)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k], depth+1)
			}
		}
	}
	walk(v, 0)

	return out
}
