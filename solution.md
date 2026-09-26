# Solution Writeup: Knull

**The problem.** Engineers jump between Kubernetes, metrics, logs, and deploy
history to diagnose production incidents and choose safe fixes. Manual
correlation is slow and risky.

**What the agent reaches.** When configured, Knull gathers workload-scoped evidence through
read-only Kubernetes, Prometheus, and GitHub MCP connections. Jev turns those
observations into typed hypotheses and a proposed next step. For the supported
memory remediation, TrueForge can author bounded validation code and run it in
a separate EKS sandbox. Knull records the evidence, action, and validation
result for operator review.

**Where it stops.** The agent stops before changing production. A person must
approve the exact action, target, and values. The backend checks that approval
and the live resource preconditions before execution, then observes recovery.
Stale or uncertain actions are blocked or escalated.

**Architecture.** A Go API stores incidents and events in PostgreSQL and Redis.
TrueForge manages investigations; read-only MCP tools collect evidence. Jev is
a Go MCP service using a configured OpenAI-compatible model. The Next.js UI
presents the fleet, timeline, sandbox result, approval, and recovery. A typed
backend executor owns production writes. The sandbox uses a separate EKS
cluster.

**How TrueForge was used.** TrueForge provides the durable agent workflow and
validation-code authoring. Knull connects it to the incident timeline and
isolated EKS validation runner. Production credentials stay outside the agent
workflow.

**Real vs mocked.** Code exists for the app, API, integrations, Jev, and EKS
sandbox path. A separate sandbox exercise reproduced the checkout memory
failure at 256Mi and passed 20 of 20 requests at 1Gi. This was not a fresh
end-to-end run through the app and TrueForge.

**Known limits.** The live TrueForge, MCP, and Jev connections are not verified
as one complete run. Bad-deployment rollback and traffic-spike remediation are
still incomplete. Do not present persisted test fixtures as live incidents.
