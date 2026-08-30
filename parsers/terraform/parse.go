// Package terraform turns `terraform show -json` output into oekaki's IR.
//
// It reads two things from the input and nothing else. `planned_values` (or
// `values`, for a state file) supplies the resources that become nodes and
// groups. `configuration` supplies the reference expressions that become
// iac_ref edges. No AWS credentials are involved and nothing is called over the
// network: the file you already have is the whole input.
package terraform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/imohiyoko/oekaki/core"
	"github.com/imohiyoko/oekaki/providers"
)

// Options tunes a parse.
type Options struct {
	// SourceDir, when set, is scanned for .tf files so that nodes carry the
	// file and line they were declared on. Optional: a plan file alone has no
	// source locations in it.
	SourceDir string

	// IncludeDataSources adds `data.*` lookups as nodes. Off by default: they
	// are usually AMI and AZ lookups that add boxes without adding meaning.
	IncludeDataSources bool

	// Scope names the estate and qualifies every id with it, so that documents
	// produced from different state files can be combined without
	// `aws_vpc.main` in one silently merging with `aws_vpc.main` in another.
	Scope string
}

// resource is the flattened form the parser works with, joining what
// planned_values knows (real attribute values, fully-qualified addresses
// including count/for_each indices) with what configuration knows (references).
type resource struct {
	address string
	typ     string
	name    string
	mode    tfjson.ResourceMode
	values  map[string]any
	// provider is the short provider name, e.g. "aws" or "vsphere". It is taken
	// from Terraform's provider_name rather than guessed from the type prefix,
	// so aliased and third-party providers are identified correctly.
	provider string
}

