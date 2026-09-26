"use client";

import { useEffect, useState } from "react";
import {
  fetchRecoveryPolicy,
  putRecoveryPolicy,
  type RecoveryPolicy,
  type RecoveryPolicySnapshot,
  type Service,
} from "@/lib/api";

type Form = {
  windowSeconds: string;
  missingData: "UNCERTAIN" | "NOT_RECOVERED";
  minReadyFraction: string;
  useErrorRate: boolean;
  maxErrorRate: string;
  errorSamples: string;
  useLatency: boolean;
  maxP95: string;
  latencySamples: string;
};

const empty: Form = {
  windowSeconds: "",
  missingData: "UNCERTAIN",
  minReadyFraction: "",
  useErrorRate: false,
  maxErrorRate: "",
  errorSamples: "10",
  useLatency: false,
  maxP95: "",
  latencySamples: "10",
};

function fromPolicy(policy: RecoveryPolicy): Form {
  return {
    windowSeconds: String(policy.windowSeconds),
    missingData: policy.missingData,
    minReadyFraction: String(policy.workload.minReadyFraction),
    useErrorRate: !!policy.errorRate,
    maxErrorRate: policy.errorRate ? String(policy.errorRate.maxRatio) : "",
    errorSamples: policy.errorRate ? String(policy.errorRate.minSamples) : "10",
    useLatency: !!policy.latencyP95,
    maxP95: policy.latencyP95 ? String(policy.latencyP95.maxP95Milliseconds) : "",
    latencySamples: policy.latencyP95 ? String(policy.latencyP95.minSamples) : "10",
  };
}

function toPolicy(form: Form): RecoveryPolicy {
  return {
    windowSeconds: Number(form.windowSeconds),
    missingData: form.missingData,
    workload: { minReadyFraction: Number(form.minReadyFraction) },
    ...(form.useErrorRate ? { errorRate: { maxRatio: Number(form.maxErrorRate), minSamples: Number(form.errorSamples) } } : {}),
    ...(form.useLatency ? { latencyP95: { maxP95Milliseconds: Number(form.maxP95), minSamples: Number(form.latencySamples) } } : {}),
  };
}

export default function RecoveryPolicyPanel({ service, onClose }: { service: Service; onClose: () => void }) {
  const [current, setCurrent] = useState<RecoveryPolicySnapshot | null>(null);
  const [form, setForm] = useState<Form>(empty);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);
    setMessage(null);
    fetchRecoveryPolicy(service.id).then((snapshot) => {
      if (!active) return;
      setCurrent(snapshot);
      setForm(snapshot ? fromPolicy(snapshot.policy) : empty);
      setLoading(false);
    }).catch((err: unknown) => {
      if (!active) return;
      setError(err instanceof Error ? err.message : "could not load recovery policy");
      setLoading(false);
    });
    return () => { active = false; };
  }, [service.id]);

  function set<K extends keyof Form>(key: K, value: Form[K]) {
    setForm((previous) => ({ ...previous, [key]: value }));
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    setMessage(null);
    if (!form.useErrorRate && !form.useLatency) {
      setError("Select at least one service indicator in addition to workload readiness.");
      return;
    }
    if (!form.windowSeconds || !form.minReadyFraction || (form.useErrorRate && !form.maxErrorRate) || (form.useLatency && !form.maxP95)) {
      setError("Enter the window and every selected threshold before saving.");
      return;
    }
    setSaving(true);
    const result = await putRecoveryPolicy(service.id, toPolicy(form));
    setSaving(false);
    if (result.error || !result.policy) {
      setError(result.error ?? "could not save recovery policy");
      return;
    }
    setCurrent(result.policy);
    setForm(fromPolicy(result.policy.policy));
    setMessage(`Policy version ${result.policy.version} saved. Future recovery assessments will use this version until it changes.`);
  }

  return <section className="recovery-panel" aria-labelledby="recovery-policy-heading">
    <div className="recovery-head"><div><span className="recovery-eyebrow">Service recovery criteria</span><h2 id="recovery-policy-heading">{service.displayName} · {service.environment}</h2></div><button type="button" className="btn btn-ghost" onClick={onClose}>Close</button></div>
    {loading ? <p className="auth-muted">Loading policy…</p> : <>
      {current ? <div className="recovery-current"><strong>Current policy · version {current.version}</strong><span>Observation window {current.policy.windowSeconds} seconds · missing data → {current.policy.missingData.replaceAll("_", " ").toLowerCase()}</span><span>Configured {new Date(current.configuredAt).toLocaleString()} · digest {current.digest.slice(0, 12)}</span></div> : <p className="auth-muted">No policy configured. Recovery cannot be confirmed automatically until you save one.</p>}
      <p className="recovery-explainer">Set explicit thresholds for this service. Recovery requires workload readiness and every selected service indicator across the full window. Missing readings never count as recovered.</p>
      <form className="recovery-form" onSubmit={save}>
        <div className="recovery-form-grid">
          <label>Observation window (seconds)<input type="number" min={60} max={3600} step={1} value={form.windowSeconds} onChange={(event) => set("windowSeconds", event.target.value)} placeholder="60–3600" required /></label>
          <label>Minimum ready fraction<input type="number" min={0.01} max={1} step="any" value={form.minReadyFraction} onChange={(event) => set("minReadyFraction", event.target.value)} placeholder="e.g. 1 for all replicas" required /></label>
          <label>When data is missing<select value={form.missingData} onChange={(event) => set("missingData", event.target.value as Form["missingData"])}><option value="UNCERTAIN">Report uncertain</option><option value="NOT_RECOVERED">Report not recovered</option></select></label>
        </div>
        <div className="recovery-signals"><div className="recovery-signal"><label className="recovery-check"><input type="checkbox" checked={form.useErrorRate} onChange={(event) => set("useErrorRate", event.target.checked)} /> Require error rate</label>{form.useErrorRate && <div className="recovery-signal-fields"><label>Maximum error ratio<input type="number" min={0} max={0.999999} step="any" value={form.maxErrorRate} onChange={(event) => set("maxErrorRate", event.target.value)} placeholder="e.g. 0.01 for 1%" required /></label><label>Minimum samples<input type="number" min={10} step={1} value={form.errorSamples} onChange={(event) => set("errorSamples", event.target.value)} required /></label></div>}</div><div className="recovery-signal"><label className="recovery-check"><input type="checkbox" checked={form.useLatency} onChange={(event) => set("useLatency", event.target.checked)} /> Require p95 latency</label>{form.useLatency && <div className="recovery-signal-fields"><label>Maximum p95 (ms)<input type="number" min={0.001} step="any" value={form.maxP95} onChange={(event) => set("maxP95", event.target.value)} placeholder="e.g. 500" required /></label><label>Minimum samples<input type="number" min={10} step={1} value={form.latencySamples} onChange={(event) => set("latencySamples", event.target.value)} required /></label></div>}</div></div>
        <div className="recovery-form-actions"><button type="submit" className="btn" disabled={saving}>{saving ? "Saving…" : current ? "Save new policy version" : "Create recovery policy"}</button></div>
      </form>
      {error && <p className="error" role="alert">{error}</p>}
      {message && <p className="auth-muted" role="status">{message}</p>}
    </>}
  </section>;
}
