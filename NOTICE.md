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
identities, operations, values, diagnostics, and assertions. Every contract
records the Accepted ADR-0019 decision as `derived=false`; this is the
provenance for GoDj's strict JSON format, compatibility tuple, closed codec,
canonical digest, atomic publication, and failure precedence. MIG-057 and
MIG-064 additionally cite pinned Django 6.1 source or tests for observed
migration identity, graph, ordered-operation, and public executor behavior.
Those Django entries are behavioral provenance, not a claim that Django uses
GoDj's data format, and do not make the other six decision-oracle contracts
Django observations. MIG-064 is reference-only and does not claim a GoDj
product loader, adapter, or CLI implementation.

GDJ-0021 independently authored MIG-065..074 with GoDj-specific project
selection, descriptor, flat source catalog, private runner protocol, failure,
counter, and publication observations. Every contract records Proposed
ADR-0021 as `kind=decision` and `derived=false`; none cites or derives from
Django source, tests, fixtures, comments, or assertion structure. The
Django-named profile, runner namespace, and oracle directory are reused only
to keep one protocol-v2 reference corpus and checksum gate. These ten
`oracle_locked` decision contracts do not claim that Django provides GoDj's
descriptor or protocol, or that GoDj already has a product project-check CLI.

GDJ-0023 independently authored REL-001..012 with GoDj-specific `authors` and
`blog` apps, rows, capture windows, normalized SQL-shape observations, and
mutation assertions. The scenarios cite exact Django 6.1 documentation, source
symbols, and tests only as behavioral provenance; all entries are
`derived=false` and do not copy or translate upstream source, fixture, comment,
or assertion structure. The relation fixture and oracle cover cross-app
ForeignKey metadata, forward/reverse access, nullable relations,
`PROTECT`/`SET_NULL`, `select_related`, and reverse `prefetch_related`. Their
`oracle_locked` status does not claim a GoDj ForeignKey product API, relation
adapter, or non-SQLite backend implementation.

GDJ-0035 Phase A locally locked an independently authored, reference-only set of
12 MIG-075..086 contracts while evaluating candidates from Proposed ADR-0034.
Until that decision is accepted, GoDj-owned relation-migration tuple, digest,
state, preflight, and SQLite DDL payloads use `kind=proposal`, decision ID
`GDJ-0035`, and `derived=false`; they are not described as Django parity or as
accepted GoDj behavior. Pinned Django BSD 3-Clause source and test references
are limited to the portions actually observed. GoDj scenarios, fixtures,
payloads, and assertions are written independently and do not copy or translate
Django source, fixtures, comments, or assertion structure. Any future copied,
translated, or adapted expression requires separate derived-work provenance and
file-level notices. Local EVID-085 records the measured artifact and test locks;
hosted exact-head verification of the Phase A tree is still pending.

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
