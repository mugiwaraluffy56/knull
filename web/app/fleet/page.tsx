"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { fetchFleet, loginURL, type FleetRow } from "@/lib/api";

// The fleet view lists services with their health, active incidents first.
// Telemetry signals are shown as unavailable until the metrics integration
// lands, so missing data never reads as healthy. It polls so backend state
// changes appear without blocking on long-running investigations.
export default function FleetPage() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [fleet, setFleet] = useState<FleetRow[]>([]);

  const load = useCallback(async () => {
    const res = await fetchFleet();
    setAuthenticated(res.authenticated);
    setFleet(res.fleet);
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  if (authenticated === null) {
    return (
      <main className="page">
        <span className="auth-muted">Loading…</span>
      </main>
    );
  }

  if (!authenticated) {
    return (
      <main className="page">
        <section className="card">
          <h1>Fleet</h1>
          <p className="subtitle">Sign in to view the fleet.</p>
          <a className="btn" href={loginURL()}>
            Sign in
          </a>
        </section>
      </main>
    );
  }

  return (
    <main className="page page-wide">
      <section className="card card-wide">
        <h1>Fleet</h1>
        <p className="subtitle">
          Services with active incidents first. Live signals are unavailable
          until a metrics integration is connected.
        </p>

        {fleet.length === 0 ? (
          <p className="auth-muted">
            No services configured yet. Add one on the{" "}
            <Link href="/services" className="navlink">
              Services
            </Link>{" "}
            page.
          </p>
        ) : (
          <table className="cred-table">
            <thead>
              <tr>
                <th>Service</th>
                <th>Environment</th>
                <th>Status</th>
                <th>Signals</th>
                <th>Incident</th>
              </tr>
            </thead>
            <tbody>
              {fleet.map((row) => {
                const incident = row.activeIncidentCount > 0;
                return (
                  <tr
                    key={`${row.key}-${row.environment}`}
                    className={incident ? "row-incident" : ""}
                  >
                    <td>
                      <strong>{row.displayName}</strong>
                      <br />
                      <code>{row.key}</code>
                    </td>
                    <td>{row.environment}</td>
                    <td>
                      <span className={`status status-${row.status}`}>
                        {row.status}
                      </span>
                    </td>
                    <td className="auth-muted">
                      {row.signalsAvailable ? "—" : "unavailable"}
                    </td>
                    <td>
                      {row.latestIncident ? (
                        <Link
                          href={`/incidents/${row.latestIncident.id}`}
                          className="navlink"
                        >
                          {row.latestIncident.state} →
                        </Link>
                      ) : (
                        <span className="auth-muted">none</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </section>
    </main>
  );
}
