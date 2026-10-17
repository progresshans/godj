# Mail behavior references

The independent email observer uses Django 6.1, commit
`fe0a859f537d4238cf49fca39073513206f83122`, particularly `django/core/mail`,
`message.py` and the `locmem.py`/`smtp.py` backends. Django is copyright
(c) Django Software Foundation and individual contributors, licensed under
[BSD-3-Clause](../LICENSE.django).

[mail_reference.py](../conformance/runners/django/mail_reference.py) runs the
installed native implementation without importing GoDj or its expected fixture.
[django61.json](testdata/django61.json) records four upstream Django module
hashes and two Python email module hashes. Python is licensed under the
[PSF License](https://docs.python.org/3/license.html). These are independently
observed behavior fixtures, not transplanted Django or Python implementation code.

Go's `net/mail`, `mime`, `net/smtp`, `crypto/tls` and `net/textproto` own their
protocol primitives. IDNA uses `golang.org/x/net/idna` v0.57.0 with the explicit
transitional lookup profile; x/net and x/text are Go sub-repositories under
their BSD-style licenses distributed in the pinned Go modules.

GoDj's immutable bounded values, contextual I/O, no automatic SMTP downgrade or
retry, SMTPUTF8 capability check, all-recipient admission and explicit final
DATA outcome intentionally differ from Python's object/API conventions.
[ADR-0078](../docs/adr/0078-mail-message-ownership-and-delivery.md) specifies
these choices; the fixture does not claim full Django mail backend parity.
