# Credential and unusable-password reference

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
