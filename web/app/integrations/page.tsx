"use client";

import { useCallback, useEffect, useState } from "react";
import {
  CREDENTIAL_KINDS,
  CREDENTIAL_SCOPES,
  listCredentials,
  loginURL,
  putCredential,
  type CredentialKind,
  type CredentialMetadata,
  type CredentialScope,
} from "@/lib/api";

// Integrations configuration. Operators store integration credentials here.
// Values are write-only: the list shows only metadata and a non-reversible
// fingerprint, never the secret.
export default function IntegrationsPage() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [creds, setCreds] = useState<CredentialMetadata[]>([]);
  const [kind, setKind] = useState<CredentialKind>("kubernetes");
  const [scope, setScope] = useState<CredentialScope>("read");
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    const res = await listCredentials();
    setAuthenticated(res.authenticated);
    setCreds(res.credentials);
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError(null);
    const err = await putCredential({ kind, scope, value });
    setSaving(false);
    if (err) {
      setError(err);
      return;
    }
    setValue("");
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
          <h1>Integrations</h1>
          <p className="subtitle">Sign in to manage integration credentials.</p>
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
        <h1>Integrations</h1>
        <p className="subtitle">
          Credentials are encrypted at rest. Values are write-only and never
          shown again.
        </p>

        <form className="cred-form" onSubmit={onSubmit}>
          <div className="field">
            <label htmlFor="kind">Integration</label>
            <select
              id="kind"
              value={kind}
              onChange={(e) => setKind(e.target.value as CredentialKind)}
            >
              {CREDENTIAL_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="scope">Scope</label>
            <select
              id="scope"
              value={scope}
              onChange={(e) => setScope(e.target.value as CredentialScope)}
            >
              {CREDENTIAL_SCOPES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
          <div className="field field-grow">
            <label htmlFor="value">Value</label>
            <input
              id="value"
              type="password"
              value={value}
              placeholder="secret value"
              onChange={(e) => setValue(e.target.value)}
              required
            />
          </div>
          <button className="btn" type="submit" disabled={saving || !value}>
            {saving ? "Saving…" : "Save"}
          </button>
        </form>
        {error && <p className="error">{error}</p>}

        <table className="cred-table">
          <thead>
            <tr>
              <th>Integration</th>
              <th>Scope</th>
              <th>Fingerprint</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {creds.length === 0 ? (
              <tr>
                <td colSpan={4} className="auth-muted">
                  No credentials stored yet.
                </td>
              </tr>
            ) : (
              creds.map((c) => (
                <tr key={c.id}>
                  <td>{c.kind}</td>
                  <td>
                    <span className={`scope scope-${c.scope}`}>{c.scope}</span>
                  </td>
                  <td>
                    <code>{c.fingerprint}</code>
                  </td>
                  <td className="auth-muted">
                    {new Date(c.updatedAt).toLocaleString()}
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
