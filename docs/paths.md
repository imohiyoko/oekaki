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
- **The same walk declared twice in one run**, across all the `--overlay` files
  that run applies. **The first declaration wins**, and the later ones are
  reported and dropped without their claims being weighed.

  That is deliberate. Left to `Normalize`, the two would fold and the
  better-ranked claim would keep the route — so the surviving *label* would
  depend on which origin each assertion happened to carry, and `oekaki diff`
  would show it flipping for no reason anybody wrote down. Within one run the
  author can simply fix it, so they are told.

  The same walk under a different `kind` is a different route, and both are
  kept: the entity exists for the gap between what may happen and what did.

Across separate runs — re-applying an overlay to a graph that already carries
the route — nothing is dropped and `Normalize`'s ordinary rule applies: the
routes fold, and the better-ranked claim keeps it. That is the same rule every
other claim in the document follows.

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

### Where a request came in

A route that arrives through a routing rule says which one. An Ingress that
matches a host and a path is exactly that rule, and the derivation now carries
it onto the route:

```console
$ oekaki paths cluster.json
2 declared routes derived by following references; nothing wrote them down
unused      shop.example.com/checkout: shop → checkout
            iac_ref route, and nothing has been seen walking it
unused      shop.example.com/reports: shop → reports
```

A listing that could only name the boxes involved was hard to act on — and one
that named only the API would have dropped what it goes through, which is the
other half of the same answer.

It is on the route as `attrs.entry`, **a list with one entry per rule**, so
something reading the JSON can tell one way in from another and group by it. A
joined string could not be told apart from either of the two paths in it.

It **names** a way in; it does not decide whether a URL matches one. An entry
is the host and the path a rule matched on, run together — `shop.example.com`,
`/checkout`, `shop.example.com/checkout`, `*.example.com/checkout` — and the
`pathType` that says whether the path is exact or a prefix is not carried,
because that is matching semantics rather than identity. Anything deciding
whether a request belongs to a rule has to read the rule.

```json
{ "nodes": ["ingress/shop/shop", "service/shop/checkout"], "kind": "iac_ref",
  "attrs": { "entry": ["shop.example.com/checkout", "shop.example.com/checkout/v2"] } }
```

Two rules that reach the same backend are both kept. Two paths to one service
is the ordinary way an API is versioned, and they are one edge — the same
Ingress to the same Service — but two facts about it, carried as `attrs.rules`
on the edge beside the `via` note a person reads.

`rules` is a **list** and `via` is words, because they are read by different
things. A list can be merged when the same edge is read twice; a joined string
cannot, since nothing can tell a value that holds several things from one that
merely contains a comma — a label selector is `app=web,tier=front`, one value.

It is the **first** hop's rule and nothing else's: a route is one way into the
estate followed by one service calling another, and a rule further down is a
second way in rather than part of this one.

And only from an edge that **routes**, and only from its `rules`. `via` is a
general "how did this come to exist" note that half the Kubernetes parser writes
— a TLS secret, an `envFrom` key, a NetworkPolicy — and reading it wherever it
appeared turned `web reads app-config` into an API somebody could be asked why
nobody uses.

`rules` holds what a rule **matched on**, and only that. A rule that matched on
a host alone names that host, which is a way in somebody can tell from another.
A rule that matched on nothing — a default backend, a rule with neither host nor
path — has no name to give, and stays in the words: a name nothing can be told
apart by is not a smaller answer, it is a wrong one.

### What an entry does not say

**A route is used or unused as a walk, not as an API path.** A route is walked
when an observed walk contains its whole participant sequence, in order and
consecutively — the [comparison by run](#why-comparison-is-by-run-and-not-by-set)
above, not "something reached the last hop".

What an observed walk never contains is *which rule let the request in*. A trace
folds to the services a request went through, so two rules that reach the same
service are one route, and traffic on either marks it walked.

So where one service is reached by two rules:

- if traffic came in on `/checkout/v2` only, the route counts as walked, and
  `/checkout` being dead is not reported;
- if nothing came in at all, the route is `unused` and the line names both
  paths, though only one of them may be the one nobody wants.

The entry says **which ways in reach this walk**, not which of them carried the
traffic. Saying which would need traffic attributed per rule — a request path
on the observation itself — and nothing collects that today. Splitting the
route into one per rule is not available either: a route is identified by its
participants, so two routes through the same services are one route, which is
what makes an observation about it addressable at all.

Where the difference matters, an overlay can write the two routes down as
separate walks through whatever tells them apart.

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

A **routing table** is read: see [where a request came in](#where-a-request-came-in)
above. An Ingress says which host and path reach which service, which is a route
being declared, and the derivation carries it.

An **API definition is not a source of routes.** OpenAPI and gRPC declare a
service's *surface* — these operations exist — and one document describes one
service, never what it calls, so the longest walk available from one is
`client → service`. What they are good for is making an API a thing the graph
carries, which is an addition to the IR and a different piece of work. See
[roadmap.md](roadmap.md).

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