// providerOf reduces a fully qualified provider name such as
// "registry.terraform.io/hashicorp/aws" to "aws".
func providerOf(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Parse reads `terraform show -json` output — either a plan or a state — and
// returns the IR.
func Parse(input []byte, opts Options) (*core.Graph, error) {
	var probe struct {
		FormatVersion    string          `json:"format_version"`
		TerraformVersion string          `json:"terraform_version"`
		PlannedValues    json.RawMessage `json:"planned_values"`
		Values           json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(input, &probe); err != nil {
		return nil, fmt.Errorf("this does not look like `terraform show -json` output: %w", err)
	}

	var (
		stateValues *tfjson.StateValues
		config      *tfjson.Config
	)

	switch {
	case len(probe.PlannedValues) > 0:
		var plan tfjson.Plan
		if err := json.Unmarshal(input, &plan); err != nil {
			return nil, fmt.Errorf("parsing plan: %w", err)
		}
		if err := plan.Validate(); err != nil {
			return nil, fmt.Errorf("parsing plan: %w", err)
		}
		stateValues, config = plan.PlannedValues, plan.Config

	case len(probe.Values) > 0:
		var state tfjson.State
		if err := json.Unmarshal(input, &state); err != nil {
			return nil, fmt.Errorf("parsing state: %w", err)
		}
		if err := state.Validate(); err != nil {
			return nil, fmt.Errorf("parsing state: %w", err)
		}
		stateValues = state.Values

	default:
		return nil, fmt.Errorf("no `planned_values` or `values` in the input: run `terraform show -json <plan-or-state>` and pass the result")
	}

	if stateValues == nil || stateValues.RootModule == nil {
		return nil, fmt.Errorf("the input describes no resources")
	}

	resources, err := flatten(stateValues.RootModule, opts)
	if err != nil {
		return nil, err
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("the input describes no managed resources")
	}

	refs := map[string][]reference{}
	if config != nil && config.RootModule != nil {
		refs = collectReferences(config.RootModule, "")
	}

	g := core.New()
	g.Metadata = &core.Metadata{
		Source:        "terraform",
		SourceVersion: probe.TerraformVersion,
	}

	// A reference names a resource, but planned_values names each *instance*
	// of it, so `aws_subnet.public` has to resolve to `aws_subnet.public[0]`
	// and `[1]` alike.
	instances := map[string][]string{}
	addresses := map[string]bool{}
	byAddress := map[string]*resource{}
	for i := range resources {
		r := &resources[i]
		byAddress[r.address] = r
		addresses[r.address] = true
		base := stripIndex(r.address)
		instances[base] = append(instances[base], r.address)
	}
	for k := range instances {
		sort.Strings(instances[k])
	}

	// deps[a] is every resource address a's configuration points at, with the
	// attribute that did the pointing. Both the edges and the containment
	// resolution below are derived from it.
	deps := map[string][]dependency{}

	// A state file has no `configuration` block, so there is nothing to read
	// references out of and they have to be recovered from the values instead.
	if config == nil {
		deps = inferDependencies(resources)
	}

	for _, r := range resources {
		base := stripAllIndexes(r.address)
		seen := map[string]bool{}
		for _, ref := range refs[base] {
			targetRef := instantiateReference(ref.target, base, r.address)
			for _, target := range resolve(targetRef, instances, addresses) {
				if target == r.address || seen[target+"\x00"+ref.attribute] {
					continue
				}
				seen[target+"\x00"+ref.attribute] = true
				deps[r.address] = append(deps[r.address], dependency{
					target:    target,
					attribute: ref.attribute,
				})
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

	sources := map[string]*core.Source{}
	if opts.SourceDir != "" {
		var err error
		sources, err = scanSources(opts.SourceDir)
		if err != nil {
			return nil, err
		}
	}

	// Containers become groups; everything else becomes a node. Which axis a
	// container groups on is the provider's decision, not this parser's: most
	// are network containers, but an Azure resource group is an ownership
	// boundary that cuts across the network rather than nesting inside it.
	containerAxes := map[string]bool{}
	for _, r := range resources {
		src := sources[stripAllIndexes(r.address)]
		if groupType, ok := providers.ContainerType(r.typ); ok {
			axis := providers.ContainerAxis(r.typ)
			containerAxes[axis] = true
			g.Groups = append(g.Groups, core.Group{
				ID:     r.address,
				Axis:   axis,
				Type:   groupType,
				Label:  label(r),
				Parent: nil, // filled in below, once every group exists
				Attrs:  pickAttrs(r),
				Source: src,
			})
			continue
		}
		g.Nodes = append(g.Nodes, core.Node{
			ID:       r.address,
			Type:     r.typ,
			Name:     label(r),
			Provider: r.provider,
			Attrs:    pickAttrs(r),
			Source:   src,
		})
	}

	isGroup := map[string]bool{}
	for _, grp := range g.Groups {
		isGroup[grp.ID] = true
	}

	// A subnet's parent is the VPC it references. Nesting comes from the same
	// reference data as the edges do, not from a special case — and, as with
	// node placement, it stops at the provider boundary.
	for i := range g.Groups {
		self := byAddress[g.Groups[i].ID]
		for _, d := range deps[g.Groups[i].ID] {
			if !isGroup[d.target] {
				continue
			}
			target, ok := byAddress[d.target]
			if !ok || self == nil {
				continue
			}
			if target.provider != self.provider {
				continue
			}
			// A container's parent has to be on the same axis. An Azure
			// virtual network names its resource group, but the resource
			// group groups by ownership rather than by network, so it is not
			// the virtual network's parent — it is a different answer to a
			// different question.
			if providers.ContainerAxis(target.typ) != g.Groups[i].Axis {
				continue
			}
			parent := d.target
			g.Groups[i].Parent = &parent
			break
		}
	}

	for _, r := range resources {
		for _, d := range deps[r.address] {
			// Containment already says "this lives inside that", so drawing it
			// again as an arrow would add a line to every box in the diagram.
			// That only holds while containment can actually express it: across
			// a provider boundary it is refused, and then the edge is the only
			// remaining way to say the reference happened.
			if isGroup[d.target] {
				target, known := byAddress[d.target]
				sameProvider := known && target.provider == r.provider
				if sameProvider {
					continue
				}
			}
			g.Edges = append(g.Edges, core.Edge{
				From:  r.address,
				To:    d.target,
				Kind:  core.EdgeIACRef,
				Attrs: map[string]any{"attribute": d.attribute},
			})
		}
	}

	g.Axes = []core.Axis{
		{ID: core.AxisNetwork, Label: "Network topology"},
		{ID: core.AxisProvider, Label: "Provider"},
		{ID: core.AxisModule, Label: "Module"},
	}
	// Only declare an extra axis if some container actually asked for it, so a
	// pure-AWS graph does not advertise an account axis it has nothing to put
	// on.
	for axis := range containerAxes {
		if !g.HasAxis(axis) {
			g.Axes = append(g.Axes, core.Axis{ID: axis, Label: axisLabel(axis)})
		}
	}

	g.Normalize()

	// Membership is resolved once per axis rather than once overall. A
	// resource can sit in a subnet and in a resource group at the same time
	// and both are true, and each answer has to be searched for separately or
	// the nearer container would hide the other.
	axes := []string{core.AxisNetwork}
	for axis := range containerAxes {
		if axis != core.AxisNetwork {
			axes = append(axes, axis)
		}
	}
	sort.Strings(axes)

	for _, axis := range axes {
		membership := resolveMembership(axis, resources, deps, isGroup, byAddress)
		if err := g.AssignGroupPaths(axis, membership); err != nil {
			return nil, err
		}
	}

	addProviderAxis(g)
	addModuleAxis(g)

	if opts.Scope != "" {
		g.ApplyScope(opts.Scope)
		g.Metadata.Scope = opts.Scope
	}

	g.Normalize()
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// addProviderAxis puts every node under the provider it came from. In an estate
// that mixes on-premises with several clouds, this is usually the first view
// anyone wants, and it needs no configuration to produce.
func addProviderAxis(g *core.Graph) {
	seen := map[string]bool{}
	for i := range g.Nodes {
		p := g.Nodes[i].Provider
		if p == "" {
			continue
		}
		id := core.AxisProvider + ":" + p
		if !seen[p] {
			seen[p] = true
			g.Groups = append(g.Groups, core.Group{
				ID:    id,
				Axis:  core.AxisProvider,
				Type:  "provider",
				Label: p,
			})
		}
		g.Nodes[i].SetGroup(core.AxisProvider, id)
	}
}

// addModuleAxis rebuilds Terraform's module tree as a grouping. A large estate
// is mostly modules, and "what does this module own" is a question the network
// topology cannot answer — the resources of one module are routinely scattered
// across several subnets, and several modules share one.
func addModuleAxis(g *core.Graph) {
	created := map[string]bool{}

	for i := range g.Nodes {
		path := modulePath(g.Nodes[i].ID)
		if len(path) == 0 {
			continue
		}

		var parent *string
		var ids []string
		for depth := range path {
			addr := strings.Join(path[:depth+1], ".")
			id := core.AxisModule + ":" + addr
			if !created[id] {
				created[id] = true
				p := parent
				g.Groups = append(g.Groups, core.Group{
					ID:     id,
					Axis:   core.AxisModule,
					Type:   "module",
					Label:  path[depth],
					Parent: p,
				})
			}
			ids = append(ids, id)
			cur := id
			parent = &cur
		}
		g.Nodes[i].SetGroup(core.AxisModule, strings.Join(ids, core.GroupSeparator))
	}
}

// modulePath returns the module call names enclosing a resource address, so
// `module.platform.module.network.aws_subnet.a` yields ["module.platform",
// "module.network"]. A root-module resource yields nothing.
func modulePath(address string) []string {
	var out []string
	parts := splitAddress(address)
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] != "module" {
			break
		}
		out = append(out, "module."+parts[i+1])
	}
	return out
}

type dependency struct {
	target    string
	attribute string
}

// resolveMembership works out which containers each node belongs to.
//
// It walks references outward one hop at a time and stops at the first distance
// where it finds any container. An EC2 instance names its subnet directly, so
// it lands in that subnet. An RDS instance names a DB subnet group, which names
// two subnets, so it lands one hop out and ends up spanning both. Taking the
// nearest hop rather than everything reachable is what keeps the instance in
// its subnet instead of floating up to the VPC.
//
// The walk never leaves the resource's own provider. Referencing something is
// not the same as living inside it, and across a provider boundary the two come
// apart completely: an on-premises VM that reads from an RDS instance is a
// perfectly ordinary arrangement, but it does not put the VM in an AWS subnet.
// Without this stop, one such reference was enough to draw a vSphere machine
// inside a VPC.
func resolveMembership(axis string, resources []resource, deps map[string][]dependency, isGroup map[string]bool, byAddress map[string]*resource) map[string][]string {
	membership := map[string][]string{}

	// Only containers on this axis end the walk. Containers on another axis
	// are walked *through* rather than stopped at, which is what lets an Azure
	// virtual machine reach its subnet: it names its resource group directly,
	// and if that counted as an answer the search would stop one hop short of
	// the network topology every time.
	onThisAxis := func(addr string) bool {
		r, ok := byAddress[addr]
		return ok && isGroup[addr] && providers.ContainerAxis(r.typ) == axis
	}

	for _, r := range resources {
		if isGroup[r.address] {
			continue
		}

		frontier := []string{r.address}
		visited := map[string]bool{r.address: true}

		for len(frontier) > 0 && membership[r.address] == nil {
			var next []string
			var found []string

			for _, addr := range frontier {
				for _, d := range deps[addr] {
					if visited[d.target] {
						continue
					}
					visited[d.target] = true

					target, known := byAddress[d.target]
					if known && target.provider != r.provider {
						continue
					}

					if onThisAxis(d.target) {
						found = append(found, d.target)
						continue
					}
					if known && providers.IsAttachment(target.typ) {
						continue
					}
					next = append(next, d.target)
				}
			}

			if len(found) > 0 {
				sort.Strings(found)
				membership[r.address] = found
				break
			}
			frontier = next
		}
	}

	return membership
}

// axisLabel gives a derived axis a readable name for a view selector.
func axisLabel(axis string) string {
	switch axis {
	case core.AxisAccount:
		return "Account / resource group"
	default:
		return axis
	}
}

// flatten walks the module tree and returns every resource worth drawing.
func flatten(m *tfjson.StateModule, opts Options) ([]resource, error) {
	var out []resource

	for _, r := range m.Resources {
		if r == nil {
			continue
		}
		if r.Mode != tfjson.ManagedResourceMode &&
			(!opts.IncludeDataSources || r.Mode != tfjson.DataResourceMode) {
			continue
		}
		values, err := redactSensitiveValues(r.AttributeValues, r.SensitiveValues)
		if err != nil {
			return nil, fmt.Errorf("resource %s: parsing sensitive_values: %w", r.Address, err)
		}
		out = append(out, resource{
			address:  escapeAddress(r.Address),
			typ:      r.Type,
			name:     r.Name,
			mode:     r.Mode,
			values:   values,
			provider: providerOf(r.ProviderName),
		})
	}

	for _, child := range m.ChildModules {
		if child != nil {
			resources, err := flatten(child, opts)
			if err != nil {
				return nil, err
			}
			out = append(out, resources...)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].address < out[j].address })
	return out, nil
}

// redactSensitiveValues removes a complete top-level attribute when any leaf
// below it is marked sensitive. Terraform's sensitivity document mirrors the
// value shape, so dropping the enclosing attribute is deliberately
// conservative and guarantees that partially-sensitive objects cannot leak a
// sibling secret through labels, inferred dependencies, or curated attrs.
func redactSensitiveValues(values map[string]any, raw json.RawMessage) (map[string]any, error) {
	if len(values) == 0 {
		return values, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return values, nil
	}

	var mask map[string]any
	if err := json.Unmarshal(raw, &mask); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		if containsSensitiveValue(mask[key]) {
			continue
		}
		out[key] = value
	}
	return out, nil
}

func containsSensitiveValue(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case map[string]any:
		for _, child := range value {
			if containsSensitiveValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsSensitiveValue(child) {
				return true
			}
		}
	}
	return false
}

type reference struct {
	// target is the raw reference string, e.g. "aws_vpc.main.id".
	target string
	// attribute is the top-level expression it appeared under, e.g. "vpc_id".
	// Nested blocks report the block name, e.g. "network_configuration".
	attribute string
}

// collectReferences walks a configuration module and returns, for each resource
// address, the references its expressions make. Child modules are walked with
// their address prefixed, so addresses match the ones planned_values reports.
func collectReferences(m *tfjson.ConfigModule, prefix string) map[string][]reference {
	out := map[string][]reference{}

	for _, r := range m.Resources {
		if r == nil {
			continue
		}
		addr := prefix + r.Address
		var refs []reference
		for attr, expr := range r.Expressions {
			refs = append(refs, walkExpression(attr, expr, prefix)...)
		}
		for _, dep := range r.DependsOn {
			refs = append(refs, reference{target: prefix + dep, attribute: "depends_on"})
		}
		if len(refs) > 0 {
			sort.Slice(refs, func(i, j int) bool {
				if refs[i].target != refs[j].target {
					return refs[i].target < refs[j].target
				}
				return refs[i].attribute < refs[j].attribute
			})
			out[addr] = refs
		}
	}

	for name, call := range m.ModuleCalls {
		if call == nil || call.Module == nil {
			continue
		}
		childPrefix := prefix + "module." + name + "."
		for addr, refs := range collectReferences(call.Module, childPrefix) {
			out[addr] = append(out[addr], refs...)
		}
	}

	return out
}

// walkExpression pulls references out of an expression, descending into nested
// blocks such as an ECS service's network_configuration.
func walkExpression(attr string, expr *tfjson.Expression, prefix string) []reference {
	if expr == nil || expr.ExpressionData == nil {
		return nil
	}

	var out []reference
	for _, r := range expr.References {
		// var.*, local.*, each.* and count.* do not name a resource; module.*
		// crosses a boundary this version does not follow.
		if isNonResourceRef(r) {
			continue
		}
		out = append(out, reference{target: prefix + r, attribute: attr})
	}
	for _, block := range expr.NestedBlocks {
		for _, nested := range block {
			out = append(out, walkExpression(attr, nested, prefix)...)
		}
	}
	return out
}

func isNonResourceRef(ref string) bool {
	for _, p := range []string{"var.", "local.", "each.", "count.", "self.", "path.", "terraform.", "module."} {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return ref == "count" || ref == "each"
}

// instantiateReference restores the concrete module instance path that the
// configuration JSON omits. For example, a reference collected as
// module.workload.aws_subnet.main from a resource planned under
// module.workload[1] must stay inside module.workload[1], not fan out across
// every instance of the module call.
func instantiateReference(ref, configAddress, instanceAddress string) string {
	configModule := modulePrefix(configAddress)
	instanceModule := modulePrefix(instanceAddress)
	if configModule != "" && strings.HasPrefix(ref, configModule) {
		return instanceModule + escapeAddress(strings.TrimPrefix(ref, configModule))
	}
	return escapeAddress(ref)
}

// resolve maps a reference string onto the resource instances it names.
// An explicitly-indexed reference names exactly one address; an unindexed
// reference expands to all instances of the resource. Prefix matching avoids
// splitting dots that legitimately occur inside quoted for_each keys.
func resolve(ref string, instances map[string][]string, addresses map[string]bool) []string {
	segments := splitAddress(ref)
	for i := len(segments); i >= 2; i-- {
		candidate := strings.Join(segments[:i], ".")
		if addresses[candidate] {
			return []string{candidate}
		}
		if addrs, ok := instances[candidate]; ok {
			return addrs
		}
	}
	return nil
}

// stripIndex turns an instance address back into its resource address:
// `aws_subnet.public[0]` becomes `aws_subnet.public`.
func stripIndex(addr string) string {
	if i := strings.LastIndex(addr, "["); i > 0 && strings.HasSuffix(addr, "]") {
		return addr[:i]
	}
	return addr
}

// stripAllIndexes maps a concrete planned address back to the corresponding
// configuration address. Configuration represents a counted/for_each module
// only once even though planned_values includes its instance key at every
// level.
func stripAllIndexes(address string) string {
	var out strings.Builder
	depth := 0
	inString := false
	escaped := false
	for _, r := range address {
		if depth == 0 {
			if r == '[' {
				depth = 1
				continue
			}
			out.WriteRune(r)
			continue
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch r {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
		}
	}
	return out.String()
}

// modulePrefix returns the concrete module portion of a Terraform address,
// including instance keys, with a trailing dot. Dots inside quoted for_each
// keys do not split address segments.
func modulePrefix(address string) string {
	segments := splitAddress(address)
	i := 0
	for i+1 < len(segments) && segments[i] == "module" {
		i += 2
	}
	if i == 0 {
		return ""
	}
	return strings.Join(segments[:i], ".") + "."
}

func splitAddress(address string) []string {
	var segments []string
	start, depth := 0, 0
	inString := false
	escaped := false
	for i, r := range address {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch r {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			if depth > 0 {
				inString = true
			}
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '.':
			if depth == 0 {
				segments = append(segments, address[start:i])
				start = i + 1
			}
		}
	}
	return append(segments, address[start:])
}

// Group paths use a slash separator, while Terraform permits slashes inside
// quoted for_each keys. Percent-encoding only the two ambiguous characters
// keeps ordinary addresses unchanged and makes the transformation collision
// free (`%2F` in the original becomes `%252F`).
func escapeAddress(address string) string {
	address = strings.ReplaceAll(address, "%", "%25")
	return strings.ReplaceAll(address, core.GroupSeparator, "%2F")
}

// label prefers the Name tag, because that is what operators call the resource
// in the console, and falls back to the Terraform resource name.
func label(r resource) string {
	if tags, ok := r.values["tags"].(map[string]any); ok {
		if name, ok := tags["Name"].(string); ok && name != "" {
			return name
		}
	}
	if name, ok := r.values["name"].(string); ok && name != "" {
		return name
	}
	return r.name
}

// pickAttrs copies the attributes the provider profile asks for, skipping nulls
// and empty collections so the IR stays readable as a diff.
func pickAttrs(r resource) map[string]any {
	keys := providers.Attrs(r.typ)
	if len(keys) == 0 {
		return nil
	}

	out := map[string]any{}
	for _, k := range keys {
		v, ok := r.values[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if t == "" {
				continue
			}
		case []any:
			if len(t) == 0 {
				continue
			}
		case map[string]any:
			if len(t) == 0 {
				continue
			}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
