// Typed client for the Knull incident API.
//
// Response shapes come from lib/generated/api.ts, which is generated from
// backend/openapi/openapi.yaml via `pnpm gen:api`. Keeping the type source in
// the OpenAPI contract means the client cannot silently drift from the server.
import type { components } from "@/lib/generated/api";

export type HealthStatus = components["schemas"]["HealthStatus"];
export type Operator = components["schemas"]["Operator"];
export type CredentialMetadata = components["schemas"]["CredentialMetadata"];
export type CredentialKind = CredentialMetadata["kind"];
export type CredentialScope = CredentialMetadata["scope"];

export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

export const CREDENTIAL_KINDS: CredentialKind[] = [
  "kubernetes",
  "prometheus",
  "github",
  "openai",
];
export const CREDENTIAL_SCOPES: CredentialScope[] = [
  "read",
  "sandbox",
  "production",
];

export interface ReadinessResult {
  reachable: boolean;
  status?: HealthStatus;
  error?: string;
}

// fetchReadiness calls the backend readiness endpoint and normalizes both the
// healthy (200) and degraded (503) responses, which share the same body shape.
// A network failure is reported as unreachable rather than thrown, so the UI
// can render a clear "API unreachable" state.
export async function fetchReadiness(): Promise<ReadinessResult> {
  try {
    const res = await fetch(`${API_BASE_URL}/readyz`, {
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    const status = (await res.json()) as HealthStatus;
    return { reachable: true, status };
  } catch (err) {
    return {
      reachable: false,
      error: err instanceof Error ? err.message : "unknown error",
    };
  }
}

// fetchMe returns the signed-in operator, or null when not authenticated.
// `cookie` is forwarded when called from a server component so the session
// travels with the request.
export async function fetchMe(cookie?: string): Promise<Operator | null> {
  try {
    const res = await fetch(`${API_BASE_URL}/api/me`, {
      cache: "no-store",
      credentials: "include",
      headers: cookie ? { cookie } : undefined,
    });
    if (res.status === 401) return null;
    if (!res.ok) return null;
    return (await res.json()) as Operator;
  } catch {
    return null;
  }
}

export interface CredentialsResult {
  authenticated: boolean;
  credentials: CredentialMetadata[];
}

export interface IntegrationState {
  status: "ok" | "unavailable" | "disabled";
  lastSuccess?: string;
  lastError?: string;
  affectedIncidents?: string[];
}

export async function fetchIntegrationHealth(): Promise<{ authenticated: boolean; integrations: Record<string, IntegrationState> }> {
  const res = await fetch(`${API_BASE_URL}/api/integrations/health`, { cache: "no-store", credentials: "include" });
  if (res.status === 401) return { authenticated: false, integrations: {} };
  if (!res.ok) return { authenticated: true, integrations: {} };
  const body = await res.json() as { integrations?: Record<string, IntegrationState> };
  return { authenticated: true, integrations: body.integrations ?? {} };
}

// listCredentials returns credential metadata. When the caller is not
// authenticated it reports authenticated=false so the page can prompt sign-in.
export async function listCredentials(
  cookie?: string,
): Promise<CredentialsResult> {
  const res = await fetch(`${API_BASE_URL}/api/integrations/credentials`, {
    cache: "no-store",
    credentials: "include",
    headers: cookie ? { cookie } : undefined,
  });
  if (res.status === 401) return { authenticated: false, credentials: [] };
  const body = (await res.json()) as { credentials: CredentialMetadata[] };
  return { authenticated: true, credentials: body.credentials ?? [] };
}

// putCredential stores a credential from the browser. Returns an error message
// on failure, or null on success.
export async function putCredential(input: {
  kind: CredentialKind;
  scope: CredentialScope;
  value: string;
}): Promise<string | null> {
  const res = await fetch(`${API_BASE_URL}/api/integrations/credentials`, {
    method: "PUT",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (res.ok) return null;
  try {
    const body = (await res.json()) as { error?: string };
    return body.error ?? `request failed (${res.status})`;
  } catch {
    return `request failed (${res.status})`;
  }
}

export function loginURL(): string {
  return `${API_BASE_URL}/api/auth/login`;
}

export type FleetRow = components["schemas"]["FleetRow"];

export interface FleetResult {
  authenticated: boolean;
  fleet: FleetRow[];
}

// fetchFleet returns the fleet view. authenticated=false prompts sign-in.
export async function fetchFleet(): Promise<FleetResult> {
  const res = await fetch(`${API_BASE_URL}/api/fleet`, {
    cache: "no-store",
    credentials: "include",
  });
  if (res.status === 401) return { authenticated: false, fleet: [] };
  const body = (await res.json()) as { fleet: FleetRow[] };
  return { authenticated: true, fleet: body.fleet ?? [] };
}

export type Service = components["schemas"]["Service"];
export type ServiceInput = components["schemas"]["ServiceInput"];
export type RecoveryPolicy = components["schemas"]["RecoveryPolicy"];
export type RecoveryPolicySnapshot = components["schemas"]["RecoveryPolicySnapshot"];

export async function fetchRecoveryPolicy(serviceId: string): Promise<RecoveryPolicySnapshot | null> {
  const res = await fetch(`${API_BASE_URL}/api/services/${serviceId}/recovery-policy`, {
    credentials: "include",
    cache: "no-store",
  });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`could not load recovery policy (${res.status})`);
  return (await res.json()) as RecoveryPolicySnapshot;
}

export async function putRecoveryPolicy(serviceId: string, policy: RecoveryPolicy): Promise<{ policy?: RecoveryPolicySnapshot; error?: string }> {
  try {
    const res = await fetch(`${API_BASE_URL}/api/services/${serviceId}/recovery-policy`, {
      method: "PUT",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(policy),
    });
    if (res.ok) return { policy: (await res.json()) as RecoveryPolicySnapshot };
    const body = (await res.json()) as { error?: string };
    return { error: body.error ?? `request failed (${res.status})` };
  } catch (err) {
    return { error: err instanceof Error ? err.message : "request failed" };
  }
}

export interface ServicesResult {
  authenticated: boolean;
  services: Service[];
}

// listServices returns configured services, or authenticated=false when the
// caller has no session.
export async function listServices(): Promise<ServicesResult> {
  const res = await fetch(`${API_BASE_URL}/api/services`, {
    cache: "no-store",
    credentials: "include",
  });
  if (res.status === 401) return { authenticated: false, services: [] };
  const body = (await res.json()) as { services: Service[] };
  return { authenticated: true, services: body.services ?? [] };
}

// MutationResult carries either a stored service or field-level validation
// errors so the form can point at the exact problem.
export interface MutationResult {
  ok: boolean;
  service?: Service;
  error?: string;
  fields?: Record<string, string>;
}

async function serviceMutation(
  url: string,
  method: "POST" | "PUT",
  input: ServiceInput,
): Promise<MutationResult> {
  const res = await fetch(url, {
    method,
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (res.ok) {
    return { ok: true, service: (await res.json()) as Service };
  }
  try {
    const body = (await res.json()) as {
      error?: string;
      fields?: Record<string, string>;
    };
    return { ok: false, error: body.error, fields: body.fields };
  } catch {
    return { ok: false, error: `request failed (${res.status})` };
  }
}

export function createService(input: ServiceInput): Promise<MutationResult> {
  return serviceMutation(`${API_BASE_URL}/api/services`, "POST", input);
}

export function updateService(
  id: string,
  input: ServiceInput,
): Promise<MutationResult> {
  return serviceMutation(`${API_BASE_URL}/api/services/${id}`, "PUT", input);
}

export async function setServiceEnabled(
  id: string,
  enabled: boolean,
): Promise<void> {
  await fetch(`${API_BASE_URL}/api/services/${id}/enabled`, {
    method: "PATCH",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled }),
  });
}

export type Incident = components["schemas"]["Incident"];
export type IncidentEvent = components["schemas"]["IncidentEvent"];
export type ActionContract = components["schemas"]["ActionContract"];
export type ActionDraft = components["schemas"]["ActionDraft"];
export type ActionDecision = components["schemas"]["ActionDecision"];
export type MemoryValidationResult = components["schemas"]["MemoryValidationResult"];

export type MemoryExecutionStatus = "ACCEPTED" | "COMPLETED" | "FAILED" | "UNKNOWN";

export interface MemoryExecutionReceipt {
  requestId: string;
  priorRequestId?: string;
  actionEventId: string;
  actionDigest: string;
  status: MemoryExecutionStatus;
  target: ActionContract["target"];
  field: string;
  submittedPatch?: unknown;
  resourceUid?: string;
  resourceVersion?: string;
  resultingValue?: string;
  observedAt: string;
}

export interface MemoryExecutionResult {
  receipt?: MemoryExecutionReceipt;
  error?: string;
  status: number;
}

async function memoryExecutionRequest(
  incidentId: string,
  actionEventId: string,
  actionDigest: string,
  reconcile: boolean,
): Promise<MemoryExecutionResult> {
  const suffix = reconcile ? "/reconcile" : "";
  try {
    const res = await fetch(`${API_BASE_URL}/api/incidents/${incidentId}/executions/memory${suffix}`, {
      method: "POST",
      credentials: "include",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ actionEventId, actionDigest }),
    });
    const body = await res.json() as MemoryExecutionReceipt | { error?: string };
    if ("requestId" in body && typeof body.requestId === "string") {
      return {
        receipt: body as MemoryExecutionReceipt,
        status: res.status,
        error: res.ok || res.status === 202 ? undefined : `request failed (${res.status})`,
      };
    }
    return { status: res.status, error: (body as { error?: string }).error ?? `request failed (${res.status})` };
  } catch (err) {
    return { status: 0, error: err instanceof Error ? err.message : "request failed" };
  }
}

// Production execution is an explicit operator action after approval. The
// backend validates the exact approval, action event, and digest again.
export function executeMemoryAction(incidentId: string, actionEventId: string, actionDigest: string): Promise<MemoryExecutionResult> {
  return memoryExecutionRequest(incidentId, actionEventId, actionDigest, false);
}

// Reconciliation reads the result of an uncertain execution and only retries
// when the backend can prove the original target preconditions still hold.
export function reconcileMemoryAction(incidentId: string, actionEventId: string, actionDigest: string): Promise<MemoryExecutionResult> {
  return memoryExecutionRequest(incidentId, actionEventId, actionDigest, true);
}

export async function incidentOperation(id: string, operation: "close" | "escalate" | "recovery/verify", expectedVersion?: number, reason = ""): Promise<string | null> {
	try {
		const res = await fetch(`${API_BASE_URL}/api/incidents/${id}/${operation}`, {method: "POST", credentials: "include", cache: "no-store", headers: {"Content-Type": "application/json"}, body: JSON.stringify({expectedVersion, reason})});
		if (res.ok) return null;
		const body = await res.json() as {error?: string};
		return body.error ?? `request failed (${res.status})`;
	} catch (err) { return err instanceof Error ? err.message : "request failed"; }
}

export interface ActionDecisionResult {
  decision?: ActionDecision;
  error?: string;
  status: number;
}

// The operator chooses an exact event and digest. The backend checks the
// authenticated operator and validation; the browser cannot grant authority.
export async function decideAction(
  incidentId: string,
  actionEventId: string,
  actionDigest: string,
  decision: "approve" | "deny",
  reason = "",
): Promise<ActionDecisionResult> {
  const endpoint = decision === "approve" ? "approvals" : "denials";
  try {
    const res = await fetch(`${API_BASE_URL}/api/incidents/${incidentId}/${endpoint}`, {
      method: "POST",
      credentials: "include",
      cache: "no-store",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ actionEventId, actionDigest, reason }),
    });
    if (res.ok) {
      return { status: res.status, decision: (await res.json()) as ActionDecision };
    }
    const body = (await res.json()) as { error?: string };
    return { status: res.status, error: body.error ?? `request failed (${res.status})` };
  } catch (err) {
    return { status: 0, error: err instanceof Error ? err.message : "request failed" };
  }
}

