# AI SRE / Autonomous Cloud Operations Agent

**Status:** Product and implementation specification  
**Source:** `docs/PRD.md`  
**Primary MVP path:** Kubernetes memory-limit regression from alert through verified recovery

## 1. Summary

The product investigates infrastructure incidents, identifies likely causes from operational evidence, validates a proposed remediation in an isolated sandbox, pauses for human approval before production changes, applies only the approved change, and verifies recovery against live signals.

This is an operational agent, not an infrastructure chat interface. Its primary experience is a GUI that lets an operator follow the incident, inspect evidence, understand the proposed action, and approve or deny it. A CLI supports development, operations, and debugging.

The first complete user journey is a Kubernetes service whose memory limit changed from `1Gi` to `256Mi`, causing pods to be OOM-killed and service health to degrade. The agent correlates the symptoms with the change, tests restoring `1Gi` in a sandbox, waits for approval, applies the approved change, and observes whether service health recovers.

## 2. Goals and non-goals

### Goals

- Start an incident investigation from an alert or operator-triggered incident.
- Correlate Kubernetes state, metrics, logs, and recent source-control changes.
- Present a likely cause with evidence, provenance, and uncertainty.
- Produce a bounded remediation and validate it before production.
- Require explicit human approval for every production mutation.
- Execute only the action the operator approved.
- Observe production after execution and report recovered, partially recovered, not recovered, or uncertain.
- Keep a durable, inspectable record of the incident lifecycle.
- Demonstrate the full loop with real Kubernetes and telemetry integrations.

### MVP non-goals

- Multi-cloud remediation across AWS, GCP, and Azure.
- Arbitrary Terraform changes.
- Database or security incidents.
- Replacing PagerDuty or providing a full incident-management platform.
- Autonomous destructive production changes.
- Hundreds of incident classes, historical learning, or self-improving runbooks.
- An enterprise RBAC product.
- Replacing a general-purpose observability platform.

## 3. Users and operating assumptions

### Primary users

- **SRE / DevOps engineer:** reviews evidence and authorizes or denies remediation.
- **Platform engineer:** configures shared infrastructure integrations and uses the product across services.
- **Operator:** monitors active incidents, investigations, and recovery.

### Assumptions

- A deployment has a stable service identity that can be mapped to Kubernetes resources, telemetry queries, and source-control configuration.
- The operator has connected read-capable integrations for Kubernetes, Prometheus, and GitHub before an incident is investigated.
- Production mutation credentials are available only to the trusted execution boundary, never to the model or browser client directly.
- The first alert source is Prometheus Alertmanager's generic webhook receiver. Manual incident creation is also available in the UI.
- Each incident has one designated environment and affected service at a time in the MVP.
- The customer supplies a production EKS cluster and a separate, dedicated sandbox EKS cluster in the same AWS account for the initial deployment. A production namespace is not an acceptable sandbox boundary.
- Per-run namespaces in the sandbox cluster control resources and lifecycle but are not treated as strong isolation boundaries; the dedicated cluster separates validation from production.
- Customer operators authenticate through their OIDC identity provider. Local development and CI use a disposable Keycloak OIDC realm.
- GitHub access uses a customer-installed GitHub App with read-only repository/content/pull-request access for mapped repositories.

## 4. Product experience

### 4.1 Fleet and incidents view

The fleet view lists configured services and their current status: `HEALTHY`, `INVESTIGATING`, `AWAITING_APPROVAL`, `REMEDIATING`, `VERIFYING`, or `INCIDENT`. It shows active incidents first, with key signals such as error rate, latency, and healthy pod count when available. Selecting a service opens its active incident or service details.

### 4.2 Incident view

The incident view presents:

- Affected service, environment, start time, current state, and alert summary.
- Current symptoms and relevant metrics.
- Investigation progress and a timestamped event timeline.
- Findings with source, observation time, query or resource reference, and supporting data.
- Root-cause hypotheses, confidence, and competing explanations when available.
- A remediation proposal with exact before-and-after state, affected resources, expected impact, and validation status.
- Approval controls when the action is eligible for approval.
- Post-action production signals and recovery outcome.

