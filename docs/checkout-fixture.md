# Checkout memory-limit fixture

`backend/cmd/checkout-fixture` is a disposable workload for the primary
`256Mi → 1Gi` validation scenario. It contains no production data or secrets.
The first `GET` or `POST /checkout` touches and retains 384 MiB by default.
At a 256 MiB container limit this should trigger an OOM kill; at 1 GiB the
same request should complete and later requests should be fast. The actual
outcome must be observed in the dedicated sandbox cluster before approval.

From `backend/`, build with
`docker build -f cmd/checkout-fixture/Dockerfile -t <sandbox-registry>/checkout-fixture:<version> .`.
Push to a pull-only sandbox registry and use the resulting immutable
`@sha256:` image digest as the sandbox workload input. The runner accepts
only images from its configured registry. Set `Container` to the fixture
container name, `Replicas` to 2, `CPU` to `500m`, and `Memory` to `1Gi` for
the candidate; the baseline run uses `256Mi` from the sealed action.

`/healthz` reports process liveness. `/metrics` exposes successful request
counts and latency for requests that returned. Because an OOM-killed process
cannot report requests that died with it, calculate validation error rate and
latency from an independent sandbox load driver or Prometheus source, and
also read Kubernetes OOM and pod readiness signals. The default-deny network
policy must be extended with narrowly scoped rules for the selected sandbox
load driver and Prometheus scraper before this fixture can be exercised.

Do not treat a local process run or a mock as the live Task 18 verification.
