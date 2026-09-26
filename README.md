# Knull

An AI SRE that investigates cloud incidents, proves a fix in a sandbox, and asks
before it touches production.

Monitoring tells you that production is broken. Knull works out why. It tests a
fix, then puts production one click away from recovery.

> [!NOTE]
> Knull is in design. This repo holds the product spec in
> [`docs/PRD.md`](docs/PRD.md). The code is not written yet, so there is nothing
> to install so far. This README explains the plan and will grow as the code
> lands.

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
  -> Jev: the memory limit is the likely cause (97% confidence)
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
- Change infrastructure or production config
- Delete a resource
- Block an IP

Before you decide, the screen shows the exact change, what it will do, and the
sandbox results that back it up.

## Parts of the system

| Part | What it does |
| --- | --- |
| **TrueForge** | The agent harness. It runs the long investigation, holds context, calls tools, spawns subagents, runs the sandbox, and pauses for approval. |
| **Jev** | The decision engine. TrueForge gathers proof; Jev turns that proof into a typed decision with a confidence score. |
| **`jev-mcp`** | The one MCP server this project builds. It exposes Jev's decisions as tools. |
| **Kubernetes MCP** | Reads cluster state. Also holds the guarded write tools. |
| **Prometheus MCP** | Runs metric queries for CPU, memory, latency, errors, and traffic. |
| **GitHub MCP** | Reads recent commits, pull requests, and manifests to spot what changed. |
| **Next.js GUI** | Shows the fleet, the live investigation, the approval screen, and recovery. |

Knull reuses MCP servers that already exist for Kubernetes, Prometheus, and
GitHub. It only builds `jev-mcp`, because that is the part that is new.

### What Jev decides

Jev answers four questions, and each answer carries a probability:

- `classify_incident` - is this `RESOURCE_EXHAUSTION`, `BAD_DEPLOYMENT`,
  `DEPENDENCY_FAILURE`, `TRAFFIC_SPIKE`, `CONFIGURATION_ERROR`, or `UNKNOWN`?
- `select_next_action` - should it `INVESTIGATE_MORE`, `TEST_REMEDIATION`,
  `REMEDIATE`, or `ESCALATE`?
- `classify_remediation_risk` - is the fix `LOW`, `MEDIUM`, or `HIGH` risk?
- `verify_recovery` - is the service `RECOVERED`, `PARTIALLY_RECOVERED`,
  `NOT_RECOVERED`, or `UNCERTAIN`?

Scores matter here. Knull can act when a score is high. When it is low, Knull
hands the incident to a person instead of guessing.

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

## Planned stack

```text
Agent runtime      TrueForge
Decision model     Jev
Agent tools        MCP
Infrastructure     Kubernetes
Metrics            Prometheus
Logs               Kubernetes logs
Source control     GitHub MCP
Sandbox            TrueForge sandbox
Backend            TypeScript / Node.js
Frontend           Next.js
State              SQLite
```

To run Knull later, you will need a Kubernetes cluster, Prometheus scraping it,
a GitHub repo with your manifests, and Node.js. Setup steps go here once the
code exists.

## Repo layout

```text
docs/PRD.md    The product spec. Start here.
README.md      This file.
```

More directories will show up as the code grows. This section will track them.

## Contributing

Read [`docs/PRD.md`](docs/PRD.md) first. It is the source of truth for what
Knull should do, and it explains the choices behind the design.

Please sign off your commits with `git commit -s`.