### 4.3 Approval panel

The panel shows the specific action, resource identity, environment, current value, proposed value, expected effect, sandbox evidence, and approval expiry or freshness status. It offers `DENY` and `APPLY` actions. Approval is bound to the displayed action. If the action or relevant resource state changes, the previous approval is invalid and the operator must review again.

### 4.4 Investigation timeline

The timeline records alert receipt, investigation steps, MCP calls, findings, Jev decisions, sandbox steps, approval or denial, production execution, and recovery verification. Tool activity should be understandable to an operator without exposing credentials or internal chain-of-thought. Display concise action/result summaries and evidence references, not private model reasoning.

## 5. End-to-end user journey

1. An alert arrives or an operator starts an investigation for a service.
2. The system validates and normalizes the alert, identifies the service and environment, and creates an incident record. Duplicate alerts for the same active incident are attached to that incident instead of starting unbounded duplicate workflows.
3. TrueForge starts a durable investigation and records the initial alert and symptoms.
4. The investigator queries Prometheus for relevant metrics and alert context; inspects Kubernetes deployments, pods, events, resource usage, and logs; and checks recent GitHub commits, pull requests, and deployment configuration.
5. Each observation is recorded with source, timestamp, target, and result. The agent forms hypotheses and gathers additional evidence where useful.
6. Jev classifies the incident and recommends a next action using the collected evidence. Unknown or insufficiently supported causes are escalated with the evidence collected so far.
7. For a remediation candidate, the planner creates a structured, bounded change. Jev classifies its risk. Actions outside the supported MVP policy are not executable and are escalated.
8. TrueForge validates the change in an isolated sandbox: applies the candidate configuration, starts the workload, generates representative load, observes workload state and metrics, and records the outcome.
9. If validation fails, the action is not offered for production approval. The workflow may gather more evidence or escalate, but does not mutate production.
10. If validation passes, the workflow enters `AWAITING_APPROVAL`. The GUI shows the exact action, expected impact, and validation results. Production mutation remains unavailable while approval is pending.
11. On denial, the workflow records the decision and reason if given, performs no production mutation, and enters `ESCALATED` or `CLOSED` according to operator choice. On approval, the system rechecks that the target resource still matches the reviewed precondition and that the approval is valid.
12. The executor applies only the approved action, records the result, and enters `VERIFYING`.
13. The verifier observes live metrics and workload health for a configured verification window. Jev returns one of `RECOVERED`, `PARTIALLY_RECOVERED`, `NOT_RECOVERED`, or `UNCERTAIN` with evidence and confidence.
14. A recovered incident can be closed. Other outcomes remain active and are escalated or returned for operator-directed follow-up. The complete timeline remains available.

## 6. Functional requirements

### 6.1 Incident intake and lifecycle

- **FR-1:** Accept an incident from a configured alert source and support operator-triggered investigation.
- **FR-2:** Normalize alert identity, service, environment, affected resource hints, start time, and symptom payload.
- **FR-3:** Reject or quarantine malformed alerts with a visible intake error; do not silently drop them.
- **FR-4:** Associate duplicate notifications with an existing active incident when service, environment, and alert identity match the configured deduplication policy.
- **FR-5:** Persist workflow state so a long-running investigation can pause and resume after process restart or approval wait.
- **FR-6:** Expose lifecycle states: `RECEIVED`, `INVESTIGATING`, `PLANNING`, `VALIDATING`, `AWAITING_APPROVAL`, `REMEDIATING`, `VERIFYING`, `RECOVERED`, `ESCALATED`, `DENIED`, `FAILED`, and `CLOSED`.
- **FR-7:** Record a reason and timestamp for every state transition. Invalid transitions must be rejected.

### 6.2 Investigation and evidence

