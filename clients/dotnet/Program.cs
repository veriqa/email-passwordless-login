// Copyright (c) Dmitrii Erusov
// SPDX-License-Identifier: MIT

// The client (relying party) of the channel quickstarts: an ordinary ASP.NET Core application that
// signs users in against Veriqa with the standard OpenID Connect handler (Authorization Code + PKCE)
// and, once signed in, lists the claims Veriqa issued. Nothing Veriqa-specific is referenced here:
// Veriqa is the issuer, and to this application it is just an OpenID Connect provider.
//
// Configuration (appsettings.json, environment variables or command-line arguments):
//   Oidc:Authority - the Veriqa issuer, https://localhost:8443 in the quickstarts;
//   Oidc:ClientId  - the client registered on the issuer, `rp`;
//   Oidc:Channel   - optional; sends acr_values=channel:<value>, so the sign-in page goes straight
//                    to that channel instead of offering every enabled one.

using System.Net;
using System.Security.Claims;
using System.Text;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.IdentityModel.Protocols.OpenIdConnect;

var builder = WebApplication.CreateBuilder(args);

var channel = builder.Configuration["Oidc:Channel"];

builder.Services
    .AddAuthentication(options =>
    {
        options.DefaultScheme = CookieAuthenticationDefaults.AuthenticationScheme;
        options.DefaultChallengeScheme = OpenIdConnectDefaults.AuthenticationScheme;
    })
    .AddCookie()
    .AddOpenIdConnect(options =>
    {
        // The handler reads {Authority}/.well-known/openid-configuration on the first sign-in, not at
        // startup: the client starts even while the issuer is still coming up.
        options.Authority = builder.Configuration["Oidc:Authority"];
        options.ClientId = builder.Configuration["Oidc:ClientId"];

        // `rp` is registered without a secret - a public client, so Veriqa requires PKCE from it.
        options.UsePkce = true;
        options.ResponseType = OpenIdConnectResponseType.Code;
        options.CallbackPath = "/signin-oidc";

        // Veriqa issues the user's name as the short OIDC `name` claim.
        options.TokenValidationParameters.NameClaimType = "name";
        options.MapInboundClaims = false;
        options.GetClaimsFromUserInfoEndpoint = true;

        // A user who presses "Cancel sign-in" on the Veriqa page comes back with error=access_denied.
        // With no path set here the refusal would go to OnRemoteFailure below, like any other failed
        // return.
        options.AccessDeniedPath = "/signin-cancelled";

        // Every other failed return - Veriqa answering error=server_error, say, because the channel did
        // not deliver the confirmation - raises OnRemoteFailure; with no handler for it the return ends
        // in an HTTP 500. The user is taken to a page that offers to start over. The error text is
        // deliberately not shown to the user: it is written to the log instead.
        options.Events.OnRemoteFailure = context =>
        {
            context.HttpContext.RequestServices.GetRequiredService<ILogger<Program>>()
                .LogWarning(context.Failure, "The sign-in return carried an error.");
            context.HandleResponse();
            context.Response.Redirect("/signin-failed");
            return Task.CompletedTask;
        };

        if (!string.IsNullOrWhiteSpace(channel))
        {
            options.Events.OnRedirectToIdentityProvider = context =>
            {
                context.ProtocolMessage.AcrValues = $"channel:{channel}";
                return Task.CompletedTask;
            };
        }
    });

builder.Services.AddAuthorization();

var app = builder.Build();

app.UseAuthentication();
app.UseAuthorization();

// An anonymous visitor is challenged straight into the Veriqa sign-in page; after confirming in the
// channel the user comes back here and sees the claims Veriqa issued.
app.MapGet("/", (ClaimsPrincipal user) =>
    user.Identity?.IsAuthenticated is true
        ? Results.Content(ClaimsPage(user), "text/html; charset=utf-8")
        : Results.Challenge());

// Where the handler sends a user who did not go through with the sign-in (AccessDeniedPath above).
// It is a route of its own because "/" challenges an anonymous visitor: sent there, the user would be
// put straight back on the sign-in page they have just left.
app.MapGet("/signin-cancelled", () => Results.Text("Sign-in was cancelled. Open / to sign in again."));

// Where OnRemoteFailure above sends a user whose sign-in ended in an error. Like the page before it,
// it is anonymous and starts nothing by itself: redirected to "/", a failure that repeats would loop.
app.MapGet("/signin-failed", () => Results.Text("Sign-in could not be completed. Open / to sign in again."));

// Drops this application's cookie only, so the flow can be replayed.
app.MapGet("/signout", () =>
    Results.SignOut(
        properties: new AuthenticationProperties { RedirectUri = "/" },
        authenticationSchemes: [CookieAuthenticationDefaults.AuthenticationScheme]));

app.Run();

static string ClaimsPage(ClaimsPrincipal user)
{
    var rows = new StringBuilder();
    foreach (var claim in user.Claims)
    {
        rows.Append("<tr><td>").Append(WebUtility.HtmlEncode(claim.Type))
            .Append("</td><td>").Append(WebUtility.HtmlEncode(claim.Value)).Append("</td></tr>");
    }

    return "<!doctype html><html><head><meta charset=\"utf-8\"><title>Signed in</title></head><body>"
        + $"<h1>Hello, {WebUtility.HtmlEncode(user.Identity?.Name ?? "user")}!</h1>"
        + "<p>Signed in with Veriqa. These are the claims it issued:</p>"
        + $"<table border=\"1\" cellpadding=\"4\"><tr><th>Claim</th><th>Value</th></tr>{rows}</table>"
        + "<p><a href=\"/signout\">Sign out</a></p></body></html>";
}
