// Package providers holds what oekaki knows about each infrastructure
// provider, and nothing else.
//
// It sits below both the parsers and the renderers on purpose. Before this
// package existed, "what is aws_ecs_service" was answered in two places —
// parsers/terraform decided which attributes to keep, renderers/style decided
// what colour to draw it — and the two tables had already drifted: sixteen
// types were listed in both and eighteen in only one, so some resources had a
// colour but no attributes and others the reverse.
//
// Splitting by concern instead of by subject is what caused that. Everything
// known about a resource type now lives in one place, and a provider is added
// by adding a file here rather than by editing the parser.
package providers

import (
	"strings"

	"github.com/imohiyoko/oekaki/core"
)

// Category groups resource types into the handful of things an operator
// actually distinguishes at a glance. Renderers map it to colour; nothing here
// knows what a colour is.
type Category string

const (
	Network  Category = "network"
	Compute  Category = "compute"
	Database Category = "database"
	Security Category = "security"
	Storage  Category = "storage"
	Generic  Category = "generic"
)

// Container describes a resource type that holds other resources.
type Container struct {
	// Type is the IR group type this becomes, e.g. "vpc" or "namespace".
	// It is the label a reader sees on the container's border, so it should
	// be the word that provider's users would use.
	Type string

	// Axis is which grouping this container belongs to. Empty means the
	// network axis, which is what most containers are.
	//
	// Not every container is a network container. An Azure resource group
	// holds a virtual network but is not part of one; it cuts across the
	// network topology rather than nesting inside it. Put on the network axis
	// it would swallow the subnets, because a virtual machine names its
	// resource group directly and reaches its subnet only through a network
	// interface — so the nearer container would win and the machine would lose
	// its subnet. On its own axis it answers a different question without
	// damaging the first.
	Axis string
}

// Profile is everything oekaki knows about one provider.
//
// It is data, not behaviour. A provider whose containment needs real logic —
// Azure's resource groups cut across virtual networks rather than nesting with
// them — is the signal to give that provider a package of its own rather than
// to add a hook here.
type Profile struct {
	// Name is the short provider name as Terraform reports it, e.g. "aws".
	Name string

	// Prefixes are the resource type prefixes this profile claims, e.g.
	// "aws_". Matching is by prefix rather than by provider name because
	// renderers see only a resource type.
	Prefixes []string

	// Containers are the types that become groups rather than nodes.
	Containers map[string]Container

	// Attachments are types that every member of a network points at, which
	// makes them useless for working out where a resource lives. Containment
	// resolution refuses to walk through them: following a security group from
	// an instance leads straight back to the VPC and loses the fact that the
	// instance is in one specific subnet.
	Attachments map[string]bool

	// Attrs lists, per resource type, the attributes worth copying into the
	// IR. Deliberately short: the IR is meant to be reviewed as a diff, and
	// carrying every computed attribute would bury the signal.
	Attrs map[string][]string

	// Categories assigns a drawing category. Types absent here fall back to
	// the heuristics in CategoryOf, and ultimately to Generic.
	Categories map[string]Category

	// Identities records what a resource is called outside its IaC: per
	// resource type, a selector key an overlay may use, mapped to the node
	// attribute holding that key's value. A dotted path descends into nested
	// blocks.
	//
	// This is the join between the IR and an overlay written by someone
	// reading an operations console, who knows a workload's name but not its
	// Terraform address. The attribute it names must also appear in Attrs, or
	// the value never reaches the IR and the join can never fire — which is
	// the same two-tables-drift failure this package was created to end, so
	// there is a test for it.
	//
	// Still data. A provider that needs a template rather than a lookup —
	// deriving "/aws/lambda/<name>" instead of reading an attribute — is the
	// signal to give that provider a package, not to add a hook here.
	Identities map[string]map[string]string

	// Highlights are the types worth naming in documentation as first-class.
	// Everything else still renders; this is about what to promise.
	Highlights []string
}

// registry is ordered by registration. Lookup takes the first prefix match, so
// a more specific prefix must be registered before a more general one.
var registry []*Profile

