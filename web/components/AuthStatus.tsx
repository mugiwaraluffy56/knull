"use client";

import { useEffect, useState } from "react";
import { API_BASE_URL, fetchMe, loginURL, type Operator } from "@/lib/api";

// AuthStatus shows the signed-in operator with a sign-out control, or a sign-in
// link. The session cookie is set on the API origin, so this must run in the
// browser (credentials: include) rather than server-side.
export function AuthStatus() {
  const [operator, setOperator] = useState<Operator | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchMe()
      .then(setOperator)
      .finally(() => setLoading(false));
  }, []);

  async function signOut() {
    await fetch(`${API_BASE_URL}/api/auth/logout`, {
      method: "POST",
      credentials: "include",
    });
    setOperator(null);
  }

  if (loading) return <span className="auth-muted">…</span>;

  if (!operator) {
    return (
      <a className="btn" href={loginURL()}>
        Sign in
      </a>
    );
  }

  return (
    <span className="auth-signed">
      <span className="auth-name">{operator.email}</span>
      <button className="btn btn-ghost" onClick={signOut}>
        Sign out
      </button>
    </span>
  );
}
