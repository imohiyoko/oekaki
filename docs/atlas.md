# The atlas: one bound set of diagrams

A single drawing of a whole estate answers "what exists" and nothing else. The
question a reader actually arrives with is narrower — what is in this
namespace, what does this instance run, what happens when this API is called —
and today the only way to ask it is to regenerate the picture with different
flags.

An **atlas** is the answer: a set of diagrams derived from one evidence graph,
with the ways between them written down. Every box that has an inside says
which diagram *is* its inside, so a reader descends by clicking.

```
level:                 the estate: containers as boxes
 └ level:ns:shop       one container: its children as boxes, its members as members
    └ detail:svc:api   one element: what it holds, and what it talks to
       └ sequence:svc:api   one call chain, in order
```

Derivation lives in [`views/atlas.go`](../views/atlas.go). Nothing there
invents a relationship: a level is a projection of containment that a parser
already recorded, and a sequence is an ordering imposed on edges that were
already claimed. Both carry the claim of whatever produced the edge
underneath, exactly as the flat renderers do.

## Why a level is flat

A level draws the containers directly inside it as single boxes rather than
nesting their contents. That is the whole difference between an atlas and the
one page it replaces. The nested drawing shows a hundred namespaces and
everything in them at once, and the answer to "what is in this namespace" is a
picture you have to find rather than one you open.

Nesting is not lost; it moved into navigation. A container box opens the level
below it, and the trail back up is the containment chain.

## Why openings are recorded rather than guessed

Whether a box has an inside is a property of the derivation, not of the
picture. A viewer that guessed would offer a door into an empty room, and a
reader who opens two empty rooms stops trying the third. So each diagram
carries its `opens` list, and a box with no opening is drawn as a leaf.

## Why a sequence says its order is derived

A static graph records that A calls B and that B calls C. It does not record
that A called B *before* it called C. The step numbers in a `sequence` diagram
are a depth-first walk in a stable order — which is how a reader reads a call
chain when nobody has traced one — and the subtitle says so.

An observed ordering is a different claim. When traces provide one it belongs
on the edges, and a sequence built from it should say *that* instead. Until
then, presenting a derived order as an observed one would be the same lie the
rest of this project exists to avoid.

## Bounds

An atlas derives a page per element, so an estate of ten thousand resources
would otherwise produce a document nobody can open. `Limit` caps the number of
diagrams and `Depth` caps a call chain. A reader who needs that estate needs a
filtered graph first, not a bigger atlas.

---

# Planned: paths as first-class subjects

Everything above draws structure. The questions that follow it are about
*use*, and they share one missing noun: a **path** — "this API, then that one,
then that one" — as something the IR can name, claim, and attach evidence to.

A path is not a new kind of truth. It is an ordered list of node ids with a
claim, exactly like every other entity here, and the sequence diagram above is
already its picture. Making it an entity is what lets the following three
things be one feature instead of three.

## Which paths are never used

An edge that is `reachable` but never `observed` is already the project's
headline finding. A *path* that is declared but never observed is the same
finding one level up, and it is the one an operator can act on: an API route
nothing has walked in ninety days is a route to delete.

This needs no new collector. It needs the observation window on the path and a
listing that sorts by how long it has been quiet.

## Spikes and silences

Call counts per path arrive the way every other measurement does: a collector
holds the credentials, reads Datadog or Prometheus, and writes an observations
document. `collectors/datadog` and `collectors/prometheus` already do this for
nodes; the subject becomes a path key rather than a node id.

An alert is then a threshold on that observation, which
`enrichers/observations` already evaluates — `state: abnormal` when the count
crosses a bound. Two bounds matter and neither is a maximum: a **spike**
against a declared baseline, and a **silence**, where the interesting value is
zero and the ordinary threshold comparison reads it as healthy.

## A path that fires when it should not

Once the known paths are written down, an observed path that is not among them
is an alert on its own — no threshold, no baseline. "This API called that one,
and nothing ever said it would" is exactly the shape of a finding this graph
can produce, and it is why the path has to be an entity: an unknown path is a
claim origin disagreeing with the declared set, which is the machinery
[`core.Conflict`](../core/graph.go) already has.

Deriving the observed path needs the request correlated across services —
a trace id, or a session. `collectors/traces` reads spans with a `trace_id`
today and folds them into edges; folding them into ordered paths instead is
the same input read one level up.

## What is recorded, and who may see it

The evidence needed for all of the above is metadata: which services, in which
order, how many times, when last seen. Request and response **bodies are not
required for any of it**, and the IR deliberately has no field that would hold
one — `LogRecordSummary` exists precisely so that a graph can be shared without
carrying customer data.

If bodies are ever worth capturing, the rule that keeps that decision safe is:
they do not enter the graph document. They stay wherever the collector put
them, behind whatever that store's own access control is, and the graph
carries a reference. Visibility is then a question for [`authz`](../authz),
whose permission catalog is fixed in code — a fourth permission below `read`
is where such a thing would go, denied unless a role says otherwise, on the
same default-deny terms as everything else there.
