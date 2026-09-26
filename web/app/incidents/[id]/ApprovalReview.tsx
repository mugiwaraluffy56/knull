"use client";

import { useEffect, useState } from "react";
import {
  decideAction,
  executeMemoryAction,
  fetchIncident,
  fetchIncidentEvents,
  reconcileMemoryAction,
  type ActionContract,
  type Incident,
  type IncidentEvent,
  type MemoryExecutionReceipt,
  type MemoryValidationResult,
} from "@/lib/api";

type Review = {
  actionEvent: IncidentEvent;
  action: ActionContract;
  validationEvent?: IncidentEvent;
  validation?: MemoryValidationResult;
  decisionEvent?: IncidentEvent;
  invalidationEvent?: IncidentEvent;
  executionEvent?: IncidentEvent;
  execution?: MemoryExecutionReceipt;
};

function latestReview(events: IncidentEvent[]): Review | null {
  const ordered = [...events].sort((a, b) => b.seq - a.seq);
  const actionEvent = ordered.find((event) => event.source === "action-plan" && event.category === "action");
  const action = actionEvent?.data?.contract as ActionContract | undefined;
  if (!actionEvent || !action || typeof action.digest !== "string" || !action.target) return null;
  const validationEvent = ordered.find((event) => {
    if (event.source !== "sandbox-validation") return false;
    const result = event.data?.result as MemoryValidationResult | undefined;
    return result?.actionEventId === actionEvent.id && result.actionDigest === action.digest;
  });
  const validation = validationEvent?.data?.result as MemoryValidationResult | undefined;
  const decisionEvents = ordered.filter((event) => event.source === "approval" && event.data?.actionEventId === actionEvent.id && event.data?.actionDigest === action.digest);
  const executionEvent = ordered.find((event) => {
    if (event.source !== "production-executor") return false;
    const receipt = event.data?.execution as MemoryExecutionReceipt | undefined;
    return receipt?.actionEventId === actionEvent.id && receipt.actionDigest === action.digest;
  });
  const execution = executionEvent?.data?.execution as MemoryExecutionReceipt | undefined;
  return {
    actionEvent,
    action,
    validationEvent,
    validation,
    decisionEvent: decisionEvents.find((event) => event.data?.decision === "APPROVED" || event.data?.decision === "DENIED"),
    invalidationEvent: decisionEvents.find((event) => event.data?.approval === "invalidated"),
    executionEvent,
    execution,
  };
}

type ReviewState = "pending" | "approved" | "denied" | "expired" | "stale" | "remediating" | "verifying" | "recovered" | "closed" | "validation-needed";

function reviewState(incident: Incident, review: Review, now: number): ReviewState {
  if (incident.state === "REMEDIATING") return "remediating";
  if (incident.state === "VERIFYING") return "verifying";
  if (incident.state === "RECOVERED") return "recovered";
  if (incident.state === "CLOSED") return "closed";
  if (review.invalidationEvent) return review.invalidationEvent.reason?.includes("expired") ? "expired" : "stale";
  if (review.decisionEvent?.data?.decision === "DENIED" || incident.state === "DENIED") return "denied";
  if (incident.state !== "AWAITING_APPROVAL") return "validation-needed";
  if (review.decisionEvent?.data?.decision === "APPROVED") {
    const expiry = review.decisionEvent.data?.expiresAt;
    return typeof expiry === "string" && Date.parse(expiry) > now ? "approved" : "expired";
  }
  if (review.validation?.passed === true && review.validationEvent?.data?.available === true) return "pending";
  return "validation-needed";
}

const stateText: Record<ReviewState, string> = {
  pending: "Awaiting your decision",
  approved: "Approved · waiting for execution",
  denied: "Denied · no production change",
  expired: "Expired · new validation required",
  stale: "Stale · new validation required",
  remediating: "Applying approved change",
  verifying: "Verifying recovery",
  recovered: "Recovered",
  closed: "Closed",
  "validation-needed": "Validation required",
};

function readableTime(value?: string): string {
  if (!value) return "Unavailable";
  const time = new Date(value);
  return Number.isNaN(time.getTime()) ? "Unavailable" : time.toLocaleString();
}

