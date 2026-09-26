# Implementation Plan: AI SRE / Autonomous Cloud Operations Agent

This checklist turns [`docs/SPEC.md`](docs/SPEC.md) into 30 build tasks in dependency order. Implement each task as a complete user-visible slice with its own verification. The stack is Go for the backend and Jev MCP server, TypeScript with Next.js App Router for the UI, PostgreSQL for application state, Redis for the shared TrueForge runtime, and TrueForge for durable agent orchestration.

## How to use this plan

- Work a task only after every task listed under **Depends on** is complete.
- Keep each task's API, UI, persistence, safety behavior, and verification together where they apply.
- Use the selected Kubernetes, Prometheus, and GitHub MCP servers recorded in `docs/SPEC.md`; pin immutable versions/digests. Build `jev-mcp` in Go as the OpenAI-backed structured decision adapter.
- Follow [`docs/SPEC.md`](docs/SPEC.md) for product behavior, safety invariants, acceptance criteria, and open decisions. Resolve any still-open production decision before implementing the behavior that depends on it, and record the decision in the spec.
- Keep task checkboxes below as implementation status. Mark a task complete only after the change is pushed and its verification criteria pass. Add the PR or commit URL beside the completion checkbox.
- Use deterministic fixtures for repeatable automated checks and the real integrations/environment for the final end-to-end acceptance path.

## Task checklist

### 1. Set up the Go and Next.js application workspace

**Depends on:** None

**Build details**

- Create a Go backend application and a Next.js App Router UI using TypeScript and React.
- Pin supported Go and Node.js toolchain versions and provide a single documented local startup flow.
- Define the Go HTTP API contract with OpenAPI and generate or validate the TypeScript client from that contract.
- Add PostgreSQL, Redis, and a disposable Keycloak OIDC realm to the local development environment, with migrations and health checks.
- Configure formatting, static analysis, build, and CI jobs for both Go and TypeScript packages.
- Add a minimal health page and backend health endpoint so a developer can confirm the whole workspace starts.

**Verify**

- A clean checkout can install dependencies, start required services, and launch the UI and API using the documented commands.
- Backend and frontend build checks pass in CI.
- The UI can reach the backend health endpoint and display its status.

**Completion:** [x] Code pushed; verification passes. PR/commit: 0340fc6

### 2. Add operator identity and integration secret handling

**Depends on:** 1

**Build details**

- Add operator sign-in using customer-configured OIDC discovery and authorization-code flow. Use a disposable Keycloak realm for local development and CI; support customer OIDC providers without a product-specific identity dependency.
- Persist the authenticated operator identity with approval and denial actions.
- Store integration credentials through the selected secret store or encrypted server-side configuration. Keep secrets out of the browser, model context, incident events, logs, and generated API responses.
- Separate read-only integration credentials from production mutation credentials and sandbox credentials.
- Add authorization checks to operator-only configuration and approval endpoints. Keep the MVP role model small; do not add enterprise RBAC.

**Verify**

- Unauthenticated users cannot access incident or integration configuration APIs.
- Approval events identify the signed-in operator.
- Secret values never appear in API responses, logs, or event history.
- Sandbox and production credentials cannot be used interchangeably.

**Completion:** [x] Code pushed; verification passes. PR/commit: fa42370

### 3. Build service and environment configuration

**Depends on:** 1, 2

**Build details**

- Create service and environment records with stable identifiers and display names.
- Store mappings to Kubernetes cluster/namespace/workload, Prometheus labels or queries, and GitHub repository/ref.
- Add UI and API flows to create, view, update, and disable mappings.
- Validate required fields and prevent one service mapping from silently targeting another environment.
- Store per-service recovery policy references without inventing universal metric thresholds.

**Verify**

- An operator can create and update a service mapping and see it reflected in the fleet view.
- Invalid or ambiguous environment mappings are rejected with actionable errors.
- Every stored integration mapping is scoped to an explicit service and environment.

**Completion:** [x] Code pushed; verification passes. PR/commit: d9b6d44

### 4. Persist incident state and audit events

**Depends on:** 1, 3

**Build details**

- Implement PostgreSQL persistence for incidents, current-state projections, and append-only events.
- Implement the incident lifecycle from `RECEIVED` through investigation, validation, approval, remediation, verification, escalation, and closure.
- Enforce allowed state transitions in the backend and persist actor, timestamp, reason, and correlation ID for each transition.
- Keep event history immutable; expose a read API for the UI.
- Add migrations and transaction boundaries for state plus event writes.

