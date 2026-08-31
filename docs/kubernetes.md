# Kubernetes manifests

`oekaki` reads a stream of Kubernetes manifests — what `helm template`,
`kustomize build` or `kubectl get -o yaml` writes — and emits the same IR the
Terraform parser does.

```console
helm template ./chart | oekaki render - -o app.svg
kubectl get all,ingress -n shop -o yaml | oekaki render - -f html -o shop.html
cat manifests/*.yaml | oekaki graph - -o shop.json
```

Nothing is read from a cluster by the parser itself and no kubeconfig is
touched: the documents you pipe in are the whole input.

## What it reads

An edge is drawn when one object names another, because that is the
relationship a manifest actually records:

| relation | from | to | read from |
| --- | --- | --- | --- |
| `selects` | Service | workload | `spec.selector` matched against the pod template's labels |
| `routes` | Ingress | Service | `backend.service.name`, and the older `backend.serviceName` |
| `scales` | HorizontalPodAutoscaler | workload | `spec.scaleTargetRef` |
| `reads` | workload, Ingress, ServiceAccount | ConfigMap, Secret | `envFrom`, `env.valueFrom`, volumes, `imagePullSecrets`, `tls[].secretName`, a ServiceAccount's own secrets |
| `mounts` | workload | PersistentVolumeClaim | `volumes[].persistentVolumeClaim` |
| `runs-as` | workload | ServiceAccount | `spec.serviceAccountName` |
| `owned-by` | any | its owner | `metadata.ownerReferences` |
| `governed-by` | StatefulSet | Service | `spec.serviceName`, the governing (usually headless) Service |
| `handled-by` | Ingress | IngressClass | `spec.ingressClassName` |
| `provisioned-by` | PersistentVolumeClaim | StorageClass | `spec.storageClassName` |
| `bound-to` | PersistentVolumeClaim | PersistentVolume | `spec.volumeName` |
| `prioritised-by` | workload | PriorityClass | `spec.priorityClassName` |
| `measures` | HorizontalPodAutoscaler | any | `spec.metrics[].object.describedObject` |
| `grants` | RoleBinding, ClusterRoleBinding | Role, ClusterRole | `roleRef` |
| `binds` | RoleBinding, ClusterRoleBinding | ServiceAccount | `subjects[]` |
| `restricts` | NetworkPolicy | workload | `spec.podSelector` matched against the pod template's labels |
| `allows-ingress`, `allows-egress` | NetworkPolicy | workload, CIDR | `ingress[].from` and `egress[].to`. The direction is part of the relation, so a peer allowed both ways keeps two edges. It cannot be an attribute: edges agreeing on ends, kind and relation are merged without their attributes being read, and one of the directions would be lost in the merge |

A Namespace becomes a container on the network axis rather than a node, so
`--axis network` nests workloads inside it.

Traffic between two workloads is **not** inferred. Manifests do not record it,
and an edge invented here would arrive in the same colour as one that was
read. Use traces, metrics, or a log inventory to add `observed` evidence when
runtime use matters.

## What it does not read yet

- **StatefulSet `volumeClaimTemplates`**, which create a PVC per replica
  rather than naming an existing one.
- **A binding's User and Group subjects.** They are not objects in the
  cluster, so there is nothing to point at; a box for a name in a text field
  would put an identity on the diagram that no manifest creates.
- **`PodDisruptionBudget` selectors**, and custom resources. These still
  become nodes; nothing is read out of them.

## NetworkPolicy

A NetworkPolicy is read the way a security group is: what the object says, and
nothing about what the network then does. Which workloads a policy isolates
becomes a `restricts` edge, and each rule's peers become `allows` edges. Both
are `iac_ref` — they are written down, not inferred.

Whether a path is *permitted* is not decided here. Two things stop it:

- **The default is allow.** A namespace with no policy lets every pod reach
  every other, so "what can reach this" is the complete graph until something
  restricts it, and the fact worth having lives in the edges that are missing.
  Missing edges cannot be drawn.
- **A policy is enforced by the CNI, which no manifest mentions.** On a cluster
  whose plugin does not implement NetworkPolicy the object is accepted and
  changes nothing. "This policy allows A" stays true there; "only A can reach
  B" does not.

So the first is written down by the parser and the second is derived by the
reachable enricher, which is where security group reachability is derived too.
Ask for it with `--reachable`:

```console
oekaki render manifests.yaml --reachable --view reachability -f html -o reach.html
```

A path is drawn when **every restricted end permits it and at least one end is
restricted**. A pair with nothing restricting either end is left out: that is
the Kubernetes default, and drawing it would draw the complete graph. Which
ends are restricted is not left implicit — the `restricts` edges say so, which
is what keeps a missing reachable edge from reading as a blocked path.

Both ends are checked. A sender whose egress is restricted and does not name
the destination cannot reach it, however open the destination is.

A policy with peers this input could not resolve permits more than what is
drawn, and the enricher reports it rather than letting the drawn subset read as
the whole.

A rule with ports and no peers allows every source on those ports, and points
at the same `external:internet` node the enrichers use.

A rule this input cannot evaluate is recorded on the policy rather than
dropped — a `namespaceSelector` with no Namespace objects to match against, or
any selector using `matchExpressions`. A policy whose reach is partly unknown
must not read as a policy that reaches only what was resolved.

## Objects it cannot place

Nothing is dropped. An object whose apiVersion is not in the table below
becomes a node with `api_unknown`, and one whose apiVersion is no longer
served becomes a node with `api_removed_in`. A referenced object that is not
in the input — a Secret nobody committed — becomes a node with
`declared_only`, because a dependency on something absent is the most useful
thing this graph can show.

