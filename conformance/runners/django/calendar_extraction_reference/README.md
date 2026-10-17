# Calendar function reference

The observer runs pinned Django 6.1 on independent SQLite and PostgreSQL 17.10 namespaces. It reads only synthetic
`input.json` and the upstream module fingerprints in `source-verification.json`; GoDj code and expected outputs are
not inputs. Each observation owns a rollback boundary, SQL and parameter records, native replay for reads, conversion
errors and a complete physical type/value snapshot. Updates retain their actual pre-rollback storage; they are not replayed.

`Extract*`, `TruncDate`, `TruncTime` and `TruncYear/Quarter/Month/Week/Day/Hour/Minute/Second` use the current UTC compatibility
profile. Date and Time remain separate domains; the latter functions preserve their input domain and weeks begin on Monday.
The observations include ISO year/week boundaries, year 1/9999, leap days, fractional seconds, NULL, explicit-offset inputs,
composition, grouping, conditional aggregates, source-row ownership, relations, updates and corrupt external storage.
The current observer defines 95 cases. A defined case is not an executed or verified comparison; the truncation extension's
complete native comparison remains pending until recorded in the current evidence.
Invalid native values and impossible signatures record the real Django result/error; GoDj's stricter source validation
must be compared explicitly rather than replacing that reference with a preferred result.

PostgreSQL MIN/MAX do not inherit the inner Trunc expression converter. A Date aggregate can retain an aware midnight
datetime, while an explicitly UTC DateTime aggregate can retain a naive wall datetime. Serialization preserves that
distinction: naive datetimes have no offset suffix, aware datetimes use UTC, and dates remain dates. The consumer checks
the raw type, exact value, UTC query parameters and the narrow declared aggregate differences in DEV-0023; the observer
does not add an offset or replace these valid upstream results with a serializer assertion.

The three native generated-consumer owners in CI prepare the pinned runtime and run both vendors before consuming
these documents. An observer document is evidence only when its source/input/module hashes, runtime, complete cases,
physical preservation and cleanup are verified. Current execution status belongs to `docs/status/TEST_EVIDENCE.md`.

Authority: Django commit `fe0a859f537d4238cf49fca39073513206f83122`, especially
[`functions/datetime.py`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/functions/datetime.py),
[`PostgreSQL operations`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/backends/postgresql/operations.py)
and [`SQLite functions`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/backends/sqlite3/_functions.py).
Django is BSD-3-Clause; the repository's source and licensing notices apply.
