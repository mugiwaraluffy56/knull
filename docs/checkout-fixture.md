# Checkout memory-limit fixture

`backend/cmd/checkout-fixture` is a disposable workload for the primary
`256Mi → 1Gi` validation scenario. It contains no production data or secrets.
The first `GET` or `POST /checkout` touches and retains 384 MiB by default.
At a 256 MiB container limit this should trigger an OOM kill; at 1 GiB the
same request should complete and later requests should be fast. The actual
outcome must be observed in the dedicated sandbox cluster before approval.

Local Docker verification on 2026-09-26 used `--memory=256m
--memory-swap=256m` and produced `OOMKilled=true`, exit code 137. The same
image under `--memory=1g --memory-swap=1g` returned `{"checkout":"ok"}`
and remained running. Docker Desktop allowed the 256 MiB process to respond
when swap was not capped, so cap swap when reproducing the memory failure
locally. These results do not substitute for dedicated EKS verification.

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

On 2026-09-26, the pinned fixture image in the dedicated Mumbai EKS sandbox
was `079485644745.dkr.ecr.ap-south-1.amazonaws.com/knull/checkout-fixture@sha256:21c227e6d4d57c486ec88824dcf672d213daa68d37b63727c878fd5bc50696a5`.
A restricted runner made separate `256Mi` and `1Gi` namespace runs. The
baseline `/checkout` request ended with EOF and independent pod observation
recorded two OOM kills with only one of two pods healthy. The candidate served
20 of 20 requests with HTTP 200; both pods were healthy and neither was OOM
killed. Both runs returned `checked` with `CleanupVerified=true`, and their
namespaces were independently confirmed absent. The load used a Kubernetes
API port-forward with a sandbox-only service-account token; port-forward may
bypass ingress NetworkPolicy and is not evidence of ingress policy behavior.
TrueForge-generated execution and sandbox-local Prometheus observations still
require their own live verification before Task 18 can pass.
