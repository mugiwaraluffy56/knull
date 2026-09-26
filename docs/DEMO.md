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
