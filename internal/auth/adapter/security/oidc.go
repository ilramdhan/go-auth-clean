package security

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"go-auth-clean/internal/auth/domain"
)

const googleIssuer = "https://accounts.google.com"

// idTokenVerifier memisahkan go-oidc agar bisa diganti di test.
type idTokenVerifier interface {
	Verify(ctx context.Context, raw string) (*oidc.IDToken, error)
}

// OIDCProvider mengimplementasikan app.OAuthProvider untuk provider OpenID Connect
// (Google): authorization code + PKCE S256 + nonce, id_token diverifikasi go-oidc.
type OIDCProvider struct {
	name     string
	conf     oauth2.Config
	verifier idTokenVerifier
	client   *http.Client
}

// NewGoogleProvider melakukan discovery ke accounts.google.com (butuh jaringan saat startup).
func NewGoogleProvider(ctx context.Context, clientID, clientSecret, redirectURL string) (*OIDCProvider, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, client), googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("google oidc discovery: %w", err)
	}
	conf := oauth2.Config{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}
	return newOIDCProvider(domain.ProviderGoogle, conf, p.Verifier(&oidc.Config{ClientID: clientID}), client), nil
}

func newOIDCProvider(name string, conf oauth2.Config, v idTokenVerifier, client *http.Client) *OIDCProvider {
	return &OIDCProvider{name: name, conf: conf, verifier: v, client: client}
}

func (p *OIDCProvider) AuthCodeURL(state, codeChallenge, nonce string) string {
	return p.conf.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

type oidcClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

var errNoIDToken = errors.New("oidc: id_token missing")

func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (domain.ExternalProfile, error) {
	if p.client != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	}
	tok, err := p.conf.Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return domain.ExternalProfile{}, fmt.Errorf("oidc exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return domain.ExternalProfile{}, errNoIDToken
	}
	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return domain.ExternalProfile{}, fmt.Errorf("oidc verify: %w", err)
	}
	if idt.Nonce != nonce || nonce == "" {
		return domain.ExternalProfile{}, errors.New("oidc: nonce mismatch")
	}
	var c oidcClaims
	if err := idt.Claims(&c); err != nil {
		return domain.ExternalProfile{}, fmt.Errorf("oidc claims: %w", err)
	}
	return domain.ExternalProfile{
		Provider: p.name, Subject: idt.Subject, Email: c.Email,
		EmailVerified: c.EmailVerified, Name: c.Name,
	}, nil
}
