# AI SRE / Autonomous Cloud Operations Agent

## One-line product

An AI SRE that investigates cloud incidents across your infrastructure, safely validates the fix, and asks before touching production.

## Product pitch

Monitoring tells you that production is broken. Our agent figures out why, proves a fix, and gets production one click away from recovery.

## 1. Problem

Modern infrastructure generates more alerts, logs, metrics, traces, deployments, and configuration changes than an SRE can continuously investigate manually.

When an incident occurs, engineers typically jump between Kubernetes, Prometheus, logs, deployment history, GitHub, cloud infrastructure, and internal runbooks to answer three questions:

1. What is actually broken?
2. What caused it?
3. What action is safe to take?

At fleet scale, this does not scale with humans. Cloudways reports using an AI SRE system across more than 845,000 customer applications, with more than 125,000 annual investigations and a 70% reduction in MTTR. This indicates that autonomous infrastructure investigation and remediation addresses a real operational need.

## 2. Product

Build an **autonomous SRE agent that continuously investigates infrastructure incidents, identifies root causes, validates remediation safely, and executes approved fixes.**

The product owns the operational path from alert to verified recovery:

```text
ALERT
  ↓
INVESTIGATION
  ↓
ROOT CAUSE
  ↓
REMEDIATION PLAN
  ↓
SAFE VALIDATION
  ↓
HUMAN APPROVAL
  ↓
PRODUCTION ACTION
  ↓
VERIFY RECOVERY
```

The engineer receives an incident after most of the investigation has already been done.

## 3. Example incident

A Kubernetes service suddenly starts failing:

```text
checkout-api

error rate       2% → 38%
latency          180ms → 3.4s
pods             4/6 unhealthy
```

The agent investigates automatically:

```text
incident detected
  ↓
Prometheus MCP: error-rate spike confirmed
  ↓
Kubernetes MCP: 2 pods in CrashLoopBackOff
  ↓
logs: OOMKilled
  ↓
GitHub MCP: latest deployment changed memory limit 1Gi → 256Mi
  ↓
Jev: likely root cause is an incorrect memory limit (97% confidence)
  ↓
TrueForge sandbox: test proposed configuration at 1Gi
  ↓
load test: error rate 0%, pods healthy 6/6
  ↓
remediation ready for approval
```

The approval screen shows the proposed production change, expected impact, and validation results:

```text
PRODUCTION CHANGE

checkout-api
memory: 256Mi → 1Gi

expected impact: restart 6 pods
validation: passed

[ DENY ]  [ APPLY ]
```

After approval, the agent applies the change and watches production until recovery is verified.

## 4. Core value

This is an operational agent, not a chat interface for infrastructure or an error explainer. It completes the loop:

```text
observe → investigate → reason → experiment → repair → verify
```

The agent's value comes from operating at a scale where people cannot continuously correlate telemetry by hand: hundreds of services, thousands of pods, millions of logs, thousands of metrics, hundreds of alerts, and continuous deployments.

## 5. Target users

**Primary:** SRE, DevOps, and platform engineering teams.

**Secondary:** cloud hosting companies, SaaS companies, internal developer platforms, Kubernetes operators, and managed infrastructure providers.

## 6. Core user stories

1. As an SRE, I want infrastructure alerts automatically investigated so I do not manually correlate metrics, logs, and deployments.
2. As an SRE, I want the agent to inspect Kubernetes state when a workload becomes unhealthy.
3. As an SRE, I want the agent to correlate an incident with recent deployments and configuration changes.
4. As an SRE, I want the agent to inspect relevant metrics and logs without deciding every query myself.
5. As an SRE, I want the agent to identify the most likely root cause and show supporting evidence.
6. As an SRE, I want low-confidence conclusions escalated instead of treated as facts.
7. As an SRE, I want proposed remediation tested before it reaches production.
8. As an SRE, I want destructive or production-changing operations blocked behind human approval.
9. As an SRE, I want to see exactly which configuration or resource the agent intends to modify.
10. As an SRE, I want the agent to monitor after remediation and determine whether the service recovered.
11. As an SRE, I want unsuccessful remediation detected and escalated automatically.
12. As an SRE, I want every investigation, decision, tool call, approval, and production change recorded.
13. As a platform engineer, I want one agent to handle incidents across many services instead of configuring a custom workflow for every application.
14. As an operator, I want known incidents resolved faster and unfamiliar incidents escalated with gathered evidence attached.

## 7. Product experience

The product is **GUI first**, with a CLI available underneath for development, operations, and debugging. The GUI makes the incident, investigation, evidence, proposed remediation, and approval moment immediately legible in a five-minute demo.

