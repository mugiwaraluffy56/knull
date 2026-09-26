# Knull live demo

## Before presenting

Run `make up`, `make migrate`, `make dev-api`, and `make dev-web` in the repository root. Sign in at <http://localhost:3000/> with the local Keycloak test operator (`operator` / `operator`).

## Live run requirements

The product reads real configured services and incidents from PostgreSQL. The current local database also contains persistent test fixtures, so use only the intended service and create a fresh incident for a real run. Do not present `Approval fixture`, `wf-test`, or `test-*` records as live incidents.

Before starting the API, configure the read-only Kubernetes MCP endpoint (`KNULL_K8S_MCP_URL`), Jev MCP endpoint (`KNULL_JEV_MCP_URL`), and TrueForge runtime (`KNULL_TRUEFORGE_URL`). Configure the corresponding read-only MCP connectors and tool allowlist in TrueForge as well (`KNULL_TRUEFORGE_INVESTIGATOR_MCP` and `KNULL_TRUEFORGE_INVESTIGATOR_TOOLS`). For the Knull investigation collector, the Kubernetes server must expose `list_pods`, `get_deployment`, `get_events`, `get_logs`, and `get_resource_usage`. Add Prometheus and GitHub MCP endpoints only when their real read-only servers are available. Jev can use OpenAI Responses by default, or OpenRouter with `KNULL_JEV_ENDPOINT=https://openrouter.ai/api/v1/responses`, `KNULL_JEV_MODEL=openai/gpt-4o-mini`, and the OpenRouter key in `KNULL_JEV_API_KEY` (the older `KNULL_JEV_OPENAI_API_KEY` remains accepted). Keep keys in process/runtime settings; never commit them or paste them into incident fields.

The service mapping must match an actual cluster, namespace, and Deployment. For a live sandbox-only investigation, label that environment `sandbox`; never call the sandbox cluster `production`. The 1Gi remediation validation requires the configured dedicated sandbox EKS cluster, pinned checkout fixture image, TrueForge validation model, and sandbox-local Prometheus. Production apply and recovery require a separate production target and are intentionally unavailable in this local setup.

## Walkthrough

1. **Workspace:** Show the health checks for the running API and its database dependencies.
2. **Services:** Open Services and verify the exact service-to-cluster/namespace/Deployment mapping. Confirm that read-only integration endpoints are connected before describing live evidence.
3. **Fresh incident:** Start an incident against that mapped service with a real observed symptom. Open its incident page and run **Collect evidence**. Show only timeline observations returned by the connected MCP tools and Jev output returned for those observations.
4. **Proposal and validation:** When Jev moves the incident to planning, use **Live remediation workflow** to select the actual observation IDs and enter the current workload UID/resource version from Kubernetes. Confirm the mapped workload really has the 256Mi limit before creating the bounded 1Gi proposal. Enter the pinned fixture image digest and run the baseline/candidate in the isolated EKS cluster. Show the recorded result and verified cleanup.
5. **Human gate:** Show the exact action details and approval controls. A real production mutation can be shown only when a separately configured production target is available; otherwise stop at review and say that the local demo has no production executor.

## Scope to state clearly

The local stack currently has TrueForge running, but its MCP investigation tool allowlist and Knull's Kubernetes/Prometheus/GitHub MCP endpoints are not configured. Jev's OpenRouter key and endpoint are also not configured in this checkout. Therefore the fresh evidence → Jev → sandbox validation → approval flow cannot be demonstrated yet. Do not use existing test records or claim a live Jev/TrueForge run. Bad-deployment and traffic-spike flows remain incomplete. Task 18's checklist waiver is not a passed TrueForge validation. Current task status is in `docs/IMPLEMENT.md`.

## Spoken continuation after the opening pitch

This starts **after** the two-speaker introduction. Keep the browser at <http://localhost:3000/> and stay signed in as the local operator. Use a fresh incident created from a service whose environment mapping matches the live target. The walkthrough is conditional on the live run requirements above.

**Speaker 1 — Home screen:** “Let me show you the operator side. This is Knull's workspace. We can see the application and its dependencies are healthy, and from here we can move into the services and incidents Knull is responsible for.”

**Speaker 2 — Click Services, then find Checkout API:** “A service is mapped to a specific environment and Kubernetes workload. We also configure the Prometheus selector and GitHub repository here. That scope matters because any later action has to target the exact workload the operator reviewed.”

**Speaker 1 — Click Integrations:** “This is where operators manage integration credentials. The values are write-only in the UI; we show a fingerprint and scope instead of showing the secret again. Investigation connections are read-only, while production changes go through the separate approval path.”

**Speaker 2 — Click Fleet, then Checkout API's incident:** “When an alert arrives, the service appears in the fleet with its incident state. Opening it gives us one timeline for observations, hypotheses, decisions, and actions, so an engineer can see how Knull got to a recommendation.”

**Speaker 1 — Scroll through the incident timeline and live remediation workflow:** “Every observation and Jev recommendation links back to evidence from the connected systems. For the checkout memory incident, we enter the workload identity directly from Kubernetes, choose the evidence we want the action bound to, and test a pinned candidate in the separate EKS sandbox. Knull records the baseline, candidate, metrics, and cleanup result. The operator can review or deny the exact action; a stale target blocks execution.”

**Speaker 2 — Point to resolution controls, then show the TrueForge settings tab if available:** “After a production action, the recovery checker looks at workload health and service metrics over the configured window. An uncertain or incomplete result stays open for follow-up. In this local demo, TrueForge is connected to the OpenAI provider, and the separate EKS checkout fixture reproduced the `256Mi` failure and passed 20 out of 20 requests at `1Gi`.”

**Speaker 1 — Close:** “So what you're seeing is the operator workflow around that incident loop: scoped evidence, a tested candidate, a human decision, and a recorded recovery assessment. Today's local walkthrough uses demonstration records; the full TrueForge-authored sandbox run through production recovery is still an integration milestone.”
