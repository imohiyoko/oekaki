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

**The properties a security review found, and what keeps them true.** A review
of the whole repository found nothing above its reporting bar. That is a
statement about how the code is written today, not a permanent one, and the
things it checked are easy to break by accident because none of them look like
security when you are in the middle of a feature. They are:

- **The page never builds markup from data.** `innerHTML` and `eval` appear
  nowhere in `renderers/html/app.js`; every label goes in through
  `textContent`, and maxGraph is told `setHtmlLabels(false)`. A label is
  somebody's resource name, and a resource name is somebody's input.
- **Every JSON block embedded in a page escapes `</`.** There are four of them
  — the graph, the atlas, the folds, the layout — and each writes `<\/`
  instead. A resource named `</script>` would otherwise end the block early
  and drop the rest of the document into the page as markup. A fifth block
  added without this is the whole class back again.
- **DOT quotes what it interpolates**, in `renderers/dot`'s `quote`. Graphviz
  itself runs as WebAssembly under wazero with an in-memory filesystem, so an
  `image=` that got through could still not read a file off this machine —
  that is a property of *how* Graphviz is embedded, and it would be lost by
  switching to a `dot` binary on PATH.
- **Names of saved things go through one regular expression.** `manage`'s
  `safeName` is the only thing between a caller and the state directory's
  filenames, and `page()` refuses `..` and anything outside the directory it
  serves.
- **Nothing shells out except the AI adapter**, which takes an explicitly
  selected executable and passes arguments without a shell.

The point of writing them down is that each one is a decision somebody could
undo in a single line while doing something else.

**A note is a claim, not a field, and the viewer never builds markup from it.**
What somebody wrote about a thing lives beside the graph with a subject and a
signature, exactly where an observation lives — a string on the resource would
be the graph saying it itself, when the whole point is that a person said it and
which person. Several notes about one thing are several notes; `Normalize` folds
only the ones that match exactly.

The rendering half is the part that is easy to undo by accident. The viewer
understands paragraphs, bold, italic, inline code and bullets, and it builds
those as elements with `textContent` — there is no string being assembled in
that path, so there is nothing that could be read back as markup. The list is
short on purpose: every addition is another shape somebody else's text can take
inside a page that draws your estate. Links are absent because a link is a
destination, and a destination in a note is a place this page would send a
reader on the say-so of whoever wrote the note. A general Markdown-to-HTML pass
here would give all of that back in one line.

**`serve` answers only to its own name.** Loopback binding stops the network;
it does not stop DNS rebinding, where a name the attacker controls starts
resolving to 127.0.0.1 and their page then talks to this server as the same
origin. The Host header is the one thing that trick cannot forge, so every
route checks it — not only the ones that write, because in local mode nobody is
asked who they are and the drawings, the graph, the journal and the roles are
all readable. No flag loosens it: the flag would be the misconfiguration.

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

**Folding runs per atlas page, not once over the estate — in an interactive
page.** A crowd on a level is not a crowd on a detail page, and one budget
spent against the whole estate lands on a page holding three of a fold's twelve
members. Every other format has no pages for an atlas to be, so folding there
falls back to folding the estate as a whole and the run says the atlas was
ignored. The cost of the per-page rule is that the same box can be folded on
one page and drawn on another, which is correct and looks inconsistent until
you know why.

**Every page of an atlas is a standalone graph document.** It costs a few times
the graph in bytes, measured and written down in atlas.md. Sharing the evidence
arrays across pages would fix the size and break the property; if the size ever
hurts, that is the trade to make deliberately rather than by accident.

---

## Diagrams

**Sequence pages could show what happened around a message.** Lifelines and
step-hiding are in, and the order says whether it was observed or derived. The
next thing a reader asks of a sequence is timing — how long each hop took, and
which one was slow — and the observations are already attached to the
participants. What is missing is a way to say that a reading belongs to a
*step* rather than to a participant.

**The UML family beyond what is derivable today.** Class and package are
done: `parsers/source` learned types, a type opens as a class diagram, and the
directory levels an atlas already builds *are* the package diagram. See
[code.md](code.md).

What is left needs something nobody is reading yet. An **object** diagram is a
picture of instances at run time, and no parser watches a program run — that
one is not "a derivation nobody has written", it is a claim nothing in the
input supports, and drawing it would be invention. **Activity** wants control
flow, which the regular expressions here do not read and a real parser would
have to supply. **Use case** and **deployment** want a source outside the code
entirely: who the actors are, and what runs where.

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

