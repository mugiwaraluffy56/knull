package httpapi

import (
	"net/http"

	"github.com/mugiwaraluffy56/knull/backend/internal/auth"
)

// handleLogin begins the OIDC auth-code flow: it generates state, nonce, and a
// PKCE verifier, stores them server-side, and redirects the browser to the
// identity provider.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.authn == nil || s.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "authentication is not configured")
		return
	}

	state, err := auth.NewState()
	if err != nil {
		s.internalError(w, "generate state", err)
		return
	}
	nonce, err := auth.NewState()
	if err != nil {
		s.internalError(w, "generate nonce", err)
		return
	}
	verifier, err := auth.NewPKCEVerifier()
	if err != nil {
		s.internalError(w, "generate pkce verifier", err)
		return
	}
	if err := s.sessions.SaveFlow(r.Context(), state, nonce, verifier); err != nil {
		s.internalError(w, "save login flow", err)
		return
	}

	http.Redirect(w, r, s.authn.AuthCodeURL(state, nonce, verifier), http.StatusFound)
}

// handleCallback completes the OIDC flow: it validates the login state, exchanges
// the code for tokens, verifies the ID token, upserts the operator, starts a
// session, and redirects back to the UI.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if s.authn == nil || s.sessions == nil || s.operators == nil {
		writeError(w, http.StatusServiceUnavailable, "authentication is not configured")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		writeError(w, http.StatusUnauthorized, "identity provider returned an error: "+errParam)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "missing state or code")
		return
	}

	nonce, verifier, err := s.sessions.TakeFlow(r.Context(), state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid login state")
		return
	}

	claims, err := s.authn.Exchange(r.Context(), code, nonce, verifier)
	if err != nil {
		s.logger.Error("oidc exchange", "error", err)
		writeError(w, http.StatusUnauthorized, "login failed")
		return
	}

	op, err := s.operators.Upsert(r.Context(), claims.Issuer, claims.Subject, claims.Email, claims.Name)
	if err != nil {
		s.internalError(w, "persist operator", err)
		return
	}

	sessionID, err := s.sessions.Create(r.Context(), op.ID)
	if err != nil {
		s.internalError(w, "create session", err)
		return
	}
	s.sessions.SetCookie(w, sessionID)

	target := s.uiBaseURL
	if target == "" {
		target = "/"
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// handleLogout revokes the session and clears the cookie. It succeeds even
// without a valid session so logout is always safe to call.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if cookie, err := r.Cookie(auth.SessionCookie); err == nil {
		if err := s.sessions.Destroy(r.Context(), cookie.Value); err != nil {
			s.logger.Error("destroy session", "error", err)
		}
	}
	s.sessions.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the signed-in operator's identity.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	op, ok := operatorFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, op)
}