A document part that holds no object — YAML that will not decode, a List item
that is not a mapping, an object with only a `generateName` and therefore no
name yet — is counted and reported. "Nothing is dropped" is a claim this makes,
and a silent skip is how such a claim stops being true.

An object the input defines twice — concatenate a base and an overlay, or two
chart renders sharing a namespace-level ConfigMap — is drawn once, from the
first definition, and counted. The repetition is a fact about the input and is
reported as one.

A Service whose selector holds a value that is not a string matches nothing
and says so. Dropping the unreadable pair would widen the match, and the extra
workloads would arrive looking exactly like the intended ones.

A cluster-scoped object — a ClusterRole, a PersistentVolume, a
CustomResourceDefinition — is left outside every namespace rather than filed
under `default`. So is an object of a kind not in the table that names no
namespace of its own: the scope is genuinely unknown there, and a guess would
be drawn as a fact.

`metadata.source_version` on the resulting graph is the **oldest Kubernetes
release that serves every apiVersion in the input**: the floor of what a
cluster must be running to accept these manifests.

The releases that accept a whole document set are the interval
`[floor, ceiling)` — the newest apiVersion sets the floor, the earliest
removal sets the ceiling. These can cross. A set holding both
`autoscaling/v2`, which arrived in 1.23, and an `extensions/v1beta1` Ingress,
which stopped being served in 1.22, is one no cluster runs. `source_version`
is then left empty and the contradiction is reported, because naming 1.23
there would promise a release that refuses half the input.

## Supported apiVersions

Checked against Kubernetes **1.37**. This is a floor, not a ceiling: a
manifest written for a later release parses as long as it uses an apiVersion
below, and one that uses something newer is drawn rather than dropped.

Releases in bold are the ones that **stopped** serving that apiVersion — an
object still using it will not apply to a cluster at or past that release.

| apiVersion | kind | since | removed in | recovers |
| --- | --- | --- | --- | --- |
| `v1` | ConfigMap | 1.0 | — | — |
| `v1` | Namespace | 1.0 | — | — |
| `v1` | PersistentVolume | 1.0 | — | — |
| `v1` | PersistentVolumeClaim | 1.0 | — | `storageClass`, `volume` |
| `v1` | Pod | 1.0 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `v1` | Secret | 1.0 | — | — |
| `v1` | Service | 1.0 | — | `selector` |
| `v1` | ServiceAccount | 1.0 | — | `secret` |
| `apps/v1` | DaemonSet | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta2` | DaemonSet | 1.8 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1` | Deployment | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta2` | Deployment | 1.8 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta1` | Deployment | 1.6 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1` | ReplicaSet | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta2` | ReplicaSet | 1.8 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1` | StatefulSet | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner`, `governingService` |
| `apps/v1beta2` | StatefulSet | 1.8 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta1` | StatefulSet | 1.6 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `autoscaling/v2` | HorizontalPodAutoscaler | 1.23 | — | `scaleTarget`, `metricObject` |
| `autoscaling/v1` | HorizontalPodAutoscaler | 1.2 | — | `scaleTarget` |
| `autoscaling/v2beta2` | HorizontalPodAutoscaler | 1.12 | **1.26** | `scaleTarget`, `metricObject` |
| `autoscaling/v2beta1` | HorizontalPodAutoscaler | 1.8 | **1.25** | `scaleTarget` |
| `batch/v1` | CronJob | 1.21 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `batch/v1beta1` | CronJob | 1.8 | **1.25** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `batch/v1` | Job | 1.2 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `extensions/v1beta1` | DaemonSet | 1.1 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `extensions/v1beta1` | Deployment | 1.1 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `extensions/v1beta1` | Ingress | 1.2 | **1.22** | `backend` |
| `extensions/v1beta1` | NetworkPolicy | 1.3 | **1.16** | `restricts`, `allows` |
| `extensions/v1beta1` | ReplicaSet | 1.2 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `networking.k8s.io/v1` | Ingress | 1.19 | — | `backend`, `tls`, `ingressClass` |
| `networking.k8s.io/v1beta1` | Ingress | 1.14 | **1.22** | `backend` |
| `networking.k8s.io/v1` | IngressClass | 1.19 | — | — |
| `networking.k8s.io/v1` | NetworkPolicy | 1.7 | — | `restricts`, `allows` |
| `policy/v1` | PodDisruptionBudget | 1.21 | — | — |
| `policy/v1beta1` | PodDisruptionBudget | 1.5 | **1.25** | — |
| `rbac.authorization.k8s.io/v1` | ClusterRole | 1.8 | — | — |
| `rbac.authorization.k8s.io/v1` | ClusterRoleBinding | 1.8 | — | `role`, `subjects` |
| `rbac.authorization.k8s.io/v1` | Role | 1.8 | — | — |
| `rbac.authorization.k8s.io/v1` | RoleBinding | 1.8 | — | `role`, `subjects` |
| `scheduling.k8s.io/v1` | PriorityClass | 1.14 | — | — |
| `storage.k8s.io/v1` | StorageClass | 1.6 | — | — |

The table is generated from `parsers/kubernetes/versions.go`; a test fails if
this document and that table disagree. Adding a kind means adding a row, and
the test refuses a row nothing recognises.

Deprecation and removal releases follow the [Kubernetes deprecated API
migration guide](https://kubernetes.io/docs/reference/using-api/deprecation-guide/).
