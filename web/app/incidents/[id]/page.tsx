"use client";

import { use, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import {
  collectEvidence,
  fetchIncident,
  fetchIncidentEvents,
  type Incident,
  type IncidentEvent,
} from "@/lib/api";

// Incident detail. Task 7 delivers the basic incident record and its ordered
// event history for navigability; task 9 enriches this with the full evidence
// timeline (findings, decisions, sandbox, approvals).
export default function IncidentPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const [incident, setIncident] = useState<Incident | null>(null);
  const [events, setEvents] = useState<IncidentEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [collecting, setCollecting] = useState(false);
  const [collectMsg, setCollectMsg] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [inc, evs] = await Promise.all([
      fetchIncident(id),
      fetchIncidentEvents(id),
    ]);
    setIncident(inc);
    setEvents(evs);
    setLoading(false);
  }, [id]);

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  if (loading) {
    return (
      <main className="page">
        <span className="auth-muted">Loading…</span>
      </main>
    );
  }

  if (!incident) {
    return (
      <main className="page">
        <section className="card">
          <h1>Incident</h1>
          <p className="subtitle">Not found or not authorized.</p>
          <Link href="/fleet" className="btn">
            Back to fleet
          </Link>
        </section>
      </main>
    );
  }

  return (
    <main className="page page-wide">
      <section className="card card-wide">
        <div className="incident-head">
          <div>
            <h1>{incident.serviceKey}</h1>
            <p className="subtitle">
              {incident.environment} · {incident.summary}
            </p>
          </div>
          <span className={`status status-${statusClass(incident.state)}`}>
            {incident.state}
          </span>
        </div>

        {incident.symptoms && Object.keys(incident.symptoms).length > 0 && (
          <ul className="deps">
            {Object.entries(incident.symptoms).map(([k, v]) => (
              <li key={k}>
                <span>{k}</span>
                <span>{v}</span>
              </li>
            ))}
          </ul>
        )}

        <div className="incident-actions">
          <button
            className="btn btn-ghost"
            disabled={collecting}
            onClick={async () => {
              setCollecting(true);
              setCollectMsg(null);
              const err = await collectEvidence(id);
              setCollecting(false);
              setCollectMsg(err ?? "Evidence collected");
              await load();
            }}
          >
            {collecting ? "Collecting…" : "Collect evidence"}
          </button>
          {collectMsg && <span className="auth-muted"> {collectMsg}</span>}
        </div>

        <h2 className="section-title">Timeline</h2>
        {events.length === 0 ? (
          <p className="auth-muted">No events yet.</p>
        ) : (
          <ol className="timeline">
            {events.map((e) => (
              <li key={e.id} className="timeline-item">
                <span className={`cat cat-${e.category ?? "system"}`}>
                  {e.category ?? "system"}
                </span>
                <span className="timeline-body">
                  {e.type === "STATE_CHANGE" ? (
                    <strong>
                      {e.fromState ? `${e.fromState} → ` : ""}
                      {e.toState}
                    </strong>
                  ) : null}
                  {e.reason && <> {e.reason}</>}
                  {e.source && (
                    <span className="timeline-src"> · {e.source}</span>
                  )}
                  {e.target && (
                    <span className="timeline-src"> → {e.target}</span>
                  )}
                  <span className="timeline-actor"> · {e.actor || "system"}</span>
                </span>
                <span className="timeline-time">
                  {new Date(e.observedAt ?? e.createdAt).toLocaleString()}
                </span>
              </li>
            ))}
          </ol>
        )}

        <Link href="/fleet" className="navlink">
          ← Back to fleet
        </Link>
      </section>
    </main>
  );
}

function statusClass(state: string): string {
  switch (state) {
    case "RECEIVED":
    case "INVESTIGATING":
    case "PLANNING":
    case "VALIDATING":
      return "INVESTIGATING";
    case "AWAITING_APPROVAL":
      return "AWAITING_APPROVAL";
    case "REMEDIATING":
      return "REMEDIATING";
    case "VERIFYING":
      return "VERIFYING";
    case "RECOVERED":
    case "CLOSED":
      return "HEALTHY";
    default:
      return "INCIDENT";
  }
}