// Register adds a profile. Called from each provider file's init.
func Register(p *Profile) { registry = append(registry, p) }

// All returns every registered profile, in registration order.
func All() []*Profile { return registry }

// Lookup finds the profile claiming a resource type, or nil.
func Lookup(resourceType string) *Profile {
	for _, p := range registry {
		for _, prefix := range p.Prefixes {
			if strings.HasPrefix(resourceType, prefix) {
				return p
			}
		}
	}
	return nil
}

// ContainerType reports the IR group type a resource type becomes, if it is a
// container at all.
func ContainerType(resourceType string) (string, bool) {
	p := Lookup(resourceType)
	if p == nil {
		return "", false
	}
	c, ok := p.Containers[resourceType]
	return c.Type, ok
}

// ContainerAxis reports which axis a container type groups on. Callers should
// check IsContainer first; a non-container reports the network axis, which is
// harmless but meaningless.
func ContainerAxis(resourceType string) string {
	p := Lookup(resourceType)
	if p == nil {
		return core.AxisNetwork
	}
	if c, ok := p.Containers[resourceType]; ok && c.Axis != "" {
		return c.Axis
	}
	return core.AxisNetwork
}

// IsContainer reports whether a resource type becomes a group.
func IsContainer(resourceType string) bool {
	_, ok := ContainerType(resourceType)
	return ok
}

// IsAttachment reports whether containment resolution should refuse to walk
// through a resource type.
func IsAttachment(resourceType string) bool {
	p := Lookup(resourceType)
	return p != nil && p.Attachments[resourceType]
}

// Attrs returns the attributes worth carrying for a resource type.
func Attrs(resourceType string) []string {
	p := Lookup(resourceType)
	if p == nil {
		return nil
	}
	return p.Attrs[resourceType]
}

// CategoryOf classifies a resource type for drawing.
//
// An unknown type falls back to Generic rather than being rejected: covering
// every provider resource is explicitly not a goal, and a generic box is more
// useful than an error.
func CategoryOf(resourceType string) Category {
	if p := Lookup(resourceType); p != nil {
		if c, ok := p.Categories[resourceType]; ok {
			return c
		}
	}

	// Substring rules catch the long tail. They are guesses, and they are
	// applied only after the explicit table has had its say.
	switch {
	case strings.Contains(resourceType, "security_group"),
		strings.Contains(resourceType, "firewall"),
		strings.Contains(resourceType, "_iam_"):
		return Security
	case strings.Contains(resourceType, "_db_"),
		strings.Contains(resourceType, "_rds_"),
		strings.Contains(resourceType, "database"),
		strings.Contains(resourceType, "_sql_"):
		return Database
	case strings.Contains(resourceType, "_lb_"),
		strings.Contains(resourceType, "_vpc_"),
		strings.Contains(resourceType, "subnet"),
		strings.Contains(resourceType, "network"):
		return Network
	case strings.Contains(resourceType, "bucket"),
		strings.Contains(resourceType, "volume"),
		strings.Contains(resourceType, "disk"):
		return Storage
	case strings.Contains(resourceType, "instance"),
		strings.Contains(resourceType, "_vm"),
		strings.Contains(resourceType, "virtual_machine"),
		strings.Contains(resourceType, "container"),
		strings.Contains(resourceType, "function"):
		return Compute
	}
	return Generic
}

// Highlights lists the resource types documentation calls first-class, across
// every registered provider.
func Highlights() []string {
	var out []string
	for _, p := range registry {
		out = append(out, p.Highlights...)
	}
	return out
}

// ShortType trims the provider prefix so labels read "ecs_service" rather than
// "aws_ecs_service". The prefix is the same on every box from that provider, so
// it carries no information while taking up horizontal space.
func ShortType(resourceType string) string {
	if p := Lookup(resourceType); p != nil {
		for _, prefix := range p.Prefixes {
			if trimmed, ok := strings.CutPrefix(resourceType, prefix); ok {
				return trimmed
			}
		}
	}
	return resourceType
}
