# Production executor identity

The backend production executor uses `rest.InClusterConfig` and the dedicated
`knull-production-executor` service account. Deploy it only after replacing the
namespace and workload placeholders in `rbac.yaml` with the exact approved
target. Give the backend pod this service account only in that namespace; do
not share the investigation MCP identity or its kubeconfig. The Role permits
`get` and `patch` on one named Deployment and nothing else.

Configure the backend with the exact cluster, namespace, workload, and SHA-256
hex digest of the API server CA certificate mounted into that pod. The runtime
checks the CA digest before opening a Kubernetes client, and rejects action
targets outside that scope. Credentials and CA private material do not belong
in this repository. The executor is not enabled by this manifest alone.

Set `KNULL_PRODUCTION_EXECUTION_ENABLED=true` and all four
`KNULL_PRODUCTION_*` scope values in the backend environment to enable the
route. Empty or partial settings leave execution disabled or prevent startup.
An authenticated operator may then POST `actionEventId` and `actionDigest` to
`/api/incidents/{id}/executions/memory` from the configured UI origin. The
backend consumes an unexpired approval, applies one JSON Patch attempt, stores
the submitted patch and resulting resource version, and enters verification
only after it reads back the approved `1Gi` value. A timeout or conflicting
read leaves the incident in remediation for explicit reconciliation; callers
must not replay the request.
