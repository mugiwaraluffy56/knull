// Package auth implements operator sign-in via OpenID Connect and manages
// server-side sessions. Session state lives in Redis; the browser holds only an
// opaque, httpOnly cookie, so no operator identity or token is exposed to
// client-side JavaScript.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// SessionCookie is the name of the opaque session cookie.
	SessionCookie = "knull_session"

	sessionPrefix  = "sess:"
	authFlowPrefix = "authflow:"
	authFlowTTL    = 10 * time.Minute
)

// ErrNoSession indicates the request carried no valid session.
var ErrNoSession = errors.New("no valid session")

// SessionManager creates, reads, and revokes operator sessions in Redis.
type SessionManager struct {
	redis        *redis.Client
	ttl          time.Duration
	cookieSecure bool
}

// NewSessionManager builds a SessionManager.
func NewSessionManager(rdb *redis.Client, ttl time.Duration, cookieSecure bool) *SessionManager {
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &SessionManager{redis: rdb, ttl: ttl, cookieSecure: cookieSecure}
}

// Create starts a session for an operator and returns its opaque id.
func (m *SessionManager) Create(ctx context.Context, operatorID uuid.UUID) (string, error) {
	id, err := randomToken(32)
	if err != nil {
		return "", err
	}
	if err := m.redis.Set(ctx, sessionPrefix+id, operatorID.String(), m.ttl).Err(); err != nil {
		return "", fmt.Errorf("persist session: %w", err)
	}
	return id, nil
}

// Resolve returns the operator id bound to a session id, or ErrNoSession.
func (m *SessionManager) Resolve(ctx context.Context, sessionID string) (uuid.UUID, error) {
	if sessionID == "" {
		return uuid.Nil, ErrNoSession
	}
	val, err := m.redis.Get(ctx, sessionPrefix+sessionID).Result()
	if errors.Is(err, redis.Nil) {
		return uuid.Nil, ErrNoSession
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read session: %w", err)
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil, ErrNoSession
	}
	return id, nil
}

// Destroy revokes a session.
func (m *SessionManager) Destroy(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	if err := m.redis.Del(ctx, sessionPrefix+sessionID).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SetCookie writes the session cookie on the response.
func (m *SessionManager) SetCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
}

// ClearCookie expires the session cookie on the response.
func (m *SessionManager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// flowState is the short-lived data tying an OIDC redirect to its callback.
type flowState struct {
	Nonce        string `json:"nonce"`
	PKCEVerifier string `json:"pkce"`
}

// SaveFlow stores per-login state (nonce, PKCE verifier) keyed by the opaque
// state parameter, with a short TTL. It defends the callback against CSRF and
// replay.
func (m *SessionManager) SaveFlow(ctx context.Context, state, nonce, pkceVerifier string) error {
	payload, err := json.Marshal(flowState{Nonce: nonce, PKCEVerifier: pkceVerifier})
	if err != nil {
		return fmt.Errorf("marshal flow state: %w", err)
	}
	if err := m.redis.Set(ctx, authFlowPrefix+state, payload, authFlowTTL).Err(); err != nil {
		return fmt.Errorf("persist flow state: %w", err)
	}
	return nil
}

// TakeFlow atomically reads and deletes the flow state for a state parameter so
// each login callback can be used exactly once.
func (m *SessionManager) TakeFlow(ctx context.Context, state string) (nonce, pkceVerifier string, err error) {
	if state == "" {
		return "", "", errors.New("missing state")
	}
	val, err := m.redis.GetDel(ctx, authFlowPrefix+state).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", errors.New("unknown or expired login state")
	}
	if err != nil {
		return "", "", fmt.Errorf("read flow state: %w", err)
	}
	var fs flowState
	if err := json.Unmarshal([]byte(val), &fs); err != nil {
		return "", "", fmt.Errorf("decode flow state: %w", err)
	}
	return fs.Nonce, fs.PKCEVerifier, nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
