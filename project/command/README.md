# Project commands

`command.New` takes explicit definitions and an immutable installed-app registry.
An empty `AppLabel` denotes a project command; other labels must be installed.
Duplicate names and reserved framework names are rejected. Declarations and
argument/option slices are copied; callbacks retain their own captured state.

Define positional arguments and named options with `String`, `Integer` or
`Boolean`. Optional inputs remain absent. Integer input is canonical signed
int64, booleans are `true`/`false`, and a bare boolean option means `true`.
Duplicate/unknown options, missing required values, invalid types and resource
limits fail before configuration checks or the handler. `--` ends option parsing.
`Values` accessors return copies, and diagnostic formatting redacts values.

`Execute` receives context and explicit stdin/stdout/stderr. Handler goroutines
are joined on cancellation, panic and Goexit; a handler must join its own work.
Both output proxies serialize writes and retain short/failed writes even when
the handler ignores them. Their lifetime ends when the handler returns.

`Using` connects a typed resource opener, handler and cleanup callback. Open runs
after input validation and pure checks. A returned resource is closed once,
including when Open also returns an error. Open retains cleanup responsibility
until it returns. The handler is joined before Close. Cleanup has a cooperative
ten-second context independent of invocation cancellation; it is never abandoned.
Each invocation owns a separate resource; there is no global registry or cache.

`Result.Executed` means the handler was entered. `Completed` means that handler
returned nil; output failure is recorded independently. Neither a nonzero exit
nor cleanup/cancellation means a DB transaction rolled back. Use explicit
transaction APIs when the command requires one. The framework never retries a
command. `Diagnostic` emits only a closed code; callback causes remain available
through `errors.Is`/`errors.As` for the owning application.

The starter project registers a generated-model `count-groups` command in
`internal/commands/commands.go`. Its `cmd/commands` executable calls
`compatibility.Serve` before loading configuration and `Registry.Serve` afterward.
`godj.toml` selects it with `commands_package = "./cmd/commands"`, separately from
the declaration runner. Commands may import generated models without breaking
generation from an empty checkout.

```sh
godj help
godj commands
godj help count-groups
godj check
godj check --list
godj check --resource database
godj command count-groups
godj command --project ../other/godj.toml count-groups
```

Global management invocations validate the declaration and generated bundle,
build the runtime readonly, recheck the same bundle and compare both linked
executables' [compatibility contracts](../compatibility/README.md). Then they run
the selected command with the project's original environment and inherited stdin,
forward stdout/stderr, preserve the child exit and reap owned processes.
The selected runtime package must be a real project directory distinct from the
declaration entrypoint. Existing source and database content are not auto-repaired.

List/help execute no registry checks or business callbacks. Plain `check` runs
the registered pure configuration check after source/bundle validation.
`Config.ResourceChecks` declares named optional checks with `Name`, `AppLabel`,
`Summary` and `Run(context.Context) error`. Names are unique and app owners must
be installed. The registry copies declarations; `ResourceDescriptions` returns
sorted metadata without callbacks. `check --list` displays that inventory without
running pure checks or opening resources.

Select I/O explicitly with `check --resource database`; repeat `--resource NAME`
to select more checks. `CheckResources(ctx, names...)` is the equivalent Go API.
Every name and duplicate is validated before any callback. Pure checks run first,
then selected checks in request order. Failure/cancellation stops later checks,
and each started callback is joined through its cleanup, including panic/Goexit.
`Run` owns its resources and must close them before returning. Ordinary commands
never run optional resource checks automatically.

The starter's `database` check reads the existing SQLite file or validates the
configured PostgreSQL connection/profile/schema through
[`database.Check`](../../settings/database/README.md). It never creates a missing
database, applies migrations or repairs schema. Application model/history drift
remains a separate migration concern. `CheckError{Field, Cause}` lets project code
explicitly expose a public configuration field; diagnostics validate the field's
bounded identifier/index syntax and never format `Cause`. Use values and paths
only in private application state, never as field names or check descriptions.

Unknown checks or invalid selections exit 2; failed checks exit 1. Diagnostics
identify the failing check and any explicit field while retaining causes for
`errors.Is`/`errors.As`. No check retries or runs a business callback afterward.
Commands use the operator's OS/database access. HTTP
authentication, authorization and CSRF remain in the Web application.

`Config.Logger` accepts a caller-owned `*slog.Logger`; nil disables logging.
Add project/invocation attributes with `With`. Framework observations contain
closed component/action/outcome, registered names and an independent cancellation
flag. Arguments, output, configuration values and arbitrary errors are omitted.
Checks, resource open/close and command completion use this same logger.
Sink errors, panic and Goexit do not change `Result` or the business error;
handlers and their defers are joined. The caller owns sink lifetime/concurrency
and must ensure its handler returns. Recursive logging with its supplied context
is suppressed. These logs are operational diagnostics, not a durable audit log.

The generated entrypoints enable JSON logs to their explicit stderr sink with
`GODJ_LOG_LEVEL=debug|info|warn|error`; absent or `off` disables them. Empty/invalid
values fail before resources open. Each process invocation receives a separate
ID and project/runtime attributes. Optional post-commit notifications can use
[`events`](../../events/README.md) while preserving the confirmed write result.

Django's [custom management commands](https://docs.djangoproject.com/en/6.1/howto/custom-management-commands/)
inform argument/help/output and model-using command behavior. GoDj uses explicit
registration and rejects duplicate/reserved names instead of Django's ordered
overrides. Fixed-source independent observation and remaining C1 acceptance are
tracked in [GDJ-0137](../../work/0137-project-core-startup.md).
