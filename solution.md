# Knull solution

## Problem

During a production incident, engineers jump between Kubernetes, metrics,
logs, and deployment history to find the cause and decide what is safe to do.
That manual investigation is slow, hard to repeat, and easy to get wrong under
pressure.

## What the agent reaches—and where it stops

Knull uses TrueForge to run a durable investigation. It gathers scoped,
read-only evidence from Kubernetes, Prometheus, and GitHub through MCP. Jev
turns that evidence into typed hypotheses and a proposed next step. For a
supported remediation, TrueForge can author bounded validation code and run it
in a separate EKS sandbox. Knull records the evidence, proposal, and validation
result for the operator to review.

The agent stops before changing production. A person must approve the exact
action, target, and expected values. The backend rechecks approval and live
resource preconditions before execution, then observes recovery. A stale or
uncertain action is blocked or escalated. The bad-deployment rollback and
traffic-spike paths are still under implementation; they are not represented as
completed end-to-end flows.

## Architecture

The Go API stores incidents and ordered events in PostgreSQL, with Redis for
supporting runtime state. TrueForge manages the investigation; read-only MCP
connectors provide infrastructure evidence. Jev is a separate Go MCP service
using a customer-configured OpenAI-compatible model API. The Next.js operator
UI presents the fleet, evidence timeline, sandbox result, approval gate, and
recovery status. A typed backend executor owns production writes; agent tools
do not receive production mutation credentials. The sandbox uses a separate
customer-controlled EKS cluster.
