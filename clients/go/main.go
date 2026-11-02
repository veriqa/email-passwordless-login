// Copyright (c) Dmitrii Erusov
// SPDX-License-Identifier: MIT

// The client (relying party) of the channel quickstarts on Go: net/http, go-oidc and x/oauth2.
// Signs users in against Veriqa with Authorization Code + PKCE and, once signed in, lists the claims
// Veriqa issued. Nothing Veriqa-specific is referenced here: to this application Veriqa is just an
// OpenID Connect provider.
//
// Configuration (environment variables, all optional on the local stand):
//
//	VERIQA_ISSUER    - the Veriqa issuer, https://localhost:8443/ in the quickstarts: go-oidc compares it
//	                   literally with the issuer in the discovery document, trailing slash included;
//	VERIQA_CLIENT_ID - the client registered on the issuer, `rp`;
//	VERIQA_CHANNEL   - sends acr_values=channel:<value>, so the sign-in page goes straight to that
//	                   channel instead of offering every enabled one;
//	VERIQA_CA_FILE   - the certificate to trust for the issuer, the one the stand's README creates.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"html/template"
	"log"
	"maps"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	redirectURI = "http://localhost:7020/signin-oidc"
	cookieName  = "quickstart-session"
)

var (
	issuer   = env("VERIQA_ISSUER", "https://localhost:8443/")
	clientID = env("VERIQA_CLIENT_ID", "rp")
	channel  = env("VERIQA_CHANNEL", "")
	caFile   = env("VERIQA_CA_FILE", "../../built-in/certs/localhost.crt")
)

// session is what a sign-in in progress keeps for the return and what a finished one keeps for the pages.
type session struct {
	verifier, state, nonce string
	claims                 map[string]any
}

// The sessions live in memory: a restart signs everyone out, which suits a local stand.
var (
	sessionsLock sync.Mutex
	sessions     = map[string]*session{}
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// issuerContext carries the HTTP client that go-oidc and x/oauth2 use to call the issuer. The stand's
// certificate is self-signed; an issuer with a public certificate needs none of this.
func issuerContext() context.Context {
	ctx := context.Background()
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return ctx
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	roots.AppendCertsFromPEM(pem)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
	return oidc.ClientContext(ctx, &http.Client{Transport: transport})
}

// oidcClient resolves the issuer on the first sign-in, not at startup: the client starts even while
// the issuer is still coming up. A failed discovery is retried on the next sign-in.
type oidcClient struct {
	lock     sync.Mutex
	ctx      context.Context
	provider *oidc.Provider
}

func (c *oidcClient) discover() (*oidc.Provider, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	provider, err := oidc.NewProvider(c.ctx, issuer)
	if err != nil {
		return nil, err
	}
	c.provider = provider
	return provider, nil
}

func oauthConfig(provider *oidc.Provider) *oauth2.Config {
	endpoint := provider.Endpoint()
	// `rp` is registered without a secret - a public client, so Veriqa requires PKCE from it.
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &oauth2.Config{
		ClientID:    clientID,
		Endpoint:    endpoint,
		RedirectURL: redirectURI,
		Scopes:      []string{oidc.ScopeOpenID, "profile"},
	}
}

// currentSession finds the session of the request's cookie and starts a new one when there is none.
func currentSession(w http.ResponseWriter, r *http.Request) *session {
	sessionsLock.Lock()
	defer sessionsLock.Unlock()
	if cookie, err := r.Cookie(cookieName); err == nil {
		if existing, ok := sessions[cookie.Value]; ok {
			return existing
		}
	}
	id := rand.Text()
	sessions[id] = &session{}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	return sessions[id]
}

// home sends an anonymous visitor to the Veriqa sign-in page and lists the claims of a signed-in one.
func (c *oidcClient) home(w http.ResponseWriter, r *http.Request) {
	s := currentSession(w, r)
	if s.claims != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := claimsPage.Execute(w, s.claims); err != nil {
			log.Printf("The claims page failed: %v", err)
		}
		return
	}
	provider, err := c.discover()
	if err != nil {
		log.Printf("The sign-in could not be started: %v", err)
		http.Redirect(w, r, "/signin-failed", http.StatusFound)
		return
	}
	s.verifier, s.state, s.nonce = oauth2.GenerateVerifier(), rand.Text(), rand.Text()
	options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(s.verifier), oidc.Nonce(s.nonce)}
	if channel != "" {
		options = append(options, oauth2.SetAuthURLParam("acr_values", "channel:"+channel))
	}
	http.Redirect(w, r, oauthConfig(provider).AuthCodeURL(s.state, options...), http.StatusFound)
}

