# Explicit timezone reference

This observer uses pinned Django 6.1 and Python 3.14.3 with an independent build of the official IANA 2025c
`tzcode` and `tzdata` release archives. `prepare_data.py` checks both archive hashes, compiles `posix_only` with
`PACKRATDATA=backzone`, `PACKRATLIST=zone.tab`, `CFLAGS=-DSTD_INSPIRED` and `AWK=gawk`, and verifies all 598 TZif
files plus the compiled `tzdata.zi` source against `data-source.json`. The complete build receipt, log, source identities,
compiler/GNU awk identities and output file hashes remain bound to the observation. Release timestamps are preserved.
GNU awk is required because upstream sorts complete zone blocks only with that implementation; the compact source has
one exact ordering and hash across these reference builds. Existing output directories are never replaced.
Python ZoneInfo reads only this verified directory. Neither preparation nor observation reads the GoDj timezone archive,
implementation, expected results or runtime receipts. The existing UTC observer's encoders and upstream module fingerprints
are explicit, separately hashed inputs. Its 118 cases remain unchanged.

An IANA release number alone does not identify historical rules: Python's `tzdata==2025.3` wheel uses a different
backzone policy and 95 of its 598 TZif files differ from the product profile. That wheel is not used as an equivalent
data oracle. The independent official-source build retains the full historical policy; differences are not waived.

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

Prepare the data with `python3 prepare_data.py --output <new-directory>`, then run the observer through the frozen
`conformance/reference/related-expressions` environment with `--data-directory <new-directory>`. The native CI owner records both
`zoned-django61-sqlite.json` and `zoned-django61-postgres.json` alongside the existing calendar reference artifacts.
Source binding, negative controls and native consumer comparisons remain required; current execution/acceptance status
belongs to [TEST_EVIDENCE](../../../../docs/status/TEST_EVIDENCE.md).

Sources: [IANA tzcode 2025c](https://data.iana.org/time-zones/releases/tzcode2025c.tar.gz),
[IANA tzdata 2025c](https://data.iana.org/time-zones/releases/tzdata2025c.tar.gz),
[Go's documented IANA build policy](https://github.com/golang/go/blob/c19862e5f8415b4f24b189d065ed739517c548ba/lib/time/update.bash),
[Django DateTime functions](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/functions/datetime.py).
IANA's release archives retain their own LICENSE and public-domain notice; Django is BSD-3-Clause.
The development build and Python/Django implementation are not copied into a GoDj binary.
