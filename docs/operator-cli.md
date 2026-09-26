# Operator CLI

Build `knullctl` from a clean clone with `cd backend && go build -o knullctl ./cmd/knullctl`.
Set `KNULL_API_URL` to the customer-owned API URL. Sign in to the web UI, then
provide the `knull_session=...` cookie as `KNULL_SESSION_COOKIE` in your shell.
Avoid saving that cookie in shell history or repository files; it expires with
the operator session. The CLI refuses to send it over plain HTTP except to
localhost and refuses redirects. It uses the same authenticated API as the UI.

Commands:

```text
knullctl check
knullctl health
knullctl services
knullctl integrations
knullctl incidents
knullctl incident INCIDENT_UUID
knullctl events INCIDENT_UUID
knullctl workflow INCIDENT_UUID
knullctl start SERVICE_UUID "checkout failures"
knullctl --json incidents
knullctl --json workflow INCIDENT_UUID
```

`check` reports API and dependency readiness. `integrations` lists credential
metadata only. `events` shows investigation progress and full incident history.
`workflow` fetches the incident and its event history through the same
authenticated API endpoints, then summarizes its current state, TrueForge
session/run identifiers, and ordered progress events. With `--json`, it returns
an object containing the raw `incident` and `events` API responses.
Each error returns a nonzero exit code. No command can bypass approval or
write to Kubernetes directly.
