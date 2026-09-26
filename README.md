# Knull

An AI SRE that investigates cloud incidents, proves a fix in a sandbox, and asks
before it touches production.

Monitoring tells you that production is broken. Knull works out why. It tests a
fix, then puts production one click away from recovery.

> [!NOTE]
> Knull is in design. The product brief is in [`docs/PRD.md`](docs/PRD.md),
> the detailed behavior and architecture are in [`docs/SPEC.md`](docs/SPEC.md),
> and the implementation checklist is in
> [`docs/IMPLEMENT.md`](docs/IMPLEMENT.md). The application code is not written
> yet, so this README describes the agreed implementation plan.

## Why this matters

Your infrastructure makes far more signals than a person can read. Alerts,
logs, metrics, traces, deploys, and config changes all pile up at once.

When something breaks, an engineer has to jump between Kubernetes, Prometheus,
logs, deploy history, GitHub, and runbooks. They do this to answer three
questions:

1. What is broken?
2. What caused it?
3. What is safe to do about it?

That work does not scale. One engineer can only chase one incident at a time,
and a large fleet has many. Knull does the first pass, so the engineer picks up
an incident that is already mostly solved.

## How it works

Knull owns the whole path from alert to proven recovery:

```text
ALERT
  -> INVESTIGATION
  -> ROOT CAUSE
  -> REMEDIATION PLAN
  -> SAFE VALIDATION
  -> HUMAN APPROVAL
  -> PRODUCTION ACTION
  -> VERIFY RECOVERY
```

### An example

Say `checkout-api` starts to fail. The error rate jumps from 2% to 38%. Latency
goes from 180ms to 3.4s. Two of six pods are sick.

Knull looks into it on its own:

```text
incident detected
  -> Prometheus: error-rate spike confirmed
  -> Kubernetes: 2 pods in CrashLoopBackOff
  -> logs: OOMKilled
  -> GitHub: last deploy cut the memory limit from 1Gi to 256Mi
  -> Jev: memory-limit regression is the leading hypothesis, backed by OOM and GitHub evidence
  -> sandbox: try 1Gi under load
  -> load test: 0% errors, 6/6 pods healthy
  -> fix ready for approval
```

Then it stops and asks you:

```text
PRODUCTION CHANGE

checkout-api
memory: 256Mi -> 1Gi

expected impact: restart 6 pods
validation: passed

[ DENY ]  [ APPLY ]
```

If you hit APPLY, Knull makes the change and watches the service until it can
show that the service is well again.

## The safety line

> [!IMPORTANT]
> Knull never changes production on its own. A human has to approve first.

Reads are free. Knull can list pods, read logs, and run metric queries whenever
it wants. Writes are not free. Each of these needs a human to say yes:

- Restart a deployment
- Scale replicas
- Change CPU or memory
- Roll back a deployment

Before you decide, the screen shows the exact change, what it will do, and the
sandbox results from a dedicated EKS cluster separate from production.

## Parts of the system

| Part | What it does |
| --- | --- |
| **TrueForge** | The agent harness. It runs the long investigation, holds context, calls tools, spawns subagents, and pauses for approval. |
| **Jev** | The OpenAI-backed decision layer. It turns evidence into schema-validated typed recommendations; it cannot approve or execute changes. |
| **`jev-mcp`** | The Go MCP server this project builds. It exposes Jev's structured decisions as tools. |
| **Kubernetes MCP** | A pinned, read-only MCP server for cluster investigation. A separate typed Go executor applies approved changes. |
| **Prometheus MCP** | A pinned, read-only MCP server for alert and metric queries. |
| **GitHub MCP** | GitHub's official MCP server, restricted to read-only access on mapped repositories. |
| **Next.js GUI** | Shows the fleet, the live investigation, the approval screen, and recovery. |

Knull reuses MCP servers for Kubernetes, Prometheus, and GitHub. It builds
`jev-mcp` and the backend policy/execution services. External MCP servers are
not granted production mutation credentials.

### What Jev decides

Jev answers four questions with typed results, evidence references, and uncertainty:

- `classify_incident` - is this `RESOURCE_EXHAUSTION`, `BAD_DEPLOYMENT`,
  `DEPENDENCY_FAILURE`, `TRAFFIC_SPIKE`, `CONFIGURATION_ERROR`, or `UNKNOWN`?
- `select_next_action` - should it `INVESTIGATE_MORE`, `TEST_REMEDIATION`,
  `REMEDIATE`, or `ESCALATE`?
- `classify_remediation_risk` - is the fix `LOW`, `MEDIUM`, or `HIGH` risk?
- `verify_recovery` - is the service `RECOVERED`, `PARTIALLY_RECOVERED`,
  `NOT_RECOVERED`, or `UNCERTAIN`?

Scores help Knull decide whether to gather more evidence or escalate. They never
authorize a production change: every production mutation requires a fresh human
approval bound to the exact proposed action.

## Scope

Knull handles three kinds of incident to start:

1. **Bad resource config.** The memory limit is too low, so the pod is
   OOMKilled.
2. **Bad deployment.** A new image crashes, so the pod lands in
   CrashLoopBackOff.
3. **Traffic spike.** The CPU saturates, so latency and errors climb.

> [!WARNING]
> These are out of scope for now: many clouds at once, free-form Terraform
> changes, database and security incidents, full incident management, writes
> without approval, a learning system, and role-based access control. The goal
> is one loop that works well.

## Planned stack and deployment

```text
Agent runtime      TrueForge
Decision model     OpenAI API through Go Jev MCP
Agent tools        MCP
Infrastructure     AWS EKS (customer account)
Metrics            Prometheus
Logs               Kubernetes logs
Source control     GitHub official MCP (read-only)
Alert intake       Prometheus Alertmanager webhook
Sandbox            Separate customer EKS cluster; fresh namespace per run
Backend            Go (`net/http`) + Go Jev MCP
Frontend           Next.js / TypeScript
State              PostgreSQL + Redis
Operator sign-in   Customer OIDC provider; Keycloak in local development
```

The initial install runs in the customer's AWS account. It requires a
production EKS cluster, a separate sandbox EKS cluster, customer-managed
PostgreSQL and Redis, Prometheus/Alertmanager, and a read-only GitHub App. The
customer configures the OpenAI API credential; redacted incident evidence is
sent to that API. Setup steps will be documented with the implementation.

## Repo layout

```text
docs/PRD.md       The product brief.
docs/SPEC.md      Detailed product and architecture specification.
docs/IMPLEMENT.md Dependency-ordered implementation checklist.
README.md         This file.
```

More directories will show up as the code grows. This section will track them.

## Contributing

Read [`docs/PRD.md`](docs/PRD.md) for the product brief and
[`docs/SPEC.md`](docs/SPEC.md) for the source of truth on product behavior and
architecture. Use [`docs/IMPLEMENT.md`](docs/IMPLEMENT.md) to track delivery.

Please sign off your commits with `git commit -s`.