// callback completes the sign-in: a refusal and a failure each have a page, a success lands on the claims.
func (c *oidcClient) callback(w http.ResponseWriter, r *http.Request) {
	// A user who presses "Cancel sign-in" on the Veriqa page comes back with error=access_denied.
	if r.URL.Query().Get("error") == "access_denied" {
		http.Redirect(w, r, "/signin-cancelled", http.StatusFound)
		return
	}
	s := currentSession(w, r)
	claims, err := c.redeem(s, r)
	if err != nil {
		// Veriqa answering error=server_error, say, because the channel did not deliver the
		// confirmation. The error text is deliberately not shown to the user, only logged.
		log.Printf("The sign-in return carried an error: %v", err)
		http.Redirect(w, r, "/signin-failed", http.StatusFound)
		return
	}
	s.claims = claims
	http.Redirect(w, r, "/", http.StatusFound)
}

// redeem turns the authorization code into tokens, verifies the ID token and adds the UserInfo claims.
func (c *oidcClient) redeem(s *session, r *http.Request) (map[string]any, error) {
	query := r.URL.Query()
	if problem := query.Get("error"); problem != "" {
		return nil, errors.New(problem + ": " + query.Get("error_description"))
	}
	if s.state == "" || query.Get("state") != s.state {
		return nil, errors.New("the state of the return does not match the sign-in")
	}
	provider, err := c.discover()
	if err != nil {
		return nil, err
	}
	token, err := oauthConfig(provider).Exchange(c.ctx, query.Get("code"), oauth2.VerifierOption(s.verifier))
	if err != nil {
		return nil, err
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("the token response carries no id_token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: clientID}).Verify(c.ctx, rawIDToken)
	if err != nil {
		return nil, err
	}
	if idToken.Nonce != s.nonce {
		return nil, errors.New("the nonce of the ID token does not match the sign-in")
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return nil, err
	}
	userInfo, err := provider.UserInfo(c.ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return nil, err
	}
	extra := map[string]any{}
	if err := userInfo.Claims(&extra); err != nil {
		return nil, err
	}
	maps.Copy(claims, extra)
	return claims, nil
}

// signout drops this application's session only, so the flow can be replayed.
func signout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		sessionsLock.Lock()
		delete(sessions, cookie.Value)
		sessionsLock.Unlock()
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// text answers an anonymous page of its own: "/" challenges an anonymous visitor, so a user sent there
// would be put straight back on the sign-in page, and a failure that repeats would loop.
func text(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(message))
	}
}

// show prints a claim value; JSON numbers arrive as float64, which would otherwise print as 1.79e+09.
func show(value any) string {
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}

var claimsPage = template.Must(template.New("claims").Funcs(template.FuncMap{"show": show}).Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Signed in</title></head><body>
<h1>Hello, {{if .name}}{{.name}}{{else}}user{{end}}!</h1>
<p>Signed in with Veriqa. These are the claims it issued:</p>
<table border="1" cellpadding="4"><tr><th>Claim</th><th>Value</th></tr>
{{range $type, $value := .}}<tr><td>{{$type}}</td><td>{{show $value}}</td></tr>
{{end}}</table>
<p><a href="/signout">Sign out</a></p></body></html>`))

func main() {
	client := &oidcClient{ctx: issuerContext()}
	http.HandleFunc("GET /{$}", client.home)
	http.HandleFunc("GET /signin-oidc", client.callback)
	http.HandleFunc("GET /signin-cancelled", text("Sign-in was cancelled. Open / to sign in again."))
	http.HandleFunc("GET /signin-failed", text("Sign-in could not be completed. Open / to sign in again."))
	http.HandleFunc("GET /signout", signout)
	log.Print("Open http://localhost:7020")
	log.Fatal(http.ListenAndServe("localhost:7020", nil))
}