- **FR-8:** Use read-only Kubernetes tools to inspect pods, deployments, events, logs, and resource usage relevant to the incident.
- **FR-9:** Query Prometheus metrics, ranges, and alert details for relevant service signals, including CPU, memory, latency, errors, saturation, and traffic when available.
- **FR-10:** Inspect recent GitHub changes relevant to deployment configuration, manifests, and application rollout.
- **FR-11:** Select follow-up queries based on initial findings; support parallel sub-investigations when they do not conflict.
- **FR-12:** Store every finding with a stable identifier, source integration, query/resource reference, observation time, summarized result, and link or payload reference where available.
- **FR-13:** Distinguish observed facts from hypotheses and decisions in the UI and incident record.
- **FR-14:** Show the root-cause classification, confidence or probability, supporting evidence, and meaningful alternatives. Do not display a confidence as calibrated unless Jev's calibration process supports that claim.
- **FR-15:** Escalate `UNKNOWN`, insufficient evidence, and integration failures without converting them into unsupported conclusions.

### 6.3 Jev decision service (`jev-mcp`)

Jev is a Go MCP service and the OpenAI model adapter. It consumes incident evidence and returns schema-constrained structured decisions through the OpenAI Responses API. The Go service validates the returned schema and checks all evidence/action references against the incident record. It does not independently collect infrastructure evidence, receive infrastructure credentials, invoke tools, approve actions, or execute infrastructure changes. Confidence is not presented as calibrated until a documented evaluation and calibration process supports that claim.

- **`classify_incident`** returns one or more ranked classes: `RESOURCE_EXHAUSTION`, `BAD_DEPLOYMENT`, `DEPENDENCY_FAILURE`, `TRAFFIC_SPIKE`, `CONFIGURATION_ERROR`, or `UNKNOWN`, with probability/confidence and evidence references.
- **`select_next_action`** returns `INVESTIGATE_MORE`, `TEST_REMEDIATION`, `REMEDIATE`, or `ESCALATE`, with rationale summary, confidence, and required evidence.
- **`classify_remediation_risk`** returns `LOW`, `MEDIUM`, or `HIGH`, with risk factors and the proposed action reference.
- **`verify_recovery`** returns `RECOVERED`, `PARTIALLY_RECOVERED`, `NOT_RECOVERED`, or `UNCERTAIN`, with the observed signals and confidence.
- **FR-16:** Reject decisions that reference missing evidence or an action not present in the incident record.
- **FR-17:** Persist decision inputs by evidence reference, outputs, model/version metadata, and timestamp for audit and replay.
- **FR-18:** A decision may recommend; authorization and execution policy remain outside Jev.

### 6.4 Remediation planning and sandbox

- **FR-19:** Represent a remediation as a typed action containing action type, target environment, resource identity, precondition/current value, desired value, expected impact, risk classification, and evidence links.
- **FR-20:** MVP action families are workload restart, replica scaling, CPU/memory adjustment, and deployment rollback, with exact support gated by the configured Kubernetes integration and sandbox ability.
- **FR-21:** A plan must be bounded to explicit resources and fields. Wildcard, ambiguous, or unbounded mutations are rejected.
- **FR-22:** The sandbox must be isolated from production resources and credentials. It may use a representative or cloned workload and must record its provenance and differences from production.
- **FR-23:** Validation applies the proposed change in the sandbox, starts the workload, generates load, observes health and relevant metrics, and records pass/fail plus raw or linked evidence.
- **FR-24:** A failed, incomplete, or unavailable validation cannot be presented as passed and cannot proceed to production approval.
- **FR-25:** Where sandbox fidelity is limited, show that limitation to the operator as part of the approval decision.

### 6.5 Approval and production execution

