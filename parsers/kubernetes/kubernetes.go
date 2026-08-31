// Package kubernetes turns Kubernetes manifests into oekaki's IR.
//
// The input is a stream of YAML documents — what `helm template`, `kustomize
// build` or `kubectl get -o yaml` writes, or a repository's manifests
// concatenated. Nothing is read from a cluster and no kubeconfig is touched.
//
// What it recovers is what one object says about another. A Service names the
// labels it selects, an Ingress names the Service it routes to, a pod template
// names the ConfigMaps, Secrets, volumes and ServiceAccount it needs. Those
// are iac_ref edges: delete the target and the source breaks. Traffic between
// two workloads is not inferred. Manifests do not record it, and an edge
// invented here would arrive in the same colour as one that was read.
//
// Every apiVersion it understands is listed in versions.go with the release
// that started serving it and, where it applies, the one that stopped. An
// object whose apiVersion no current cluster serves still becomes a node,
// carrying the release that removed it; so does one this parser has never
// heard of. Dropping either would turn a finding into an absence.
package kubernetes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/imohiyoko/oekaki/core"
)

// Options tunes a parse.
type Options struct {
	// File names the document the manifests came from, so nodes can point back
	// at it. A stream read from a pipe has no name of its own.
	File string

	// DefaultNamespace is given to namespaced objects that do not name one,
	// the way a cluster applies the context's namespace at apply time. Empty
	// means "default", which is what kubectl would use.
	DefaultNamespace string

	// Scope names the estate and qualifies every id with it, so manifests from
	// two clusters can be combined without one cluster's `deployment/shop/api`
	// silently merging with the other's.
	Scope string
}

// Result reports what a parse found and what it could not place, alongside the
// graph. The counts are what a caller prints; nothing here changes the graph.
type Result struct {
	Graph *core.Graph

	// Objects is how many objects were read. A List document holds several,
	// and empty documents — which `helm template` emits freely — hold none,
	// so this is not a count of YAML documents.
	Objects int

	// MinimumRelease is the oldest Kubernetes release that serves every
	// apiVersion in the input. Empty when nothing recognised was found, and
	// empty when no release serves them all: see Incompatible.
	MinimumRelease string

	// Incompatible is set when the input mixes apiVersions that no single
	// release serves — something here needs a cluster at or past the release
	// that stopped serving something else here. The floor and the ceiling are
	// reported so the contradiction can be read rather than guessed at.
	Incompatible bool
	Floor        string
	Ceiling      string

	// Removed lists objects whose apiVersion is no longer served by
	// SupportedThrough, and Unknown those whose apiVersion this parser does
	// not know. Both are drawn; both are worth saying out loud.
	Removed []string
	Unknown []string

	// Skipped lists documents that held something this could not turn into an
	// object: YAML that would not decode, a List item that is not a mapping,
	// an object with only a generateName and therefore no id yet. They are
	// counted because "nothing is dropped" is a claim this package makes, and
	// a silent skip is how that claim stops being true.
	Skipped []string

	// Duplicates lists objects the input defines more than once. Concatenating
	// a base and an overlay does that, and so do two chart renders sharing a
	// namespace-level ConfigMap. The first definition is drawn and the rest
	// are counted: an IR error about a duplicate id would report the tool's
	// problem instead of the input's.
	Duplicates []string
}

// builder carries the state one parse needs: the graph being filled, the
// objects indexed by the id they will have, and the edges already drawn. The
// index is what lets a reference find an object rather than guess at its id,
// and what keeps a large input from costing a scan per reference.
type builder struct {
	g    *core.Graph
	opts Options
	all  []object
	byID map[string]*object

	// nsLabels are the labels of the Namespace objects in the input. A policy
	// naming a namespaceSelector can only be evaluated against namespaces
	// somebody sent; there is no way to look one up.
	nsLabels map[string]map[string]string
	edges    map[string]bool
}

// object is one decoded manifest, kept as a map because the fields worth
// reading differ per kind and a struct per kind would be a great deal of code
// to say the same few things.
type object struct {
	apiVersion string
	kind       string
	name       string
	namespace  string
	body       map[string]any
	api        API
	known      bool
	line       int
}

