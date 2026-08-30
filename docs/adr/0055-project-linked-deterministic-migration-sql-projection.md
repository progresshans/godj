# ADR-0055: Project-linked Deterministic Migration SQL Projection

- 상태: Proposed
- 날짜: 2026-08-31
- 관련 work/contract: GDJ-0054, MIG-129..MIG-138, Q-010, Q-012
- 대체하는 ADR: 없음

## 맥락

Completed GDJ-0049..0052는 migration definition 생성, 상태 조회와 latest/exact target 실행·plan을 제공합니다. 그러나
사용자가 exact migration의 current forward SQL을 database를 열지 않고 검토하는 표준 경계는 없습니다. Existing
`Executor.Plan`은 applied history와 revision-fenced session을 읽는 semantic execution plan이고 `SchemaEditor`는 실제 mutation
port이므로 둘 중 어느 것도 DB-free `sqlmigrate`의 올바른 abstraction이 아닙니다.

Django 6.1의 `sqlmigrate`는 connection/schema editor를 사용하고 prefix/backwards와 conditional transaction wrapper를
제공합니다. GoDj는 pinned Django의 overlapping observable SQL meaning을 reference로 삼되 exact-name, forward-only,
built-in DB-free, wrapper-free profile을 독립적으로 결정해야 합니다.

## 결정 기준

- Complete definition load, exact target identity, target-before historical state와 immutable backend dialect/profile/schema가 SQL
  projection의 migration-domain input
- Execution compiler와 SQL type/identifier/default/relation meaning 공유
- Database opener/history/transaction/editor와 credential zero
- Migration identity와 forward direction을 잃지 않는 backend request
- Fail-closed typed-nil/unsupported/malformed renderer와 raw cause/secret redaction
- Bounded deterministic output와 truthful one-write semantics
- SQLite/PostgreSQL built-in과 repository-external project configuration의 같은 public contract

## 고려한 선택지

### A. `Executor.Plan` 또는 live backend의 dry-run을 재사용

Applied history/session/lock을 읽고 execution capability를 요구하므로 exact definition의 DB-free projection이 아닙니다. Live
state에 따라 결과와 failure precedence가 달라지고 credential-free command라는 경계도 깨집니다.

### B. Mutation `SchemaEditor`에 SQL collector mode를 추가

Mutation port의 transaction/recorder ownership과 collector 의미가 섞입니다. Third-party editor가 no-I/O라는 성질도 보장할 수
없고 execution side effect를 실수로 호출할 표면이 커집니다.

### C. Pure forward materializer와 별도 identity-bearing renderer port

Root가 loaded catalog와 historical before-state에서 exactly-one forward request를 만들고, backend renderer가 current static
support와 shared compiler를 통해 statement body만 반환합니다. Global owner가 결과를 재검증하고 canonical bytes를 한 번
게시합니다.

## 제안 결정

선택지 C를 제안합니다.

1. Public command는 exact `godj sqlmigrate APP EXACT_NAME`과 optional trailing `--project PATH` 두 형태뿐입니다.
   `zero`는 literal name이며 prefix/latest/reverse/app-zero 의미가 없습니다.
2. Complete source load, graph/chronology validation과 exact target lookup은 renderer availability/config failure보다 먼저입니다.
3. Root는 target dependency-before historical state에서 ordered operations를 deep-clone해 exactly-one forward intent를
   materialize합니다.
4. Backend port는 migration `{App,Name}`과 `MigrationIntent`를 함께 받는 forward-only request입니다. Bare intent,
   `AppliedMigration`과 public `HistoryTransition`은 사용하지 않습니다.
   Private apply-validator sharing은 허용하되 PostgreSQL recorder-only 255-byte identity limit을 renderer에 유출하지 않고
   loader-owned current identity bounds를 따릅니다.
5. Existing execution `MigrationCapabilities`, `Executor.Plan`, lifecycle capability preflight, backend opener/session/history/
   transaction/recorder와 mutation `SchemaEditor`는 SQL projection에 재사용하지 않습니다.
6. Built-in renderer는 execution compiler helper를 공유하고 backend-specific static representability를 fail-closed합니다.
   SQLite는 zero-config, PostgreSQL은 immutable validated schema-only config이며 URL/credential/handle을 받지 않습니다.
   PostgreSQL constructor는 error를 조기 반환하지 않고 valid/invalid immutable renderer를 반환해 complete-load/target precedence를
   보존합니다. Invalid raw schema text는 diagnostic에 보존하지 않습니다.
7. `project.Config`의 direct renderer가 command authority입니다. Supported built-in project는 one normalized selection에서
   opener/renderer를 파생하지만 custom coherence를 framework가 증명한다고 주장하지 않습니다.
   Article configuration도 environment/profile을 한 번 snapshot한 같은 immutable selection에서 둘을 파생하고 `sqlmigrate`는
   opener나 저장된 opener error를 관찰하지 않습니다.