### Application shape

```text
Next.js GUI
    ↓
TrueForge
    ↓
MCP servers
    ↓
Kubernetes / Prometheus / GitHub
```

### Three primary views

1. **Fleet and incidents** — service health and active incidents.
2. **Live investigation timeline** — tool activity, findings, evidence, and decisions.
3. **Approval and recovery metrics** — proposed change, validation, approval controls, and post-action recovery.

Example incident view:

```text
checkout-api                         INCIDENT

error rate   38%                    pods  4/6

Agent investigation
✓ Prometheus queried
✓ Pod events inspected
✓ Logs inspected
✓ Latest deployment checked

Root cause
Memory limit regression            97% confidence

Proposed fix
256Mi → 1Gi

[ DENY ]  [ APPLY ]
```

### UI components

- **Fleet:** checkout-api — INCIDENT; auth-api — HEALTHY; orders-api — HEALTHY.
- **Incident:** service status, current symptoms, investigation progress, root cause, confidence, and evidence.
- **Agent timeline:** timestamped alert, inspection, discovery, validation, approval, and recovery events.
- **Action panel:** exact before/after configuration, expected impact, validation status, and DENY/APPLY controls.
- **Recovery:** live error rate, healthy pod count, and p95 latency after the production change.

## 8. Architecture

```text
Infrastructure: Kubernetes / cloud / services
                    │
                 telemetry
                    │
      Prometheus / logs / alert sources
                    │
               MCP servers
                    │
              ┌───────────┐
              │ TrueForge │
              └─────┬─────┘
                    │
          incident investigation
                    │
   Kubernetes MCP · Prometheus MCP · GitHub MCP
                    │
                    ▼
                   Jev
                    │
           structured decision
                    │
         remediation planner
                    │
           TrueForge sandbox
                    │
           human approval
                    │
       Kubernetes production MCP
                    │
           verify recovery
```

### TrueForge

TrueForge is the primary agent harness. It handles:

- Long-running investigation and context
- Tools and MCP connections
- Subagents for branching investigations
- Sandboxed execution
- Approval pauses
- Recovery and resume
- Execution trace

TrueFoundry uses TrueForge subagents for branching investigations and approval checkpoints before production-changing actions.

### MCP strategy

Reuse existing MCP servers aggressively. Do not spend hackathon time building Kubernetes, Prometheus, or GitHub integrations from scratch. TrueForge can connect to catalog MCP servers or register remote MCP server URLs. The only MCP server to build for the MVP is **`jev-mcp`**, which exposes the project's differentiating decision layer.

**Kubernetes MCP** — read tools run freely; production mutation tools require approval.

```text
list_pods · describe_pod · get_deployment · get_events
get_logs · get_resource_usage · restart_workload
patch_deployment · scale_deployment
```

**Prometheus MCP**

```text
query_metric · query_range · get_alert
```

Used for CPU, memory, latency, errors, saturation, and traffic.

**GitHub MCP**

Used to inspect recent commits and PRs, deployment configuration, Kubernetes manifests, and other recent changes.

**Optional:** Grafana, Loki, or Slack MCP only after the core flow works.

### TrueForge and MCP Gateway

TrueForge orchestrates the agent and connects it to external systems through MCP. Where available, TrueFoundry's MCP Gateway can centralize integrations, credentials, authentication, and error handling rather than making every agent manage direct integrations independently.

### TrueFoundry AI Gateway

The AI Gateway is optional and can provide budgets, rate limits, and traces without requiring changes to the agent code.

## 9. Jev decision engine

Jev is the **decision engine**, not the investigator. TrueForge gathers evidence; Jev turns it into typed decisions with calibrated probabilities so the workflow can act above a threshold and request review when uncertainty is higher.

Expose decisions through `jev-mcp`:

### `classify_incident`

Possible classes: `RESOURCE_EXHAUSTION`, `BAD_DEPLOYMENT`, `DEPENDENCY_FAILURE`, `TRAFFIC_SPIKE`, `CONFIGURATION_ERROR`, `UNKNOWN`.

### `select_next_action`

Possible actions: `INVESTIGATE_MORE`, `TEST_REMEDIATION`, `REMEDIATE`, `ESCALATE`.

### `classify_remediation_risk`

Possible levels: `LOW`, `MEDIUM`, `HIGH`.

### `verify_recovery`

Possible outcomes: `RECOVERED`, `PARTIALLY_RECOVERED`, `NOT_RECOVERED`, `UNCERTAIN`.