// Parse reads a stream of YAML documents.
func Parse(raw []byte, opts Options) (*Result, error) {
	objects, skipped, err := decode(raw, opts)
	if err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		// The reasons are part of the message. "Every document is missing
		// apiVersion" is a specific claim, and it is wrong for a file whose
		// documents were skipped for some other reason.
		if len(skipped) > 0 {
			return nil, fmt.Errorf("no Kubernetes objects: %s", strings.Join(skipped, "; "))
		}
		return nil, errors.New("no Kubernetes objects: the input holds no documents")
	}

	g := core.New()
	g.Metadata = &core.Metadata{Source: "kubernetes", Scope: opts.Scope}
	g.Axes = []core.Axis{{ID: core.AxisNetwork, Label: "Namespace"}}

	// The duplicates are dropped before anything else looks at the list. A
	// second definition left in it would go on matching label selectors,
	// which then draw edges to the first definition's node: an object that
	// was not used deciding what an object that was used connects to.
	unique := make([]object, 0, len(objects))
	res := &Result{Graph: g, Objects: len(objects), Skipped: skipped}
	seenID := map[string]bool{}
	for _, o := range objects {
		if seenID[o.id()] {
			res.Duplicates = append(res.Duplicates, o.id())
			continue
		}
		seenID[o.id()] = true
		unique = append(unique, o)
	}

	b := &builder{g: g, opts: opts, all: unique, byID: map[string]*object{},
		nsLabels: map[string]map[string]string{}, edges: map[string]bool{}}
	for i := range unique {
		o := &unique[i]
		b.byID[o.id()] = o
		if o.known && o.api.Removed != "" && !o.api.Served(SupportedThrough) {
			res.Removed = append(res.Removed, o.id())
		}
		if !o.known {
			res.Unknown = append(res.Unknown, o.id())
			continue
		}
		if compare(o.api.Since, res.Floor) > 0 {
			res.Floor = o.api.Since
		}
		if o.api.Removed != "" && (res.Ceiling == "" || compare(o.api.Removed, res.Ceiling) < 0) {
			res.Ceiling = o.api.Removed
		}
	}
	// The releases that accept all of this are [floor, ceiling). When the
	// ceiling is at or below the floor that interval is empty: one object
	// needs a release that another object was already removed from. Reporting
	// the floor anyway would name a cluster that will refuse half the input.
	res.Incompatible = res.Ceiling != "" && res.Floor != "" && compare(res.Floor, res.Ceiling) >= 0
	if !res.Incompatible {
		res.MinimumRelease = res.Floor
	}
	g.Metadata.SourceVersion = res.MinimumRelease

	for i := range unique {
		b.add(&unique[i])
	}
	for i := range unique {
		b.relate(&unique[i])
	}

	sort.Strings(res.Removed)
	sort.Strings(res.Unknown)
	sort.Strings(res.Duplicates)
	g.Normalize()
	if opts.Scope != "" {
		g.ApplyScope(opts.Scope)
	}
	return res, nil
}

