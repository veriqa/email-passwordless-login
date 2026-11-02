<?php
// Copyright (c) Dmitrii Erusov
// SPDX-License-Identifier: MIT

// The client (relying party) of the channel quickstarts on PHP: jumbojett/openid-connect-php.
// Signs users in against Veriqa with Authorization Code + PKCE and, once signed in, lists the claims
// Veriqa issued. Nothing Veriqa-specific is referenced here: to this application Veriqa is just an
// OpenID Connect provider. Started with the PHP built-in server, this file answers every route.
//
// Configuration (environment variables, all optional on the local stand):
//   VERIQA_ISSUER    - the Veriqa issuer, https://localhost:8443 in the quickstarts;
//   VERIQA_CLIENT_ID - the client registered on the issuer, `rp`;
//   VERIQA_CHANNEL   - sends acr_values=channel:<value>, so the sign-in page goes straight to that
//                      channel instead of offering every enabled one;
//   VERIQA_CA_FILE   - the certificate to trust for the issuer, the one the stand's README creates.

declare(strict_types=1);

// PHP without a php.ini prints notices into the response, where they break the redirects: to the log.
ini_set('display_errors', '0');

require __DIR__ . '/vendor/autoload.php';

use Jumbojett\OpenIDConnectClient;

const REDIRECT_URI = 'http://localhost:7020/signin-oidc';

function setting(string $name, string $fallback): string
{
    $value = getenv($name);
    return $value === false || $value === '' ? $fallback : $value;
}

// Builds the OpenID Connect client. The library keeps the state, the nonce and the PKCE verifier of a
// sign-in in progress in the PHP session.
function oidcClient(): OpenIDConnectClient
{
    // `rp` is registered without a secret - a public client, so Veriqa requires PKCE from it. The library
    // sends no client authentication when PKCE is on and the secret is empty.
    $oidc = new OpenIDConnectClient(setting('VERIQA_ISSUER', 'https://localhost:8443'), setting('VERIQA_CLIENT_ID', 'rp'), '');
    $oidc->setRedirectURL(REDIRECT_URI);
    $oidc->addScope(['profile']);
    $oidc->setCodeChallengeMethod('S256');

    // The stand's certificate is self-signed; an issuer with a public certificate needs none of this.
    $caFile = setting('VERIQA_CA_FILE', __DIR__ . '/../../built-in/certs/localhost.crt');
    if (is_file($caFile)) {
        $oidc->setCertPath($caFile);
    }

    $channel = setting('VERIQA_CHANNEL', '');
    if ($channel !== '') {
        $oidc->addAuthParam(['acr_values' => "channel:$channel"]);
    }
    return $oidc;
}

// Starts the sign-in on "/" and completes it on the return: the library tells the two apart by the
// `code` parameter. A refusal and a failure each have a page, a success lands on the claims.
function signIn(): void
{
    try {
        $oidc = oidcClient();
        $oidc->authenticate();
        $_SESSION['claims'] = array_merge((array) $oidc->getVerifiedClaims(), (array) $oidc->requestUserInfo());
        redirect('/');
    } catch (Exception $error) {
        // Veriqa answering error=server_error, say, because the channel did not deliver the
        // confirmation. The error text is deliberately not shown to the user, only logged.
        error_log('The sign-in failed: ' . $error->getMessage());
        redirect('/signin-failed');
    }
}

function redirect(string $location): never
{
    header("Location: $location", true, 302);
    exit;
}

function plainText(string $message): void
{
    header('Content-Type: text/plain; charset=utf-8');
    echo $message;
}

function claimsPage(array $claims): void
{
    $encode = static fn (mixed $value): string => htmlspecialchars(is_scalar($value) ? (string) $value : json_encode($value));
    $rows = '';
    foreach ($claims as $type => $value) {
        $rows .= '<tr><td>' . $encode($type) . '</td><td>' . $encode($value) . '</td></tr>';
    }
    echo '<!doctype html><html><head><meta charset="utf-8"><title>Signed in</title></head><body>'
        . '<h1>Hello, ' . $encode($claims['name'] ?? 'user') . '!</h1>'
        . '<p>Signed in with Veriqa. These are the claims it issued:</p>'
        . '<table border="1" cellpadding="4"><tr><th>Claim</th><th>Value</th></tr>' . $rows . '</table>'
        . '<p><a href="/signout">Sign out</a></p></body></html>';
}

session_start();

switch (parse_url($_SERVER['REQUEST_URI'], PHP_URL_PATH)) {
    case '/':
        // An anonymous visitor goes to the Veriqa sign-in page, a signed-in one sees the claims.
        isset($_SESSION['claims']) ? claimsPage($_SESSION['claims']) : signIn();
        break;
    case '/signin-oidc':
        // A user who presses "Cancel sign-in" on the Veriqa page comes back with error=access_denied.
        if (($_GET['error'] ?? '') === 'access_denied') {
            redirect('/signin-cancelled');
        }
        signIn();
        break;
    // Anonymous pages of their own: "/" challenges an anonymous visitor, so a user sent there would be
    // put straight back on the sign-in page, and a failure that repeats would loop.
    case '/signin-cancelled':
        plainText('Sign-in was cancelled. Open / to sign in again.');
        break;
    case '/signin-failed':
        plainText('Sign-in could not be completed. Open / to sign in again.');
        break;
    case '/signout':
        // Drops this application's session only, so the flow can be replayed.
        session_destroy();
        redirect('/');
    default:
        http_response_code(404);
}
