// Typed client for the Knull incident API.
//
// Response shapes come from lib/generated/api.ts, which is generated from
// backend/openapi/openapi.yaml via `pnpm gen:api`. Keeping the type source in
// the OpenAPI contract means the client cannot silently drift from the server.
import type { components } from "@/lib/generated/api";

export type HealthStatus = components["schemas"]["HealthStatus"];

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

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