- **FR-26:** All production mutation requires explicit human approval, regardless of Jev risk level. No MVP action is autonomously approved.
- **FR-27:** Production read operations may run without approval. Mutation examples requiring approval include restart, scale, resource changes, rollback, production configuration changes, deletion, and IP blocking.
- **FR-28:** Approval references an immutable action version or digest and includes approver identity and timestamp. Denial is likewise recorded.
- **FR-29:** Approval is invalidated if the action changes, the target changes, the environment changes, or the resource no longer satisfies the displayed precondition.
- **FR-30:** Before applying, re-read the target and compare it with the action's precondition. If it has drifted, stop and require a new plan and approval.
- **FR-31:** The executor uses least-privilege credentials held by the trusted runtime or gateway. Credentials are never sent to the model, browser, timeline, or user-facing logs.
- **FR-32:** Execution is idempotent where the provider allows it. Record provider operation identifiers and distinguish accepted, completed, failed, and unknown results.
- **FR-33:** A timeout with unknown mutation outcome must be reconciled by reading production state before retry; never blindly repeat a potentially non-idempotent action.
- **FR-34:** Denial, expired approval, stale precondition, policy rejection, or execution failure must leave production unchanged by this workflow.

### 6.6 Recovery verification and closure

- **FR-35:** After a successful mutation, observe the affected workload and the incident's relevant service metrics for a verification window.
- **FR-36:** Report `RECOVERED`, `PARTIALLY_RECOVERED`, `NOT_RECOVERED`, or `UNCERTAIN`; attach the observed signal values and time range.
- **FR-37:** Recovery criteria must be configurable by service or incident class. The initial PRD does not define universal numeric thresholds; use scenario-specific demo thresholds and show the window used.
- **FR-38:** Only `RECOVERED` can be auto-marked resolved. Other outcomes remain active and require operator follow-up or explicit closure.
- **FR-39:** Do not automatically roll back or attempt another production action after a failed recovery in the MVP. Present evidence and escalate for a new human decision.

### 6.7 Audit, CLI, and application surfaces

- **FR-40:** Maintain an append-only incident event history covering intake, tool calls, findings, Jev decisions, sandbox execution, approval/denial, production execution, verification, and closure.
- **FR-41:** Redact credentials and sensitive values from all persisted event data and UI output.
- **FR-42:** Provide GUI screens for fleet, incident details, timeline, action review, approval, and recovery.
- **FR-43:** Provide a CLI for development, operations, and debugging of incidents and integrations. The PRD does not require end-user CLI remediation; production actions must retain the same approval policy.
- **FR-44:** Display clear integration health and actionable errors when Kubernetes, Prometheus, GitHub, sandbox, or Jev is unavailable.

## 7. MVP incident coverage

The MVP supports three incident classes:

1. **Resource configuration regression:** memory limit lowered from `1Gi` to `256Mi`, causing OOM kills and degraded service.
2. **Bad deployment:** a new image causes `CrashLoopBackOff`; the agent correlates the rollout and can propose a validated rollback.
3. **Traffic spike:** increased traffic causes CPU saturation, latency, and errors; the agent identifies saturation and can propose a supported, sandbox-validated resource or scale change.

The resource configuration regression is the primary acceptance path. For the other two classes, integration-specific remediation may be deferred if safe sandbox validation cannot be demonstrated; the incident must still investigate and escalate with evidence.

## 8. Safety model

### Trust boundaries

- The model proposes hypotheses and actions; deterministic policy and execution services enforce permissions and approval.
- MCP read tools are separated from mutation tools. Mutation tools are unavailable to the workflow until a valid approval is recorded.
- Sandbox credentials and production credentials are separate. The sandbox cannot address production targets.
- The GUI is an approval client, not an authority. The backend validates approver identity, action digest, approval state, and target preconditions.

### Required safety invariants

1. No production mutation occurs without explicit approval for that exact action.
2. No action proceeds after its target or relevant current state changes without renewed review.
3. A denied, stale, expired, or failed approval cannot be interpreted as approval.
4. A sandbox pass cannot guarantee production safety; disclose environment differences.
5. A successful API response cannot be treated as recovery; verify live behavior.
6. An unknown action result is reconciled before retry.
7. Agent-provided text, logs, manifests, and tool outputs are untrusted data and cannot change system policy or approval state.

