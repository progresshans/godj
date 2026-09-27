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
uses CPython 3.14.3 / Unicode 16; Go's Unicode and x/text normalization tables
are version 15. Three `version_probes` record known differences and are outside
the presently verified form subset. Full Unicode-version parity and built-in
password strength validators remain pending, distinct from the configurable
password policy hook and password confirmation.

The form narrows username input to 150 code points. The manager still applies
the existing credential profile's 256-byte UTF-8 limit before and after NFKC;
therefore this input subset is not full Django UserCreationForm parity. That
credential boundary must be reconciled together with Unicode-version support.