// decode splits the stream into objects. A document that is empty, or that is
// a List, or that is not a Kubernetes object at all, is skipped rather than
// failing the parse: manifest streams routinely carry all three.
func decode(raw []byte, opts Options) ([]object, []string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var out []object
	var skipped []string
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("reading YAML: %w", err)
		}
		var body map[string]any
		if err := doc.Decode(&body); err != nil {
			// A document that is a scalar or a list is not an object. Empty
			// ones are not worth mentioning; anything else is.
			if doc.Kind != 0 && (doc.Kind != yaml.DocumentNode || len(doc.Content) > 0) {
				skipped = append(skipped, fmt.Sprintf("line %d: not a Kubernetes object", doc.Line))
			}
			continue
		}
		items, dropped := flatten(body)
		for _, why := range dropped {
			skipped = append(skipped, fmt.Sprintf("line %d: %s", doc.Line, why))
		}
		for _, item := range items {
			o := object{
				apiVersion: str(item, "apiVersion"),
				kind:       str(item, "kind"),
				name:       str(item, "metadata", "name"),
				namespace:  str(item, "metadata", "namespace"),
				body:       item,
				line:       doc.Line,
			}
			if o.apiVersion == "" || o.kind == "" {
				skipped = append(skipped, fmt.Sprintf("line %d: no apiVersion or kind", doc.Line))
				continue
			}
			if o.name == "" {
				// An object with only a generateName has no name until the
				// cluster gives it one, so it has no id here either. It is
				// reported rather than dropped: an object nobody mentioned is
				// indistinguishable from an object that was not there.
				what := o.kind
				if g := str(item, "metadata", "generateName"); g != "" {
					what += " " + g + "*"
				}
				skipped = append(skipped, fmt.Sprintf("line %d: %s has no name", doc.Line, what))
				continue
			}
			o.api, o.known = lookup(o.apiVersion, o.kind)
			// The default namespace is only supplied for kinds known to live
			// in one. A ClusterRole or a PersistentVolume does not, and a kind
			// this build has never heard of might not either — filing one
			// under `default` would put it in a namespace that never held it,
			// and the diagram would not admit the guess.
			if o.known && !o.api.Namespaced {
				// A cluster-scoped object does not live in a namespace, and a
				// manifest that names one anyway is ignored by the cluster.
				// Honouring it here would file the object somewhere the
				// cluster never puts it.
				o.namespace = ""
			}
			if o.namespace == "" && o.known && o.api.Namespaced {
				o.namespace = opts.DefaultNamespace
				if o.namespace == "" {
					o.namespace = "default"
				}
			}
			out = append(out, o)
		}
	}
	return out, skipped, nil
}

// flatten unwraps a List, which is what `kubectl get -o yaml` returns when it
// is asked for more than one object.
func flatten(body map[string]any) ([]map[string]any, []string) {
	// A List is recognised by holding items, not by its name alone. A custom
	// resource called AllowList is an object, and dropping it would lose the
	// whole document while claiming nothing was there.
	if !strings.HasSuffix(str(body, "kind"), "List") || dig(body, "items") == nil {
		return []map[string]any{body}, nil
	}
	items, isList := dig(body, "items").([]any)
	if !isList {
		// `items: {}` is a List with nothing readable in it, which is not the
		// same as a List with nothing in it.
		return nil, []string{"a List whose items are not a sequence"}
	}
	var out []map[string]any
	var skipped []string
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			skipped = append(skipped, fmt.Sprintf("item %d of a List is not an object", i))
			continue
		}
		out = append(out, m)
	}
	return out, skipped
}

// id is the stable identity: kind, namespace and name, the three things that
// make a Kubernetes object unique. Cluster-scoped objects have two.
func (o *object) id() string {
	kind := strings.ToLower(o.kind)
	if o.kind == "Namespace" || o.namespace == "" {
		return kind + "/" + o.name
	}
	return kind + "/" + o.namespace + "/" + o.name
}

// add turns one object into a node, or into a group when it is a Namespace.
func (b *builder) add(o *object) {
	if o.kind == "Namespace" {
		b.ensureNamespace(o.name, b.source(o.line))
		b.nsLabels[o.name] = strMap(o.body, "metadata", "labels")
		return
	}
	b.ensureNamespace(o.namespace, nil)

	attrs := map[string]any{"apiVersion": o.apiVersion, "kind": o.kind}
	if o.namespace != "" {
		attrs["namespace"] = o.namespace
	}
	if !o.known {
		attrs["api_unknown"] = true
	} else if o.api.Removed != "" {
		attrs["api_removed_in"] = o.api.Removed
	}
	if image := firstImage(o); image != "" {
		attrs["image"] = image
	}
	if replicas, ok := num(o.body, "spec", "replicas"); ok {
		attrs["replicas"] = replicas
	}

	b.g.Nodes = append(b.g.Nodes, core.Node{
		ID:       o.id(),
		Type:     strings.ToLower(o.kind),
		Name:     o.name,
		Provider: "kubernetes",
		Groups:   placement(o.namespace),
		Attrs:    attrs,
		Source:   b.source(o.line),
	})
}

