# Dedicated sandbox cluster prerequisite

Knull's validation cluster must be a customer-provisioned EKS cluster separate
from every production cluster. Per-run namespaces limit resources and make
cleanup possible; they are not the isolation boundary. Configure the incident
API with a kubeconfig containing only a sandbox-only Kubernetes identity.
The identity needs to read `kube-system` namespace identity and create, apply,
read, and delete namespaces, ResourceQuotas, LimitRanges, NetworkPolicies, and
Deployments in the sandbox cluster. It must have no production permissions.

Set `KNULL_SANDBOX_KUBECONFIG` to that kubeconfig's path and
`KNULL_SANDBOX_CONTEXT` to its exact context. Set
`KNULL_SANDBOX_CLUSTER_UID` to the UID of the sandbox `kube-system` namespace
and `KNULL_PRODUCTION_CLUSTER_UIDS` to a comma-separated list of the same UIDs
from all production clusters. The runner compares the live sandbox UID to the
expected UID and rejects every listed production UID before creating a run.
Set `KNULL_SANDBOX_IMAGE_REGISTRY` to the pull-only registry prefix and,
if needed, `KNULL_SANDBOX_PULL_SECRET_NAME` to a pull-only Secret installed in
each new run namespace by a trusted admission controller. Knull does not copy
production image credentials, Secrets, data, or workload environment values.
An image must be pinned by digest.

For each proposal, `POST /api/incidents/{id}/sandbox-runs` accepts the stored
`actionEventId` and a sanitized workload containing `imageDigest`, `container`,
`replicas`, `cpu`, and `memory`. The runner creates an isolated run namespace,
quota, limit range, default-deny network policy, and restricted deployment.
It disables service-account token automount. The candidate's changed field
must match the sealed action contract. It then deletes the namespace and
verifies deletion. The incident timeline stores provenance, limitations,
cleanup result, and failure details. The status `prepared` means the sandbox
was provisioned and cleaned up; it is not a passing validation. Task 18 adds
load and recovery checks before approval can become possible.

Live verification requires a real sandbox EKS cluster and sandbox-only
credentials. Run the scenario with a candidate image that can start without
production Secrets or data. Verify the production identity cannot access the
sandbox and the sandbox identity cannot access any production cluster before
allowing an approval workflow.
