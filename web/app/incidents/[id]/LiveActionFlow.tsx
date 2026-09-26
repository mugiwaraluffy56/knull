"use client";

import { useEffect, useMemo, useState } from "react";
import { createActionPlan, listServices, validateMemoryAction, type Incident, type IncidentEvent, type Service } from "@/lib/api";

type LiveDeployment = { uid: string; resourceVersion: string; container: string; memory?: string };

function deploymentFromEvidence(value: unknown, workload: string, depth = 0): LiveDeployment | null {
  if (depth > 8 || value === null || typeof value !== "object") return null;
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = deploymentFromEvidence(item, workload, depth + 1);
      if (found) return found;
    }
    return null;
  }
  const record = value as Record<string, unknown>;
  const metadata = record.metadata as Record<string, unknown> | undefined;
  const spec = record.spec as Record<string, unknown> | undefined;
  const template = spec?.template as Record<string, unknown> | undefined;
  const podSpec = template?.spec as Record<string, unknown> | undefined;
  const containers = Array.isArray(podSpec?.containers) ? podSpec.containers as Array<Record<string, unknown>> : [];
  if (metadata?.name === workload && typeof metadata.uid === "string" && typeof metadata.resourceVersion === "string" && containers.length > 0) {
    const chosen = containers.find((item) => {
      const resources = item.resources as Record<string, unknown> | undefined;
      const limits = resources?.limits as Record<string, unknown> | undefined;
      return limits?.memory === "256Mi";
    }) ?? containers[0];
    const resources = chosen.resources as Record<string, unknown> | undefined;
    const limits = resources?.limits as Record<string, unknown> | undefined;
    return { uid: metadata.uid, resourceVersion: metadata.resourceVersion, container: typeof chosen.name === "string" ? chosen.name : "", memory: typeof limits?.memory === "string" ? limits.memory : undefined };
  }
  for (const child of Object.values(record)) {
    const found = deploymentFromEvidence(child, workload, depth + 1);
    if (found) return found;
  }
  return null;
}