// relate reads one object's references to others.
func (b *builder) relate(o *object) {
	switch o.kind {
	case "Service":
		b.selects(o)
	case "Ingress":
		b.routes(o)
	case "HorizontalPodAutoscaler":
		b.scales(o)
	case "NetworkPolicy":
		b.restricts(o)
	case "PersistentVolumeClaim":
		b.claims(o)
	case "ServiceAccount":
		b.identity(o)
	case "RoleBinding", "ClusterRoleBinding":
		b.grants(o)
	}
	if o.kind == "StatefulSet" {
		// The governing Service is what gives a StatefulSet's pods their
		// names. It is usually headless, so nothing else in the graph links
		// the two.
		if name := str(o.body, "spec", "serviceName"); name != "" {
			b.edge(o.id(), b.reference("Service", o.namespace, name), "governed-by", nil)
		}
	}
	if spec := podSpec(o); spec != nil {
		b.mounts(o, spec)
		if class := str(spec, "priorityClassName"); class != "" {
			b.edge(o.id(), b.reference("PriorityClass", "", class), "prioritised-by", nil)
		}
	}
	b.owners(o)
}

// selects joins a Service to the workloads whose pod template carries every
// label in its selector. A Service with no selector — ExternalName, or
// Endpoints managed by hand — selects nothing, and saying so is not the same
// as finding nothing.
func (b *builder) selects(svc *object) {
	selector, whole := strMapAll(svc.body, "spec", "selector")
	if len(selector) == 0 && whole {
		b.setAttr(svc.id(), "selects", "nothing: no selector")
		return
	}
	// A selector is matched only when every pair of it was readable. Dropping
	// an unreadable pair would widen the match rather than narrow it, and the
	// extra workloads would arrive looking exactly like the right ones.
	if !whole {
		b.setAttr(svc.id(), "selects", "not resolved: part of the selector is not a string")
		return
	}
	matched := 0
	for i := range b.all {
		target := &b.all[i]
		if target.namespace != svc.namespace || podLabels(target) == nil {
			continue
		}
		if !covers(podLabels(target), selector) {
			continue
		}
		matched++
		b.edge(svc.id(), target.id(), "selects", map[string]any{"selector": joined(selector)})
	}
	if matched == 0 {
		b.setAttr(svc.id(), "selects", "nothing in this input matches "+joined(selector))
	}
}

// routes joins an Ingress to the Services it names. Both the v1 shape
// (backend.service.name) and the v1beta1 one (backend.serviceName) are read,
// because a repository that still has the older shape is exactly the
// repository that needs to see it drawn.
func (b *builder) routes(ing *object) {
	backends := map[string]string{}
	collect := func(backend any, where string) {
		name := str(backend, "service", "name")
		if name == "" {
			name = str(backend, "serviceName")
		}
		if name != "" {
			backends[name] = where
		}
	}
	collect(dig(ing.body, "spec", "defaultBackend"), "default backend")
	collect(dig(ing.body, "spec", "backend"), "default backend")
	for _, rule := range seq(ing.body, "spec", "rules") {
		for _, p := range seq(rule, "http", "paths") {
			// A rule that names neither host nor path still is not the
			// default backend: it catches everything, which is a different
			// statement from catching what nothing else did.
			where := str(rule, "host") + str(p, "path")
			if where == "" {
				where = "any host and path"
			}
			collect(dig(p, "backend"), where)
		}
	}
	for _, name := range sortedKeys(backends) {
		to := b.reference("Service", ing.namespace, name)
		b.edge(ing.id(), to, "routes", map[string]any{"via": backends[name]})
	}

	// The certificate an Ingress presents is a Secret it cannot start without,
	// and it is named nowhere else.
	for _, tls := range seq(ing.body, "spec", "tls") {
		if name := str(tls, "secretName"); name != "" {
			b.edge(ing.id(), b.reference("Secret", ing.namespace, name), "reads",
				map[string]any{"via": "tls"})
		}
	}
	if class := str(ing.body, "spec", "ingressClassName"); class != "" {
		b.edge(ing.id(), b.reference("IngressClass", "", class), "handled-by", nil)
	}
}

