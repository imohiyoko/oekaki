# Notebook

Things worth building, and things already decided.

[roadmap.md](roadmap.md) is what a release contains. This is the working note
behind it: ideas that have not earned a version number yet, and — just as
important — the decisions that have already been argued out, so they do not get
argued out again from the beginning.

Entries are kept in whatever order makes them readable, not in priority order.
Nothing here is a commitment.

---

## Decided

Short entries, each one a conclusion somebody can point at instead of
relitigating.

**Alert rules are documents, not an expression language.** A rule names a
subject selector, a metric, a comparison, a window and a severity — the same
shape as the roles and overlay documents, validated by a schema, diffable in a
pull request, with no evaluator and nothing to sandbox. The escape hatch for
anything more complicated already exists: a collector or an external evaluator
writes its conclusion as an observation or an overlay, which is exactly where
vendor-specific logic and credentials are supposed to live. An expression
language would be the second mechanism this project keeps managing not to
build.

**Request and response bodies do not enter the graph.** Every question asked so
far — unused routes, spikes, silences, a route that fired unannounced — needs
who, in what order, how often and when last, and none of that is customer data.
`LogRecordSummary` exists precisely so a graph can be shared without carrying a
body. If bodies are ever worth capturing they stay where the collector put
them, behind that store's own access control, and the graph carries a
reference.

**Visibility narrower than the diagram is a fourth permission, below `read`.**
That is the answer for anything the diagram may show one person and not
another: request bodies if they ever arrive, free-text notes if they turn out
to be sensitive. The catalog stays fixed in code and stays shallow; a
permission nobody wrote a check for is a configuration file promising a
protection that does not exist.

**A derived order says it is derived.** A sequence numbered by walking declared
edges is not an observed order, and the page says so. When traces provide a
real order, that is a different claim and the drawing should say *that*
instead.

**Every page of an atlas is a standalone graph document.** It costs a few times
the graph in bytes, measured and written down in atlas.md. Sharing the evidence
arrays across pages would fix the size and break the property; if the size ever
hurts, that is the trade to make deliberately rather than by accident.

---

## Diagrams

**Sequence pages should draw lifelines, and let steps be folded away.** Today a
sequence is a numbered call chain laid out downward, which is readable but is
not the diagram people mean. Folding the middle of a sequence — "show me the
start and the end, hide the six hops in between" — is the feature that makes a
long one usable at all.

**A sequence should prefer a recorded path over the walk it derives**, and say
which of the two it is drawing. The path entity exists now; the atlas has not
been taught about it.

**The UML family beyond what is derivable today.** Class, object, activity, use
case, package and deployment diagrams cannot come from Terraform or Kubernetes
input — they need code. `parsers/source` already builds a dependency graph, and
that is the foundation: a class diagram is a projection of it, an object
diagram is one of its instances, a package diagram is its containment axis. The
navigation to reach them exists; only the derivations are missing.

The reading that ties them together, and the reason the atlas was built the way
it was: a use case opens into the communication diagram behind it, a
communication diagram is a cluster of APIs, and one API opens into its
sequence.

**ER: a table should open into its columns.** Same shape as an EC2 instance
opening into the applications it runs — a node whose inside is not a
containment axis. The atlas already handles that shape through hold relations;
what is missing is a parser that records columns at all.

---

## Paths and monitoring

**Declared routes should be written down, not derived.** `oekaki paths` derives
them from references when nothing else has, and says so. Better sources, in
rough order of value: an overlay somebody authored, an OpenAPI or gRPC
definition, a routing table, an ingress or gateway configuration.

**Spike and silence need the rule document.** The threshold machinery already
turns a bound into a state; what is missing is somewhere to write the bound
down per route, and a baseline to compare a spike against. Silence is the one a
maximum reads wrong — the interesting value is zero — which is why `quiet` is a
finding rather than a threshold today.

**Correlating a request or a session end to end.** `collectors/traces` folds
spans by trace id already. A session is the same idea over a longer span and a
different id, and the interesting version of the question is "this person's
requests, in order, across services". It needs an id the collector can carry
without carrying who the person is.

**Datadog and Prometheus collectors should write `path_requests`.** Nothing new
is needed in the graph — the subject becomes a path key and the document is the
one those collectors already write.

---

## Sharing and authoring

**Address an element, not just a diagram.** The atlas puts the open diagram in
`?at=`. A shared link should be able to say *this box* or *this line* — a
fragment naming a node id or an edge key — and, when a server is serving it,
which generation. Three parts: generation, diagram, element. This is what makes
a link usable in a conversation ("look at this one") rather than an invitation
to go and search.

**Free-text notes on a node or an edge, in Markdown.** A pen affordance in the
detail panel opens an editor; what comes out is an assertion like every other
edit here, so it carries a claim, exports with the overlay, and says who wrote
it. Two things to be careful about:

- Rendering Markdown must not become a way to put HTML into the page. This
  renderer escapes `</script>` in its own data for exactly this reason; a note
  is somebody else's text arriving in the same document. Escape, then render a
  small known set of formatting — not a general Markdown-to-HTML pass.
- Authoring is `write`. Reading is `read`, unless notes turn out to be
  something the diagram may show one person and not another, in which case see
  the decision above.

---

## Older things still true

**Cross-module references are not resolved**, `--source-dir` does not recurse,
and state input recovers only the references that left an identifier behind.
These are in the roadmap's known limitations and are still the honest list.
