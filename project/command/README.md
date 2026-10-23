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

List/help execute no registry checks or business callbacks. `check` runs only the
registered pure configuration check, after source/bundle validation. Resource
opening is an explicit command action; automatic migrations and account creation
are never part of it. Commands use the operator's OS/database access. HTTP
authentication, authorization and CSRF remain in the Web application.

Django's [custom management commands](https://docs.djangoproject.com/en/6.1/howto/custom-management-commands/)
inform argument/help/output and model-using command behavior. GoDj uses explicit
registration and rejects duplicate/reserved names instead of Django's ordered
overrides. Fixed-source independent observation and remaining C1 acceptance are
tracked in [GDJ-0137](../../work/0137-project-core-startup.md).
