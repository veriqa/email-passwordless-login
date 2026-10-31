# Email passwordless login for ASP.NET Core — OpenID Connect magic link in Docker

Email sign-in with no password and no code: one-tap email or a magic link. A standard OpenID Connect
(OIDC) provider in Docker for an ASP.NET Core app, plus a minimal client app that signs in against it.

## What you get

Open the client app and it sends you to the sign-in page. You enter your email address, a letter
with a magic link arrives, you open the link and confirm — and you are back in the app, signed in,
looking at the claims the provider issued. No password, no form to fill in, no SMS code.

Out of the box nothing leaves your machine: the letters go to [Mailpit](https://mailpit.axllent.org/),
a local mail catcher with a web inbox, so the stand works without any mail account. Point it at a
real SMTP server when you want real mail.

The provider is [Veriqa](https://veriqa.app): an open-source OpenID Connect provider built on
OpenIddict that confirms sign-ins in messaging apps — Telegram, WhatsApp, Instagram, Messenger,
WeChat, Viber, other channels or Email. Email is the channel for users without a messaging app. The
client knows nothing about Veriqa: it is the stock ASP.NET Core OpenID Connect handler
(Authorization Code + PKCE), so the same setup works for any stack with an OIDC library.

Each folder next to `clients/` is one way to connect Email, with a stand of its own; they share the
client app in `clients/dotnet/`. `built-in/` uses the Email channel built into Veriqa.

```
built-in/            the stand with the Email channel built into Veriqa
  docker-compose.yml   Veriqa from the published image, Email channel only, and Mailpit
  .env.example         the values you fill in
clients/dotnet/      the client app (ASP.NET Core, Microsoft.AspNetCore.Authentication.OpenIdConnect)
```

## Before you start

- [Docker](https://docs.docker.com/get-docker/) with Compose
- [.NET SDK 10](https://dotnet.microsoft.com/download) — for the client app and the HTTPS
  certificate

That is all: no mail account and no public address are needed.

## Run it

**1. Fill in the settings.**

```bash
cd built-in
cp .env.example .env
```

Pick any password for `CERT_PASSWORD`. Leave the `SMTP_*` variables empty to use Mailpit.

**2. Create the HTTPS certificate.** OpenID Connect needs HTTPS even locally: the provider serves
it at `https://localhost:8443`, the client at `https://localhost:7020`. Both use the .NET
development certificate:

```bash
dotnet dev-certs https --trust
dotnet dev-certs https -ep certs/localhost.pfx -p <CERT_PASSWORD from .env>
chmod 644 certs/localhost.pfx
```

`--trust` makes your machine trust the certificate; on Linux follow what the command prints. The
`chmod` (Linux and macOS) lets the non-root user of the container read the file; on Windows skip it
and write the path as `certs\localhost.pfx`.

**3. Start the provider.**

```bash
docker compose up -d
docker compose ps
```

Wait until the `veriqa` service reads `healthy`, then check that it is ready:
`https://localhost:8443/health/ready` answers `200`.

**4. Start the client.**

```bash
dotnet run --project ../clients/dotnet -- --Oidc:Channel email
```

**5. Sign in.** Open `https://localhost:7020` and enter any email address. Open the Mailpit inbox
at `http://localhost:8025`, open the letter and click the link, then confirm. You land back on the
client with the list of your claims. `/signout` drops the local session so you can go again.

The link in the letter points at `https://localhost:8443`, so open it on the same machine. On a
deployed server the link carries its public address and opens anywhere, the phone included.

## Real mail instead of Mailpit

Set the SMTP variables in `.env` and restart with `docker compose up -d`:

| Variable | Value |
| --- | --- |
| `SMTP_HOST`, `SMTP_PORT` | your mail server; `587` for STARTTLS, `465` for implicit TLS |
| `SMTP_USE_SSL` | `false` on `587`, `true` on `465` |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | set both or neither |
| `EMAIL_FROM_ADDRESS` | the sender address your server may send from |

## How it fits together

- `clients/dotnet/` asks the provider for a sign-in with `acr_values=channel:email`, so the sign-in page goes
  straight to Email. Without `--Oidc:Channel` it offers every enabled channel.
- `built-in/docker-compose.yml` registers the client (`rp`, redirect
  `https://localhost:7020/signin-oidc`), sets the issuer to `https://localhost:8443` and enables the
  Email channel with the magic link.
- The stand runs in the `Development` environment: sessions live in memory and tokens are signed
  with development certificates, so a restart forgets everything — it is a local stand, not a
  deployment.
- Email also has a second mode, the one-tap email: the user sends a pre-filled letter instead of
  receiving one, so the sign-in no longer depends on your mail reaching their inbox. It needs
  inbound mail delivery, so it is not part of this stand.

## Going further

- Email channel settings, both modes and how to choose between them:
  [Channels → Email](https://veriqa.app/docs/guides/channels#email).
- A production setup with a database, real certificates and a reverse proxy:
  [Quickstart self-hosted](https://veriqa.app/docs/quickstart/self-hosted).
- Veriqa itself — the source, the other samples and the documentation:
  [gitlab.com/veriqa/veriqa](https://gitlab.com/veriqa/veriqa) · [veriqa.app](https://veriqa.app).

## License

This sample is MIT-licensed — copy it into your own code freely. Veriqa itself is open source under
MPL-2.0.
