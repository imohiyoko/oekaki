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

A container descends into the code it runs. A build record joins a workload to
the repository that built it, and when somebody has said which input that
repository is, the box opens as a **code map**: the functions of that
repository, what calls what, and what it imports. From there a function opens
its own page and a file opens what it declares, so the descent from a running
container to one function — or to one class — is a sequence of clicks. See
[builds.md](builds.md).

Behind the box, and not also in front of it. Source carries no position on an
estate's axis — a function is not in a namespace, a subscription or a VPC — and
a level page draws what the axis places nowhere at its root, which put every
file and function of a repository on the estate's front page next to the two or
three things the estate is made of. Code the atlas draws behind a box is taken off
the front page once that page exists — the question is asked of the finished
atlas, never of one still being built, so a box is behind a door or on the
front page and never in neither. Code no box opens is still drawn on the
level, because there would be nowhere else for it —
and so is every bit of it when the atlas is drawn on the source axis with
`--axis`, where the levels are the repository's own directories and the code is
the subject rather than a guest.

Every box on the map is on a line: a function because it calls or is called, a
file because it imports, a package because something reached it. A function
nothing calls but which calls something is where a request comes in, so it
stays; one that neither calls nor is called says nothing about flow, and a
thousand of them is the unreadable single picture this whole file is about.
Not types either: a declaration takes part in flow only through the functions
that use it, and those are here.

The line has to be one the map will actually draw: both of its ends on this
page, and nobody having said it is not there. A call out of the repository is
still a call, but it is not a line here, and a box kept for it would sit on the
page with nothing attached to it — while a line somebody has suppressed is one
a person went to the trouble of denying, and drawing a page out of those is the
opposite of what the denial was for.

These are clicks and not nesting, so they are written out rather than drawn as
a tree like the one above:

```
level:                                   the estate
  click the task definition   ->  detail:aws_ecs_task_definition.api
  click the repository        ->  codemap:repository:acme/checkout   what it runs, as code
  click a function            ->  detail:…#HandleOrder               what it calls
  click a file                ->  detail:…/http.go                   what it declares
  click a type there          ->  detail:…#type:Order                drawn as a class
```

The last two are one route and the one above them is another: a type is
declared in a file, not in a function, so the class diagram is reached through
the file rather than through the code that uses the type.

The trail back up is not that sequence reversed. Every one of those pages
belongs under the level its element sits in — `level:`, for code, which sits in
no container — so the breadcrumbs go back there rather than to the page the
reader came from. Deriving the same estate twice, or reaching the same element
from two neighbours, would otherwise give one page two different trails, and
the trail is the thing a reader who has descended four times is relying on.

No line on that map crosses into the estate around it. Which function serves
which API operation, and which import carries which outbound call, is written
down nowhere — so the map says what the parser read and stops there.

A code graph descends the same way, and a **type** opens as a class diagram
rather than as the generic inside-of-a-box page — one element still has one
inside, read the way the thing itself is written. See
[code.md](code.md#clicking-a-type).

```console
$ oekaki render plan.json -f html --atlas -o estate.html
```

The page opens on the root level. A box with a `⟩` has an inside: double-click
it, or press the button in the detail panel. Backspace and the breadcrumbs go
back up, the browser's own back button walks the pages it walked, and the open
diagram is in the URL — so a link hands somebody the page you were on rather
than the estate to search through. `--atlas-depth` bounds a call chain and
`--atlas-limit` bounds the document.

## Linking to one thing

Three parts name what somebody is looking at, and the URL carries all three:

| | |
| --- | --- |
| generation | the path — a served page is a file, and the directory it sits in is the generation somebody kept |
| diagram | `?at=` |
| element | the fragment: `#node:…`, `#group:…` or `#edge:…` |

The fragment is written on every selection, because the address bar is where
people copy from; the detail panel also has a button that hands it over, because
a fragment somebody has to notice is a feature nobody uses. It is *replaced*
rather than pushed — picking a box is not somewhere you navigated to, and Back
should leave the page you were on rather than walking your last six clicks.

Turning the page drops the element: it belonged to the page being left, and
carrying it forward would hand somebody a link that points at nothing. A link
that names something the page does not draw says so, for the same reason.

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
