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

```console
$ oekaki render plan.json -f html --atlas -o estate.html
```

The page opens on the root level. A box with a `⟩` has an inside: double-click
it, or press the button in the detail panel. Backspace and the breadcrumbs go
back up, the browser's own back button walks the pages it walked, and the open
diagram is in the URL — so a link hands somebody the page you were on rather
than the estate to search through. `--atlas-depth` bounds a call chain and
`--atlas-limit` bounds the document.

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

The cost is worth knowing before it surprises somebody. Every page is a
standalone graph document, so an element that appears on several of them is
copied onto each, and so is the evidence attached to it. Measured on a
synthetic estate of 96 services in 8 namespaces, with 288 readings and 96
classified log records:

| | |
| --- | --- |
| the graph | 121 KB |
| its atlas, 193 diagrams | 1.6 MB — about 13× |
| readings, copied across pages | 2745, about 9× |

That is the shape of the growth: a few times the graph, not a few percent, and
driven by how many pages an element appears on rather than by how big the
estate is. It is bounded — `Limit` stops at 400 diagrams by default — and it is
the price of every page being a document that stands on its own. Lower
`--atlas-limit` on a big estate, or narrow the graph first.

---

# Paths

Everything above draws structure. The questions that follow it are about *use*,
and they share one noun the graph now has: a **path**. What it is, what the
four findings mean, and how a count becomes an alert are in
[paths.md](paths.md).

Folding composes with this: `--fold` alongside `--atlas` folds each page on its
own terms, so opening a namespace of forty replicas gives three boxes rather
than forty. See [folding.md](folding.md).

## Sequences

A sequence page draws lifelines: the participants along the top, a column under
each, and the messages between them in the order they happened. It is laid out
by arithmetic rather than by ELK, because a sequence has no layout problem —
the participants are a row and the messages are an order — and a graph engine
asked to draw one produces a picture of the same edges, which is what the
reader already had one level up.

### Where the order came from

A recorded route beats a walk this package worked out, and the page says which
of the two it is drawing.

| | |
| --- | --- |
| `observed` | something walked this route, and the document records it as a path |
| `derived` | read off the declared references: A calls B and B calls C, so a request probably goes A, B, C. Nobody saw it happen |

"A request went this way" and "the references say a request could go this way"
are different claims, and a reader four pages down has no other way to tell
which one they are looking at — so it is a field on the diagram and a badge in
the breadcrumbs, not a sentence somewhere.

Only an *observed* route wins. A declared route is the same kind of reading the
walk already is, and preferring it would put "observed" on an order nobody saw.
Where several routes start at the same participant the longest wins, because a
route that goes further tells the reader more and the shorter ones are usually
its beginning.

A step of a recorded route with no edge under it is still drawn. The route
saying a request went from here to there is already the claim that it went, and
dropping the step would lose evidence the document has.

One call is one step, however many kinds of evidence found it. A reference the
configuration declares and a trace of the same call are two claims about one
thing; numbering them separately would say the request went to the ledger
twice, and the one that is drawn is the one that saw it happen.

### Putting the middle aside

A long call chain is read for one part of itself, and the hops in the middle
are the reason nobody reads it. A message can be put aside from its own panel;
the band left in its place says how many, and clicking it puts them back.

Hiding is not filtering. The step is still in the document, the drawing still
says it is there, and the participants keep their columns — taking a message
away must not rearrange the sequence around it.
