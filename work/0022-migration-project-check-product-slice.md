---
id: GDJ-0022
status: active
updated: 2026-08-10
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "f7fbbd50465a610ed9492227909eece524455f15"
depends_on: ["GDJ-0021"]
contracts: ["MIG-065..MIG-074", "Q-010", "Q-012"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "go.mod"
  - "go.sum"
  - "cmd/godj/**"
  - "project/**"
  - "internal/projectcheck/**"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/project_external_consumer.go.txt"
  - "conformance/README.md"
  - "conformance/contracts/migration-project-check-manifest.json"
  - "conformance/runners/django/tests/test_migration_project_check_scenarios.py"
  - "conformance/runners/godj/migration_project_check_scenarios.go"
  - "conformance/runners/godj/runner.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/migration_definition_source_artifacts_test.go"
  - "conformance/internal/protocol/migration_state_reconstruction_artifacts_test.go"
  - "conformance/internal/protocol/migration_lifecycle_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0022-project-runtime-and-global-migration-check.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0022-migration-project-check-product-slice.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Project-Linked Migration Check Product Slice

## 사용자에게 보이는 결과

완료 후 Unix 사용자는 프로젝트 또는 그 하위 디렉터리에서 다음 둘 중 하나를 실행할 수 있습니다.

```text
godj migrations check
godj migrations check --project <descriptor-file>
```

명령은 DB를 열거나 migration을 적용하지 않고 exact `godj.toml`이 가리키는 project package를 private
runner로 build합니다. Linked project code가 명시한 flat migration-definition roots를 안전하게 읽고
existing `migrations/definition.Load`로 검증한 뒤, 성공 시 source/definition count와 canonical digest를
stdout의 JSON 한 줄로 돌려줍니다. 실패는 ADR-0021의 closed category/code 한 줄과 exit `1`, `2`, `3`
또는 handled Unix SIGINT의 `130`으로 구분합니다.

Project entrypoint는 다음 최소 public API를 명시적으로 호출합니다.

```go
err := project.Run(
	ctx,
	project.Config{MigrationDefinitionRoots: []string{"migrations"}},
	os.Args[1:],
	os.Stdin,
	os.Stdout,
)
```

`project.Run`은 direct public project command dispatcher가 아닙니다. Exact private runner argv만 처리하며,
normal project command, `serve`, `migrate`, generator 또는 custom command 표면은 이번 작업에서 만들지
않습니다.

## 목표

- Accepted ADR-0021의 exact descriptor/project selection/build/protocol/discovery/exit/cancel 의미를 독립
  제품 코드로 구현
- `cmd/godj`에 exact 두 `migrations check` argv를 가진 실제 global CLI 추가
- Public `project.Config`와 context/error/I/O 전달형 `project.Run` entrypoint 추가
- Linked flat discovery가 safe `[]definition.Source`를 만들고 `definition.Load`를 정확히 한 번 호출
- Global/linked/protocol package 방향을 고정하고 `migrations/definition`의 zero-I/O pure loader 경계 보존
- MIG-065..074의 열한 번째 actual GoDj adapter를 actual product data로 구현하고 10개를 `passing`으로 전환
- 제품 집계를 11 adapter/115 contract의 `110 passing + 5 deviation`으로 확장
- Existing reference 11 set/115 contract/110 ordered cross-binding, oracle/static/SHA bytes 보존
- Public external project fixture와 actual `cmd/godj` process E2E로 build/wire/cleanup을 검증
- Existing 10 hosted executions에 product CLI 전용 4-leg matrix를 더해 exact 14 required executions 검증
- 같은 Draft PR #1에 activation, implementation, evidence commit을 순서대로 쌓고 새 PR을 만들지 않음

## 비목표와 금지 경계

- `migrations/definition/**`, root `migrations/**`, `migrations/backend/**`, `schema/**`, `db/**` 변경
- `conformance/projectcheck/**`를 제품 코드로 이동·복사·import하거나 그 bytes를 변경
- Django Python scenario/oracle/static fixture/SHA256SUMS와 기존 10 reference/product artifact payload 변경
- Public global orchestration Go API, mutable registration/init hook 또는 global singleton
- Direct project-binary `migrations check`, `serve`, `migrate`, custom command dispatcher와 installed binary lifecycle
- Full CLI/library/generator semver negotiation, stale generated output repair, persistent runner cache
- Writer, `makemigrations`, upgrade/downgrade, codec v2+, executable/custom/data/raw-SQL operation
- DB/applied-history/schema-drift check, plan/execute, adoption/repair, unknown-commit/crash reconciliation
- PostgreSQL/MySQL/non-SQLite adapter, service-only DB job, multi-DB router와 distributed coordination
- Windows runtime/path/process support 또는 Windows green-skip job
- Recursive/module/embed/remote/watch/glob discovery, symlink follow와 hardlink rejection
- Build/runner hard CPU/RSS/disk/network/time sandbox, SIGTERM/SIGHUP/crash stale-temp scavenging
- Same-user adversarial TMPDIR/temp-base rename·rebind, explicit NETRC/Git/SSH/GOAUTH/compiler/PATH helper와
  network credential/config read isolation
- Caller-provided arbitrary Reader/Writer가 context와 무관하게 영구 block하는 경우의 강제 cancellation
- Raw compiler/runner diagnostic 또는 loader file/pointer detail의 public wire/user 출력
- Broken stdout/stderr sink의 atomic delivery 보장

Flat filesystem discovery는 linked product runner의 필수 부분이므로 이번 단면에 포함하지만 writer와
upgrade는 포함하지 않습니다. PostgreSQL CI는 actual backend가 query/write/transaction/schema/
migration/recorder/revision lifecycle와 durable restart를 검증할 때 별도 작업으로 추가합니다.

## 선행 조건과 기준 상태

- Baseline은
  `codex/revision-fenced-migration-lifecycle@f7fbbd50465a610ed9492227909eece524455f15`
  (`docs: record hosted project check completion validation`)이며 checkout은 clean합니다.
- Baseline Draft PR #1 run `31322959993`은 existing 2 + project-check 4 + SQLite 4의 exact 10 hosted
  executions을 성공했습니다. 이 run은 GDJ-0022 activation/implementation diff의 증거로 재사용하지
  않습니다.
- [GDJ-0020](0020-migration-definition-loader-product-slice.md)과 Accepted
  [ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 caller-provided bytes의
  bounded pure loader와 immutable `Set`/`LoadReport`를 완료했습니다.
- [GDJ-0021](0021-migration-project-check-compatibility-contracts.md)과 Accepted
  [ADR-0021](../docs/adr/0021-project-linked-migration-check.md)은 MIG-065..074의 exact external 의미와
  4,705-line Unix test-only feasibility proof를 완료했습니다. Product package/API/adapter는 없습니다.
- 현재 reference는 11 set/115 contract/110 ordered cross-binding이고 MIG-065..074는 10
  `oracle_locked`입니다. Product는 10 adapter/105 contract=`100 passing + 5 deviation`입니다.
- Repository에는 `cmd/godj`, public `project`, production project-check orchestration/discovery package가
  없습니다. `golang.org/x/sys` v0.47.0은 test-only proof 때문에 현재 indirect requirement입니다.
  Product Unix code의 direct import가 생기면 `go mod tidy`가 같은 version을 direct requirement로 승격할
  수 있으므로 `go.mod`/`go.sum`을 허용하되 version/hash 변경과 새 dependency 추가는 금지합니다.
- 사용자 또는 다른 agent가 만든 allowed-path 밖 변경은 보존합니다. Public API/ADR/CURRENT는 integration
  owner 한 명만 최종 편집합니다.

## Contract와 provenance

MIG-065..074의 title/slug/phase/comparison/result/error/24-field metrics는 GDJ-0021 artifact를 그대로
사용합니다. 이는 Django `migrate --check` parity가 아니라 `decision/ADR-0021/derived=false`인 GoDj
decision contract입니다. Product adapter가 생겨도 provenance와 oracle bytes는 바꾸지 않습니다.

제품 전환은 manifest `status`만 10개 모두 `oracle_locked`에서 `passing`으로 바꿉니다. Python reference
test는 이 status-only 변경만 허용하며 scenario/oracle/static/checksum은 byte-for-byte 고정합니다.
Product `godjcheck`는 actual adapter를 실행해 exact oracle과 0-diff여야 합니다. Static not-implemented
fixture는 계속 ordered mismatch 10개/exit 1이어야 합니다.

## 공개 API와 package 경계

Proposed [ADR-0022](../docs/adr/0022-project-runtime-and-global-migration-check.md)의 activation API는 exact
다음 두 export뿐입니다.

```go
package project

type Config struct {
	MigrationDefinitionRoots []string
}

func Run(
	ctx context.Context,
	config Config,
	argv []string,
	stdin io.Reader,
	stdout io.Writer,
) error
```

- `Run`은 진입 즉시 `argv`와 roots를 deep-copy하고 mutable global state를 만들지 않습니다.
- Zero roots는 valid하며 canonical empty `definition.Load()`를 정확히 한 번 호출합니다.
- Nil context/stdin/stdout, wrong private argv, response write failure와 internal invariant failure는 error를
  caller에게 전달합니다. `Run`은 `os.Exit`, `os.Args`, `os.Stdin`, `os.Stdout`을 직접 소유하지 않습니다.
- Valid private request의 known request/discovery/definition/graph logical failure는 exact closed response를
  stdout에 한 번 쓴 뒤 nil을 반환해 runner transport exit 0을 가능하게 합니다.
- Completed request bytes의 framing/schema fault, discovery filesystem permission/read/stat failure와
  definition/graph failure는 ADR-0021 closed logical response로 deterministic하게 분류합니다. Stdin reader
  자체의 read error, context cancellation, stdout write failure와 internal invariant failure는 Go error로
  caller에게 전달합니다. 같은 failure를 임의로 logical/error 두 경로 중 하나로 선택하지 않습니다.
- `project.Main`, public protocol/error/report/metric type, registration option과 init hook은 금지합니다.

External project `main`은 context와 process I/O를 명시적으로 전달하고, returned error에서 sanitized
constant stderr와 nonzero transport exit만 소유합니다. Private error detail을 protocol/user output에
재게시하지 않습니다.

Product import graph는 다음 단방향입니다.

```text
cmd/godj
  -> internal/projectcheck
       -> internal/projectcheck/protocol

project
  -> internal/projectcheck/linked
       -> internal/projectcheck/protocol
       -> migrations/definition
            -> migrations + schema/ir
```

`internal/projectcheck` global과 `linked`는 서로 직접 import하지 않습니다. Protocol leaf만 공유합니다.
Root migrations/schema/db package가 project-check child를 역-import하지 않고 product code는 conformance를
import하지 않습니다.

## Internal report와 actual adapter

ADR-0021의 exact 24 oracle metric은 public API/wire에 넣지 않습니다. Internal global/linked kernels는
각 actual callsite에서 immutable value report를 채웁니다. Linked report의 loader counters는 actual
`definition.Load`가 반환한 `LoadReport`가 유일한 source입니다.

같은 module의 product conformance adapter는 internal global kernel에 in-process backend를 주입해 actual
linked kernel을 호출하고 두 actual report를 결합합니다. Expected oracle 값, scenario expected metric,
digest/category/code 상수의 재생 또는 static fixture read로 report를 만들지 않습니다. 별도 black-box
E2E는 external fixture가 public `project.Run`을 import한 project package와 actual `cmd/godj` binary를
build/run해 OS process, private wire, cleanup과 public output을 검증합니다.

Public `project.Run`과 product adapter는 서로 다른 linked behavior path를 가질 수 없습니다. Go
`internal` import rule로 module 밖에 닫힌 `internal/projectcheck/linked.Run`은 internally exported
immutable `Report`와 error를 항상 반환합니다. Public facade는 그 entrypoint를
정확히 한 번 호출하고 Report를 버리며, adapter는 같은 entrypoint를 정확히 한 번 호출해 Report를
소비합니다. Identical input에서 public facade와 internal entrypoint의 response bytes 및 returned error
identity/meaning은 같아야 합니다. Facade delegation call count 1, byte/error equivalence와 roots/argv
snapshot을 mutation test와 AST gate로 고정합니다. Adapter가 expected observation을 재생하는 대체 kernel은
금지합니다.

Adapter mutation gate는 descriptor, root/enumeration order, matching symlink, protocol version, syntax-broken
build, definition document와 duplicate response를 각각 바꾸고 actual observation 및
`protocol.Compare` diff가 변하는지 증명합니다. Product adapter는 oracle/static/Python fixture/candidate
path를 읽지 못하며 MIG ID나 expected digest를 결과로 hard-code하지 못합니다.

## Inherited external behavior

Product code는 ADR-0021의 다음 exact 계약을 재해석 없이 구현합니다.

- Exact CLI argv 두 개와 invalid argument selection-before-I/O precedence
- Nearest byte-exact `godj.toml`, explicit descriptor, retained-handle no-follow/identity/race semantics
- Canonical descriptor subset v1과 `./` project package grammar
- Shell 없는 `go build -mod=readonly`, private 0700 workspace/env/cache/HOME/XDG/telemetry
- Exact magic argv, strict protocol v1 one-value+EOF, duplicate/UTF-8/integer/digest/closed schema
- Canonical project-relative flat `*.godj.json` discovery, root/source byte order, no-follow file read
- 11 inclusive accepted-input/catalog/wire/retained-output caps와 combined-fault precedence
- Closed category/code taxonomy와 public exit `0/1/2/3/130`
- Process-group SIGINT/cancel/reap/drain/raw-discard/single cleanup/publication order
- Success public JSON result one line; failure category/code stderr one line; partial result 0
- GoDj DB/recorder/revision lifecycle direct call 0 and `definition.Load` exactly once

Implementation이 contract proof helper를 호출하거나 product behavior를 expected fixture로 재생하면 완료가
아닙니다. Product unit/adversarial/E2E test가 same external meaning을 독립 검증해야 합니다.

## Unix support와 concurrency

GDJ-0022 runtime support는 Linux와 macOS의 amd64/arm64입니다. Public API와 command의 OS-specific
implementation은 `darwin || linux` build constraints를 사용합니다. Windows stub/green skip/지원 주장은
추가하지 않으며 unsupported OS taxonomy를 조용히 발명하지 않습니다.

`project.Run`은 process-global registration을 쓰지 않고 진입 시 process cwd를 physical project root로
한 번 snapshot해 retained handle에 결속하며 config/argv와 local handles만 소유합니다. Process cwd가
고정된 한 project 안에서 independent config/root slice로 concurrent call해도 race-free여야 합니다.
서로 다른 project root는 global이 각각 별도 child process cwd로 격리하며 한 process에서 `os.Chdir`로
전환하지 않습니다. Concurrent external `os.Chdir`와 같은 project file의 in-place mutation atomicity는
명시적 비목표입니다. Global command 한 invocation의 child group/workspace/report/publication은 다른
invocation과 공유하지 않습니다.

## Hosted CI 확장

Existing required topology는 full Ubuntu x64 1 + exact macOS arm64 1 + independent test-only project-check
matrix 4 + actual SQLite matrix 4 = 10 executions입니다. GDJ-0022 implementation head에서는 proof matrix와
SQLite matrix를 그대로 보존하고 actual product CLI matrix 4개를 별도로 추가해 exact
`2 + 4 + 4 + 4 = 14` required executions을 만듭니다.

Product matrix는 existing exact labels/coordinates를 재사용합니다.

| Label | GOOS | GOARCH |
|---|---|---|
| `ubuntu-22.04` | `linux` | `amd64` |
| `ubuntu-24.04-arm` | `linux` | `arm64` |
| `macos-15-intel` | `darwin` | `amd64` |
| `macos-26` | `darwin` | `arm64` |

각 product leg는 pinned checkout/setup-go, Go 1.26.5, `fail-fast:false`, 20분 timeout, exact GOOS/GOARCH
assertion, no `continue-on-error`, final tracked+porcelain clean gate를 사용합니다. Exact package target은
`./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj`이며 normal, race,
CGO-disabled, vet를 별도 step으로 모두 실행합니다. Actual CLI external-project E2E와 adapter focused test는
normal/race package gate 안에 포함합니다. Full Ubuntu에는 Linux/386 compile-only gate를 추가하고,
existing full/exact Python/oracle/checksum/no-rewrite는 보존합니다.

Test-only proof 4 legs를 product test로 대체하지 않습니다. 두 독립 구현이 모두 green이어야 합니다.
PostgreSQL/MySQL service-only job은 계속 금지합니다. Actual backend가 생기면 별도 work에서 digest-pinned
image/driver, health, UTC+C or approved collation, actual query/write/transaction/schema/migration/recorder/
revision lifecycle와 durable restart, expected==executed, skipped=0, no continue-on-error, clean tree를 먼저
required로 만듭니다.

## 구현 단계

1. **Activation**
   - 이 work를 `active`, ADR-0022를 `Proposed`로 만들고 CURRENT/ROADMAP/Q-010/Q-012/index를 동기화
   - Public API compile spike와 internal import graph/Unix build feasibility를 검토
   - Activation exact head에서 기존 10-job hosted baseline을 별도 확인
2. **Protocol/public facade**
   - `internal/projectcheck/protocol` strict request/response/taxonomy/result 구현
   - Public `project.Config`/`project.Run`과 external compile fixture 구현
   - API/export/import/ownership과 public-facade/internal-entrypoint 동치성 gate 구현
3. **Linked product kernel**
   - Project-root/root/candidate retained-handle discovery, caps와 actual `definition.Load` handoff 구현
   - Report/failure mapping, root/source permutation, TOCTOU, race/CGO0/count-20 검증
4. **Global product kernel와 CLI**
   - Arg/descriptor selection, workspace/env, build/runner process, strict response와 publication 구현
   - `cmd/godj` signal/cwd/env/stdio/exit wrapper와 actual external project E2E 구현
5. **Product adapter/status/CI**
   - Actual MIG-065..074 adapter와 false-green mutations 연결
   - Manifest status-only 10 `passing`, Makefile/current-count gates 11/115=`110+5`로 갱신
   - 4-leg product matrix를 추가하고 exact expanded topology static gate 구현
6. **Evidence/completion**
   - Focused/full/exact/local gates, two independent P0-P3 audits와 same-PR 14-job hosted run
   - ADR-0022 Accepted 여부, work/CURRENT/matrix/evidence/general docs를 actual 결과에 맞춰 완료

## 완료 조건

- [ ] Public external fixture가 exact `project.Config`/`project.Run` API로 compile하고 extra export가 없음
- [ ] Public facade와 adapter가 동일 linked entrypoint를 exactly once 호출하고 public/internal의 response
  bytes/error가 동일하며 roots/argv mutation snapshot이 보존됨
- [ ] `godj migrations check` exact implicit/explicit success가 actual binary/process에서 동작
- [ ] External project/public facade가 success, malformed private request, MIG-073 definition failure,
  stdout writer failure와 caller cancellation을 actual path로 검증
- [ ] Actual `cmd/godj` process가 success, invalid descriptor/build/definition failure와 handled SIGINT를
  public exit/output/cleanup까지 검증
- [ ] MIG-072 syntax-broken project package가 actual `go build -mod=readonly`에서 build1/runner0/read0/
  Load0, public build failure, project tree/go.sum 불변과 private cleanup을 증명
- [ ] MIG-065..074 actual adapter가 locked oracle과 result/error/24 metrics 0-diff
- [ ] Manifest 10 status만 `passing`; reference oracle/static/SHA/checksum/scenario bytes unchanged
- [ ] Product exact 11 adapter/115 contract=`110 passing + 5 deviation`; static exit1/mismatch10 유지
- [ ] Product code는 conformance/test candidate를 import·read하지 않고 core loader에 FS/I/O를 추가하지 않음
- [ ] Definition Load exactly 1, direct orchestration Planner/DB/recorder/revision lifecycle 0
- [ ] Descriptor/protocol/discovery 11 limits maximum-1/equal/+1와 combined precedence PASS
- [ ] Marker/root/source symlink, replacement, identity, permission/I/O and post-read race gates PASS
- [ ] Private build env/tree no-rewrite, process cancel/reap/drain/raw-discard/cleanup/publication gates PASS
- [ ] Nil dependency, panic-free arbitrary bytes, short write, caller cancellation과 blocking-I/O limitation gate PASS
- [ ] Normal/race/CGO0/vet/count20, Linux/386 compile, `make ci`, exact Python/oracle/no-rewrite PASS
- [ ] `go mod tidy` 뒤 x/sys v0.47.0 directness 외 dependency/version/hash 변화가 없고 clean 재실행 PASS
- [ ] Existing 10 executions + product 4 = exact 14 hosted executions all required/success
- [ ] Independent contract/security final audits P0/P1/P2/P3=0
- [ ] Work/ADR/CURRENT/matrix/evidence/general docs and Q-010/Q-012 actual state synchronized

## 진행 기록

- [x] 현재 checkout과 CURRENT/ROADMAP/Q-010/Q-012 대조
- [x] CLI vs PostgreSQL vs writer/upgrade 독립 우선순위 감사
- [x] Product package/API/24-metric feasibility 중간 감사
- [ ] Activation 문서 independent final audit와 exact-head CI
- [ ] Protocol/public API implementation
- [ ] Linked/global product implementation
- [ ] Adapter/status/CI implementation
- [ ] Completion evidence와 문서

## 수정 파일

Activation 단계에서는 이 파일, ADR-0022, CURRENT, ROADMAP, OPEN_QUESTIONS와 두 index만 변경합니다.
Implementation/adapter/completion의 실제 path는 frontmatter allowlist 안에서 단계별로 기록합니다.

## 결정된 사항

- 2026-08-10: CURRENT와 independent priority audit를 근거로 PostgreSQL/relations나 writer bundle보다
  project-check 제품화를 다음 단면으로 선택
- 2026-08-10: Flat discovery는 linked product의 필수 내부 단계로 포함하고 writer/upgrade는 분리
- 2026-08-10: Public API 후보를 explicit immutable-config `project.Run(ctx, config, argv, stdin, stdout) error`
  로 제한하고 mutable registration/init/global CLI library API를 배제
- 2026-08-10: Existing proof 4-leg를 보존하고 product 4-leg를 별도로 더한 14-job 목표 채택
- 2026-08-10: PostgreSQL/MySQL은 actual adapter/contract 전 service-only CI 금지 유지

## 미결정/Blocker

외부 blocker는 없습니다. 다음은 의도적으로 이번 work 뒤에도 open입니다.

- Direct project command dispatcher와 broader public project API
- Full CLI/library/generator semver/upgrade와 persistent build cache
- Writer/makemigrations/format v2 upgrade
- Windows runtime와 fatal-signal/crash scavenging
- DB-aware check, PostgreSQL/MySQL와 multi-DB

ADR-0022는 Proposed입니다. API compile spike와 independent audit에서 P0/P1이 나오면 제품 코드를 만들기
전에 이 activation 문서를 교정합니다.

## 테스트 증거

- Baseline local/hosted: GDJ-0021 EVID-024..026 및 exact baseline run `31322959993` 10/10 success
- Activation runtime/product tests: 아직 실행하지 않음
- Activation expanded 14-job CI: 아직 구현/실행하지 않음
- Evidence ID: 구현 후 새 ID를 append

## 위험과 rollback

- Public project API가 너무 넓으면 향후 command/app registry를 잠그므로 exact two-export surface만 허용
- Test-only proof를 복사하면 independent evidence와 product가 같은 결함을 공유하므로 import/read/AST gate로 금지
- Process/filesystem 구현은 false green이 쉬우므로 internal injected actual-data adapter와 black-box OS E2E를 둘 다 요구
- Private build는 user `init()` side effect를 막지 않으며 DB-free는 GoDj-owned DB/lifecycle 0만 뜻함
- 4개 macOS/Linux product job 비용은 늘지만 independent proof/product/SQLite failure를 분리해 관찰 가능
- Rollback은 GDJ-0022 activation/implementation/status commit만 되돌리고 Accepted ADR-0021 artifact와
  completed GDJ-0020 loader를 변경하지 않음

## 다음 정확한 작업

Integration owner는 이 activation exact 7-file diff를 independent audit하고 Markdown/frontmatter/link/
scope를 검증한 뒤 same Draft PR #1에 commit/push합니다. Activation head의 existing 10-job CI를 확인한
다음 `internal/projectcheck/protocol`과 public `project` compile spike부터 시작합니다.

## 결과와 인수인계

GDJ-0022는 현재 activation 단계입니다. Product code/status는 아직 바뀌지 않았고 MIG-065..074는 계속
`oracle_locked`, product는 10 adapter/105 contract=`100 passing + 5 deviation`입니다. 구현 전까지
Accepted ADR-0021의 test-only proof를 사용 가능한 CLI/API로 표현하지 않습니다.