**Verify**

- Valid transitions update current state and append one corresponding event atomically.
- Invalid transitions are rejected and do not append a success event.
- Restarting the backend preserves incidents, state, and event history.
- Concurrent state changes cannot silently overwrite an approval or execution transition.

**Completion:** [x] Code pushed; verification passes. PR/commit: e32aef2

### 5. Receive and deduplicate alerts

**Depends on:** 3, 4

**Build details**

- Implement an authenticated Prometheus Alertmanager generic webhook endpoint and normalize its payload to the provider-neutral incident intake contract.
- Support firing and resolved notifications, Alertmanager grouping labels, notification retries, and duplicate delivery.
- Normalize alert identity, service, environment, symptom values, event time, and affected-resource hints.
- Validate the customer-configured webhook secret before accepting the alert.
- Deduplicate repeated notifications according to service, environment, and alert identity; append new notification events to the existing incident.
- Record malformed or unauthorized deliveries as visible intake failures without starting an investigation.

**Verify**

- A valid alert creates one incident and starts a workflow request.
- Repeated deliveries attach to the active incident rather than creating duplicate investigations.
- Invalid signatures and malformed payloads are rejected and leave an auditable intake result.

**Completion:** [x] Code pushed; verification passes. PR/commit: e6c21cf

### 6. Let operators start investigations

**Depends on:** 3, 4

**Build details**

- Add an authenticated operator action to start an investigation for a configured service and environment.
- Require an incident summary and allow optional symptom/resource details.
- Create the same incident and workflow records used by alert intake, with the operator as initiator.
- Prevent accidental duplicate manual starts for an already active matching incident, while allowing an explicit new incident when appropriate.

**Verify**

- An operator can start an investigation from a service and see its incident record.
- The created record contains the initiating operator and supplied symptoms.
- Invalid service/environment references are rejected before a workflow starts.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 7. Show fleet health and active incidents

**Depends on:** 3, 4

**Build details**

- Build the fleet page with service name, environment, health/incident state, active incident count, and available key signals.
- Sort active incidents ahead of healthy services and provide clear links into incident details.
- Handle missing metrics and integration errors as unavailable data, not as healthy values.
- Refresh state without blocking the page on long-running investigations.

**Verify**

- Services with active incidents are visually distinguishable and navigable.
- State changes from the backend appear in the fleet view.
- Missing telemetry is labeled unavailable and cannot make an unhealthy service appear healthy.
- The page remains usable with an empty fleet and with integration failures.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 8. Run durable investigations through TrueForge

**Depends on:** 4, 5, 6

**Build details**

- Implement a Go TrueForge client using its HTTP API and Server-Sent Events; do not require the TypeScript SDK in the backend.
- Start a TrueForge session for each incident and persist the session/run identifiers.
- Stream workflow progress into the incident event model.
- Support workflow pause, resume, cancellation, and reconnect after backend restart.
- Add bounded retry behavior for safe session reads and explicit handling for unavailable TrueForge sessions.

**Verify**

- Alert-created and operator-created incidents both start the same durable workflow.
- Workflow progress is visible through incident events.
- A paused session can resume after reconnect or backend restart without duplicating prior production actions.
- TrueForge errors leave a visible incident failure/escalation state.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 9. Build the incident evidence timeline

**Depends on:** 4, 8

**Build details**

- Build a chronological timeline for alert receipt, workflow progress, integration calls, findings, Jev decisions, sandbox actions, approvals, production execution, and recovery.
- Render concise operator-readable summaries with source, target, and observation time.
- Separate observed facts, hypotheses, decisions, and actions in the display.
- Link evidence to its incident and preserve ordering when events arrive asynchronously.
- Redact sensitive fields before persistence and rendering.

**Verify**

- An operator can reconstruct the incident sequence from the timeline.
- Out-of-order arrivals display in the correct event-time order while retaining ingestion time.
- Sensitive values and private model reasoning are absent from the timeline.
- Empty, loading, and failed states are handled clearly.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 10. Show Kubernetes findings in incidents

**Depends on:** 3, 8, 9

**Build details**