## 9. Proposed domain model

These entities describe the product contract; exact storage schema is an implementation decision.

- **Service:** stable identifier, display name, environment, Kubernetes mapping, telemetry query mapping, source-control mapping, and verification policy.
- **Incident:** stable identifier, service/environment, alert identity, symptoms, lifecycle state, timestamps, and closure state.
- **Investigation event:** ordered event with type, timestamp, actor/system, concise summary, and related evidence/action/decision identifiers.
- **Evidence:** source, target, query/resource reference, observation interval, normalized summary, payload reference, and integrity metadata.
- **Decision:** decision type, input evidence references, typed output, confidence/probability, version metadata, and timestamp.
- **Remediation action:** immutable action version, environment, explicit target, operation, before/after values, preconditions, expected impact, risk, and evidence references.
- **Validation run:** sandbox identity, action version, workload inputs, load profile, observed signals, result, limitations, and timestamps.
- **Approval:** action digest, approver, decision, timestamp, optional reason, and validity state.
- **Execution:** action/approval references, provider request/result identifiers, status, observed resulting state, and timestamps.
- **Recovery assessment:** verification window, signals, Jev result, confidence, evidence references, and timestamp.

Events are append-only. Mutable projections may be used for current UI state, but the event history is the audit record.

## 10. Integration contracts

### Kubernetes MCP

Use `containers/kubernetes-mcp-server` with read-only mode and a dedicated Kubernetes read-only service account for list/describe pods, deployments, events, logs, and resource usage. The MCP server is never given production mutation privileges. The Go production executor uses a separately configured Kubernetes client and narrow RBAC to restart, patch, or scale only allowlisted mapped resources after policy and approval checks. Every operation includes environment and resource identity and returns structured status/errors.

### Prometheus MCP

Use `prometheus/prometheus-mcp` with only read-only query, range query, metric metadata, labels, and alert/rule lookup tools enabled. Disable administrative tools such as reload and shutdown. Each query/result includes query text or identifier, evaluation interval, units when known, and timestamp. Queries must be scoped to the service/environment mapping.

### GitHub MCP

Use GitHub's official MCP server with only read-only tools enabled, authenticated by a customer-installed GitHub App restricted to mapped repositories and read-only contents/metadata/pull-request permissions. Read recent commits, pull requests, deployment configuration, manifests, and other mapped changes. Results include repository/ref, commit or PR identity, timestamp, and relevant diff/configuration reference.

### Jev MCP

Implements the four typed decision operations in Section 6.3 in Go. Jev calls the OpenAI Responses API for model-backed analysis and requires schema-constrained structured output. The Go service validates every response against the operation schema and verifies all evidence/action references against the incident record before returning it. Jev has no infrastructure credentials and cannot invoke MCP tools, approve actions, change incident state, or execute mutations. Model name and API request limits are configuration; model/version and request metadata are recorded with each decision.

### Alert source

Prometheus Alertmanager sends notifications to an authenticated generic webhook endpoint. The Go incident API verifies the configured shared secret, normalizes the Alertmanager payload, handles resolved notifications, and deduplicates repeat notifications. The UI also supports operator-triggered incidents. Additional alert providers are out of initial scope.

### Sandbox

Use a customer-provisioned, dedicated sandbox EKS cluster that is separate from every production cluster; the cluster is the isolation boundary. For each validation, provision a fresh namespace for resource controls and cleanup, with a unique run identity, strict resource quotas, restricted Pod Security settings, disabled service-account token automount, and default-deny network policy with only required egress. The sandbox executor has no production credentials or kubeconfig and can address only the sandbox cluster. Use sanitized workload configuration and pull-only image access; never copy production Secret values or production data. Apply the proposed action, run the bounded workload/load scenario, collect Kubernetes and sandbox-local Prometheus signals, then delete the namespace and its resources. A missing, unhealthy, or insufficiently isolated sandbox must fail closed: validation is not passed and production approval is unavailable.