// scales joins an autoscaler to what it scales.
func (b *builder) scales(hpa *object) {
	kind := str(hpa.body, "spec", "scaleTargetRef", "kind")
	name := str(hpa.body, "spec", "scaleTargetRef", "name")
	if kind == "" || name == "" {
		return
	}
	to := b.reference(kind, hpa.namespace, name)
	b.edge(hpa.id(), to, "scales", nil)

	// An autoscaler can be driven by a metric belonging to some other object,
	// which makes that object something the workload's replica count depends
	// on.
	for _, m := range seq(hpa.body, "spec", "metrics") {
		k, n := str(m, "object", "describedObject", "kind"), str(m, "object", "describedObject", "name")
		if k != "" && n != "" {
			b.edge(hpa.id(), b.reference(k, hpa.namespace, n), "measures",
				map[string]any{"metric": str(m, "object", "metric", "name")})
		}
	}
}

// mounts reads a pod spec for everything it names: config, secrets, storage
// and the identity it runs as.
func (b *builder) mounts(o *object, spec any) {
	type ref struct{ kind, name, relation, how string }
	var refs []ref

	for _, key := range []string{"containers", "initContainers", "ephemeralContainers"} {
		for _, c := range seq(spec, key) {
			for _, e := range seq(c, "envFrom") {
				if n := str(e, "configMapRef", "name"); n != "" {
					refs = append(refs, ref{"ConfigMap", n, "reads", "envFrom"})
				}
				if n := str(e, "secretRef", "name"); n != "" {
					refs = append(refs, ref{"Secret", n, "reads", "envFrom"})
				}
			}
			for _, e := range seq(c, "env") {
				if n := str(e, "valueFrom", "configMapKeyRef", "name"); n != "" {
					refs = append(refs, ref{"ConfigMap", n, "reads", "env " + str(e, "name")})
				}
				if n := str(e, "valueFrom", "secretKeyRef", "name"); n != "" {
					refs = append(refs, ref{"Secret", n, "reads", "env " + str(e, "name")})
				}
			}
		}
	}
	for _, v := range seq(spec, "volumes") {
		switch {
		case str(v, "configMap", "name") != "":
			refs = append(refs, ref{"ConfigMap", str(v, "configMap", "name"), "reads", "volume " + str(v, "name")})
		case str(v, "secret", "secretName") != "":
			refs = append(refs, ref{"Secret", str(v, "secret", "secretName"), "reads", "volume " + str(v, "name")})
		case str(v, "persistentVolumeClaim", "claimName") != "":
			refs = append(refs, ref{"PersistentVolumeClaim", str(v, "persistentVolumeClaim", "claimName"), "mounts", "volume " + str(v, "name")})
		}
		// A projected volume holds the same references one level further in,
		// and names its Secret `name` rather than `secretName`. A workload
		// that mounts its config this way depends on it exactly as much.
		for _, src := range seq(v, "projected", "sources") {
			if n := str(src, "configMap", "name"); n != "" {
				refs = append(refs, ref{"ConfigMap", n, "reads", "projected volume " + str(v, "name")})
			}
			if n := str(src, "secret", "name"); n != "" {
				refs = append(refs, ref{"Secret", n, "reads", "projected volume " + str(v, "name")})
			}
		}
	}
	for _, s := range seq(spec, "imagePullSecrets") {
		if n := str(s, "name"); n != "" {
			refs = append(refs, ref{"Secret", n, "reads", "imagePullSecrets"})
		}
	}
	// The implicit `default` ServiceAccount is left out. Every pod has one, so
	// drawing it would add an arrow per workload that says nothing about this
	// particular workload.
	if n := str(spec, "serviceAccountName"); n != "" && n != "default" {
		refs = append(refs, ref{"ServiceAccount", n, "runs-as", "serviceAccountName"})
	}

	for _, r := range refs {
		to := b.reference(r.kind, o.namespace, r.name)
		b.edge(o.id(), to, r.relation, map[string]any{"via": r.how})
	}
}

// owners reads metadata.ownerReferences, which a cluster fills in and a
// repository does not. It is what makes a `kubectl get` dump show a
// ReplicaSet under its Deployment rather than beside it.
func (b *builder) owners(o *object) {
	for _, ref := range seq(o.body, "metadata", "ownerReferences") {
		kind, name := str(ref, "kind"), str(ref, "name")
		if kind == "" || name == "" {
			continue
		}
		to := b.reference(kind, o.namespace, name)
		b.edge(o.id(), to, "owned-by", nil)
	}
}