- Deploy a pinned `containers/kubernetes-mcp-server` release in read-only mode with a dedicated Kubernetes read-only service account; deny Secret access and any unnecessary resource/tool sets.
- Inspect deployments, pods, events, logs, and resource usage for the mapped service.
- Scope every call to the configured cluster, namespace, and workload.
- Normalize results into evidence records and timeline events, retaining links or references to source objects.
- Display provider failures and partial results without fabricating missing evidence.

**Verify**

- A representative incident shows pod health, relevant events, logs, and deployment state in the timeline.
- A service mapping cannot cause a read to target an unrelated environment.
- Failed tool calls appear as failures and do not become successful findings.
- The investigation has no production mutation capability through this read-only connection.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 11. Show Prometheus findings in incidents

**Depends on:** 3, 8, 9

**Build details**

- Deploy a pinned `prometheus/prometheus-mcp` release with a read-only tool allowlist; explicitly disable administrative tools such as reload and shutdown.
- Give the MCP server read-only Prometheus credentials.
- Query relevant alerts, instant metrics, and ranges for CPU, memory, latency, errors, saturation, and traffic where configured.
- Scope queries to service and environment mappings; record query identity, time range, units when known, and result timestamps.
- Display metric data and missing-data conditions in the timeline and incident view.

**Verify**

- A representative incident displays its configured service signals with units/time ranges.
- A query error or empty series is distinguishable from a zero value.
- Queries for one environment cannot return results for another through the service mapping.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 12. Correlate GitHub deployment changes

**Depends on:** 3, 8, 9

**Build details**

- Deploy a pinned release of GitHub's official `github/github-mcp-server` with only required read tools enabled.
- Authenticate through a customer-installed GitHub App restricted to mapped repositories with read-only contents, metadata, and pull-request permissions.
- Retrieve recent commits, pull requests, deployment configuration, and manifest changes relevant to the service.
- Associate evidence with repository, ref, commit/PR identity, time, and relevant diff/configuration reference.
- Present change correlation as evidence for a hypothesis, not as proof by itself.

**Verify**

- A memory-limit change from `1Gi` to `256Mi` appears as linked evidence for the primary scenario.
- Missing repositories or permissions are shown as unavailable evidence.
- The agent never receives GitHub write capability through this integration.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 13. Implement the Jev MCP server in Go

**Depends on:** 1

**Build details**

- Create `jev-mcp` as a Go service using the official MCP Go SDK.
- Expose typed tools for incident classification, next-action selection, remediation-risk classification, and recovery verification.
- Call the OpenAI Responses API for model-backed decisions using strict JSON Schema output; validate responses against local schemas before returning them.
- Require every returned evidence/action reference to resolve to records supplied by the backend; reject invented or missing references.
- Configure model name, timeout, bounded retry policy for safe requests, token/request budget, and customer-supplied API credential outside incident payloads.
- Include model/decision version and confidence metadata in outputs where applicable.
- Keep the service side-effect-free with respect to infrastructure.

**Verify**

- MCP clients can discover and call all four tools using the supported protocol.
- Invalid inputs and missing evidence return structured errors.
- Jev MCP cannot call infrastructure tools, access production credentials, approve actions, or mutate incident state.
- Invalid schema output, unknown evidence references, timeout, or provider errors return typed failures and cannot advance an incident toward execution.
- Contract tests validate request and response shapes.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 14. Classify incident causes from evidence

**Depends on:** 10, 11, 12, 13

**Build details**

- Send collected evidence to Jev for incident classification.
- Support `RESOURCE_EXHAUSTION`, `BAD_DEPLOYMENT`, `DEPENDENCY_FAILURE`, `TRAFFIC_SPIKE`, `CONFIGURATION_ERROR`, and `UNKNOWN`.
- Persist the decision input references, output, confidence, and version metadata.
- Show ranked hypotheses, supporting evidence, and uncertainty in the incident view.
- Escalate when the result is unknown, low-confidence by configured policy, or unsupported by evidence.

**Verify**

- The memory-limit scenario classifies as resource/configuration exhaustion with references to OOM evidence and the configuration change.
- An unknown scenario escalates rather than presenting a fabricated root cause.
- Every displayed claim links to evidence or is labeled as a hypothesis.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 15. Select the next investigation or response action

**Depends on:** 13, 14

**Build details**

