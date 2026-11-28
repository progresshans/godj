# Duration input provenance

- derived: true (grammar expressions in `duration.go`)
- Source: Django 6.1, commit `fe0a859f537d4238cf49fca39073513206f83122`, `django/utils/dateparse.py`
- Symbols: `standard_duration_re`, `iso8601_duration_re`, `postgres_interval_re`
- Copyright: Django Software Foundation and individual contributors
- License: [BSD-3-Clause](../../LICENSE.django)
- Modifications: Go positional captures and Unicode decimal ranges, no lookahead, explicit trailing newline, and Go normalized Duration output.

The independent reference runner and its Go-specific fixtures observe public APIs; they do not copy upstream test fixtures or assertions.
The floating component accumulator is independently implemented to match observed `datetime.timedelta` behavior; CPython is a behavioral reference.
This product helper has no separate registered conformance contract. Its derived grammar provenance is recorded here beside the implementation.
