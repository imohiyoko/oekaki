# The surface: what a service offers

The graph can say that a request came in on `shop.example.com/orders` and that
it reached the checkout service. It could not, until now, say that
`GET /orders/{id}` exists.

An OpenAPI document says exactly that and nothing more. It declares a
service's **surface** — these operations exist — and it is not a source of
routes: one document describes one service and never says what that service
calls. So reading one adds entities, not arrows.

```console
$ oekaki graph cluster.yaml --api service/shop/checkout=openapi.yaml -o estate.json
```

## What is in it

| node | what it is |
| --- | --- |
| `api` | one operation: a method and a path |

| container | what it is |
| --- | --- |
| `api_surface` | one document: the operations it declares, together |

| relation | between | what it says |
| --- | --- | --- |
| `declares` | element → operation | somebody said this element serves that operation |
| `serves` | function → operation | somebody said this code answers it; see [below](#which-function-serves-it) |

Each operation carries what a listing filters on, apart from its name so that
nothing has to take the name back apart:

| attribute | |
| --- | --- |
| `method` | `GET`, `POST`, … |
| `path` | `/orders/{id}`, as written |
| `operation_id` | the document's `operationId`, when it has one |
| `deprecated` | present and true only when the document says so |
| `tags` | the document's tags, in an order two runs agree on |

The summary becomes the description a viewer shows. The surface carries the
API's own version (`info.version`) and the `servers` the document says it is
served at.

## Why the join is written down rather than guessed

A document names itself with a title somebody wrote in a repository. An estate
names its boxes with ids. `Checkout` and `service/shop/checkout` look alike,
and matching them on the strength of that is exactly the invention the rest of
this project refuses — the same refusal as [not guessing which container came
from which repository](roadmap.md).

So the id is on the flag, and whoever knows the estate writes it:

```console
--api service/shop/checkout=openapi.yaml
```

The id names an element, and a container is one: a namespace owns a surface
as well as a service does, because nodes and containers share one namespace
and an edge may point at either.

Without it the operations still arrive. A surface read on its own is a
listing — which operations exist, which are deprecated, which belong to a
team — and the graph says nobody has placed it by joining it to nothing. An id
that names nothing in the graph is an error rather than a silent drop: the
join was the question the flag was answering.

The owner is what comes before the `=`, so a leading `=` says there is no
owner — which is how a document whose path has an `=` in it is written down:

```console
--api =reports/q1=final.yaml
```

## Which function serves it

`--api` answers "who offers these operations" at the granularity of the estate:
one id, one document, every operation on it. That is the right granularity for
the box a request arrives at, and the wrong one for the question a reader has
once they are behind it — *which code answers this*.

The other half is written down the same way, one operation at a time, as an
overlay assertion:

```json
{
  "assert": "serves",
  "subject":   { "node": "repo-2-svc:file:handler/http.go#HandleOrder" },
  "operation": { "node": "api/checkout/get/orders/{id}" },
  "author": "operator",
  "note": "the router registers it under this path"
}
```

It joins a `code_function` to an `api` operation and refuses anything else by
name: a file, a surface, the service. The two ends are `subject` and
`operation` rather than `from` and `to` because an edge assertion's ends are
interchangeable and these are not — the claim is false if they are swapped, and
naming them apart is what lets the refusal say which end was wrong.

Neither end is ever adopted. A selector that matches nothing is dropped and
reported, whatever `--overlay-unmatched` says, because a function nobody parsed
is not code and inventing one would put a box on the code map that no reading
of the repository produced. Ids are the ones in the graph, so with more than
one input they are the qualified ones — `repo-2-svc:file:…`.

It carries no `kind`. What somebody writes down is what a service is meant to
do; `observed` comes from something that watched it, and `reachable` says
nothing here at all. The edge is `iac_ref`, like a declared route.

Denying one is an ordinary `edge.suppress` naming the same two ends and
`iac_ref` — it says nothing about the relation, the way every edge assertion
says nothing about it, and it reaches the claim whichever of the two was
written first. The kind is not optional in practice: `kind` defaults to
`observed`, and a denial written without one lands on an observed line nobody
drew rather than on the claim. A denied claim never puts the operation on a code map — a box
there because of a line somebody denied would be the page arguing with
itself — so the line is drawn, faintly, only where the operation is on the page
for a claim nobody denied.

What it is for is the descent. A reader who arrived from a running container,
went behind it to the repository and is looking at its code map can now see
which of those functions is where a request comes in — including the ones
nothing in the tree calls, which are exactly the ones the call graph cannot
place. See [atlas.md](atlas.md).

A router registration is the same fact written in the source, and reading one
would land the same edge without anybody typing it. That is a parser's job, per
framework, and none of them is read yet.

## Why an operation is not matched to a route

A route carries `attrs.entry`, the host and path a rule matched on, and an
operation carries a path of its own. They are not compared, and the comparison
would be wrong if it were made: an Ingress rule is `Prefix` or `Exact`, a
server URL carries a base path, and an operation path holds `{id}` where a
request holds a number. Deciding whether a request belongs to a rule means
reading the rule — which is [what paths.md says about `entry`](paths.md), and
this is the same line drawn from the other side.

What both halves are for is the question underneath: *which API is nobody
using*. The route says a way in was quiet; the surface says what is on the
other side of it. Joining them is a claim somebody can write down — an
overlay assertion is a claim with an author — and not one this parser makes
from two strings looking alike.

## Why a surface is a container rather than a node

One document, one surface: that is the document's own claim about itself, and
containment is what the IR has for saying it. It holds whether or not anybody
has said which element serves those operations, so the operations are never
loose in the drawing.

It is on an axis of its own, `api`, because a surface is not network
containment. An operation is not in a namespace; the service that offers it
is. Nesting by `--axis api` draws the surfaces, and the default network view
leaves them where they are.

Two documents with one title are refused rather than merged. They are two
surfaces the estate cannot tell apart, and putting their operations in one
container would answer "which API is this" with a box holding two of them.

## What is not read

**Swagger 2.0.** Its paths are relative to `basePath`, so an operation read as
written would be at a path the service does not serve. It is refused by name
rather than read approximately.

**Schemas, parameters, responses, security.** They describe how to call an
operation, and nothing here draws that yet. The reading is deliberately the
shallow one: the entity, and the few fields a listing is built from.

**`$ref`.** A path item that is only a reference is counted and reported —
`1 paths held no operation` — rather than followed. Following one means
resolving a file the document points at, and what arrives here should be one
document read once.

An anchor is not that. `&defaults`, `*defaults` and `<<:` are the document
written twice and already resolved — there is no second file — so they are
read, and an operation that arrives by one is declared like any other.

**gRPC.** A `.proto` service declares the same shape — these operations
exist — and is the obvious second reader. It is not written yet.
