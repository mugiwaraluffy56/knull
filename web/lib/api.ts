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

export type Service = components["schemas"]["Service"];
export type ServiceInput = components["schemas"]["ServiceInput"];

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