function remaining(expiresAt: string | undefined, now: number): string {
  if (!expiresAt) return "Unavailable";
  const seconds = Math.max(0, Math.ceil((Date.parse(expiresAt) - now) / 1000));
  if (!Number.isFinite(seconds)) return "Unavailable";
  return seconds === 0 ? "Expired" : `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, "0")}s remaining`;
}

function RunSummary({ label, run, evidence }: {
  label: string;
  run: MemoryValidationResult["baseline"];
  evidence: MemoryValidationResult["baselineEvidence"];
}) {
  return <div className="approval-run">
    <div className="approval-run-title"><strong>{label}</strong><span>{run.status === "checked" && run.cleanupVerified ? "Checked · cleaned up" : "Incomplete"}</span></div>
    <div className="approval-value">{run.workload.memory} memory limit · {run.namespace}</div>
    <div className="approval-run-signals">
      <span>Healthy pods <strong>{evidence.pods.healthy}/{evidence.pods.desired}</strong></span>
      <span>OOM kills <strong>{evidence.pods.oomKills}</strong></span>
      <span>Errors <strong>{(evidence.metrics.errorRate * 100).toFixed(1)}%</strong></span>
      <span>p95 <strong>{evidence.metrics.p95Milliseconds.toFixed(0)} ms</strong></span>
    </div>
    <div className="approval-secondary">{evidence.metrics.samples} metric samples · load script exit {evidence.script.exitCode} · script artifact {evidence.script.artifactRef}</div>
  </div>;
}

