"use client";

import { useEffect, useMemo, useState } from "react";
import { createActionPlan, listServices, validateMemoryAction, type Incident, type IncidentEvent, type Service } from "@/lib/api";

export default function LiveActionFlow({ incident, events, onRefresh }: { incident: Incident; events: IncidentEvent[]; onRefresh: () => Promise<void> }) {
  const [service, setService] = useState<Service | null>(null);
  const [resourceUid, setResourceUid] = useState("");
  const [resourceVersion, setResourceVersion] = useState("");
  const [container, setContainer] = useState("");
  const [imageDigest, setImageDigest] = useState("");
  const [replicas, setReplicas] = useState("2");
  const [cpu, setCpu] = useState("500m");
  const [evidenceIds, setEvidenceIds] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    listServices().then(({ services }) => setService(services.find((item) => item.key === incident.serviceKey && item.environment === incident.environment) ?? null)).catch(() => setService(null));
  }, [incident.serviceKey, incident.environment]);

  const evidence = useMemo(() => events.filter((event) => event.category === "observation" && event.data?.available !== false), [events]);
  const plan = useMemo(() => [...events].reverse().find((event) => event.source === "action-plan" && event.category === "action"), [events]);

  if (incident.state !== "PLANNING") return null;
  const setEvidence = (id: string, checked: boolean) => setEvidenceIds((old) => checked ? [...new Set([...old, id])] : old.filter((value) => value !== id));

  async function propose(event: React.FormEvent) {
    event.preventDefault();
    if (!service || !evidenceIds.length) return;
    setBusy(true); setMessage(null);
    const result = await createActionPlan(incident.id, {
      type: "ADJUST_MEMORY", environment: incident.environment,
      target: { cluster: service.k8sCluster, namespace: service.k8sNamespace, kind: "Deployment", name: service.k8sWorkload, container },
      field: "resources.limits.memory", currentValue: "256Mi", desiredValue: "1Gi",
      preconditions: { resourceUid, resourceVersion, currentValue: "256Mi" },
      expectedImpact: "Raise the memory limit to address the OOM observed in the selected live evidence.", risk: "MEDIUM", evidenceIds,
    });
    setMessage(result.error ?? "Action sealed from the live service mapping and selected evidence.");
    setBusy(false);
    await onRefresh();
  }

  async function validate() {
    if (!plan || !container || !imageDigest) return;
    setBusy(true); setMessage(null);
    const result = await validateMemoryAction(incident.id, plan.id, { imageDigest, container, replicas: Number(replicas), cpu, memory: "1Gi" });
    setMessage(result.error ?? (result.result?.passed ? "Live sandbox validation passed and cleanup completed." : result.result?.failure ?? "Sandbox validation finished without a pass."));
    setBusy(false);
    await onRefresh();
  }

  return <section className="incident-resolution" aria-labelledby="live-action-title">
    <h2 id="live-action-title">Live remediation workflow</h2>
    <p>Build a proposal from the real service mapping and evidence, then run it in the configured isolated EKS sandbox. This does not change production.</p>
    {!service ? <p className="error">Could not load the mapped service. Refresh before continuing.</p> : <>
      <p className="auth-muted">Target: {service.k8sCluster} / {service.k8sNamespace} / Deployment / {service.k8sWorkload} · {incident.environment}</p>
      {!plan ? <form className="svc-form" onSubmit={propose}>
        <div className="field"><label htmlFor="live-container">Container name from the live Deployment</label><input id="live-container" value={container} onChange={(event) => setContainer(event.target.value)} required /></div>
        <div className="field"><label htmlFor="live-resource-uid">Live resource UID</label><input id="live-resource-uid" value={resourceUid} onChange={(event) => setResourceUid(event.target.value)} required /></div>
        <div className="field"><label htmlFor="live-resource-version">Live resource version</label><input id="live-resource-version" value={resourceVersion} onChange={(event) => setResourceVersion(event.target.value)} required /></div>
        <p className="auth-muted">Confirm the target currently has a 256Mi memory limit. Copy UID and resource version from the live Kubernetes evidence or an independent read-only query.</p>
        <fieldset className="cred-table"><legend>Evidence used for this proposal</legend>
          {evidence.length === 0 ? <p className="auth-muted">No available observation events yet. Collect live evidence first.</p> : evidence.map((item) => <label key={item.id} className="field"><input type="checkbox" checked={evidenceIds.includes(item.id)} onChange={(event) => setEvidence(item.id, event.target.checked)} /> {item.source}: {item.reason}</label>)}
        </fieldset>
        <button className="btn" type="submit" disabled={busy || !service || evidenceIds.length === 0}>{busy ? "Sealing…" : "Create 256Mi → 1Gi proposal"}</button>
      </form> : <>
        <p className="auth-muted">Proposal {plan.id.slice(0, 8)} is bound to the selected evidence and exact mapped Deployment.</p>
        <div className="field"><label htmlFor="sandbox-image-digest">Pinned checkout fixture image digest for sandbox</label><input id="sandbox-image-digest" value={imageDigest} onChange={(event) => setImageDigest(event.target.value)} placeholder="registry/repository@sha256:…" /></div>
        <div className="field"><label htmlFor="sandbox-replicas">Candidate replicas</label><input id="sandbox-replicas" type="number" min="1" max="5" value={replicas} onChange={(event) => setReplicas(event.target.value)} /></div>
        <div className="field"><label htmlFor="sandbox-cpu">Candidate CPU limit</label><input id="sandbox-cpu" value={cpu} onChange={(event) => setCpu(event.target.value)} /></div>
        <p className="auth-muted">Knull runs the 256Mi baseline and 1Gi candidate as isolated, cleaned-up EKS workloads. A successful run is required before the existing approval controls unlock.</p>
        <button className="btn" type="button" disabled={busy || !imageDigest || !container} onClick={validate}>{busy ? "Running in EKS…" : "Run live sandbox validation"}</button>
      </>}
    </>}
    {message && <p role="status">{message}</p>}
  </section>;
}
