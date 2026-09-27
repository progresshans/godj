# Ordinary accounts

`identity/account` provides login/logout and self-service password changes over an explicitly configured `web/sessionauth.Runtime`.
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

[ADR-0076](../../docs/adr/0076-credential-snapshots-and-session-binding.md) records the pinned Django comparison and intentional atomicity differences. [Validation evidence](../../docs/status/TEST_EVIDENCE.md) separates local, generated-client and Hosted verification. Password reset and other authentication providers remain separate lifecycle work.
