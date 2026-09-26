import { fetchReadiness } from "@/lib/api";
import Link from "next/link";

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
    <main className="home-page">
      <div className="home-content">
        <div className="eyebrow"><span className="eyebrow-dot" /> AI SRE CONTROL PLANE</div>
        <section className="home-hero">
          <h1>Clarity when<br /><span>systems break.</span></h1>
          <p>Knull connects your services, investigates incidents, and keeps operators in control of every production change.</p>
          <div className="hero-actions">
            <Link href="/fleet" className="btn">View fleet <span aria-hidden="true">↗</span></Link>
            <Link href="/services" className="btn btn-ghost">Manage services</Link>
          </div>
        </section>

        <section className="home-grid" aria-label="Workspace overview">
          <div className="spotlight-card">
            <div className="spotlight-orb" />
            <span className="card-kicker">01 / OBSERVE</span>
            <div>
              <h2>Know what needs attention.</h2>
              <p>A live view of every service and its active incidents.</p>
              <Link href="/fleet">Open fleet <span aria-hidden="true">↗</span></Link>
            </div>
          </div>
          <div className="overview-stack">
            <div className="overview-card health-card">
              <div className="overview-topline"><span className="card-kicker">WORKSPACE STATUS</span><span className={`badge badge-${tone}`}>{overall}</span></div>
              <div className="health-figure">{result.reachable ? Object.keys(result.status?.dependencies ?? {}).length : "—"}<span> dependencies checked</span></div>
              {result.reachable ? (
                <ul className="deps">
                  {Object.entries(result.status?.dependencies ?? {}).map(([name, state]) => (
                    <li key={name} className={state === "ok" ? "dep-ok" : "dep-bad"}>
                      <span>{name}</span><span>{state}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="error">Could not reach the incident API. Start it with <code>make dev-api</code>. Error: {result.error}</p>
              )}
              {result.status?.checkedAt && <p className="health-checked">Last checked {new Date(result.status.checkedAt).toLocaleString()}</p>}
            </div>
            <div className="overview-card quick-card">
              <span className="card-kicker">02 / ACT</span>
              <h2>Evidence before action.</h2>
              <p>Review investigations, proposed changes, and recovery from one place.</p>
              <Link href="/fleet" className="text-link">Explore incidents <span aria-hidden="true">↗</span></Link>
            </div>
          </div>
        </section>
      </div>
    </main>
  );
}