## 11. Architecture and component responsibilities

### Technology stack

- **Frontend:** TypeScript, React, and Next.js App Router. Use the Node.js 24 LTS runtime for the web application.
- **Backend:** Go service using the standard `net/http` package for the incident API, service configuration, approval policy, execution, and recovery APIs.
- **Workflow runtime:** TrueForge, called by the Go backend through its language-neutral HTTP and Server-Sent Events API. The TypeScript SDK is not required by the backend.
- **Model decisions:** Go `jev-mcp` calls the OpenAI Responses API and returns validated, schema-constrained JSON. OpenAI receives only minimized, redacted incident evidence; the customer supplies the API credential. The model cannot call infrastructure tools or authorize/execute actions.
- **MCP:** Use `containers/kubernetes-mcp-server` for read-only Kubernetes investigation, `prometheus/prometheus-mcp` with a read-only tool allowlist, and GitHub's official `github/github-mcp-server` with read-only repository tools. Pin server images/releases by immutable version or digest. Implement `jev-mcp` in Go with the official MCP Go SDK.
- **Persistence:** PostgreSQL for application service configuration, incident projections, decisions, approvals, and audit events. Redis supports the shared, multi-replica TrueForge deployment.
- **Deployment:** self-hosted, single-tenant installation in the customer's AWS account. The UI, Go backend, TrueForge workflow runtime, Jev MCP server, and application state run on the customer's production EKS cluster. A separate dedicated sandbox EKS cluster is required for remediation validation. PostgreSQL and Redis may be customer-managed services; in-cluster dependencies are supported only for development and non-production environments.
- **Network boundary:** the installation initiates outbound connections to Alertmanager, configured integrations, GitHub, and OpenAI. It does not require inbound access from Knull-operated infrastructure. Production mutations use a separate, narrowly scoped identity and typed Go executor; the model and read-only MCP servers cannot mutate production.
- **Gateways:** TrueFoundry MCP Gateway and AI Gateway remain optional integrations as described below.

The frontend and backend communicate through a versioned HTTP API described by an OpenAPI contract; generate or validate the TypeScript client from that contract. Keep the Go services in one deployable backend initially, with Jev MCP as a separate process because it has its own MCP server contract.

The initial production distribution is a customer-installed Helm release in the customer's AWS account. The customer provisions the production and sandbox EKS clusters and managed PostgreSQL/Redis endpoints. It has no required Knull-hosted control plane or telemetry dependency. A hosted control plane can be considered later as an optional deployment model; it must not be required for incident response or remediation in this release.

```text
Alert source ──> Incident API ──> Durable workflow (TrueForge)
                                      │
                         ┌────────────┼───────────┐
                         ▼            ▼           ▼
                    MCP adapters   Jev MCP    Sandbox adapter
                  K8s/Prom/GitHub               │
                         │                      │
                         └──────────┬───────────┘
                                    ▼
                           Approval/policy service
                                    │
                           Production executor
                                    │
                                    ▼
                            Recovery verifier
                                    │
                          Event store / UI API
                                    │
                              Next.js GUI
```

- **Next.js GUI:** fleet, incident, timeline, approval, and recovery views. It does not contain privileged integration credentials.
- **Go incident API/backend:** intake, service mapping, incident/event persistence, state transition validation, approval policy, production execution, recovery verification, and UI data access.
- **TrueForge workflow:** durable orchestration, tool scheduling, context, pause/resume, subagents, sandbox invocation, and execution trace. The backend drives it through HTTP/SSE.
- **MCP adapters:** read-only external Kubernetes, Prometheus, and GitHub MCP servers. Production mutation is performed only by the typed Go executor after backend policy and approval checks.
- **Jev MCP:** a Go MCP server for typed classification, next-action, risk, and recovery decisions.
- **Policy/approval service:** enforces action allowlist, exact-action approval, approval freshness, and state preconditions.
- **Sandbox adapter:** isolates validation from production and returns reproducible results.
- **Production executor:** performs approved mutations and reconciles provider outcomes.
- **Recovery verifier:** queries live signals and emits a typed recovery assessment.
- **State:** PostgreSQL stores application data and the append-only incident event history; current UI state may use projections. Redis supports TrueForge's shared multi-replica runtime.