// fetchIncident returns one incident, or null when not found / unauthenticated.
export async function fetchIncident(id: string): Promise<Incident | null> {
  const res = await fetch(`${API_BASE_URL}/api/incidents/${id}`, {
    cache: "no-store",
    credentials: "include",
  });
  if (!res.ok) return null;
  return (await res.json()) as Incident;
}

// collectEvidence triggers read-only investigation for an incident. Returns an
// error message on failure, or null on success.
export async function collectEvidence(id: string): Promise<string | null> {
  const res = await fetch(`${API_BASE_URL}/api/incidents/${id}/collect`, {
    method: "POST",
    credentials: "include",
  });
  if (res.ok) return null;
  try {
    const body = (await res.json()) as { error?: string };
    return body.error ?? `request failed (${res.status})`;
  } catch {
    return `request failed (${res.status})`;
  }
}

export async function createActionPlan(incidentId: string, draft: ActionDraft): Promise<{ eventId?: string; error?: string }> {
  try {
    const res = await fetch(`${API_BASE_URL}/api/incidents/${incidentId}/plans`, {
      method: "POST", credentials: "include", cache: "no-store",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(draft),
    });
    if (res.ok) return { eventId: ((await res.json()) as { eventId: string }).eventId };
    const body = (await res.json()) as { error?: string };
    return { error: body.error ?? `request failed (${res.status})` };
  } catch (err) { return { error: err instanceof Error ? err.message : "request failed" }; }
}

