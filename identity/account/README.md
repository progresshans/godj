# Ordinary accounts

`identity/account` provides login/logout, self-service password changes and optional email-based password reset over an explicitly configured `web/sessionauth.Runtime`.
It does not own identity storage or choose password strength policy. Article's `internal/siteapp` is the complete composition example.

1. Open a stored Identity runtime and create its session manager.
2. Bind both `LoginPersistence(manager)` and `PasswordChangePersistence(manager, validators...)` into the Web runtime.
3. Include `AllowedNextPaths(basePath)` in the runtime's redirect allowlist and cover both HTML/API prefixes with its cookie paths.
4. Create `identityaccount.New(Config{Apps: registry, Namespace: namespace, Auth: runtime})` and install its routes and middleware together.

An Admin sharing the runtime declares the account paths in `admin.SiteConfig.AdditionalNextPaths`.
Admin staff/model admission remains independent of ordinary account access. Startup performs no account reads or password work.

| Route | Behavior |
|---|---|
| GET/POST `/account/login/` | CSRF-protected ordinary login, bounded local `next` |
| POST `/account/logout/` | CSRF-protected durable logout |
| GET/POST `/account/password/` | Old password, new password and confirmation Form |
| GET `/account/password/done/` | Authenticated completion page |
| GET `/api/account/csrf/` | Authenticated 204 and masked CSRF response header |
| POST `/api/account/password/` | Session/CSRF JSON command with `old_password` and `new_password` |
| GET `/api/account/openapi.json` | Authenticated OpenAPI for the actual JSON operations |

The command requires no staff role or model permission. It accepts no target ID or revision. A confirmed change atomically updates the current user's password/revision, rotates the current session, revokes that user's other sessions and records a value-free audit. The current payload and absolute session lifetime are retained; `last_login` is unchanged.

Password inputs preserve whitespace and never reappear in HTML. Failed preflight checks do not refresh or delete session rows. JSON success is 204 with a replacement session cookie; retain the existing CSRF cookie. Form success redirects to the completion page. A 503 `outcome_unknown` means the commit result is uncertain: reconcile account state before submitting another change. The command never automatically retries an uncertain write.

Forms accept URL-encoded UTF-8 input, JSON commands accept `application/json`. The body limit is 64 KiB, individual input limit 4096 bytes, and query parameters are only accepted as a single login GET `next`. Duplicate password Form values produce errors; duplicate/unknown JSON members are rejected. JSON string-limit failures are 400, body-limit failures 413.

## Optional password reset

Bind `Runtime.PasswordResetPersistence(manager, resetConfig)` into the Web runtime. Build `identity.NewPasswordResetMailer` from that persistence's `Resetter()`, an explicit `mail.Sender`, From address and trusted origin. Pass the mailer and a mandatory private `ReportError` callback as `Config.PasswordReset`. The mailer's `ConfirmPath` must equal the account `BasePath` plus `/reset`. Install the account middleware as well as its routes so routing, negotiation and execution failures receive the reset privacy headers.

Article's `siteapp.Config.WithPasswordReset` composes these owners together. It applies the global `WithPasswordValidators` policy plus any reset-specific validators. Sender, keys, origin and reporting are explicit; startup does not send mail. Omitting the reset configuration keeps the original route set and authenticated schema access.

| Additional route | Behavior |
|---|---|
| GET/POST `/account/reset/` | Email Form and CSRF-protected submission |
| GET `/account/reset/sent/` | Uniform request acknowledgement |
| GET `/account/reset/complete/` | Completion page and login link |
| GET/POST `/account/reset/<str:uid>/<str:token>/` | Exchange the email token for a server-side proof, then confirm the new password at `set-password` |
| GET `/api/account/reset/` | Anonymous 204 with a paired CSRF cookie and masked header |
| POST `/api/account/reset/` | JSON email request, requiring the CSRF pair and same-origin admission |
| GET `/api/account/reset/<str:uid>/` | Check the current session-bound proof and obtain a masked CSRF token |
| POST `/api/account/reset/<str:uid>/` | JSON completion with only `new_password` |

When reset is enabled, `/api/account/openapi.json` is accessible before login and also issues the anonymous CSRF pair. Existing password-change operations still require an authenticated session. `CSRFOnly` never resolves a login principal. Proof operations additionally declare `SessionCookieRequired` so generated clients send the opaque proof cookie; the reset handler validates it.

Every valid email submission receives the same Form redirect or JSON 204, including unknown, inactive or unusable accounts and delivery/infrastructure failures. This acknowledgement is not delivery confirmation or a constant-time guarantee. Private failures go to the configured reporter; sender and reporter panics cannot expose account eligibility. No automatic delivery retry occurs.

Follow the email URL in a cookie-retaining browser/client. It rotates or creates an opaque session and immediately redirects to a token-free `set-password` URL. The UID is canonical unpadded base64url for the principal ID. A raw-token POST also only establishes proof, after CSRF checks. JSON completion never accepts a raw token. Missing, expired, malformed, wrong-target or consumed proof gives an invalid-link page or JSON 403 `invalid_reset_link`. All reset responses use `Cache-Control: no-store` and `Referrer-Policy: no-referrer`.

Form confirmation applies required/mismatch/password-policy ordering without redisplaying passwords; the JSON command does not accept a confirmation field. Final proof, session and credential checks precede an atomic password/revision change, target-session revocation, proof cleanup and audit. No automatic login occurs and `last_login` stays unchanged. An existing target login session is cleared; anonymous or other-user sessions rotate with their latest non-proof payload and original absolute lifetime. Completion preserves its independent CSRF cookie. Safe API requests that omit that cookie may receive a new pair, which the client must retain before its next command.

A completion 503 `outcome_unknown` publishes no replacement session cookie and must not be automatically retried. The two-DB tests own actual rollback/unknown effects; the independent generated client additionally verifies the typed response and single-request behavior.

[ADR-0076](../../docs/adr/0076-credential-snapshots-and-session-binding.md) records the pinned Django comparison and intentional atomicity differences. [Validation evidence](../../docs/status/TEST_EVIDENCE.md) separates local, generated-client and Hosted verification. Other authentication providers and the complete UserCreationForm contract remain separate lifecycle work.
