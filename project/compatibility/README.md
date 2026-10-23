# Linked framework compatibility

`Current()` reports the effective framework identity from Go build metadata and
the exact Schema IR, project-bundle, generator-roster and private-command
contracts compiled into the executable. The generator token hashes the same
roster used in project snapshots and manifests. It is not a source integrity hash.

`Serve` answers only its exact metadata probe, without settings, app callbacks,
declaration loaders or backend opening. Call it at the beginning of an entrypoint.
`project.Run` also handles this probe before dispatching private operations.
`Parse` bounds the response and rejects unknown/duplicate keys, malformed strings,
unknown module identities and incomplete contract information.

`Check` requires identical contracts. Effective fixed-module paths and versions
must also match exactly. Two explicitly identified development/local-replacement
builds may agree on contracts; their source bytes are not thereby attested equal.
Unknown identities and mixed development/fixed-module pairs are rejected.
Display-only version reporting remains in [`version`](../../version/README.md).

The global `commands`, custom `help`, `check` and `command` paths probe the
declaration runner and the command runtime before any command handler. Generated
source also passes the existing source/bundle checks before and after runtime
compilation. A successful metadata comparison alone does not prove generated
files are current or validate stored database state. No upgrade or repair occurs.

C1 still owns extending this common diagnosis to all existing mutation/server
entrypoints and independently installed fixed modules. Existing private command
protocol validation remains active; it is not counted as completing those new
acceptance cases. See [GDJ-0137](../../work/0137-project-core-startup.md).
