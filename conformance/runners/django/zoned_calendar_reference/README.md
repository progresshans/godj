# Explicit timezone reference

This observer uses pinned Django 6.1 and Python 3.14.3 with Python's independently distributed `tzdata==2025.3`
(IANA 2025c). The dependency lock binds its wheel; `data-source.json` additionally binds every installed TZif file,
the complete 598-name/alias inventory and package identity. It does not read the GoDj timezone archive, implementation,
expected results or runtime receipts. The existing UTC observer's value/error encoders and upstream module fingerprints
are shared as explicit, separately hashed inputs. The UTC observer's 118 cases remain unchanged.

The new input defines 11 timezone fixtures, 21 DateTime extraction/truncation functions and 7 composition, filter,
group, relation, collection and update cases per fixture. Eight separate boundary/corruption cases preserve the actual
upstream success or error, SQLSTATE, conversion stage, native replay and physical storage. That is 316 defined native
cases per backend, not a claim of completed execution or parity. Each read/update owns a rollback boundary; temporary
tables, namespace and local files are removed and checked before success is published.

Python ZoneInfo separately observes 15 UTC instants in all 598 named zones, including local range overflow and the
2400/2800 recurrence boundary. This checks the independent data/provider layer; it is not described as 598 database
query executions. PostgreSQL uses its own timezone database when Django compiles `AT TIME ZONE`. Every ordinary
fixture instant therefore records the actual PostgreSQL offset and its pinned Python counterpart. A mismatch fails
the observation after retaining its full document, rather than treating a server data difference as a product result.
GoDj's query execution uses its embedded rules and does not depend on the PostgreSQL data installation.

`Trunc*` native replay retains the naive wall value. Separate ZoneInfo `fold=0` conversion details preserve the exact
offset and resulting instant, including missing local times. Direct Django conversion and SQL UPDATE assignment are
observed separately: an UPDATE of a naive truncated wall value may store it under the database connection's UTC zone.
The consumer must verify this explicit upstream difference, not call the results equal or overwrite the observation.

Run through the frozen `conformance/reference/related-expressions` environment. The native CI owner records both
`zoned-django61-sqlite.json` and `zoned-django61-postgres.json` alongside the existing calendar reference artifacts.
Source binding, negative controls and native consumer comparisons remain required; current execution/acceptance status
belongs to [TEST_EVIDENCE](../../../../docs/status/TEST_EVIDENCE.md).

Sources: [Python tzdata 2025.3](https://pypi.org/project/tzdata/2025.3/),
[pinned Python data source](https://github.com/python/tzdata/tree/d14cebc1f27c90ca244dac9f12327fffc14d5cf9),
[Django DateTime functions](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/functions/datetime.py).
Python tzdata is Apache-2.0 with public-domain IANA data; Django is BSD-3-Clause. Neither dependency is copied into a GoDj binary.
