# Linked framework compatibility

`Current()` reports the effective framework identity from Go build metadata and
the exact Schema IR, project-bundle, generator-roster and private-command
contracts compiled into the executable. The generator token hashes the same
roster used in project snapshots and manifests. It is not a source integrity hash.

`Serve` answers only its exact metadata probe, without settings, app callbacks,
declaration loaders or backend opening. Call it at the beginning of an entrypoint.
`project.Run` also handles this probe before dispatching private operations.
`Parse` bounds the response and rejects unknown/duplicate keys, malformed strings,
unknown module identities and incomplete contract information. One trailing LF is
permitted as the frame terminator; other noncanonical whitespace is rejected.

`Check` requires identical contracts. Effective fixed-module paths and versions
must also match exactly. Two explicitly identified development/local-replacement
builds may agree on contracts; their source bytes are not thereby attested equal.
Unknown identities and mixed development/fixed-module pairs are rejected.
Display-only version reporting remains in [`version`](../../version/README.md).

All project-linked global commands probe the declaration executable immediately
following its readonly build. `runserver` and management commands also probe their
separate generated runtime before starting it. `createsuperuser` performs its
probe before checking the terminal or reading credentials. `startproject` verifies
the candidate declaration, HTTP and command binaries before publishing sources.

The same private executable is probed and subsequently invoked. Each probe has a
15-second deadline and bounded output; its process group and drainers are joined
before accepting its response. The global report counts metadata probes and their
responses separately, and includes their children in the total reap count.
Failures use the closed `project_compatibility_error` vocabulary, except management
commands retain their `project_command_error` envelope. Raw probe output is not
republished. No command is retried or automatically upgraded/repaired.

Call `Serve` before configuration or main-level instrumentation. Starting any Go
executable still runs its package initializers; the probe cannot suppress those.
Resource opening and registration therefore belong to explicit callbacks, not
package `init`. The SQL consumer records both real initializer processes and
checks their cleanup separately from the single renderer invocation.

Generated source still passes the existing source/bundle checks before and after
runtime compilation. A successful metadata comparison does not attest identical
source bytes, current generated files or stored database state. Native conformance
adapters use an explicit fixture launcher identity; independently installed tool
and runtime identities require real module/binary acceptance.

C1 still owns independently installed fixed-module combinations and whole-platform
acceptance. Existing private command response validation remains active and never
accepts a global compatibility failure from a linked operation. See
[GDJ-0137](../../work/0137-project-core-startup.md).