Every decision returns a probability or confidence value. Example:

```text
BAD_DEPLOYMENT      0.94
CONFIG_ERROR        0.04
UNKNOWN             0.02
```

## 10. Sandbox validation

The agent must validate a remediation before applying it to production. It must not simply decide to change a memory limit and execute immediately.

```text
proposed fix
  ↓
isolated environment
  ↓
apply configuration
  ↓
start workload
  ↓
generate load
  ↓
observe metrics
  ↓
validate behavior
```

TrueForge's sandbox isolates local execution while keeping external tool credentials on the harness side.

## 11. Human approval and safety boundary

Production mutation is the line the agent cannot cross alone. Require approval for actions such as:

- Restarting a deployment
- Scaling replicas
- Modifying CPU or memory
- Rolling back a deployment
- Modifying infrastructure or production configuration
- Deleting a resource
- Blocking an IP

Read operations run without approval. The production action screen must show the exact proposed change, expected impact, and validation evidence. The agent pauses for a human decision before mutation.

For the MVP, one clear approval gate is enough. TrueFoundry supports approval at both the TrueForge workflow layer and MCP Gateway tool boundary.

## 12. Incident workflow

```text
alert received
  ↓
create incident session
  ↓
retrieve affected service
  ↓
inspect Kubernetes
  ↓
query telemetry and inspect logs
  ↓
check recent deployments
  ↓
generate hypotheses and collect evidence
  ↓
Jev root-cause decision
  ↓
generate remediation
  ↓
sandbox validation
  ↓
Jev remediation decision
  ↓
human approval
  ↓
production execution
  ↓
monitor metrics
  ↓
Jev recovery decision
  ↓
close, retry, or escalate
```

## 13. Hackathon MVP scope

Support exactly three incident classes:

1. **Bad resource configuration:** memory limit too low → OOMKilled.
2. **Bad deployment:** new image → CrashLoopBackOff.
3. **Traffic spike:** CPU saturation → latency and errors.

The primary demo is the memory limit incident. A real Kubernetes deployment begins healthy at `1Gi`, is deliberately changed to `256Mi`, and starts crashing. The agent uses live telemetry and cluster evidence, correlates the configuration change, tests `1Gi` in a sandbox, pauses for approval, applies the production change, and verifies recovery.

## 14. Out of scope for the hackathon

- Multi-cloud support or AWS, GCP, and Azure simultaneously
- Arbitrary Terraform remediation
- Database or security incidents
- Full incident management or PagerDuty replacement
- Fully autonomous destructive changes
- Hundreds of incident classes
- Historical learning system
- Self-improving runbooks
- Enterprise RBAC system

The goal is **one excellent autonomous operations loop**, not an entire Datadog replacement.

## 15. Success criteria

The MVP succeeds if:

1. A real Kubernetes workload fails.
2. A real metric or alert triggers the investigation.
3. TrueForge starts and maintains the incident workflow.
4. The agent uses at least two real MCP-connected systems.
5. It finds the actual root cause.
6. Jev makes a structured decision.
7. A remediation is tested before production.
8. TrueForge pauses before the mutation.
9. A human approves.
10. The production Kubernetes resource actually changes.
11. The agent verifies recovery using live metrics.
12. The full flow is visible in the demo.

## 16. Hackathon demo script

1. Show checkout-api in the fleet with an active incident: error rate 41%, healthy pods 4/6, p95 latency 3.2s.
2. Start the TrueForge investigation and show real MCP activity: Prometheus query, Kubernetes inspection, pod events and logs, and recent deployment correlation.
3. Show the evidence: two pods OOMKilled and a recent memory limit change from `1Gi` to `256Mi`.
4. Show Jev's structured root-cause decision and 97% confidence.
5. Show the proposed `256Mi → 1Gi` fix being tested in a sandbox under load, with zero crashes, error rate below 1%, and p95 latency near 190ms.
6. Stop at the approval screen. Show the exact production change and expected pod rollout.
7. Click APPLY and show the production Kubernetes resource changing.
8. Show live recovery: error rate 41% → 0.3%, healthy pods 4/6 → 6/6, p95 latency 3.2s → 184ms.

## 17. Technology stack

```text
Agent runtime      TrueForge
Models             TrueFoundry AI Gateway (optional)
Decision model     Jev
Agent tools        MCP
Infrastructure     Kubernetes
Metrics            Prometheus
Logs               Kubernetes logs (Loki optional)
Source control     GitHub MCP
Sandbox            TrueForge sandbox
Backend            TypeScript / Node.js
Frontend           Next.js
State              SQLite
```