**Declared routes should be written down, not derived.** The overlay half is
done: `assert: "path"` takes a walk of selectors and records it with the claim
of whoever wrote it, and the derivation is skipped when a document carries one.

Derivation stays as the fallback, and its two failure modes are the argument
for writing routes down: where there are no references to follow it finds
nothing, so every observed route reads as unannounced; where there are many, it
finds combinations a request *can* take and nobody does, which arrive as
`unused`. Both bury the routes that matter.

The routing side is done too, and it turned out to be already half there. A
Kubernetes Ingress records `routes` edges carrying the host and path it
matches, and the derivation already walked them — it was throwing the host and
path away. Now it keeps them, so a listing can say which API is unused rather
than only which boxes were involved.

**OpenAPI and gRPC are not a source of routes**, which is worth writing down
because the list above said they were. Those documents declare a service's
*surface* — these operations exist — and one of them describes one service,
never what it calls. From an OpenAPI document alone the longest walk available
is `client → service`. What they are actually good for is making an API a thing
the graph carries, which is what "a communication diagram is a cluster of APIs,
and one API opens into its sequence" needs — an addition to the IR, and a
different piece of work from this one.

**Spike and silence.** Done — [rules.md](rules.md). A bound is written down
per subject or per route, silence is a condition of its own because the
interesting value is zero, and a rule that could not be applied says so instead
of passing quietly.

The baseline is done too, and the answer was not to compute one. Nothing here
knows which usual an estate means — a moving average, a median of the same hour
last week, a quantile over a season — and every one of those is a choice made
over history this program does not keep. So a collector works it out and writes
it back as an ordinary observation, and a rule names that reading as its bound.
The one thing the rule keeps is the multiplier, because "twice what it usually
is" is policy and policy belongs in the document somebody reviews.

What that leaves open is a **collector that actually writes one**. Datadog and
Prometheus can both produce a moving average in a query; nothing here has asked
them to yet.

**Correlating a request or a session end to end.** The counting half is done:
a route says how many distinct sessions walked it, and nothing but the count
comes out — see the guarantee in [paths.md](paths.md). What is not done is the
interesting version of the question, "this person's requests, in order, across
services", which is a listing rather than a number and would have to be
answered without the graph ever holding the id.

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

**Free-text notes on a node or an edge, in Markdown.** Done — see
[notes.md](notes.md) and the decision above. What is still open is anything
finer than the diagram's own `read`: a note only some readers should see, or a
reader who may read every note and write none.

**Usage per API path, not per walk.** A route says which ways in reach it —
the host and path an Ingress matched — but whether it was walked is decided by
its participants: an observed walk has to contain the whole sequence, in order.
What an observed walk never carries is which rule let the request in, because a
trace folds to the services a request went through. One service reached by
`/checkout` and `/checkout/v2` is one route: traffic on either marks it walked,
and a listing cannot say which of the two is dead.

What that needs is the request path on the observation itself, which no
collector writes. It cannot be solved by splitting the route into one per rule:
a route is identified by its participants, so two routes through the same
services are one route — and that identity is what makes an observation about a
route addressable. Either the evidence gains a field, or the answer stays at
the walk.

---

## Wanted, not started

**Which system is this container?** The code graph says what the source
declares and the Terraform graph says what runs, and between them is an image
tag that nothing here reads the source of.

In an enterprise that join is already automated and already written down: a
pipeline builds an image from a commit, and a pull request writes that image's
digest into the IaC. So the record of "this container is that repository at that
commit" lives in the CI system — GitHub Actions or whatever stands in its place
— and that is the only place it exists. Matching a repository name against an
image name because they look alike is precisely the invention this project
refuses.

The shape follows the boundary that is already here: a collector reads the
build's own record and writes it as ordinary evidence, the join arrives as an
`observed` edge with a claim naming the run it came from, and a flag refuses to
read any of it — whether a CI system belongs in the picture is the estate's
decision, not this program's default. Written down in
[roadmap.md](roadmap.md#what-builds-this-image--the-missing-half-of-the-code-to-infrastructure-join)
so the mechanism is not invented in a hurry later.

---

## Older things still true

**Cross-module references are not resolved**, `--source-dir` does not recurse,
and state input recovers only the references that left an identifier behind.
These are in the roadmap's known limitations and are still the honest list.