- Call Jev to select `INVESTIGATE_MORE`, `TEST_REMEDIATION`, `REMEDIATE`, or `ESCALATE` using incident evidence and classification.
- Call Jev to classify remediation risk as `LOW`, `MEDIUM`, or `HIGH` when an action exists.
- Persist decisions and their evidence references and show a concise rationale summary.
- Keep policy enforcement separate from Jev; no confidence or risk result can authorize production mutation.

**Verify**

- The workflow follows the typed next-action result and records it.
- Missing or invalid Jev decisions stop progression and escalate.
- All risk levels still require the approval workflow before production mutation.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 16. Create bounded remediation plans

**Depends on:** 15

**Build details**

- Define a versioned action contract with action type, environment, exact resource identity, fields to change, current value, desired value, preconditions, expected impact, risk, and evidence references.
- Implement an allowlist for supported MVP operations: restart workload, scale replicas, adjust CPU/memory, and rollback deployment where safely supported.
- Reject wildcards, ambiguous targets, unbounded patches, unsupported actions, and actions missing preconditions.
- Render a human-readable before/after summary from the same immutable action contract used by the executor.

**Verify**

- Supported actions are fully scoped and produce stable, versioned action records.
- Malformed, ambiguous, and unsupported actions cannot reach validation or execution.
- The UI summary and executor input derive from the same action version.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 17. Provision isolated sandbox runs

**Depends on:** 2, 16

**Build details**

- Require a customer-provisioned dedicated sandbox EKS cluster that is separate from every production cluster; document this as an installation prerequisite.
- For each run, create a fresh namespace with a unique run identity, strict quotas/limit ranges, restricted Pod Security settings, disabled service-account token automount, and default-deny network policy with only required egress.
- Use a sandbox-only Kubernetes identity and pull-only image credentials; enforce cluster targeting so sandbox calls cannot reach production clusters.
- Copy only sanitized workload configuration; never copy production Secret values or production data.
- Apply the candidate action, run bounded workload/load checks, collect Kubernetes and sandbox-local Prometheus signals, and delete the namespace and its resources with cleanup verification.
- Treat per-run namespaces as cleanup and resource-control boundaries only; the dedicated sandbox cluster is the production isolation boundary.
- Record sandbox provenance, workload inputs, action version, environment differences, logs, and observed outputs.
- Surface cleanup failures and sandbox limitations to operators.

**Verify**

- A validation run can be created and cleaned up successfully.
- Sandbox credentials cannot access or mutate production resources, and production credentials cannot be mounted in the sandbox.
- Provider failure or missing sandbox fidelity cannot be represented as a passing validation.
- Failed cleanup, policy enforcement, network isolation, or cluster identity checks make validation fail and block production approval.
- The incident links to the sandbox run and its evidence.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 18. Validate the primary memory-limit remediation

**Depends on:** 10, 11, 12, 16, 17

**Build details**

- Configure a representative checkout workload that can reproduce OOM failure at `256Mi` and stable operation at `1Gi`.
- Apply the proposed `256Mi` to `1Gi` change only in the sandbox.
- Start the workload, generate the selected load profile, and observe pod health, error rate, and latency.
- Store pass/fail results, verification window, metric values, workload state, and sandbox/production differences.

**Verify**

- The known failing `256Mi` configuration reproduces the expected workload failure in the sandbox.
- The proposed `1Gi` configuration passes the configured sandbox checks.
- Failed, incomplete, or unavailable validation cannot become eligible for production approval.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 19. Enforce action-specific approval

**Depends on:** 2, 4, 18

**Build details**

- Add approval and denial APIs that bind the operator decision to an immutable action version/digest.
- Persist approver identity, timestamp, decision, optional reason, and action reference.
- Re-read the production target before execution and compare it with the reviewed precondition.
- Invalidate approvals when the action, target, environment, or relevant current value changes or the approval expires.
- Enforce the gate in the backend/executor; the GUI cannot grant its own authority.

**Verify**

- Mutation is impossible while approval is pending or denied.
- Approval for one action version cannot authorize a modified action.
- A stale target blocks execution and requests new validation and approval.
- Every approval and denial is present in the audit history with operator identity.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 20. Build the approval and denial experience

**Depends on:** 9, 18, 19

**Build details**

