package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Claims are the operator identity fields taken from a verified ID token.
type Claims struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
}

// Authenticator wraps the OIDC provider and OAuth2 client for the auth-code
// flow with PKCE.
type Authenticator struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	issuer   string
}

// NewAuthenticator performs OIDC discovery against the issuer and builds an
// Authenticator. It fails if the issuer is unreachable, so a misconfigured
// deployment surfaces at startup rather than at first login.
func NewAuthenticator(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*Authenticator, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	return &Authenticator{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		oauth: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
		issuer: issuer,
	}, nil
}

// AuthCodeURL builds the provider authorization URL for a login, binding it to
// the given state, nonce, and PKCE challenge.
func (a *Authenticator) AuthCodeURL(state, nonce, pkceVerifier string) string {
	challenge := pkceChallenge(pkceVerifier)
	return a.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// Exchange trades an authorization code for tokens, then verifies the returned
// ID token against the expected nonce and returns the operator claims.
func (a *Authenticator) Exchange(ctx context.Context, code, nonce, pkceVerifier string) (Claims, error) {
	token, err := a.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", pkceVerifier),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("token exchange: %w", err)
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Claims{}, fmt.Errorf("no id_token in token response")
	}
	return a.verifyIDToken(ctx, rawID, nonce)
}

// verifyIDToken checks the ID token signature, audience, expiry, and nonce.
func (a *Authenticator) verifyIDToken(ctx context.Context, rawID, expectedNonce string) (Claims, error) {
	idToken, err := a.verifier.Verify(ctx, rawID)
	if err != nil {
		return Claims{}, fmt.Errorf("verify id token: %w", err)
	}
	if expectedNonce != "" && idToken.Nonce != expectedNonce {
		return Claims{}, fmt.Errorf("id token nonce mismatch")
	}
	var raw struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&raw); err != nil {
		return Claims{}, fmt.Errorf("decode id token claims: %w", err)
	}
	return Claims{
		Issuer:  idToken.Issuer,
		Subject: idToken.Subject,
		Email:   raw.Email,
		Name:    raw.Name,
	}, nil
}

// NewState returns a random opaque state/nonce value.
func NewState() (string, error) {
	return randomToken(24)
}

// NewPKCEVerifier returns a fresh PKCE code verifier.
func NewPKCEVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
