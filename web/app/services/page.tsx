"use client";

import { useCallback, useEffect, useState } from "react";
import {
  createService,
  listServices,
  loginURL,
  setServiceEnabled,
  updateService,
  type Service,
  type ServiceInput,
} from "@/lib/api";

const EMPTY: FormState = {
  key: "",
  displayName: "",
  environment: "",
  k8sCluster: "",
  k8sNamespace: "",
  k8sWorkload: "",
  prometheusLabels: "",
  githubRepo: "",
  githubRef: "",
  recoveryPolicyRef: "",
};

interface FormState {
  key: string;
  displayName: string;
  environment: string;
  k8sCluster: string;
  k8sNamespace: string;
  k8sWorkload: string;
  prometheusLabels: string; // "k=v, k=v"
  githubRepo: string;
  githubRef: string;
  recoveryPolicyRef: string;
}

function labelsToText(labels: Record<string, string> | undefined): string {
  if (!labels) return "";
  return Object.entries(labels)
    .map(([k, v]) => `${k}=${v}`)
    .join(", ");
}

function textToLabels(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const pair of text.split(",")) {
    const t = pair.trim();
    if (!t) continue;
    const idx = t.indexOf("=");
    if (idx <= 0) continue;
    out[t.slice(0, idx).trim()] = t.slice(idx + 1).trim();
  }
  return out;
}

function toInput(f: FormState): ServiceInput {
  return {
    key: f.key,
    displayName: f.displayName,
    environment: f.environment,
    k8sCluster: f.k8sCluster,
    k8sNamespace: f.k8sNamespace,
    k8sWorkload: f.k8sWorkload,
    prometheusLabels: textToLabels(f.prometheusLabels),
    githubRepo: f.githubRepo,
    githubRef: f.githubRef,
    recoveryPolicyRef: f.recoveryPolicyRef,
  };
}

export default function ServicesPage() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [services, setServices] = useState<Service[]>([]);
  const [form, setForm] = useState<FormState>(EMPTY);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    const res = await listServices();
    setAuthenticated(res.authenticated);
    setServices(res.services);
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  function set<K extends keyof FormState>(k: K, v: string) {
    setForm((f) => ({ ...f, [k]: v }));
  }

  function startEdit(svc: Service) {
    setEditingId(svc.id);
    setFields({});
    setError(null);
    setForm({
      key: svc.key,
      displayName: svc.displayName,
      environment: svc.environment,
      k8sCluster: svc.k8sCluster,
      k8sNamespace: svc.k8sNamespace,
      k8sWorkload: svc.k8sWorkload,
      prometheusLabels: labelsToText(svc.prometheusLabels),
      githubRepo: svc.githubRepo ?? "",
      githubRef: svc.githubRef ?? "",
      recoveryPolicyRef: svc.recoveryPolicyRef ?? "",
    });
  }

  function resetForm() {
    setEditingId(null);
    setForm(EMPTY);
    setFields({});
    setError(null);
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError(null);
    setFields({});
    const input = toInput(form);
    const res = editingId
      ? await updateService(editingId, input)
      : await createService(input);
    setSaving(false);
    if (!res.ok) {
      setError(res.error ?? "save failed");
      setFields(res.fields ?? {});
      return;
    }
    resetForm();
    await load();
  }

  async function toggle(svc: Service) {
    await setServiceEnabled(svc.id, !svc.enabled);
    await load();
  }

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
          <h1>Services</h1>
          <p className="subtitle">Sign in to configure services.</p>
          <a className="btn" href={loginURL()}>
            Sign in
          </a>
        </section>
      </main>
    );
  }

  const fieldInputs: Array<{ k: keyof FormState; label: string; ph?: string }> =
    [
      { k: "key", label: "Key", ph: "checkout-api" },
      { k: "displayName", label: "Display name", ph: "Checkout API" },
      { k: "environment", label: "Environment", ph: "production" },
      { k: "k8sCluster", label: "K8s cluster", ph: "prod-eks" },
      { k: "k8sNamespace", label: "K8s namespace", ph: "shop" },
      { k: "k8sWorkload", label: "K8s workload", ph: "checkout-api" },
      { k: "prometheusLabels", label: "Prometheus labels", ph: "app=checkout, environment=production" },
      { k: "githubRepo", label: "GitHub repo", ph: "acme/checkout" },
      { k: "githubRef", label: "GitHub ref", ph: "main" },
      { k: "recoveryPolicyRef", label: "Recovery policy ref", ph: "optional" },
    ];

  return (
    <main className="page page-wide">
      <section className="card card-wide">
        <h1>Services</h1>
        <p className="subtitle">
          Each service maps to one Kubernetes workload, Prometheus selector, and
          GitHub repo in one explicit environment.
        </p>

        <form className="svc-form" onSubmit={onSubmit}>
          {fieldInputs.map(({ k, label, ph }) => (
            <div className="field" key={k}>
              <label htmlFor={k}>{label}</label>
              <input
                id={k}
                value={form[k]}
                placeholder={ph}
                onChange={(e) => set(k, e.target.value)}
              />
              {fields[k] && <span className="field-error">{fields[k]}</span>}
            </div>
          ))}
          <div className="svc-form-actions">
            <button className="btn" type="submit" disabled={saving}>
              {saving ? "Saving…" : editingId ? "Update service" : "Add service"}
            </button>
            {editingId && (
              <button
                type="button"
                className="btn btn-ghost"
                onClick={resetForm}
              >
                Cancel
              </button>
            )}
          </div>
        </form>
        {error && <p className="error">{error}</p>}

        <table className="cred-table">
          <thead>
            <tr>
              <th>Service</th>
              <th>Environment</th>
              <th>Workload</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {services.length === 0 ? (
              <tr>
                <td colSpan={5} className="auth-muted">
                  No services configured yet.
                </td>
              </tr>
            ) : (
              services.map((svc) => (
                <tr key={svc.id}>
                  <td>
                    <strong>{svc.displayName}</strong>
                    <br />
                    <code>{svc.key}</code>
                  </td>
                  <td>{svc.environment}</td>
                  <td className="auth-muted">
                    {svc.k8sCluster}/{svc.k8sNamespace}/{svc.k8sWorkload}
                  </td>
                  <td>
                    <span
                      className={`scope ${svc.enabled ? "scope-read" : "scope-production"}`}
                    >
                      {svc.enabled ? "enabled" : "disabled"}
                    </span>
                  </td>
                  <td className="row-actions">
                    <button
                      className="btn btn-ghost"
                      onClick={() => startEdit(svc)}
                    >
                      Edit
                    </button>
                    <button className="btn btn-ghost" onClick={() => toggle(svc)}>
                      {svc.enabled ? "Disable" : "Enable"}
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </section>
    </main>
  );
}