8. Renderer는 operation당 exact one semicolon-free body를 반환합니다. Root는 2,048 statements/16 MiB candidate cap,
   valid UTF-8, nonempty, whitespace/control/canonical body와 exact cardinality를 검증하고 각 body를 복사합니다.
9. Global layer만 `;\n`을 붙여 전체 bytes를 buffer한 뒤 stdout에 한 번 write를 시도합니다. Empty는 write zero입니다.
   Short/error는 prefix를 노출할 수 있고 retry나 두 번째 stderr publication을 하지 않으므로 OS-atomic이라고 부르지 않습니다.
10. Logical failure는 SQL zero bytes와 stable SQL category/code만 반환합니다. Raw renderer error, partial SQL, definition/source,
    URL/credential와 child stderr를 `Error`, `Unwrap`, protocol, log/artifact에 보존하지 않습니다.
11. Context cancellation은 root/built-in의 bounded checkpoints에서 cooperative하게 처리합니다. Arbitrary custom renderer를
    강제 중단한다고 주장하지 않습니다.
12. DB-free는 framework/built-in의 opener/session/history/recorder/transaction/editor와 credential/handle zero만 뜻합니다.
    Project build/module fetch/user init/custom renderer I/O까지 offline/sandboxed라는 뜻이 아니며 live schema/data/profile,
    applied state, transaction atomicity와 execution success를 증명하지 않습니다.
13. Exported `project.Config` field 추가는 first externally supported release 전 current-only source change입니다. Keyed/unkeyed
    external literal impact와 constructor assignability를 별도 compile contract로 검증합니다.
14. Root는 complete load/graph/chronology/target/materialization 뒤 renderer nil/typed-nil/configuration을 검사하고 renderer를
    정확히 한 번 호출합니다. Request와 returned strings는 모두 detached clone이며 built-in renderer value는 immutable/race-safe입니다.
15. Current scope는 declarative before-state만으로 SQL body가 결정되는 CreateModel/AddField입니다. Required AddField를
    projection해도 live table-empty/cardinality preflight나 적용 가능성을 주장하지 않으며 실제 execute가 fresh live preflight를
    계속 소유합니다. Physical catalog에서 remake plan을 만들어야 하는 SQLite ForeignKey RemoveField는 stable capability failure와
    SQL zero bytes로 닫습니다.
16. Private wire의 JSON worst-case escape를 포함한 response hard cap 후보는 101 MiB입니다. Public exit 후보는 invalid
    argv/identity `2`, renderer unavailable/render failed/invalid rendered SQL `3`, capability/resource limit `1`이며 Phase B 전에
    closed taxonomy test로 확정합니다.

## 결과

- SQL review가 live applied history나 database credential 없이 exact definition bytes에서 결정됩니다.
- Execution SQL과 renderer SQL이 compiler helper를 공유하므로 backend meaning drift를 줄입니다.
- Mutation/lifecycle interfaces의 authority를 넓히지 않고 third-party renderer에는 명시적 cooperative contract를 둡니다.
- Output publication은 logical all-or-nothing이지만 terminal write failure의 physical prefix는 복구할 수 없습니다.
- PostgreSQL schema configuration은 command construction에 필요하지만 server/catalog 존재 여부는 확인하지 않습니다.

## Accepted 전 확인할 것

- Pinned Django 6.1 source/runtime에서 exact overlapping observation과 intentional difference를 고정했는가
- Exported type/package/function 이름과 zero-invalid request construction이 external module에서 자연스러운가
- PostgreSQL invalid schema configuration의 precedence가 complete-load-before-render 규칙과 일치하는가
- Shared compiler extraction이 execution SQL bytes/semantics를 바꾸지 않는가
- Error taxonomy가 raw custom cause와 partial SQL을 `Error`/`Unwrap`/wire 어디에도 보존하지 않는가
- Exact limits, cancellation, typed-nil, parallel determinism, write short/error와 no-DB traces가 product test로 관찰되는가
- Current CreateModel/AddField compiler drift canary, required AddField live-preflight non-claim과 SQLite physical-catalog remake
  rejection이 execution authority를 과장하지 않는가

## 의도적으로 결정하지 않은 것

- Reverse/prefix/latest/app-only/multiple target SQL과 transaction wrapper
- Applied/live schema-aware executability, explain/optimizer와 destructive/custom/data/raw SQL operation
- Multi-DB, MySQL/Windows와 arbitrary third-party renderer discovery
- Custom renderer의 no-I/O/coherence 강제와 OS-level atomic stdout
- General migration semver/upgrade/repair/adoption, Q-010/Q-012의 broader resolution
