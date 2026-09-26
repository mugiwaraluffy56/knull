# Five-minute local demo

## Before presenting

Run `make up`, `make migrate`, `make dev-api`, and `make dev-web` in the repository root. Set `KNULL_TRUEFORGE_URL=http://localhost:8790` and `KNULL_TRUEFORGE_INVESTIGATOR_MODEL` to a configured provider/model before starting the API if the local TrueForge runtime is available. Open <http://localhost:3000/>. The local Keycloak test operator is `operator` / `operator`.

## Walkthrough

1. **Workspace:** Show the home page and healthy PostgreSQL/Redis status. Explain that the operator UI and Go API run locally, while the Kubernetes deployment package is under `infra/helm/knull`.
2. **Services:** Open Services and show how an environment maps to one Kubernetes workload, Prometheus selector, and GitHub repository.
3. **Incidents:** Open Fleet, choose an incident, and show the event timeline, evidence, proposed action, approval boundary, and recovery controls. Explain that action digests and target preconditions are rechecked before mutation.
4. **Runtime:** Show <http://localhost:8790/settings> to confirm the TrueForge OpenAI provider is connected, then use the API log to show `trueforge workflow enabled`.
5. **Sandbox evidence:** Describe the checkout fixture result: `256Mi` reproduced OOM, while `1Gi` served 20/20 requests in the dedicated EKS sandbox. The full TrueForge-authored EKS run and live production recovery verification have not been demonstrated end to end.

## Scope to state clearly

The local demo does not use a customer production cluster. Bad-deployment and traffic-spike flows are incomplete. Do not present the Task 18 live verification waiver as a passed TrueForge run. Current task status is in `docs/IMPLEMENT.md`.

## Spoken continuation after the opening pitch

This starts **after** the two-speaker introduction. Keep the browser at <http://localhost:3000/> and stay signed in as the local operator. The local database contains test fixtures; use the named Checkout API service rather than an Approval fixture row.

**Speaker 1 — Home screen:** “Let me show you the operator side. This is Knull's workspace. We can see the application and its dependencies are healthy, and from here we can move into the services and incidents Knull is responsible for.”

**Speaker 2 — Click Services, then find Checkout API:** “A service is mapped to a specific environment and Kubernetes workload. We also configure the Prometheus selector and GitHub repository here. That scope matters because any later action has to target the exact workload the operator reviewed.”

**Speaker 1 — Click Integrations:** “This is where operators manage integration credentials. The values are write-only in the UI; we show a fingerprint and scope instead of showing the secret again. Investigation connections are read-only, while production changes go through the separate approval path.”

**Speaker 2 — Click Fleet, then Checkout API's incident:** “When an alert arrives, the service appears in the fleet with its incident state. Opening it gives us one timeline for observations, hypotheses, decisions, and actions, so an engineer can see how Knull got to a recommendation.”

**Speaker 1 — Scroll through the incident timeline:** “This is the handoff point for the engineer. They can inspect the evidence and collect more if needed. For a validated remediation, Knull presents the exact proposed change. The operator can deny it, or approve that specific version; a stale target blocks execution.”

**Speaker 2 — Point to resolution controls, then show the TrueForge settings tab if available:** “After a production action, the recovery checker looks at workload health and service metrics over the configured window. An uncertain or incomplete result stays open for follow-up. In this local demo, TrueForge is connected to the OpenAI provider, and the separate EKS checkout fixture reproduced the `256Mi` failure and passed 20 out of 20 requests at `1Gi`.”

**Speaker 1 — Close:** “So what you're seeing is the operator workflow around that incident loop: scoped evidence, a tested candidate, a human decision, and a recorded recovery assessment. Today's local walkthrough uses demonstration records; the full TrueForge-authored sandbox run through production recovery is still an integration milestone.”
