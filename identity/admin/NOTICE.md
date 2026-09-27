# Input validation provenance

`forms.go` implements the observable grammar of Django 6.1 `UsernameField`,
`UnicodeUsernameValidator`, `EmailValidator` and password confirmation. The
grammar, including character ranges for quoted email local parts, is adapted
from Django commit `fe0a859f537d4238cf49fca39073513206f83122`,
`django/contrib/auth/forms.py`, `django/contrib/auth/validators.py` and
`django/core/validators.py`, under the [BSD-3-Clause license](../../LICENSE.django).
Copyright (c) Django Software Foundation and individual contributors.

The [observer](../../conformance/runners/django/identity_admin_reference.py)
executes those installed Django objects with independently authored synthetic
inputs. It records input and upstream source hashes. The checked-in reference
uses CPython 3.14.3 / Unicode 16. The implementation uses the pinned
[Unicode 16 profile](../../internal/unicode16/NOTICE.md), independently generated
from official UCD data. Outlined Latin, Todhri and new decimal digits are part
of the regular input corpus. Built-in password strength policies and their separately licensed source data
are documented in [Password validation](../PASSWORD_VALIDATION.md). Hosts choose
the policy explicitly; confirmation remains a separate form requirement.

The creation form narrows username input to 150 code points; editing retains
the User Schema IR's 256-code-point storage limit. The credential and private
CLI transport allow 1,024 UTF-8 bytes, with model character bounds checked
separately before persistence. This explicit GoDj storage profile does not
claim Django's default User schema or complete UserCreationForm parity.
