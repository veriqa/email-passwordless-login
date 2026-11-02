# Copyright (c) Dmitrii Erusov
# SPDX-License-Identifier: MIT
"""The client (relying party) of the channel quickstarts on Python: Flask and Authlib.

Signs users in against Veriqa with Authorization Code + PKCE and, once signed in, lists the claims
Veriqa issued. Nothing Veriqa-specific is referenced here: to this application Veriqa is just an
OpenID Connect provider.

Configuration (environment variables, all optional on the local stand):
  VERIQA_ISSUER    - the Veriqa issuer, https://localhost:8443 in the quickstarts;
  VERIQA_CLIENT_ID - the client registered on the issuer, `rp`;
  VERIQA_CHANNEL   - sends acr_values=channel:<value>, so the sign-in page goes straight to that
                     channel instead of offering every enabled one;
  VERIQA_CA_FILE   - the certificate to trust for the issuer, the one the stand's README creates.
"""

import logging
import os
import secrets
from html import escape
from pathlib import Path

from authlib.integrations.flask_client import OAuth
from flask import Flask, redirect, request, session

ISSUER = os.environ.get("VERIQA_ISSUER", "https://localhost:8443")
CLIENT_ID = os.environ.get("VERIQA_CLIENT_ID", "rp")
CHANNEL = os.environ.get("VERIQA_CHANNEL", "")
CA_FILE = Path(os.environ.get("VERIQA_CA_FILE", Path(__file__).parent / "../../built-in/certs/localhost.crt"))
REDIRECT_URI = "http://localhost:7020/signin-oidc"

# Authlib calls the issuer through requests, which reads the trusted certificates from this variable.
# The stand's certificate is self-signed; an issuer with a public certificate needs none of this.
if CA_FILE.exists():
    os.environ["REQUESTS_CA_BUNDLE"] = str(CA_FILE)

app = Flask(__name__)
# Signs the session cookie. A new key on every start signs everyone out, which suits a local stand;
# a deployed application keeps the key in its secret store.
app.secret_key = secrets.token_hex()

# Authlib reads {issuer}/.well-known/openid-configuration on the first sign-in, not at startup: the
# client starts even while the issuer is still coming up.
oauth = OAuth(app)
oauth.register(
    name="veriqa",
    client_id=CLIENT_ID,
    server_metadata_url=f"{ISSUER}/.well-known/openid-configuration",
    client_kwargs={
        "scope": "openid profile",
        # `rp` is registered without a secret - a public client, so Veriqa requires PKCE from it.
        "token_endpoint_auth_method": "none",
        "code_challenge_method": "S256",
    },
)


@app.get("/")
def home():
    """Sends an anonymous visitor to the Veriqa sign-in page and lists the claims of a signed-in one."""
    claims = session.get("claims")
    if claims:
        return claims_page(claims)
    try:
        extra = {"acr_values": f"channel:{CHANNEL}"} if CHANNEL else {}
        return oauth.veriqa.authorize_redirect(REDIRECT_URI, **extra)
    except Exception:
        logging.exception("The sign-in could not be started.")
        return redirect("/signin-failed")


@app.get("/signin-oidc")
def callback():
    """Completes the sign-in: a refusal and a failure each have a page, a success lands on the claims."""
    # A user who presses "Cancel sign-in" on the Veriqa page comes back with error=access_denied.
    if request.args.get("error") == "access_denied":
        return redirect("/signin-cancelled")
    try:
        token = oauth.veriqa.authorize_access_token()
        session["claims"] = {**token["userinfo"], **oauth.veriqa.userinfo(token=token)}
    except Exception:
        # Veriqa answering error=server_error, say, because the channel did not deliver the
        # confirmation. The error text is deliberately not shown to the user, only logged.
        logging.exception("The sign-in return carried an error.")
        return redirect("/signin-failed")
    return redirect("/")


# Anonymous pages of their own: "/" challenges an anonymous visitor, so a user sent there would be put
# straight back on the sign-in page, and a failure that repeats would loop.
@app.get("/signin-cancelled")
def signin_cancelled():
    return "Sign-in was cancelled. Open / to sign in again.", {"Content-Type": "text/plain"}


@app.get("/signin-failed")
def signin_failed():
    return "Sign-in could not be completed. Open / to sign in again.", {"Content-Type": "text/plain"}


@app.get("/signout")
def signout():
    """Drops this application's session only, so the flow can be replayed."""
    session.clear()
    return redirect("/")


def claims_page(claims: dict) -> str:
    rows = "".join(f"<tr><td>{escape(str(k))}</td><td>{escape(str(v))}</td></tr>" for k, v in claims.items())
    return (
        '<!doctype html><html><head><meta charset="utf-8"><title>Signed in</title></head><body>'
        f"<h1>Hello, {escape(str(claims.get('name', 'user')))}!</h1>"
        "<p>Signed in with Veriqa. These are the claims it issued:</p>"
        f'<table border="1" cellpadding="4"><tr><th>Claim</th><th>Value</th></tr>{rows}</table>'
        '<p><a href="/signout">Sign out</a></p></body></html>'
    )


if __name__ == "__main__":
    app.run(host="localhost", port=7020)
