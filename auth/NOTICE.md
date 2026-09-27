# Credential and login lifecycle reference

The credential lifecycle is authored against observable behavior in Django 6.1,
commit `fe0a859f537d4238cf49fca39073513206f83122`, particularly
`django/contrib/auth/hashers.py`, `base_user.py`, `backends.py`, `forms.py` and `__init__.py`.
Django is copyright (c) Django Software Foundation and individual contributors,
under the [BSD-3-Clause license](../LICENSE.django).

The independent [observer](../conformance/runners/django/unusable_password_reference.py)
runs the installed Django code with private SQLite/PostgreSQL databases. Its
fixtures record upstream file hashes. No GoDj source or expected fixture produces
those reference observations.

GoDj reserves the `!` prefix for deliberate unusable-password values. Its marker
contains 32 fresh random bytes encoded as URL-safe base64, not Django's internal
40-character suffix format. The marker remains opaque credential material and
changes the session stamp on every replacement. `PasswordHasher` owns usable
hashes only; credential authenticators perform dummy verification for unusable
passwords and always deny password authentication. Corrupt/unsupported usable
hashes remain explicit execution errors, as required by the stored-credential
contract; they are not silently treated as deliberate disablement.

`IsPasswordUsable` and the public profile flag classify this representation;
they do not verify a hash or decide account admission. A Go zero/empty encoding
is false (and rejected for a stored credential), rather than adopting Python's
`None`/empty-string input conventions. Account activation and supported hash
algorithms remain separate checks.

The independent [login observer](../conformance/runners/django/login_lifecycle_reference.py)
also records the pinned auth signal, models, backend, Admin authentication form,
and session middleware/backend source hashes. Its native HTTP observations cover
last-login timing, rejection, account switching and persistence failures.
GoDj always rotates a re-login key and couples durable identity/session writes in
one transaction with current-credential admission. These intentional differences
are recorded in [ADR-0076](../docs/adr/0076-credential-snapshots-and-session-binding.md#로그인-관찰과-세션-수립).

The independent [self-service password observer](../conformance/runners/django/password_change_reference.py)
uses the pinned native PasswordChangeView/PasswordChangeForm and session middleware.
It records unmodified persistence-failure and post-validation-race effects as well
as successful rotation and last_login invariance. GoDj's current-credential fence,
field-only patch and atomic password/session/audit transaction are intentional
strengthenings, described in ADR-0076. Product Form/API exposure is separate from
the session-bound service and Web runtime capability.

The independent [password-reset observer](../conformance/runners/django/password_reset_reference.py)
executes the pinned PasswordResetTokenGenerator, PasswordResetForm, SetPasswordForm
and in-process mail backend with private SQLite/PostgreSQL databases. It records
token invalidation/expiry/key fallback, eligible recipients, mail failures and
stale-form overwrites, with eight upstream module hashes. No network mail is sent.
This reference baseline alone does not implement GoDj password reset or establish
parity for the native HTTP reset views.
