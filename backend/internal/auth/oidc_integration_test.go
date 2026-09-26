package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests run against a live OIDC provider (the local Keycloak realm). They
// are skipped unless KNULL_TEST_OIDC_ISSUER is set, so the offline unit suite is
// unaffected. CI and local `make check-auth` set the variable.
func oidcTestEnv(t *testing.T) (issuer, clientID, clientSecret string) {
	t.Helper()
	issuer = os.Getenv("KNULL_TEST_OIDC_ISSUER")
	if issuer == "" {
		t.Skip("KNULL_TEST_OIDC_ISSUER not set; skipping live OIDC test")
	}
	clientID = getEnvOr("KNULL_TEST_OIDC_CLIENT_ID", "knull-backend")
	clientSecret = getEnvOr("KNULL_TEST_OIDC_CLIENT_SECRET", "knull-local-secret")
	return issuer, clientID, clientSecret
}

func getEnvOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestAuthenticatorDiscoveryAndAuthCodeURL(t *testing.T) {
	issuer, clientID, clientSecret := oidcTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	a, err := NewAuthenticator(ctx, issuer, clientID, clientSecret, "http://localhost:8080/api/auth/callback")
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	got := a.AuthCodeURL("state123", "nonce123", "verifier-abc")
	for _, want := range []string{"client_id=" + clientID, "state=state123", "nonce=nonce123", "code_challenge=", "code_challenge_method=S256"} {
		if !strings.Contains(got, want) {
			t.Errorf("auth code URL missing %q: %s", want, got)
		}
	}
}

// TestExchangeVerifiesRealIDToken proves the verifier accepts a genuine token
// from the provider. It uses the direct-access (password) grant to obtain an ID
// token, then runs it through the same verification path the callback uses.
func TestExchangeVerifiesRealIDToken(t *testing.T) {
	issuer, clientID, clientSecret := oidcTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	a, err := NewAuthenticator(ctx, issuer, clientID, clientSecret, "http://localhost:8080/api/auth/callback")
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}

	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"username":      {getEnvOr("KNULL_TEST_OIDC_USERNAME", "operator")},
		"password":      {getEnvOr("KNULL_TEST_OIDC_PASSWORD", "operator")},
		"scope":         {"openid profile email"},
	}
	resp, err := http.PostForm(issuer+"/protocol/openid-connect/token", form)
	if err != nil {
		t.Fatalf("password grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password grant status = %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tok.IDToken == "" {
		t.Fatal("no id_token returned")
	}

	claims, err := a.verifyIDToken(ctx, tok.IDToken, "")
	if err != nil {
		t.Fatalf("verify id token: %v", err)
	}
	if claims.Subject == "" {
		t.Fatal("claims missing subject")
	}
	if claims.Email != "operator@knull.local" {
		t.Errorf("email = %q, want operator@knull.local", claims.Email)
	}
	if !strings.Contains(claims.Issuer, "/realms/knull") {
		t.Errorf("issuer = %q, want realm issuer", claims.Issuer)
	}
}
