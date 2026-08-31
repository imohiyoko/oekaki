# Kubernetes manifests

`oekaki` reads a stream of Kubernetes manifests — what `helm template`,
`kustomize build` or `kubectl get -o yaml` writes — and emits the same IR the
Terraform parser does.

```console
$ helm template ./chart | oekaki render - -o app.svg
$ kubectl get all,ingress -n shop -o yaml | oekaki render - -f html -o shop.html
$ cat manifests/*.yaml | oekaki graph - -o shop.json
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
| `reads` | workload | ConfigMap, Secret | `envFrom`, `env.valueFrom`, volumes, `imagePullSecrets` |
| `mounts` | workload | PersistentVolumeClaim | `volumes[].persistentVolumeClaim` |
| `runs-as` | workload | ServiceAccount | `spec.serviceAccountName` |
| `owned-by` | any | its owner | `metadata.ownerReferences` |

A Namespace becomes a container on the network axis rather than a node, so
`--axis network` nests workloads inside it.

Traffic between two workloads is **not** inferred. Manifests do not record it,
and an edge invented here would arrive in the same colour as one that was
read. Use traces, metrics, or a log inventory to add `observed` evidence when
runtime use matters.

## What it does not read yet

- **NetworkPolicy**, which would be a `reachable` edge rather than an
  `iac_ref` one. The semantics are worth getting right rather than early:
  a policy only restricts pods it selects, and a graph that says otherwise
  would report firewall rules that do not exist.
- **StatefulSet `volumeClaimTemplates`**, which create a PVC per replica
  rather than naming an existing one.
- **RBAC**, `PodDisruptionBudget`, `StorageClass`, and custom resources.
  These still become nodes; nothing is read out of them.

## Objects it cannot place

Nothing is dropped. An object whose apiVersion is not in the table below
becomes a node with `api_unknown`, and one whose apiVersion is no longer
served becomes a node with `api_removed_in`. A referenced object that is not
in the input — a Secret nobody committed — becomes a node with
`declared_only`, because a dependency on something absent is the most useful
thing this graph can show.

`metadata.source_version` on the resulting graph is the **oldest Kubernetes
release that serves every apiVersion in the input**: the floor of what a
cluster must be running to accept these manifests.

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
| `v1` | PersistentVolumeClaim | 1.0 | — | — |
| `v1` | Pod | 1.0 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `v1` | Secret | 1.0 | — | — |
| `v1` | Service | 1.0 | — | `selector` |
| `v1` | ServiceAccount | 1.0 | — | — |
| `apps/v1` | DaemonSet | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1` | Deployment | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta2` | Deployment | 1.8 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1beta1` | Deployment | 1.6 | **1.16** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1` | ReplicaSet | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `apps/v1` | StatefulSet | 1.9 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `autoscaling/v2` | HorizontalPodAutoscaler | 1.23 | — | `scaleTarget` |
| `autoscaling/v1` | HorizontalPodAutoscaler | 1.2 | — | `scaleTarget` |
| `autoscaling/v2beta2` | HorizontalPodAutoscaler | 1.12 | **1.26** | `scaleTarget` |
| `autoscaling/v2beta1` | HorizontalPodAutoscaler | 1.8 | **1.25** | `scaleTarget` |
| `batch/v1` | CronJob | 1.21 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `batch/v1beta1` | CronJob | 1.8 | **1.25** | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `batch/v1` | Job | 1.2 | — | `configmap`, `secret`, `pvc`, `serviceaccount`, `owner` |
| `extensions/v1beta1` | Ingress | 1.2 | **1.22** | `backend` |
| `networking.k8s.io/v1` | Ingress | 1.19 | — | `backend` |
| `networking.k8s.io/v1beta1` | Ingress | 1.14 | **1.22** | `backend` |

The table is generated from `parsers/kubernetes/versions.go`; a test fails if
this document and that table disagree. Adding a kind means adding a row, and
the test refuses a row nothing recognises.

Deprecation and removal releases follow the [Kubernetes deprecated API
migration guide](https://kubernetes.io/docs/reference/using-api/deprecation-guide/).
