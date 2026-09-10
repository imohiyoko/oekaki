# Paths: what actually gets walked

Everything else in this graph is said one hop at a time. An edge records that
checkout calls the ledger. Nothing records that a request arrives at the
gateway, passes through checkout and ends at the ledger — and that is the thing
an operator acts on.

A **path** is that order, as an entity: an ordered list of participants with a
claim, carrying the same three kinds an edge does.

| kind | means |
| --- | --- |
| `iac_ref` | the configuration says this route exists |
| `reachable` | the network permits it |
| `observed` | something walked it |

The gap between the first and the last is the point, exactly as it is for
edges. One level up, it answers questions an edge cannot.

```console
$ oekaki graph plan.json --traces spans.json -o graph.json
$ oekaki paths graph.json --since 30d
```

```text
partial     gateway → reports → archive
            walked as far as reports; nothing has been seen going on to archive  (last 2026-05-01T10:00:00Z, 1 requests)
unexpected  gateway → ledger
            something walked this route and no declared route contains it in this order  (last 2026-09-03T02:13:00Z, 1 requests)
```

## The four answers

| | |
| --- | --- |
| `unused` | declared, and nothing has ever been seen walking it — a route to delete |
| `partial` | walked as far as some hop and never further — requests reach the ledger and never go on to the archive |
| `quiet` | walked, and then stopped, before `--since` |
| `unexpected` | walked, and no declared route contains it *in that order* |

They are not severities. Which of them matters is a property of the estate: an
unused route in a service being retired is the goal, and a route that fired
unannounced in a payment system is an incident. The listing names what was
found and leaves ranking to the reader.

### Why `partial` exists

Without it, a request that stopped early has to be either a walk of the
declared route or a surprise, and both are wrong. Reporting it as unannounced
is the false alarm that makes a listing worth ignoring; reporting the declared
route as unused is a false negative in the other direction. Which hop it stops
at is the useful part.

### Why comparison is by run and not by set

The order is the whole reason a path is an entity. A request that went gateway
then ledger is not a walk of gateway, checkout, ledger with a hop missing: it
is a different thing happening, and it is the one worth waking somebody for. So
an observed route counts as declared only when some declared route contains it
as **consecutive** participants.

## Where the two sides come from

**Observed** routes come from traces. `collectors/traces` folds spans into the
routes they walked: a trace is a tree, so each root-to-leaf chain is one route,
because flattening a fan-out into a single walk would claim an order between
two branches that nothing observed. A route is rooted at a span that names no
caller — a trace whose entry span was sampled away is skipped rather than
rooted at whichever service sorted first.

The same route in a thousand traces is one path carrying a count of a thousand.
Traffic moves the number, not the size of the document — and a service called
twice in one trace was called twice.

### One request, and the reason there were four

A trace is one request. A **session** is what ties several together: the same
person, the same job, the same run of a batch. A span may carry a
`session_id`, and when it does a route says how many distinct sessions walked
it as well as how many times it was walked:

```text
path_requests  gateway → checkout → ledger  3
path_sessions  gateway → checkout → ledger  2
```

The difference is the whole point. One person clicking three times and three
people finding the same route are not the same fact, and the count of walks
cannot tell them apart —— which matters most for the route nothing much uses:
a hundred walks from one session is one caller with a retry loop, and three
walks from three sessions is a route somebody depends on.

**The value is counted, and nothing but the count comes out.** Whether a
session id identifies a person is the caller's business and the caller's risk,
so the guarantee has to be stated exactly: the value is read, and the distinct
values of a route are held in memory while the fold counts them — that is what
counting distinct things requires. What never happens is writing one down.
Nothing derived from a session id reaches the graph, the IR, or any file this
program produces except the number of them. In memory for the length of one
fold, and a count on the way out.

Traces that carry no session say nothing about sessions, rather than claiming
there was one.

Both readings are ordinary observations, so a rule can be about either —
`{"is": "below", "metric": "path_sessions", "value": 2}` is "used by one
caller only", which is a different question from "hardly used".

### Give the spans their ids

`span_id` and `parent_span_id` are optional on a span, and worth providing.
The tree has to be built from span identity, not from service names: a cache
called by both auth and checkout is one *service* with two callers, and joining
its children to its name produces `gateway → checkout → cache → redis` when
redis was only ever reached through auth. That is a route nobody walked,
invented by the collector, in a document whose whole purpose is telling apart
what was claimed from what was seen.

So when the ids are there, the walk follows them. When they are not, the fold
happens only where names cannot be ambiguous — no service with two different
callers — and a trace that cannot be ordered is **reported** rather than
guessed at, as an unmatched assertion naming its trace id. A trace nothing
could read and a route nothing walked are different facts.

**Declared** routes are written down, in an overlay:

