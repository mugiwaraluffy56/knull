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
              <li key={e.id} id={`event-${e.id}`} className="timeline-item">
                <span className={`cat cat-${e.category ?? "system"}`}>
                  {e.category ?? "system"}
                </span>
                <div className="timeline-body">
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
                  {e.source === "prometheus" && <PrometheusFinding event={e} />}
                  {e.source === "github" && <GitHubFinding event={e} />}
                  {e.source === "jev" && <JevClassification event={e} />}
                </div>
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

function JevClassification({ event }: { event: IncidentEvent }) {
  const data = event.data;
  if (!data || typeof data.decision !== "object" || data.decision === null) return null;
  const decision = data.decision as { classes?: Array<{ class?: string; confidence?: number; evidence_ids?: string[] }>; rationale?: string };
  const metadata = data.metadata as { model?: string; decision_version?: string; calibrated?: boolean } | undefined;
  const classes = Array.isArray(decision.classes) ? decision.classes : [];
  return <div className="metric-finding">
    <strong>Cause hypotheses</strong>
    {classes.map((hypothesis, index) => <div key={index}>
      {index + 1}. {hypothesis.class} · {typeof hypothesis.confidence === "number" ? `${Math.round(hypothesis.confidence * 100)}% model confidence` : "confidence unavailable"}
      {Array.isArray(hypothesis.evidence_ids) && hypothesis.evidence_ids.length > 0 && <span> · evidence: {hypothesis.evidence_ids.map((id, refIndex) => <span key={id}>{refIndex > 0 ? ", " : ""}<a href={`#event-${id}`}>{id.slice(0, 8)}</a></span>)}</span>}
    </div>)}
    {decision.rationale && <div>Hypothesis summary: {decision.rationale}</div>}
    {metadata && <div className="auth-muted">{metadata.model} · {metadata.decision_version} · {metadata.calibrated ? "calibrated" : "uncalibrated confidence"}</div>}
  </div>;
}

function PrometheusFinding({ event }: { event: IncidentEvent }) {
  const data = event.data;
  if (!data) return null;
  const signal = typeof data.signal === "string" ? data.signal : "metric";
  const unit = typeof data.unit === "string" ? data.unit : "";
  const result = typeof data.result === "string" ? data.result : "";
  const query = typeof data.query === "string" ? data.query : "";
  const window = data.window as
    | { start?: string; end?: string; step?: string }
    | undefined;

  return (
    <div className="metric-finding">
      <strong>{signal}</strong>: {data.available === false ? "unavailable" : result || "no active alerts"}
      {unit && <span> · {unit}</span>}
      {window?.start && window?.end && (
        <span> · {window.start} to {window.end} ({window.step})</span>
      )}
      {query && <code className="metric-query">{query}</code>}
    </div>
  );
}

function GitHubFinding({ event }: { event: IncidentEvent }) {
  const data = event.data;
  if (!data || data.available === false) return null;
  const url = typeof data.url === "string" ? data.url : "";
  const message = typeof data.message === "string" ? data.message : "";
  const files = Array.isArray(data.files) ? data.files : [];
  const pullRequests = Array.isArray(data.pullRequests) ? data.pullRequests : [];

  return (
    <div className="metric-finding">
      {url && <a href={url} target="_blank" rel="noopener noreferrer">View commit</a>}
      {message && <span> · {message}</span>}
      {files.map((item, index) => {
        const file = item as { path?: string; relevantLines?: string[] };
        return (
          <div key={index}>
            {file.path}
            {Array.isArray(file.relevantLines) && file.relevantLines.length > 0 && (
              <code className="metric-query">{file.relevantLines.join("\n")}</code>
            )}
          </div>
        );
      })}
      {pullRequests.map((item, index) => {
        const pr = item as { number?: number; title?: string; url?: string };
        return <div key={index}>PR #{pr.number}: <a href={pr.url} target="_blank" rel="noopener noreferrer">{pr.title}</a></div>;
      })}
    </div>
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
