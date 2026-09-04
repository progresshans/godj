# Third-party notices

## Django

GoDj uses Django 6.1 as a development-time behavioral reference and test
dependency. Django is installed from PyPI by the conformance workflow and is not
part of a GoDj binary or generated source.

GoDj's conformance scenarios, including the query, write, migration, Save
lifecycle, QuerySet evaluation/cache, and migration planning fixtures, are
independently written behavioral scenarios. This also includes the
multi-migration plan execution and before-write operation/recorder failure
fixtures, the recorder-backed fresh restart planning fixtures, and the
historical project-state reconstruction and migration definition source
fixtures. Their manifest entries point to pinned Django documentation, source
symbols, and tests for provenance but do not copy or translate Django source,
fixtures, comments, or assertion structure. This notice does not classify those
scenarios as derivative works.

GDJ-0017 independently authored the migration lifecycle scenarios for fresh,
targeted, failed, and restarted execution. MIG-047..056 use GoDj-specific apps,
tables, operations, sentinels, and assertions; every provenance entry is marked
`derived=false`. The separate revision-fence feasibility harness is GoDj test
code and does not embed Django source or fixtures.

GDJ-0019 independently authored MIG-057..064 with GoDj-specific documents,
identities, operations, values, diagnostics, and assertions. The pre-release
compatibility reset rebaselined the current eight-contract corpus to the single
current format defined by Accepted ADR-0035; every current contract records
that decision as `derived=false`. The former ADR-0019 compatibility-tuple
corpus remains historical Git/evidence, not a supported input format. MIG-057
and MIG-064 additionally cite pinned Django 6.1 source or tests for observed
migration identity, graph, ordered-operation, and public executor behavior.
Those Django entries are behavioral provenance, not a claim that Django uses
GoDj's data format, and do not make the other six decision-oracle contracts
Django observations. All eight contracts retain their existing `passing`
status and registered GoDj product adapter coverage; the reference assets and
their provenance do not attribute the product format to Django.

GDJ-0021 independently authored MIG-065..074 with GoDj-specific project
selection, descriptor, flat source catalog, private runner protocol, failure,
counter, and publication observations. All ten current contracts record
Accepted ADR-0021 as `kind=decision` and `derived=false`; the definition-format
touching MIG-065..068 and MIG-073 additionally record Accepted ADR-0035 after
the current-only reset. None cites or derives from Django source, tests,
fixtures, comments, or assertion structure. The
Django-named profile, runner namespace, and oracle directory are reused only
to keep one protocol-v2 reference corpus and checksum gate. These ten
contracts retain their existing `passing` status and registered GoDj
project-check product adapter coverage; none of that claims Django provides
GoDj's descriptor, protocol, or CLI semantics.

GDJ-0023 independently authored REL-001..012 with GoDj-specific `authors` and
`blog` apps, rows, capture windows, normalized SQL-shape observations, and
mutation assertions. The scenarios cite exact Django 6.1 documentation, source
symbols, and tests only as behavioral provenance; all entries are
`derived=false` and do not copy or translate upstream source, fixture, comment,
or assertion structure. The relation fixture and oracle cover cross-app
ForeignKey metadata, forward/reverse access, nullable relations,
`PROTECT`/`SET_NULL`, `select_related`, and reverse `prefetch_related`. Their
current 12 contracts are all `passing` through registered GoDj product adapters.
That product coverage does not attribute the behavior to copied Django code and
does not claim non-SQLite backend implementation.

GDJ-0035 Phase A historically locked an independently authored, reference-only
set of 12 MIG-075..086 contracts while evaluating candidates from Proposed
ADR-0034. Those original bytes retain historical `kind=proposal`, decision ID
`GDJ-0035`, and `derived=false` provenance in Git/evidence. The current
checked-in same-ID diagnostic corpus was regenerated for the ADR-0035
current-only format and records ADR-0035 (plus ADR-0017 for MIG-086) as
`kind=decision`, `derived=false`. All twelve contracts remain `oracle_locked`
and unregistered, so this rebaseline is neither Django parity nor product
publication. Pinned Django BSD 3-Clause source and test references are limited
to the portions actually observed. GoDj scenarios, fixtures, payloads, and
assertions are written independently and do not copy or translate Django
source, fixtures, comments, or assertion structure. Any future copied,
translated, or adapted expression requires separate derived-work provenance and
file-level notices. EVID-085 and EVID-086 retain the original local and hosted
Phase A proof; they do not prove the current reset bytes.

GDJ-0014 connected a GoDj product adapter to the existing recorder-restart
scenarios and changed their implementation status; it did not add a new
upstream-derived scenario corpus. GDJ-0015 independently authored the
historical-state scenarios with GoDj-specific apps, models, tables, values, and
assertions. Their pinned Django source and test symbols record behavioral
provenance only; MIG-037..046 are all marked `derived=false`.

Django is licensed under the BSD 3-Clause license. A copy is included in
[`LICENSE.django`](LICENSE.django). If a future file copies, translates, or
adapts upstream expression, that file must be marked as derived in the contract
manifest and carry source commit, path, symbol, modification, and license
metadata close to the file.

Django is a trademark of the Django Software Foundation. GoDj is an independent
project and is not endorsed by the Django Software Foundation.

## modernc.org/sqlite and modernc.org/libc

GoDj's M1/M2 SQLite backend directly uses `modernc.org/sqlite v1.56.0`, whose
locked dependency graph includes `modernc.org/libc v1.74.4`. Both packages are
licensed under BSD 3-Clause terms. Their upstream license texts are preserved
as [`LICENSE.modernc-sqlite`](LICENSE.modernc-sqlite) and
[`LICENSE.modernc-libc`](LICENSE.modernc-libc).

These notices cover the two selected packages, not a completed audit of every
transitive dependency in a future distributed binary. GoDj has not selected
its own distribution license yet, and no release artifact should be published
until the full dependency notice set and root project license are approved.

## pgx

GoDj's GDJ-0038 PostgreSQL backend directly uses
`github.com/jackc/pgx/v5 v5.10.0` through its `database/sql` bridge. pgx is
licensed under the MIT License; its upstream license text is preserved as
[`LICENSE.pgx`](LICENSE.pgx).

The selected module graph also includes `pgpassfile`, `pgservicefile`, and
`puddle` under MIT terms and `golang.org/x/sync` and `golang.org/x/text` under
BSD 3-Clause terms. This notice records the implementation dependency; it is
not the complete binary-distribution audit required before a release. GoDj's
own distribution license and the full transitive notice set remain release
gates.