- Show the target service/environment, resource identity, current and proposed values, expected impact, sandbox results, evidence, validation limits, and approval freshness.
- Add `APPLY` and `DENY` controls bound to the backend action version.
- Show pending, approved, denied, expired, stale, and execution states clearly.
- Require an optional denial reason and record it when provided.
- Ensure the approval screen is keyboard accessible and usable at incident speed.

**Verify**

- An operator can inspect all decision-relevant details before approving.
- Denial causes no production mutation and is visible in the timeline.
- Stale/expired actions cannot be approved from an old screen state.
- The UI reflects the backend decision after reload/reconnect.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 21. Execute an approved memory-limit change

**Depends on:** 2, 10, 19, 20

**Build details**

- Use the typed Go Kubernetes executor with a separate narrowly scoped production service account; do not enable write mode on the investigation MCP server.
- Execute only the approved memory patch for the exact cluster, namespace, workload, and field.
- Revalidate action digest and target precondition immediately before the mutation.
- Record provider request/result identifiers, submitted patch, status, and resulting resource state without logging credentials.
- Transition the incident into verification only after a reconciled successful mutation.

**Verify**

- A valid approval changes only the approved resource field in production.
- No approval, wrong target, stale value, or altered action produces no mutation.
- The execution result and resulting resource value appear in incident history.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 22. Reconcile uncertain production outcomes safely

**Depends on:** 21

**Build details**

- Classify production execution as accepted, completed, failed, or unknown.
- On timeout or lost response, read production state and reconcile against the approved desired value before retry.
- Add idempotency keys or equivalent provider-safe deduplication where supported.
- Escalate conflicting or ambiguous results without blindly repeating the mutation.

**Verify**

- A simulated timeout after successful mutation is reconciled without duplicate action.
- A timeout before mutation can be safely retried only after state reconciliation.
- Conflicting state produces an escalation and no additional mutation.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 23. Configure recovery signals and windows

**Depends on:** 3, 11

**Build details**

- Add per-service or per-incident recovery policy for required signals, comparison thresholds, observation window, and missing-data behavior.
- Support workload health and configured service indicators such as error rate and latency.
- Validate that policies include enough signals to avoid treating an API success as recovery.
- Show the policy/window used with every recovery assessment.

**Verify**

- Operators can configure and view a recovery policy for a service.
- Missing or invalid thresholds prevent automatic recovered status.
- Each assessment records the exact policy version and observation window.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 24. Verify recovery using live production signals

**Depends on:** 13, 21, 22, 23

**Build details**

- Query live Kubernetes health and configured Prometheus signals after a mutation.
- Observe for the service's configured verification window and store time ranges and measured values.
- Call Jev's recovery decision with observed evidence and persist the typed result.
- Support `RECOVERED`, `PARTIALLY_RECOVERED`, `NOT_RECOVERED`, and `UNCERTAIN`.
- Keep partial, failed, uncertain, and no-data outcomes active for operator follow-up.

**Verify**

- The primary scenario reports recovery only when workload and configured service signals meet policy.
- Missing metrics cannot yield a recovered result.
- Every result shows signal values, time window, Jev version, and evidence references.
- No automatic second production action is taken after an unsuccessful assessment.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 25. Close or escalate incidents

**Depends on:** 9, 14, 24

**Build details**

- Add incident resolution and escalation actions to the incident view.
- Allow automatic resolution only for verified `RECOVERED` outcomes.
- Keep other outcomes active and show the evidence, missing information, and recommended operator follow-up.
- Record closure actor, time, and reason; preserve the complete event history.

**Verify**

- Verified recovery can close the incident with an audit event.
- Partial, failed, uncertain, and unknown incidents remain open until operator action.
- Operator closure records identity and optional reason without deleting history.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 26. Support the bad-deployment incident path

**Depends on:** 12, 13, 16, 17, 19, 20, 22, 24

**Build details**

- Detect a representative new image that causes `CrashLoopBackOff` using Kubernetes events/logs and GitHub deployment history.
- Classify the likely bad deployment and link the relevant image/configuration change.
- Build a rollback action with explicit old/new image references and target preconditions.
- Validate the rollback in the sandbox, require approval, execute only the approved rollback, then verify recovery.

**Verify**