export async function validateMemoryAction(incidentId: string, actionEventId: string, workload: components["schemas"]["SandboxWorkload"]): Promise<{ result?: MemoryValidationResult; error?: string }> {
  try {
    const res = await fetch(`${API_BASE_URL}/api/incidents/${incidentId}/validations/memory`, {
      method: "POST", credentials: "include", cache: "no-store",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify({ actionEventId, workload }),
    });
    const body = await res.json() as MemoryValidationResult | { result?: MemoryValidationResult; error?: string };
    return res.ok ? { result: body as MemoryValidationResult } : { result: (body as { result?: MemoryValidationResult }).result, error: (body as { error?: string }).error ?? `request failed (${res.status})` };
  } catch (err) { return { error: err instanceof Error ? err.message : "request failed" }; }
}

// fetchIncidentEvents returns an incident's ordered audit history.
export async function fetchIncidentEvents(id: string): Promise<IncidentEvent[]> {
  const res = await fetch(`${API_BASE_URL}/api/incidents/${id}/events`, {
    cache: "no-store",
    credentials: "include",
  });
  if (!res.ok) return [];
  const body = (await res.json()) as { events: IncidentEvent[] };
  return body.events ?? [];
}

export interface StartInvestigationResult {
  ok: boolean;
  incident?: Incident;
  error?: string;
  activeIncidentId?: string;
}

// startInvestigation opens an operator-initiated incident for a service. On a
// duplicate-active conflict it returns the existing incident id so the UI can
// point at it.
export async function startInvestigation(input: {
  serviceId: string;
  summary: string;
  force?: boolean;
}): Promise<StartInvestigationResult> {
  const res = await fetch(`${API_BASE_URL}/api/incidents`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (res.ok) return { ok: true, incident: (await res.json()) as Incident };
  try {
    const body = (await res.json()) as {
      error?: string;
      activeIncidentId?: string;
    };
    return { ok: false, error: body.error, activeIncidentId: body.activeIncidentId };
  } catch {
    return { ok: false, error: `request failed (${res.status})` };
  }
}