export default function ApprovalReview({ incident, events, onRefresh }: {
  incident: Incident;
  events: IncidentEvent[];
  onRefresh: () => Promise<void>;
}) {
  const review = latestReview(events);
  const [now, setNow] = useState(() => Date.now());
  const [reason, setReason] = useState("");
  const [processing, setProcessing] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [executionReceipt, setExecutionReceipt] = useState<MemoryExecutionReceipt | null>(null);
  const [executionMessage, setExecutionMessage] = useState<string | null>(null);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  if (!review) return null;
  const { action, validation, validationEvent, decisionEvent, invalidationEvent } = review;
  const matchingLocalExecution = executionReceipt?.actionEventId === review.actionEvent.id && executionReceipt.actionDigest === action.digest
    ? executionReceipt
    : undefined;
  const execution = matchingLocalExecution && (!review.execution || Date.parse(matchingLocalExecution.observedAt) >= Date.parse(review.execution.observedAt))
    ? matchingLocalExecution
    : review.execution;
  const state = reviewState(incident, review, now);
  const canDecide = state === "pending" && !processing;
  const expiresAt = typeof decisionEvent?.data?.expiresAt === "string" ? decisionEvent.data.expiresAt : undefined;
  const limitations = [...new Set([...(validation?.baseline.limitations ?? []), ...(validation?.candidate.limitations ?? [])])];
  const differences = [...new Set([...(validation?.baseline.environmentDifferences ?? []), ...(validation?.candidate.environmentDifferences ?? [])])];

  async function submit(decision: "approve" | "deny") {
    if (!review || !canDecide) return;
    setProcessing(true);
    setMessage(null);
    try {
      // Re-read immediately before posting. A poll interval alone can leave
      // an old plan visible while another operator changes the incident.
      const [freshIncident, freshEvents] = await Promise.all([
        fetchIncident(incident.id),
        fetchIncidentEvents(incident.id),
      ]);
      const freshReview = latestReview(freshEvents);
      if (!freshIncident || !freshReview || freshIncident.version !== incident.version || freshReview.actionEvent.id !== review.actionEvent.id || freshReview.action.digest !== action.digest || freshReview.validationEvent?.id !== validationEvent?.id || reviewState(freshIncident, freshReview, Date.now()) !== "pending") {
        setMessage("This review changed. Check the latest action and validation before deciding.");
        await onRefresh();
        return;
      }
      const result = await decideAction(incident.id, review.actionEvent.id, action.digest, decision, decision === "deny" ? reason.trim() : "");
      if (result.error) {
        setMessage(result.status === 409 ? "This action is no longer eligible. Review the latest incident state." : result.error);
      } else {
        setMessage(decision === "approve" ? "Approved for this exact action. Knull will recheck the live target before changing production." : "Denied. No production change was authorized.");
        setReason("");
      }
      await onRefresh();
    } catch {
      setMessage("Could not refresh the incident. No decision was submitted; try again.");
    } finally {
      setProcessing(false);
    }
  }

  async function runExecution(reconcile = false) {
    if (!review || processing) return;
    setProcessing(true);
    setExecutionMessage(null);
    try {
      if (!reconcile) {
        // Approval is a separate operator action from execution. Re-read the
        // exact approved review before making the explicit production request.
        const [freshIncident, freshEvents] = await Promise.all([
          fetchIncident(incident.id),
          fetchIncidentEvents(incident.id),
        ]);
        const freshReview = latestReview(freshEvents);
        if (!freshIncident || !freshReview || freshIncident.version !== incident.version || freshReview.actionEvent.id !== review.actionEvent.id || freshReview.action.digest !== action.digest || reviewState(freshIncident, freshReview, Date.now()) !== "approved") {
          setExecutionMessage("This approval changed or expired. Refresh the incident before executing.");
          await onRefresh();
          return;
        }
      }
      const result = reconcile
        ? await reconcileMemoryAction(incident.id, review.actionEvent.id, action.digest)
        : await executeMemoryAction(incident.id, review.actionEvent.id, action.digest);
      if (result.receipt) setExecutionReceipt(result.receipt);
      if (result.error) setExecutionMessage(result.receipt
        ? "The request returned an execution receipt. Check its status below before taking another action."
        : result.error);
      else setExecutionMessage(null);
      await onRefresh();
    } catch {
      setExecutionMessage("Could not confirm the execution result. Refresh the incident and reconcile before retrying.");
    } finally {
      setProcessing(false);
    }
  }

  return <section className="approval-review" aria-labelledby="approval-heading">
    <div className="approval-topline"><span className="approval-eyebrow">Production action review</span><span className={`approval-state approval-state-${state}`} role="status" aria-live="polite">{stateText[state]}</span></div>
    <h2 id="approval-heading">{action.type === "ADJUST_MEMORY" ? "Restore the memory limit" : action.type.replaceAll("_", " ").toLowerCase()}</h2>
    <p className="approval-lead">Review the exact target, validated change, and sandbox evidence before making a decision.</p>

    <div className="approval-grid">
      <div className="approval-detail"><span>Service / environment</span><strong>{incident.serviceKey} / {action.environment}</strong></div>
      <div className="approval-detail"><span>Resource</span><strong>{action.target.cluster} / {action.target.namespace} / {action.target.kind} / {action.target.name}{action.target.container ? ` / ${action.target.container}` : ""}</strong></div>
      <div className="approval-detail"><span>Field</span><strong>{action.field}</strong></div>
      <div className="approval-detail"><span>Risk</span><strong>{action.risk}</strong></div>
    </div>

    <div className="approval-change" aria-label="Proposed change"><div><span>Current</span><strong>{action.currentValue}</strong></div><span aria-hidden="true" className="approval-arrow">→</span><div><span>Proposed</span><strong>{action.desiredValue}</strong></div></div>
    <div className="approval-impact"><span>Expected impact</span><p>{action.expectedImpact}</p></div>
    <div className="approval-precondition">Live target must still have UID <code>{action.preconditions.resourceUid}</code>, version <code>{action.preconditions.resourceVersion}</code>, and {action.field} = <code>{action.preconditions.currentValue}</code>. Knull checks this again before execution.</div>

    <div className="approval-section-head"><h3>Sandbox validation</h3><span>{validationEvent ? readableTime(validationEvent.observedAt ?? validationEvent.createdAt) : "Not available"}</span></div>
    {validation && validationEvent?.data?.available === true ? <>
      <p className={`approval-validation ${validation.passed ? "approval-validation-pass" : "approval-validation-fail"}`}>{validation.passed ? "Passed · failure reproduced, candidate recovered" : `Failed · ${validation.failure || "validation incomplete"}`}</p>
      {validation.baseline?.workload && validation.candidate?.workload && validation.baselineEvidence?.pods && validation.candidateEvidence?.pods && <div className="approval-runs"><RunSummary label="Baseline" run={validation.baseline} evidence={validation.baselineEvidence} /><RunSummary label="Candidate" run={validation.candidate} evidence={validation.candidateEvidence} /></div>}
    </> : <p className="approval-validation approval-validation-fail">A successful sandbox validation for this exact action has not been recorded.</p>}

    <div className="approval-section-head"><h3>Evidence and limits</h3></div>
    <div className="approval-evidence">{action.evidenceIds.map((id) => <a key={id} href={`#event-${id}`}>Evidence {id.slice(0, 8)} ↗</a>)}</div>
    <div className="approval-limits"><strong>Validation limits</strong>{limitations.length === 0 && differences.length === 0 ? <p>No limitations or environment differences recorded.</p> : <ul>{limitations.map((item) => <li key={`limit-${item}`}>{item}</li>)}{differences.map((item) => <li key={`difference-${item}`}>Environment difference: {item}</li>)}</ul>}</div>

    <div className="approval-meta"><span>Action {action.version} · digest <code>{action.digest}</code></span><span>{decisionEvent ? `Decision by ${decisionEvent.actor} · ${readableTime(decisionEvent.observedAt ?? decisionEvent.createdAt)}` : "No decision recorded"}</span>{expiresAt && <span>Approval expires {readableTime(expiresAt)} · {remaining(expiresAt, now)}</span>}{invalidationEvent && <span>{invalidationEvent.reason}</span>}</div>

    {state === "pending" && <div className="approval-controls">
      <div className="approval-deny-field"><label htmlFor="denial-reason">Denial reason <span>(optional)</span></label><textarea id="denial-reason" value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} placeholder="What should the team investigate next?" rows={2} disabled={processing} /></div>
      <div className="approval-buttons"><button type="button" className="approval-deny" disabled={!canDecide} onClick={() => submit("deny")}>DENY</button><button type="button" className="approval-apply" disabled={!canDecide} onClick={() => submit("approve")}>{processing ? "Checking…" : "APPLY"}</button></div>
      <p id="approval-help">APPLY records approval for this exact action. Production execution requires a fresh target check.</p>
    </div>}
    {message && <p className="approval-message" role="status" aria-live="polite">{message}</p>}

    {(state === "approved" || state === "remediating" || execution) && <div className="approval-controls approval-execution">
      <div className="approval-section-head"><h3>Production execution</h3><span>{execution ? readableTime(execution.observedAt) : "Approval recorded"}</span></div>
      {execution ? <>
        <p className={`approval-validation ${execution.status === "COMPLETED" ? "approval-validation-pass" : execution.status === "FAILED" ? "approval-validation-fail" : "approval-validation-wait"}`} role="status" aria-live="polite">
          {execution.status === "COMPLETED" ? `Completed · live value ${execution.resultingValue ?? "confirmed"}`
            : execution.status === "ACCEPTED" ? "Accepted · patch submitted; live result still needs confirmation"
              : execution.status === "UNKNOWN" ? "Unknown · the write outcome could not be confirmed"
                : "Failed · no successful production change was confirmed"}
        </p>
        <div className="approval-meta"><span>Request <code>{execution.requestId}</code></span>{execution.resourceVersion && <span>Resource version <code>{execution.resourceVersion}</code></span>}{execution.resultingValue && <span>Observed value <code>{execution.resultingValue}</code></span>}</div>
      </> : <p className="approval-validation approval-validation-wait">{state === "remediating" ? "Execution was claimed, but no receipt is recorded yet. Reconcile the exact approved action to inspect the live target." : "Approval is recorded for this exact action. Production will change only after you start execution."}</p>}
      {state === "approved" && !execution && <div className="approval-buttons"><button type="button" className="approval-apply" disabled={processing} onClick={() => runExecution(false)}>{processing ? "Checking target…" : "EXECUTE APPROVED CHANGE"}</button></div>}
      {state === "remediating" && (!execution || execution.status === "ACCEPTED" || execution.status === "UNKNOWN") && <div className="approval-buttons"><button type="button" className="approval-apply" disabled={processing} onClick={() => runExecution(true)}>{processing ? "Reconciling…" : "RECONCILE OUTCOME"}</button></div>}
      <p id="execution-help">Execution uses the approved action event and digest. An uncertain result must be reconciled against the live target before retrying.</p>
      {executionMessage && <p className="approval-message" role="status" aria-live="polite">{executionMessage}</p>}
    </div>}
  </section>;
}