// reference resolves a named object, adding a placeholder node when the input
// does not contain it. A Deployment that mounts a Secret nobody committed is
// the most useful thing a manifest graph can show, and it can only show it if
// the missing end has somewhere to point.
//
// The id is built the way object.id builds one, which is why this cannot be a
// plain concatenation: a cluster-scoped object has no namespace segment, and
// composing one anyway produces an id that matches nothing in the input and a
// second, phantom box beside the real one.
func (b *builder) reference(kind, namespace, name string) string {
	lower := strings.ToLower(kind)
	// An object that is in the input decides its own id. Look for it in the
	// referring namespace first, then at cluster scope.
	for _, id := range []string{lower + "/" + namespace + "/" + name, lower + "/" + name} {
		if o, ok := b.byID[id]; ok {
			return o.id()
		}
	}

	// Absent. Where it would live is decided by the table when the kind is
	// known, and otherwise by whether the reference came from something that
	// has a namespace at all.
	api, known := lookupKind(kind)
	scoped := namespace
	if known && !api.Namespaced {
		scoped = ""
	}
	want := lower + "/" + name
	if scoped != "" {
		want = lower + "/" + scoped + "/" + name
	}
	if _, ok := b.g.Node(want); !ok {
		attrs := map[string]any{"kind": kind, "declared_only": true}
		if scoped != "" {
			attrs["namespace"] = scoped
		}
		b.ensureNamespace(scoped, nil)
		b.g.Nodes = append(b.g.Nodes, core.Node{
			ID:       want,
			Type:     lower,
			Name:     name,
			Provider: "kubernetes",
			Groups:   placement(scoped),
			Attrs:    attrs,
		})
	}
	return want
}

func (b *builder) ensureNamespace(name string, src *core.Source) {
	if name == "" {
		return
	}
	id := namespaceID(name)
	for i := range b.g.Groups {
		if b.g.Groups[i].ID == id {
			if b.g.Groups[i].Source == nil {
				b.g.Groups[i].Source = src
			}
			return
		}
	}
	b.g.Groups = append(b.g.Groups, core.Group{
		ID: id, Axis: core.AxisNetwork, Type: "namespace", Label: name, Source: src,
	})
}

// namespaceID keeps the separator out of a group id, which the IR reserves for
// group paths.
func namespaceID(name string) string { return "namespace:" + name }

// placement puts a namespaced object in its namespace and leaves a
// cluster-scoped one at the top level, where it belongs.
func placement(namespace string) map[string]string {
	if namespace == "" {
		return nil
	}
	return map[string]string{core.AxisNetwork: namespaceID(namespace)}
}

func (b *builder) edge(from, to, relation string, attrs map[string]any) {
	if from == "" || to == "" || from == to {
		return
	}
	// Two rules can name the same peer, and a workload can reach one
	// ConfigMap through both an envFrom and a volume. Dropping the second
	// call keeps the first one's ports and loses the rest; Normalize would
	// not recover them either, since it merges on from, to, kind and relation
	// without reading attributes.
	key := from + "\x00" + to + "\x00" + relation
	if b.edges[key] {
		for i := range b.g.Edges {
			e := &b.g.Edges[i]
			if e.From == from && e.To == to && e.Relation == relation {
				e.Attrs = widen(e.Attrs, attrs)
				break
			}
		}
		return
	}
	b.edges[key] = true
	b.g.Edges = append(b.g.Edges, core.Edge{
		From: from, To: to, Kind: core.EdgeIACRef, Relation: relation, Attrs: attrs,
	})
}

// widen adds what a second reading of the same edge saw. Values are joined
// rather than replaced: two rules allowing one peer on different ports allow
// both, and keeping one of them would narrow the edge to a rule that is only
// half of it.
func widen(into, extra map[string]any) map[string]any {
	if into == nil {
		return extra
	}
	for key, value := range extra {
		current, ok := into[key]
		if !ok {
			into[key] = value
			continue
		}
		have, haveOK := current.(string)
		add, addOK := value.(string)
		if !haveOK || !addOK {
			continue
		}
		// Compared as whole values, not as text. "8080" contains "80", and a
		// substring test would drop a port because another one spells it.
		if partsOf(have)[add] {
			continue
		}
		into[key] = have + ", " + add
	}
	return into
}