- The failing rollout is correlated with the relevant deployment change.
- Rollback is never offered for production before successful sandbox validation and approval.
- Stale image state blocks execution and requires a fresh plan.
- Recovery uses live workload and service signals.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 27. Support the traffic-spike incident path

**Depends on:** 10, 11, 13, 16, 17, 19, 20, 22, 23, 24

**Build details**

- Detect a representative traffic increase with CPU saturation, elevated latency, and errors.
- Correlate Prometheus traffic/saturation evidence with Kubernetes workload state.
- Propose a supported scale or resource adjustment with bounded target and expected impact.
- Validate the action in the sandbox, require approval, execute it, and verify live recovery.

**Verify**

- Traffic increase and saturation are shown as separate evidence signals.
- Unsupported or unvalidated actions are escalated without production mutation.
- Approved scaling/resource changes target only the reviewed workload and values.
- Recovery and non-recovery outcomes follow the configured policy.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 28. Add an operator CLI

**Depends on:** 2, 3, 4, 8

**Build details**

- Build a Go CLI for checking service/integration configuration, starting an investigation, viewing incident status, and inspecting workflow progress.
- Reuse the same authenticated backend APIs and output types as the GUI.
- Provide readable human output and structured JSON output for scripts.
- Keep production mutation behind the same approval service; the CLI cannot bypass policy.

**Verify**

- An operator can start and inspect an incident from the CLI.
- Invalid IDs, missing permissions, and service errors return actionable messages and nonzero exit codes.
- No CLI command can directly invoke an unapproved production mutation.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 29. Add integration health and operational controls

**Depends on:** 2, 4, 8, 10, 11, 12, 13, 17, 21, 24

**Build details**

- Expose health and last-success/error status for TrueForge, MCP providers, Jev, sandbox, PostgreSQL, and Redis.
- Add structured logs, traces, and correlation IDs across intake, workflow, tools, approval, execution, and recovery.
- Add bounded retry/backoff for safe reads, provider timeouts, cancellation propagation, and stuck-workflow detection.
- Configure model/tool budgets and rate limits; use the AI Gateway when available without requiring it for the core product.
- Apply secret and sensitive-log redaction before persistence and export.

**Verify**

- Operators can identify a failed integration and the incidents affected by it.
- Provider timeouts and stuck workflows become visible, actionable states.
- Logs/traces connect an incident to its external calls without exposing credentials.
- Configured budgets stop or escalate work predictably when exhausted.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________

### 30. Deploy and verify the complete product

**Depends on:** 5–29

**Build details**

- Package the Go services and Next.js UI as deployable containers for Kubernetes.
- Publish a Helm release for installation into a customer-controlled EKS cluster in the customer's AWS account. No Knull-hosted control plane or telemetry service may be required for the product to operate.
- Treat a separate customer sandbox EKS cluster as a required production prerequisite; the installer must verify connectivity and isolation before enabling remediation approval.
- Configure customer-managed PostgreSQL/Redis connections for production, secret injection, health/readiness checks, migrations, and safe upgrade/rollback behavior. Support in-cluster dependencies only for development and non-production installs.
- Define least-privilege Kubernetes service accounts and RBAC for read, sandbox, and production mutation paths. Document required AWS workload identity and outbound network destinations.
- Make the OpenAI API credential customer-configured. Document that incident evidence is sent to the selected model API, redact secrets, and minimize submitted evidence.
- Document environment setup, integration configuration, operator access, installation and upgrades, incident operations, and recovery from a failed deployment.
- Run the primary memory-limit flow and the bad-deployment and traffic-spike flows against a real Kubernetes environment with live integrations.
- Resolve defects found by those acceptance runs and record any remaining spec gaps explicitly.

**Verify**

- A clean deployment reaches healthy readiness for UI, API, workflow, and Jev MCP services.
- An engineer can install and upgrade the complete product in a clean customer-style AWS/EKS environment using the documented Helm release.
- Product operation requires no inbound access from Knull-operated infrastructure; integration and model API egress is documented and configurable.
- All three end-to-end incident paths satisfy their acceptance criteria in `docs/SPEC.md`.
- Approval denial and stale-action checks prove production remains unchanged.
- An approved action changes the intended resource and the verifier reports the observed recovery result.
- Operating and troubleshooting instructions are sufficient for another engineer to deploy and run the system.

**Completion:** [ ] Code pushed; verification passes. PR/commit: ____________________