TrueFoundry MCP Gateway may centralize MCP integrations, credentials, authentication, and error handling where available. TrueFoundry AI Gateway is optional and may provide budgets, rate limits, and traces.

## 12. State transitions

```text
RECEIVED → INVESTIGATING → PLANNING → VALIDATING
                                      ├─ failure/unknown → ESCALATED
                                      └─ pass → AWAITING_APPROVAL
AWAITING_APPROVAL ├─ deny → DENIED → ESCALATED or CLOSED
                  ├─ stale/expired → PLANNING or ESCALATED
                  └─ approve → REMEDIATING
REMEDIATING ├─ success → VERIFYING
            ├─ failure → ESCALATED
            └─ unknown result → reconcile, then VERIFYING or ESCALATED
VERIFYING ├─ recovered → RECOVERED → CLOSED
          └─ partial/not recovered/uncertain → ESCALATED
```

The operator may close an escalated incident with a recorded reason. Restarting or changing a workflow does not erase prior events. Any new remediation creates a new immutable action version and requires fresh validation and approval.

## 13. Non-functional requirements

- **Durability:** workflow and approval waits survive service restarts; event history is retained for the configured incident retention period.
- **Safety:** mutation policy is enforced server-side and remains effective if the GUI or model misbehaves.
- **Least privilege:** separate read, sandbox, and production mutation credentials and scopes.
- **Auditability:** every state transition and external call has a timestamp, actor, result, and correlation ID.
- **Resilience:** transient provider failures use bounded retries for safe reads; mutations use reconciliation before retry.
- **Responsiveness:** show incident receipt and investigation progress promptly; do not block the UI on long-running workflow execution.
- **Privacy:** redact credentials and sensitive payload fields; limit raw log retention and expose source references where possible.
- **Cost control:** support model/tool budgets and rate limits; AI Gateway integration is optional.
- **Accessibility:** primary incident and approval controls must be keyboard accessible and have readable status labels.
- **Operability:** surface integration health, workflow failures, execution status, and stuck approval/verification states.

Specific latency, availability, retention, and throughput SLO values are not defined in the PRD and must be set for the target deployment.

## 14. Acceptance criteria

### Primary memory-limit scenario

1. A real or representative alert for `checkout-api` creates an incident and appears in the fleet view.
2. The workflow queries Prometheus and inspects Kubernetes; the timeline shows successful calls and results.
3. The workflow observes unhealthy pods and OOM events, and retrieves relevant logs.
4. The workflow finds the change from `1Gi` to `256Mi` in GitHub deployment configuration or history and links the evidence.
5. Jev classifies the likely cause and returns a structured decision referencing the evidence.
6. The system proposes restoring `1Gi`, with the target and before/after values explicit.
7. The sandbox applies the proposed setting, starts the workload, runs load, and records passing behavior and metrics.
8. The workflow enters `AWAITING_APPROVAL`; no production mutation occurs before approval.
9. The GUI displays exact change, expected impact, validation results, and `DENY` / `APPLY` actions.
10. Denial leaves production unchanged. Approval records approver and action digest, rechecks preconditions, and permits only that action.
11. The executor applies the production change and records the provider result.
12. Recovery verification observes workload health and service metrics over a defined window and reports the result with evidence.
13. Partial, failed, or uncertain recovery remains active and is escalated; the workflow does not silently close it.
14. The full incident event history can be inspected after completion.

### Other MVP classes

