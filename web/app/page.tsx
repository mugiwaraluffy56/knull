import { fetchReadiness } from "@/lib/api";

// The home page is a health page: it confirms the browser can reach the backend
// and shows each dependency's status. This is the task-1 proof that the whole
// workspace starts and talks end to end. It re-renders on every request so the
// status is always live.
export const dynamic = "force-dynamic";

export default async function HomePage() {
  const result = await fetchReadiness();

  const overall = !result.reachable
    ? "API unreachable"
    : result.status?.status === "ok"
      ? "Healthy"
      : "Degraded";

  const tone = overall === "Healthy" ? "ok" : "bad";

  return (
    <main className="page">
      <section className="card">
        <h1>Knull</h1>
        <p className="subtitle">AI SRE — workspace health</p>

        <div className={`badge badge-${tone}`}>{overall}</div>

        {result.reachable ? (
          <ul className="deps">
            {Object.entries(result.status?.dependencies ?? {}).map(
              ([name, state]) => (
                <li key={name} className={state === "ok" ? "dep-ok" : "dep-bad"}>
                  <span>{name}</span>
                  <span>{state}</span>
                </li>
              ),
            )}
            {result.status?.checkedAt && (
              <li className="checked">
                <span>checked</span>
                <span>{result.status.checkedAt}</span>
              </li>
            )}
          </ul>
        ) : (
          <p className="error">
            Could not reach the incident API. Start it with{" "}
            <code>make dev-api</code>. Error: {result.error}
          </p>
        )}
      </section>
    </main>
  );
}
