# Independent project development observations

`observe.py` creates a temporary project with public `django startproject` and
`startapp`, explicitly installs two authored apps, and uses real management
commands, SQLite and HTTP. It does not import GoDj, inspect Go tests or consume
expected results. `source-verification.json` binds 15 selected installed modules
to upstream commit `fe0a859f537d4238cf49fca39073513206f83122` (BSD-3-Clause).
This is a module inventory, not a whole-wheel identity. Fixtures are original
synthetic project code; no upstream implementation is copied into them.

Public references are Django 6.1's [management commands](https://docs.djangoproject.com/en/6.1/ref/django-admin/),
[custom commands](https://docs.djangoproject.com/en/6.1/howto/custom-management-commands/),
[app initialization](https://docs.djangoproject.com/en/6.1/ref/applications/),
and [development server](https://docs.djangoproject.com/en/6.1/ref/django-admin/#runserver).
The observer requires the pinned polling environment without Watchman. It owns
bounded process groups and output readers, verifies cleanup, and uses isolated
HTTP cookie jars with redirects and ambient proxies disabled.
An authored logging handler records the pinned reloader's debug messages for
the two edited source files. Each edit waits for that process to observe the
current file timestamp, including after a syntax error. HTTP readiness or an
error traceback alone does not establish that filesystem watching has begun.
The observer does not patch the reloader or read its private state. A separate
regression control delays the authored log sink before the failed child's first
snapshot and still requires the complete actual HTTP and database results.

`input.json` names nine comparison boundaries. Actual results include process
changes, source failure availability, old/new stored values after explicit
migration, single-run behavior, restart, an existing authenticated session's
group membership and current grant, CSRF denial, and a command's confirmed save
before its error. Model reload is observed before checking that no migration
happened implicitly. PIDs, paths and timestamps identify live processes but are
removed from comparable results. Source failure has one explicitly bounded
[native difference](../../../../docs/DEVIATIONS.md#dev-0027--실패한-개발-서버-후보와-이전-앱의-유지).

Bootstrap observations additionally record preserved source on duplicate project
creation, explicit app installation, a DB-free check and the order/model lookup
of public `AppConfig.ready` calls. Go's explicit dependency/resource ownership
and generated model bootstrap require its native scaffold and lifecycle tests;
Python import and app registry internals are not implementation requirements.

The Python suite executes two hash seeds, checks source/case completeness, and
mutates real child HTTP responses to prove the observer depends on runtime
output. Its raw log contains `DJANGO_DEVELOPMENT_OBSERVATION`. Native
[`runserverproduct`](../../../runserverproduct/) consumers independently read the
same input bytes and emit `GODJ_DEVELOPMENT_OBSERVATION` from Notes, Article and
the scaffold's project command. The no-reload phase also audits Go invocations
so a slow rebuild cannot be mistaken for disabled watching.

The mandatory `project-reference-comparison` CI job compares those observations
and the six shared [event projections](../event_reference/) across all 12 native
OS/architecture/mode coordinates. It downloads artifacts from the same run,
checks every member checksum, source/tree/run/attempt and runtime identity,
revalidates full discovery ownership and mandatory phases, and requires exactly
one observation from each successful test. Missing, skipped, duplicated, stale
or truncated evidence fails. Shared results must match exactly; only the closed
source-failure difference is accepted. This job reads results and does not run
the product or reference suites again.

Current execution status belongs to [TEST_EVIDENCE](../../../../docs/status/TEST_EVIDENCE.md).
Writing or compiling this observer is not independent comparison success.