// partsOf splits a joined attribute back into the values it was built from.
func partsOf(joined string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(joined, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out[part] = true
		}
	}
	return out
}

func (b *builder) setAttr(id, key string, value any) {
	if n, ok := b.g.Node(id); ok {
		if n.Attrs == nil {
			n.Attrs = map[string]any{}
		}
		n.Attrs[key] = value
	}
}

func (b *builder) source(line int) *core.Source {
	if b.opts.File == "" {
		return nil
	}
	return &core.Source{File: b.opts.File, Line: line}
}

// podSpec finds the pod template inside a workload. The path differs per kind
// and there is no way to derive it, which is why the kinds are listed.
func podSpec(o *object) any {
	switch o.kind {
	case "Pod":
		return dig(o.body, "spec")
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		return dig(o.body, "spec", "template", "spec")
	case "CronJob":
		return dig(o.body, "spec", "jobTemplate", "spec", "template", "spec")
	}
	return nil
}

// podLabels are the labels a Service selector is matched against: the pod's
// own for a bare Pod, the template's for anything that makes pods.
func podLabels(o *object) map[string]string {
	switch o.kind {
	case "Pod":
		return strMap(o.body, "metadata", "labels")
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job":
		return strMap(o.body, "spec", "template", "metadata", "labels")
	case "CronJob":
		return strMap(o.body, "spec", "jobTemplate", "spec", "template", "metadata", "labels")
	}
	return nil
}

func firstImage(o *object) string {
	for _, c := range seq(podSpec(o), "containers") {
		if image := str(c, "image"); image != "" {
			return image
		}
	}
	return ""
}

// covers reports whether labels contains every pair in selector, which is what
// a Service selector means.
func covers(labels, selector map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func joined(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// claims reads what a PersistentVolumeClaim asks for. Both ends are
// cluster-scoped, and both are ways for something outside the namespace to
// break a workload inside it.
func (b *builder) claims(pvc *object) {
	if class := str(pvc.body, "spec", "storageClassName"); class != "" {
		b.edge(pvc.id(), b.reference("StorageClass", "", class), "provisioned-by", nil)
	}
	if volume := str(pvc.body, "spec", "volumeName"); volume != "" {
		b.edge(pvc.id(), b.reference("PersistentVolume", "", volume), "bound-to", nil)
	}
}

// identity reads the Secrets a ServiceAccount carries. A workload reaches them
// without naming them, which is exactly why the account has to.
func (b *builder) identity(sa *object) {
	for _, key := range []string{"secrets", "imagePullSecrets"} {
		for _, s := range seq(sa.body, key) {
			if name := str(s, "name"); name != "" {
				b.edge(sa.id(), b.reference("Secret", sa.namespace, name), "reads",
					map[string]any{"via": key})
			}
		}
	}
}

// grants reads a binding: the role it hands out, and who receives it. A
// ServiceAccount's permissions are not written on the account, so this is the
// only edge that says what a workload is allowed to do.
func (b *builder) grants(rb *object) {
	if kind, name := str(rb.body, "roleRef", "kind"), str(rb.body, "roleRef", "name"); kind != "" && name != "" {
		b.edge(rb.id(), b.reference(kind, rb.namespace, name), "grants", nil)
	}
	for _, s := range seq(rb.body, "subjects") {
		kind, name := str(s, "kind"), str(s, "name")
		if kind != "ServiceAccount" || name == "" {
			// A User or a Group is not an object in the cluster. There is
			// nothing to point at, and inventing a box for a name in a text
			// field would put an identity on the diagram that no manifest
			// creates.
			continue
		}
		namespace := str(s, "namespace")
		if namespace == "" {
			namespace = rb.namespace
		}
		b.edge(rb.id(), b.reference("ServiceAccount", namespace, name), "binds", nil)
	}
}