export default function LiveActionFlow({ incident, events, onRefresh }: { incident: Incident; events: IncidentEvent[]; onRefresh: () => Promise<void> }) {
  const [service, setService] = useState<Service | null>(null);
  const [resourceUid, setResourceUid] = useState("");
  const [resourceVersion, setResourceVersion] = useState("");
  const [currentMemory, setCurrentMemory] = useState("");
  const [risk, setRisk] = useState<"LOW" | "MEDIUM" | "HIGH">("MEDIUM");
  const [expectedImpact, setExpectedImpact] = useState("Raise the memory limit to address the OOM observed in the selected live evidence.");
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
  const classification = useMemo(() => [...events].reverse().find((event) => event.source === "jev" && event.category === "hypothesis"), [events]);
  const nextAction = useMemo(() => [...events].reverse().find((event) => event.source === "jev-next-action" && event.category === "decision"), [events]);

  useEffect(() => {
    if (!service) return;
    const deploymentEvent = [...events].reverse().find((event) => event.source === "kubernetes" && event.data?.tool === "get_deployment");
    const live = deploymentEvent && deploymentFromEvidence(deploymentEvent.data, service.k8sWorkload);
    if (!live) return;
    setResourceUid(live.uid);
    setResourceVersion(live.resourceVersion);
    if (live.container) setContainer(live.container);
    if (live.memory) setCurrentMemory(live.memory);
  }, [events, service]);

  if (incident.state !== "PLANNING") return null;
  const decision = nextAction?.data?.decision as { action?: string; rationale?: string } | undefined;
  const classified = classification?.data?.decision as { classes?: Array<{ class?: string }> } | undefined;
  const leadingClass = classified?.classes?.[0]?.class;
  if (decision?.action !== "TEST_REMEDIATION" && decision?.action !== "REMEDIATE") {
    return <section className="incident-resolution"><h2>Live remediation workflow</h2><p>Waiting for Jev to recommend a tested remediation from the collected evidence. Current incident state alone does not authorize a proposal.</p></section>;
  }
  if (leadingClass !== "RESOURCE_EXHAUSTION") {
    return <section className="incident-resolution"><h2>Live remediation workflow</h2><p>Jev recommended remediation, but this demo flow only supports the resource-exhaustion checkout scenario. No memory action is offered for this incident.</p></section>;
  }
  const setEvidence = (id: string, checked: boolean) => setEvidenceIds((old) => checked ? [...new Set([...old, id])] : old.filter((value) => value !== id));

  async function propose(event: React.FormEvent) {
    event.preventDefault();
    if (!service || !evidenceIds.length) return;
    if (currentMemory !== "256Mi") {
      setMessage("This tested checkout path only supports a live Deployment observed at 256Mi. Knull did not create a proposal.");
      return;
    }
    setBusy(true); setMessage(null);
    const result = await createActionPlan(incident.id, {
      type: "ADJUST_MEMORY", environment: incident.environment,
      target: { cluster: service.k8sCluster, namespace: service.k8sNamespace, kind: "Deployment", name: service.k8sWorkload, container },
      field: "resources.limits.memory", currentValue: currentMemory, desiredValue: "1Gi",
      preconditions: { resourceUid, resourceVersion, currentValue: currentMemory },
      expectedImpact, risk, evidenceIds,
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
    <p>Jev recommends a tested remediation: {decision.rationale || "resource-exhaustion scenario"} Build a proposal from the mapped service and selected live evidence, then run it in the configured isolated EKS sandbox. This does not change production.</p>
    {!service ? <p className="error">Could not load the mapped service. Refresh before continuing.</p> : <>
      <p className="auth-muted">Target: {service.k8sCluster} / {service.k8sNamespace} / Deployment / {service.k8sWorkload} · {incident.environment}</p>
      {!plan ? <form className="svc-form" onSubmit={propose}>
        <div className="field"><label htmlFor="live-container">Container name from the live Deployment</label><input id="live-container" value={container} onChange={(event) => setContainer(event.target.value)} required /></div>
        <div className="field"><label htmlFor="live-memory">Observed memory limit</label><input id="live-memory" value={currentMemory} onChange={(event) => setCurrentMemory(event.target.value)} placeholder="Read from live Deployment evidence" required /></div>
        <div className="field"><label htmlFor="live-resource-uid">Live resource UID</label><input id="live-resource-uid" value={resourceUid} onChange={(event) => setResourceUid(event.target.value)} required /></div>
        <div className="field"><label htmlFor="live-resource-version">Live resource version</label><input id="live-resource-version" value={resourceVersion} onChange={(event) => setResourceVersion(event.target.value)} required /></div>
        <div className="field"><label htmlFor="action-risk">Operator-assessed risk</label><select id="action-risk" value={risk} onChange={(event) => setRisk(event.target.value as "LOW" | "MEDIUM" | "HIGH")}><option value="LOW">Low</option><option value="MEDIUM">Medium</option><option value="HIGH">High</option></select></div>
        <div className="field"><label htmlFor="action-impact">Expected impact</label><input id="action-impact" value={expectedImpact} maxLength={500} onChange={(event) => setExpectedImpact(event.target.value)} required /></div>
        <p className="auth-muted">The demo path accepts only a live 256Mi limit and proposes 1Gi. UID, resource version, container, and limit are filled from the Kubernetes get_deployment result when available; verify them against the timeline before continuing.</p>
        <fieldset className="cred-table"><legend>Evidence used for this proposal</legend>
          {evidence.length === 0 ? <p className="auth-muted">No available observation events yet. Collect live evidence first.</p> : evidence.map((item) => <label key={item.id} className="field"><input type="checkbox" checked={evidenceIds.includes(item.id)} onChange={(event) => setEvidence(item.id, event.target.checked)} /> {item.source}: {item.reason}</label>)}
        </fieldset>
        <button className="btn" type="submit" disabled={busy || !service || evidenceIds.length === 0 || currentMemory !== "256Mi" || !resourceUid || !resourceVersion || !container}>{busy ? "Sealing…" : "Create 256Mi → 1Gi proposal"}</button>
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
