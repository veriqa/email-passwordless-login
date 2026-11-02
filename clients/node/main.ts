// Copyright (c) Dmitrii Erusov
// SPDX-License-Identifier: MIT

// The client (relying party) of the channel quickstarts on Node.js: Express and openid-client.
// Signs users in against Veriqa with Authorization Code + PKCE and, once signed in, lists the claims
// Veriqa issued. Nothing Veriqa-specific is referenced here: to this application Veriqa is just an
// OpenID Connect provider. Node.js runs this TypeScript file directly, with no build step.
//
// Configuration (environment variables, all optional on the local stand):
//   VERIQA_ISSUER    - the Veriqa issuer, https://localhost:8443 in the quickstarts;
//   VERIQA_CLIENT_ID - the client registered on the issuer, `rp`;
//   VERIQA_CHANNEL   - sends acr_values=channel:<value>, so the sign-in page goes straight to that
//                      channel instead of offering every enabled one;
//   VERIQA_CA_FILE   - the certificate to trust for the issuer, the one the stand's README creates.

import { randomBytes } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { getCACertificates, setDefaultCACertificates } from "node:tls";

import express from "express";
import session from "express-session";
import * as client from "openid-client";

const ISSUER = process.env.VERIQA_ISSUER ?? "https://localhost:8443";
const CLIENT_ID = process.env.VERIQA_CLIENT_ID ?? "rp";
const CHANNEL = process.env.VERIQA_CHANNEL ?? "";
const CA_FILE = process.env.VERIQA_CA_FILE ?? join(import.meta.dirname, "../../built-in/certs/localhost.crt");
const REDIRECT_URI = "http://localhost:7020/signin-oidc";

// What a sign-in in progress keeps for the return, and what a finished one keeps for the pages.
declare module "express-session" {
  interface SessionData {
    pending?: { verifier: string; state: string; nonce: string };
    claims?: Record<string, unknown>;
  }
}

// The stand's certificate is self-signed; an issuer with a public certificate needs none of this.
if (existsSync(CA_FILE)) {
  setDefaultCACertificates([...getCACertificates("default"), readFileSync(CA_FILE, "utf8")]);
}

// Discovery runs on the first sign-in, not at startup: the client starts even while the issuer is
// still coming up. `rp` is registered without a secret - a public client, so Veriqa requires PKCE.
let configuration: client.Configuration | undefined;
async function discover(): Promise<client.Configuration> {
  configuration ??= await client.discovery(new URL(ISSUER), CLIENT_ID, undefined, client.None());
  return configuration;
}

const app = express();

// In-memory sessions and a new key on every start sign everyone out, which suits a local stand; a
// deployed application keeps the key in its secret store and the sessions in a shared store.
app.use(session({ secret: randomBytes(32).toString("hex"), resave: false, saveUninitialized: false }));

// Sends an anonymous visitor to the Veriqa sign-in page and lists the claims of a signed-in one.
app.get("/", async (req, res) => {
  if (req.session.claims) {
    res.type("html").send(claimsPage(req.session.claims));
    return;
  }
  try {
    const config = await discover();
    const pending = {
      verifier: client.randomPKCECodeVerifier(),
      state: client.randomState(),
      nonce: client.randomNonce(),
    };
    req.session.pending = pending;
    const url = client.buildAuthorizationUrl(config, {
      redirect_uri: REDIRECT_URI,
      scope: "openid profile",
      code_challenge: await client.calculatePKCECodeChallenge(pending.verifier),
      code_challenge_method: "S256",
      state: pending.state,
      nonce: pending.nonce,
      ...(CHANNEL ? { acr_values: `channel:${CHANNEL}` } : {}),
    });
    res.redirect(url.href);
  } catch (error) {
    console.error("The sign-in could not be started.", error);
    res.redirect("/signin-failed");
  }
});

// Completes the sign-in: a refusal and a failure each have a page, a success lands on the claims.
app.get("/signin-oidc", async (req, res) => {
  // A user who presses "Cancel sign-in" on the Veriqa page comes back with error=access_denied.
  if (req.query.error === "access_denied") {
    res.redirect("/signin-cancelled");
    return;
  }
  try {
    const config = await discover();
    const pending = req.session.pending;
    delete req.session.pending;
    const tokens = await client.authorizationCodeGrant(config, new URL(req.originalUrl, REDIRECT_URI), {
      pkceCodeVerifier: pending?.verifier,
      expectedState: pending?.state,
      expectedNonce: pending?.nonce,
      idTokenExpected: true,
    });
    const idClaims = tokens.claims()!;
    const userInfo = await client.fetchUserInfo(config, tokens.access_token, idClaims.sub);
    req.session.claims = { ...idClaims, ...userInfo };
    res.redirect("/");
  } catch (error) {
    // Veriqa answering error=server_error, say, because the channel did not deliver the confirmation.
    // The error text is deliberately not shown to the user, only logged.
    console.error("The sign-in return carried an error.", error);
    res.redirect("/signin-failed");
  }
});

// Anonymous pages of their own: "/" challenges an anonymous visitor, so a user sent there would be put
// straight back on the sign-in page, and a failure that repeats would loop.
app.get("/signin-cancelled", (_req, res) => {
  res.type("text").send("Sign-in was cancelled. Open / to sign in again.");
});
app.get("/signin-failed", (_req, res) => {
  res.type("text").send("Sign-in could not be completed. Open / to sign in again.");
});

// Drops this application's session only, so the flow can be replayed.
app.get("/signout", (req, res) => {
  req.session.destroy(() => res.redirect("/"));
});

function escapeHtml(value: unknown): string {
  return String(value).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

function claimsPage(claims: Record<string, unknown>): string {
  const rows = Object.entries(claims)
    .map(([type, value]) => `<tr><td>${escapeHtml(type)}</td><td>${escapeHtml(value)}</td></tr>`)
    .join("");
  return (
    '<!doctype html><html><head><meta charset="utf-8"><title>Signed in</title></head><body>' +
    `<h1>Hello, ${escapeHtml(claims.name ?? "user")}!</h1>` +
    "<p>Signed in with Veriqa. These are the claims it issued:</p>" +
    `<table border="1" cellpadding="4"><tr><th>Claim</th><th>Value</th></tr>${rows}</table>` +
    '<p><a href="/signout">Sign out</a></p></body></html>'
  );
}

app.listen(7020, "localhost", () => console.log("Open http://localhost:7020"));