- A bad deployment with `CrashLoopBackOff` is investigated and correlated with a recent image/deployment change; an unvalidated or unsupported rollback is not applied.
- A traffic spike with CPU saturation is investigated using metric and workload evidence; any scale/resource proposal is sandbox-validated and approval-gated.
- If required integrations fail, the incident shows which evidence is unavailable and escalates without fabricating a root cause.

## 15. Testing strategy

Test user-visible behavior at the end-to-end incident workflow seam, using fixture MCP systems for deterministic coverage and a real-cluster demo path for integration validation. The repository currently has no application code or existing tests to reuse.

- **Workflow acceptance tests:** alert intake through verified recovery, including timeline and incident state.
- **Safety tests:** no mutation before approval; denial has no side effect; exact-action binding; stale resource blocks execution; modified action requires fresh approval; ambiguous execution is reconciled before retry.
- **Failure tests:** missing integrations, malformed alerts, Jev timeout/invalid output, sandbox failure, provider timeout, workflow restart, and metric gaps.
- **UI tests:** fleet status, evidence timeline, action details, approval state, and recovery results are legible and reflect backend state.
- **Integration tests:** actual Kubernetes, Prometheus, GitHub, sandbox, and Jev connections for the primary demonstration, with credentials isolated from test fixtures.
- **Security tests:** secret redaction, least-privilege tool exposure, untrusted tool/log content unable to alter policy, and server-side approval enforcement.

Tests assert observable outcomes and safety properties rather than internal module structure. No prior test patterns exist in the current repository.

## 16. Demo flow

1. Show the service fleet with `checkout-api` in incident state, error rate elevated, latency elevated, and unhealthy pods.
2. Start or receive the alert and show Prometheus, Kubernetes, logs, and GitHub activity in the timeline.
3. Show OOM events and the memory change from `1Gi` to `256Mi` as evidence.
4. Show Jev's structured root-cause decision and confidence, clearly marked as a model decision.
5. Show the proposed `256Mi → 1Gi` change and sandbox validation results.
6. Stop at the approval gate; show the exact change and expected rollout impact.
7. Approve and show the production resource change.
8. Show live recovery signals: error rate, healthy pod count, and p95 latency, then show the incident's final recovery assessment.

Example confidence estimates and specific before/after error rates or latency in the PRD are demo illustrations, not calibrated probabilities or universal acceptance thresholds.

## 17. Delivery phases

1. **Foundation:** service mapping, incident intake, durable event model, GUI shell, and fixture integrations.
2. **Read-only investigation:** TrueForge workflow, Kubernetes/Prometheus/GitHub MCP connections, evidence timeline, and Jev decisions.
3. **Sandbox validation:** typed remediation proposals and isolated validation for the memory-limit scenario.
4. **Approval and execution:** action-bound approval, precondition recheck, production executor, and mutation audit.
5. **Recovery and hardening:** live verification, escalation behavior, failure reconciliation, and real-cluster demo.
6. **Additional MVP scenarios:** bad deployment and traffic spike investigation and safe supported remediations.

## 18. Open decisions before production use

- How are services mapped to namespaces, workloads, Prometheus labels, and GitHub repositories?
- What Jev model/version provides each decision, and how are probabilities calibrated?
- Which confidence/risk thresholds trigger more investigation or escalation? These thresholds must not bypass human approval.
- What per-service recovery windows and signal thresholds define recovery?
- What approval expiry is required? The installation runs in the customer account; its Kubernetes service accounts and RBAC must separate read, sandbox, and production mutation capabilities.
- Which AWS workload identity configuration is required for optional AWS API integrations, and which exact outbound destinations must customers allow?
- What OpenAI data-retention and regional requirements apply to customer incident evidence sent through the configured API credential?
- What is the event and raw-log retention policy, and which fields require redaction?
- What are the deployment SLOs for latency, availability, throughput, and cost?

## 19. References

- Product requirements: `docs/PRD.md`
- Issue tracker and triage conventions: `docs/agents/issue-tracker.md`, `docs/agents/triage-labels.md`
- Domain documentation rules: `docs/agents/domain.md`
