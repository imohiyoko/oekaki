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

	// Documents is how many YAML documents held an object. Empty documents,
	// which `helm template` emits freely, are not counted.
	Documents int

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
	objects, err := decode(raw, opts)
	if err != nil {
		return nil, err
	}
	if len(objects) == 0 {
		return nil, errors.New("no Kubernetes objects: every document is missing apiVersion or kind")
	}

	g := core.New()
	g.Metadata = &core.Metadata{Source: "kubernetes", Scope: opts.Scope}
	g.Axes = []core.Axis{{ID: core.AxisNetwork, Label: "Namespace"}}

	res := &Result{Graph: g, Documents: len(objects)}
	index := map[string]*object{}
	for i := range objects {
		o := &objects[i]
		index[o.id()] = o
		if o.known && o.api.Removed != "" && !o.api.Served(SupportedThrough) {
			res.Removed = append(res.Removed, o.id())
		}
		if !o.known {
			res.Unknown = append(res.Unknown, o.id())
		}
		if !o.known {
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

	for i := range objects {
		add(g, &objects[i], opts)
	}
	for i := range objects {
		relate(g, &objects[i], objects, opts)
	}

	sort.Strings(res.Removed)
	sort.Strings(res.Unknown)
	g.Normalize()
	if opts.Scope != "" {
		g.ApplyScope(opts.Scope)
	}
	return res, nil
}

// decode splits the stream into objects. A document that is empty, or that is
// a List, or that is not a Kubernetes object at all, is skipped rather than
// failing the parse: manifest streams routinely carry all three.
func decode(raw []byte, opts Options) ([]object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var out []object
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading YAML: %w", err)
		}
		var body map[string]any
		if err := doc.Decode(&body); err != nil {
			continue
		}
		for _, item := range flatten(body) {
			o := object{
				apiVersion: str(item, "apiVersion"),
				kind:       str(item, "kind"),
				name:       str(item, "metadata", "name"),
				namespace:  str(item, "metadata", "namespace"),
				body:       item,
				line:       doc.Line,
			}
			if o.apiVersion == "" || o.kind == "" || o.name == "" {
				continue
			}
			o.api, o.known = lookup(o.apiVersion, o.kind)
			// The default namespace is only supplied for kinds known to live
			// in one. A ClusterRole or a PersistentVolume does not, and a kind
			// this build has never heard of might not either — filing one
			// under `default` would put it in a namespace that never held it,
			// and the diagram would not admit the guess.
			if o.namespace == "" && o.known && o.api.Namespaced {
				o.namespace = opts.DefaultNamespace
				if o.namespace == "" {
					o.namespace = "default"
				}
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// flatten unwraps a List, which is what `kubectl get -o yaml` returns when it
// is asked for more than one object.
func flatten(body map[string]any) []map[string]any {
	if !strings.HasSuffix(str(body, "kind"), "List") {
		return []map[string]any{body}
	}
	var out []map[string]any
	for _, item := range seq(body, "items") {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
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
func add(g *core.Graph, o *object, opts Options) {
	if o.kind == "Namespace" {
		ensureNamespace(g, o.name, source(opts, o.line))
		return
	}
	ensureNamespace(g, o.namespace, nil)

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

	g.Nodes = append(g.Nodes, core.Node{
		ID:       o.id(),
		Type:     strings.ToLower(o.kind),
		Name:     o.name,
		Provider: "kubernetes",
		Groups:   placement(o.namespace),
		Attrs:    attrs,
		Source:   source(opts, o.line),
	})
}

// relate reads one object's references to others.
func relate(g *core.Graph, o *object, all []object, opts Options) {
	switch o.kind {
	case "Service":
		selects(g, o, all)
	case "Ingress":
		routes(g, o, all)
	case "HorizontalPodAutoscaler":
		scales(g, o, all)
	}
	if spec := podSpec(o); spec != nil {
		mounts(g, o, spec, all, opts)
	}
	owners(g, o, all)
}

// selects joins a Service to the workloads whose pod template carries every
// label in its selector. A Service with no selector — ExternalName, or
// Endpoints managed by hand — selects nothing, and saying so is not the same
// as finding nothing.
func selects(g *core.Graph, svc *object, all []object) {
	selector := strMap(svc.body, "spec", "selector")
	if len(selector) == 0 {
		setAttr(g, svc.id(), "selects", "nothing: no selector")
		return
	}
	matched := 0
	for i := range all {
		target := &all[i]
		if target.namespace != svc.namespace || podLabels(target) == nil {
			continue
		}
		if !covers(podLabels(target), selector) {
			continue
		}
		matched++
		edge(g, svc.id(), target.id(), "selects", map[string]any{"selector": joined(selector)})
	}
	if matched == 0 {
		setAttr(g, svc.id(), "selects", "nothing in this input matches "+joined(selector))
	}
}

// routes joins an Ingress to the Services it names. Both the v1 shape
// (backend.service.name) and the v1beta1 one (backend.serviceName) are read,
// because a repository that still has the older shape is exactly the
// repository that needs to see it drawn.
func routes(g *core.Graph, ing *object, all []object) {
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
		to := reference(g, all, "Service", ing.namespace, name)
		edge(g, ing.id(), to, "routes", map[string]any{"via": backends[name]})
	}
}

// scales joins an autoscaler to what it scales.
func scales(g *core.Graph, hpa *object, all []object) {
	kind := str(hpa.body, "spec", "scaleTargetRef", "kind")
	name := str(hpa.body, "spec", "scaleTargetRef", "name")
	if kind == "" || name == "" {
		return
	}
	to := reference(g, all, kind, hpa.namespace, name)
	edge(g, hpa.id(), to, "scales", nil)
}

// mounts reads a pod spec for everything it names: config, secrets, storage
// and the identity it runs as.
func mounts(g *core.Graph, o *object, spec any, all []object, opts Options) {
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
		to := reference(g, all, r.kind, o.namespace, r.name)
		edge(g, o.id(), to, r.relation, map[string]any{"via": r.how})
	}
}

// owners reads metadata.ownerReferences, which a cluster fills in and a
// repository does not. It is what makes a `kubectl get` dump show a
// ReplicaSet under its Deployment rather than beside it.
func owners(g *core.Graph, o *object, all []object) {
	for _, ref := range seq(o.body, "metadata", "ownerReferences") {
		kind, name := str(ref, "kind"), str(ref, "name")
		if kind == "" || name == "" {
			continue
		}
		to := reference(g, all, kind, o.namespace, name)
		edge(g, o.id(), to, "owned-by", nil)
	}
}

// reference resolves a named object, adding a placeholder node when the input
// does not contain it. A Deployment that mounts a Secret nobody committed is
// the most useful thing a manifest graph can show, and it can only show it if
// the missing end has somewhere to point.
func reference(g *core.Graph, all []object, kind, namespace, name string) string {
	want := strings.ToLower(kind) + "/" + namespace + "/" + name
	for i := range all {
		if all[i].id() == want {
			return want
		}
	}
	if _, ok := g.Node(want); !ok {
		ensureNamespace(g, namespace, nil)
		attrs := map[string]any{"kind": kind, "declared_only": true}
		if namespace != "" {
			attrs["namespace"] = namespace
		}
		g.Nodes = append(g.Nodes, core.Node{
			ID:       want,
			Type:     strings.ToLower(kind),
			Name:     name,
			Provider: "kubernetes",
			Groups:   placement(namespace),
			Attrs:    attrs,
		})
	}
	return want
}

func ensureNamespace(g *core.Graph, name string, src *core.Source) {
	if name == "" {
		return
	}
	id := namespaceID(name)
	for i := range g.Groups {
		if g.Groups[i].ID == id {
			if g.Groups[i].Source == nil {
				g.Groups[i].Source = src
			}
			return
		}
	}
	g.Groups = append(g.Groups, core.Group{
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

func edge(g *core.Graph, from, to, relation string, attrs map[string]any) {
	if from == "" || to == "" || from == to {
		return
	}
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Relation == relation {
			return
		}
	}
	g.Edges = append(g.Edges, core.Edge{
		From: from, To: to, Kind: core.EdgeIACRef, Relation: relation, Attrs: attrs,
	})
}

func setAttr(g *core.Graph, id, key string, value any) {
	if n, ok := g.Node(id); ok {
		if n.Attrs == nil {
			n.Attrs = map[string]any{}
		}
		n.Attrs[key] = value
	}
}

func source(opts Options, line int) *core.Source {
	if opts.File == "" {
		return nil
	}
	return &core.Source{File: opts.File, Line: line}
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
