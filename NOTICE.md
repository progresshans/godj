# Third-party notices

## Django

GoDj uses Django 6.1 as a development-time behavioral reference and test
dependency. Django is installed from PyPI by the conformance workflow and is not
part of a GoDj binary or generated source.

GoDj's conformance scenarios, including the query, write, migration, Save
lifecycle, and QuerySet evaluation/cache fixtures, are independently written
behavioral scenarios. Their manifest entries point to pinned Django
documentation, source symbols, and tests for provenance but do not copy or
translate Django source, fixtures, comments, or assertion structure. This
notice does not classify those scenarios as derivative works.

Django is licensed under the BSD 3-Clause license. A copy is included in
[`LICENSE.django`](LICENSE.django). If a future file copies, translates, or
adapts upstream expression, that file must be marked as derived in the contract
manifest and carry source commit, path, symbol, modification, and license
metadata close to the file.

Django is a trademark of the Django Software Foundation. GoDj is an independent
project and is not endorsed by the Django Software Foundation.

## modernc.org/sqlite and modernc.org/libc

GoDj's M1 SQLite backend directly uses `modernc.org/sqlite v1.56.0`, whose
locked dependency graph includes `modernc.org/libc v1.74.4`. Both packages are
licensed under BSD 3-Clause terms. Their upstream license texts are preserved
as [`LICENSE.modernc-sqlite`](LICENSE.modernc-sqlite) and
[`LICENSE.modernc-libc`](LICENSE.modernc-libc).

These notices cover the two selected packages, not a completed audit of every
transitive dependency in a future distributed binary. GoDj has not selected
its own distribution license yet, and no release artifact should be published
until the full dependency notice set and root project license are approved.