```json
{
  "assert": "path",
  "through": [
    { "name": "public", "type": "aws_lb" },
    { "name": "api",    "type": "aws_ecs_service" },
    { "name": "main",   "type": "aws_db_instance" }
  ],
  "label": "checkout",
  "note": "the only route a request is meant to take"
}
```

Each participant is a **selector**, resolved by the same ladder as every other
subject in an overlay, because a person writing a route down knows it as "the
checkout service" rather than as a Terraform address.

The walk is applied whole or not at all, and a hop is never adopted — whatever
`--overlay-unmatched` says. A route is *about* boxes that are already there, and
adopting a mistyped hop would put a box nobody parsed in the middle of the walk:
the route would then be permanently `unused` while the real one stayed
`unexpected`, which is both of the failures this assertion exists to remove,
manufactured from a typo, in silence.

A hop that names a **container** is refused for a plainer reason: a container
does not call anything, which is why the IR refuses a path through one.

Two more are refused for the same reason as the first: a route that cannot be
walked is a route permanently `unused`, and nobody would ever find out why.

- **A hop that repeats the one before it.** `a → a` is a typo; `a → b → a` is a
  real loop, which is why the IR allows repeats and why this is checked here
  rather than there.
- **The same walk declared twice in one run.** `Normalize` folds routes that
  agree and keeps the better-ranked claim, so the second label would go with
  the one it dropped — silently, and differently depending on which origin each
  assertion carried, which would show up as a label flipping in `oekaki diff`
  for no reason anybody wrote down. The same walk under a different `kind` is a
  different route, and both are kept.

Every refusal names the assertion, says why, and appears on stderr as well as
in `--overlay-report`.

The route carries the claim of whoever wrote it, so a listing can say who said
so — and `oekaki diff` reports it when it changes, which is the point of writing
it down rather than deriving it.

**An overlay cannot say a route was walked.** `kind` defaults to `iac_ref` and
`observed` is refused: what a person writes down is a claim about what *may*
happen, and what *did* happen comes from something that watched. Letting an
overlay claim otherwise would put a hand-written route on the observed side of
the very comparison the entity exists for.

### When nothing has written them down

`oekaki paths` derives them by following declared references from wherever a
request can arrive, and says so on stderr rather than presenting them as
somebody's claim. A route is only as declared as its weakest hop: one that
depends on a rule the network merely permits is `reachable`, not `iac_ref`.

A route somebody wrote down is always preferred, and the derivation is skipped
entirely when the document carries one. Derivation is a fallback with two
failure modes worth knowing: where there are no references to follow it finds
nothing, and every observed route then reads as unannounced; where there are
many, it finds combinations a request can take and nobody does, which arrive as
`unused`. Both bury the routes that actually matter, which is the reason to
write them down.

An API definition or a routing table would produce the same thing — a declared
`core.Path` — and neither is read yet.

## How a count becomes an alert

The number of walks is an ordinary observation whose subject is the path's key:

```json
{ "subject": "path:Z2F0ZXdheQ.Y2hlY2tvdXQ.bGVkZ2Vy", "metric": "path_requests",
  "value": 1284, "observed_at": "2026-09-02T10:00:01Z" }
```

That is deliberate. Everything that already knows how to read a measurement —
a threshold, a window, the viewer's time cutoff, two collectors disagreeing —
knows how to read this one, and no second mechanism had to be built for
routes. A collector holding Datadog or Prometheus credentials writes the same
document `collectors/datadog` already writes; only the subject changes.

`enrichers/observations` turns a threshold into a state, which is the spike.
The silence is the one a threshold reads wrong — the interesting value is zero,
and `value > limit` calls that healthy — so it is the `quiet` finding above
rather than a bound.

## What is deliberately not read

Nothing here looks at request or response bodies. A route is who, in what
order, how often, and when last, which is everything the four findings need,
and none of it is customer data. The IR has no field that would hold a body,
and `LogRecordSummary` exists so that a graph can be shared without carrying
one.

If bodies are ever worth capturing, the rule that keeps that safe is that they
do not enter the graph document: they stay where the collector put them, behind
that store's own access control, and the graph carries a reference. Visibility
is then a question for [`authz`](../authz), whose permission catalog is fixed
in code — a fourth permission below `read` is where it would go, denied unless
a role says otherwise.

## Determinism

Timestamps are compared as moments, not as text. The collector writes RFC3339
with nanoseconds while a relative cutoff is written to the second, and `.`
sorts before `Z` — so a string comparison puts a walk at `10:00:00.5Z` before a
cutoff of `10:00:00Z`, and reports a route walked seconds ago as having
stopped.

`views.Paths` reads no clock. `--since 30d` is resolved by the command, against
the caller's clock, and the resolved moment is written into the JSON output —
so a listing says which moment it was asking about even when the question was
relative. Two runs over the same document produce the same list, which is what
makes a finding something you can commit, diff, and argue with.
