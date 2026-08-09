# ADR-0022: Public Project Runtime and Global Migration Check

- 상태: Accepted
- 날짜: 2026-08-10
- 관련 work/contract: [GDJ-0022](../../work/0022-migration-project-check-product-slice.md),
  MIG-065..MIG-074, Q-010, Q-012
- 선행 결정: [ADR-0004](0004-cli-and-project-binary.md),
  [ADR-0020](0020-migration-definition-loader-product-shape.md),
  [ADR-0021](0021-project-linked-migration-check.md)
- 대체하는 ADR: 없음

## 맥락

ADR-0004는 global `godj`와 project-linked binary의 책임을 분리했고, ADR-0021은 가장 작은 DB-free
`godj migrations check`의 external meaning을 exact contract와 test-only feasibility로 Accepted했습니다.
결정 당시 repository에는 `cmd/godj`, public project entrypoint, production filesystem/process kernel과
actual MIG-065..074 adapter가 없었습니다. `conformance/projectcheck`의 4,705줄은 모두 Unix `_test.go`이고
product package/API가 아니므로 제품 구현은 그 proof와 독립이어야 했습니다.

제품화를 위해서는 linked project가 migration roots와 private request를 처리할 최소 public API가
필요합니다. 동시에 test-only shape를 그대로 승격하거나 global CLI가 project code/definition loader를
직접 import하면 ADR-0004/0020의 ownership이 무너집니다. Public API를 future `serve`/`migrate`/custom
command 전체로 넓히지 않으면서 actual command를 실행할 경계를 먼저 고정해야 합니다.

## 결정 기준

- Accepted MIG-065..074 external meaning의 exact 구현
- All actual I/O의 explicit `context.Context`와 `error` 전달
- Project config ownership과 input mutation isolation
- Global/project/protocol/definition loader의 단방향 import graph
- Protocol/detail metric을 public API에 노출하지 않는 최소 export surface
- Product와 test-only proof의 independent implementation/evidence
- Unix filesystem/process safety와 exact cancel/reap/cleanup
- Actual adapter가 expected constants 없이 24 metrics를 관찰할 수 있는 내부 seam
- Future direct project commands와 CLI/library/generator semver를 잠그지 않는 확장성
- Linux/macOS amd64/arm64의 required hosted validation

## 고려한 선택지

### Test-only `conformance/projectcheck`를 product package로 이동

가장 빠르지만 reference feasibility와 product가 같은 implementation이 되어 independent evidence를 잃습니다.
Test metrics/hook가 public/product surface로 새고 conformance import 방향도 뒤집힙니다. 채택하지 않습니다.

### Global CLI가 filesystem discovery와 `definition.Load`까지 직접 소유

Single binary로 단순하지만 project code가 소유할 future app/settings/root semantics를 global tool에
복제합니다. ADR-0004의 linked ownership과 versioned process boundary를 무효화하므로 채택하지 않습니다.

### Mutable registration/init 또는 plugin interface

Project main이 init에서 roots/callback을 등록하면 API 호출은 짧지만 global state, import-order, concurrent
test contamination과 arbitrary plugin ABI를 만듭니다. Go binary build boundary가 이미 있으므로 채택하지
않습니다.

### Explicit config와 testable runner function

Project main이 immutable input을 명시하고 argv/stdin/stdout/context를 전달합니다. Private protocol을
public type으로 노출하지 않으면서 roots를 linked code가 소유하고, future Config field를 additive하게
확장할 수 있습니다. 이번 단면에 채택합니다.

## 결정

### Public project API

새 `github.com/progresshans/godj/project` package의 exact export surface는 다음입니다.

```go
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

`Run`은 roots와 argv를 진입 즉시 deep-copy합니다. Zero roots는 valid empty catalog이며 actual
`definition.Load()`를 한 번 호출합니다. Nil dependencies, wrong private argv, stdin reader error, context
cancellation, stdout writer failure와 internal invariant failure는 error를 전달합니다. Completed request
bytes의 framing/schema fault, discovery filesystem permission/read/stat failure와 known definition/graph
failure는 exact closed logical response를 stdout에 한 번 쓰고 nil을 반환합니다. 이는 private runner
transport exit 0과 logical outcome을 분리하며 동일 failure의 arbitrary dual classification을 금지합니다.

`Run`은 `os.Args`, `os.Stdin`, `os.Stdout`, `os.Exit`, signal registration과 global mutable registry를
소유하지 않습니다. `project.Main`, public protocol/error/report/metric/helper option은 만들지 않습니다.
Project main은 process values를 전달하고 returned error일 때만 constant sanitized diagnostic과 nonzero
transport exit을 소유합니다. Exact magic argv는 private contract이고 direct public project command가
아닙니다.

### Product packages와 import direction

- `cmd/godj`: process cwd/env/args/stdio, handled Unix SIGINT와 final exit만 소유
- `internal/projectcheck`: global selection/build/process/publication orchestration
- `internal/projectcheck/protocol`: descriptor-independent strict request/response/result/error taxonomy leaf
- `internal/projectcheck/linked`: roots/discovery/Load/report kernel
- `project`: public facade that delegates to linked kernel and discards internal report

```text
cmd/godj -> internal/projectcheck -> internal/projectcheck/protocol
project  -> internal/projectcheck/linked -> internal/projectcheck/protocol
                                      \-> migrations (PlanningError taxonomy only)
                                      \-> migrations/definition
```

Global과 linked kernel은 서로 직접 import하지 않습니다. `migrations/definition`은 path/FS/CLI를 받지
않고 caller-provided bytes pure loader로 남습니다. Core migrations/schema/db package는 child를 역-import하지
않습니다. Linked의 direct `migrations` import는 loader가 반환한 existing
`*migrations.PlanningError`를 closed category/code로 분류하는 read-only taxonomy edge로 한정하고,
Planner/lifecycle/recorder/backend 호출과 graph 재구현을 금지합니다. Product code는 conformance를
import하거나 candidate files/artifacts를 읽지 않습니다.

### Internal report와 conformance

Global/linked kernels는 actual callsites에서 immutable internal report를 만듭니다. Linked loader counters는
`definition.Load`의 returned `LoadReport`에서만 옵니다. Public project API, private runner protocol과 user
output에는 report/detail을 노출하지 않습니다.

Same-module actual adapter는 internal dependency seam을 사용해 global kernel과 actual linked kernel을
in-process로 연결하고 두 report를 결합합니다. 이는 expected oracle/fixture replay가 아닙니다. 별도
black-box test는 external project package가 public API를 import하고 actual `cmd/godj`와 OS child process를
통과하도록 해 injection seam의 process false green을 막습니다.

Go `internal` import rule로 module 밖에 닫힌 `internal/projectcheck/linked.Run`은 internally exported
immutable `Report`와 error를 항상 반환합니다. Public `project.Run`은 그 entrypoint를 정확히 한 번 호출하고
Report를 버리며 sibling product adapter는 같은 entrypoint를 정확히 한 번 호출해 Report를 소비합니다.
Identical input에서 public facade와 internal entrypoint의 response bytes 및 returned error identity/meaning은
같아야 합니다. 이 동치성과 facade-to-linked call count를 dynamic mutation 및 AST gate로 잠가 adapter-only
alternate implementation을 금지합니다.

### External behavior와 platform

Descriptor, selection, private build/env, wire, discovery, caps, category/code, exits, cancellation/cleanup과
publication은 Accepted ADR-0021을 그대로 구현합니다. Product work가 새 precedence/taxonomy를 발명하지
않습니다.

Runtime support는 `darwin || linux`의 amd64/arm64입니다. Windows runtime/stub/green skip과 unsupported
taxonomy는 이번 ADR에서 만들지 않습니다. Global/project state는 invocation-local이며 independent roots의
concurrent `project.Run`과 global command는 race-free여야 합니다. `project.Run`은 진입 시 process cwd를
physical project root로 한 번 snapshot하고 retained handle에 결속합니다. 같은 고정 cwd 안의 concurrent
call만 지원하며 서로 다른 project는 separate built child processes로 격리합니다. 한 process에서
`os.Chdir`하거나 concurrent external cwd mutation을 지원한다고 주장하지 않습니다.

### Hosted verification

Existing full/exact 2 + independent proof 4 + actual SQLite 4의 10 executions을 보존합니다. Product
workflow는 same four official Linux/macOS x64/arm64 labels의 actual product matrix 4와 Ubuntu
Python compatibility matrix 4를 더해 exact 18 required executions을 사용합니다. Product legs는
normal/race/CGO-disabled/vet, actual external E2E, exact GOOS/GOARCH, clean worktree를 검증합니다.

Exact oracle의 CPython 3.14.3/Django 6.1/darwin-arm64는 변경하지 않습니다. Portability legs는
Django 6.1 supported minor의 reviewed exact micro `3.12.13`, `3.13.15`, `3.14.3`, `3.14.7`을
`actions/setup-python` v6.2.0으로 구성하고 `setup-uv`/uv 0.12.3과 isolated exact
Django/asgiref/sqlparse를 사용합니다. 각 leg는 exact runtime/dependency, portable 174 tests/16
intentional skips, 115-scenario canonical payload size/hash와 clean tree를 검증합니다. Floating minor를
사용하지 않고 새 Python micro는 review된 pin update로 반영합니다.

Portable Ubuntu와 Python compatibility jobs는 current uv 0.12.3을 사용하지만 exact darwin oracle job은
profile/oracle/static payload에 기록된 uv 0.10.12를 유지합니다. Exact manager fingerprint 승격은 모든
reference artifact를 다시 검토하는 별도 계약 변경이며 이 product slice의 status-only 전환이 아닙니다.

PostgreSQL/MySQL service-only CI는 actual adapter가 없으므로 금지합니다. 향후 backend job은 실제
query/write/transaction/schema/migration/recorder/revision lifecycle와 durable persistence, pinned service/
driver/locale, expected==executed, skipped=0을 먼저 요구합니다.

## 결과

- 사용 가능한 첫 global GoDj migration command와 explicit project-linked API가 구현됐습니다.
- ADR-0004의 global/project ownership과 ADR-0020 pure loader 경계가 실제 package graph로 검증됐습니다.
- Build마다 private runner를 만들기 때문에 비용이 있지만 persistent cache/installed lifecycle을 성급히
  고정하지 않습니다.
- Public surface는 두 export뿐이지만 `Config` field와 `Run` signature는 장기 compatibility 책임이 됩니다.
- Unix no-follow/process-group 구현과 4-leg product + 4-leg Python compatibility CI 비용이
  추가됐습니다.
- MIG-065..074 actual adapter가 구현되어 10 contract가 `passing`으로 전환됐고 제품 집계는
  11 adapter/115 contract의 `110 passing + 5 deviation`입니다.

## 의도적으로 결정하지 않은 것

- Direct public project command dispatcher, app/settings registry와 custom commands
- Global orchestration Go library API와 programmatic result/error API
- CLI/library/generator semver, upgrade/repair와 persistent runner cache
- Writer/makemigrations, codec v2/custom/data/raw-SQL operation
- Recursive/module/embed/remote/watch discovery
- Windows path/process semantics와 fatal-signal/crash scavenging
- User-visible raw build/runner/loader detail policy
- Same-user hostile TMPDIR/temp-base rebind, explicit NETRC/Git/SSH/GOAUTH/compiler/PATH/network helper
  isolation과 arbitrary blocking caller Reader/Writer의 강제 cancellation
- DB-aware migration check, PostgreSQL/MySQL와 multi-DB

## 검증

- External consumer compile/export allowlist and input-mutation ownership tests
- Public facade와 actual adapter의 same linked entrypoint exactly-once, public/internal response/error
  equivalence와 roots/argv snapshot mutation tests
- Strict descriptor/protocol/discovery/taxonomy/cap precedence product tests independent from test-only proof
- Actual linked `definition.Load` once/report and direct Planner/DB/lifecycle 0 AST+runtime gates
- External public project process의 success/malformed request/MIG-073 failure/cancel/stdout-error와 actual
  `cmd/godj`의 success/descriptor/build/definition/SIGINT public output/tree no-rewrite/private cleanup E2E
- Nil dependencies, arbitrary bytes panic-free, short-write/error and owned cancellation tests; caller-controlled
  permanently blocking Reader/Writer and explicit external tool/credential environment remain documented limits
- MIG-065..074 actual adapter 0-diff and expected-constant/oracle-read mutation gates
- Normal/race/CGO-disabled/vet/count-20, Linux/386 compile, full/exact/no-rewrite gates
- Four Linux/macOS x64/arm64 product jobs plus preserved proof/SQLite/full/exact jobs
- Independent contract and filesystem/process security audit before Accepted

## 채택 근거와 남은 검증

Exact public external compile, global/linked/protocol 제품 kernel, actual CLI/process E2E, MIG-065..074
0-diff adapter, local normal/race/CGO-disabled/vet/count-20와 independent global/linked/adapter audits가
P0/P1/P2/P3 finding 0으로 완료되어 이 ADR을 Accepted합니다. 일상 local/Ubuntu portable/Python
compatibility는 uv 0.12.3을 사용하고, reference artifact payload에 manager fingerprint가 포함된 historical
exact darwin oracle만 uv 0.10.12를 유지합니다.

Initial implementation head `06858dd6aafeb20449bc4fbfa9aeac78c7a794ce`의 hosted run
`31329231255`는 네 Python leg가 모두 테스트 전 brittle uv exact-string assertion에서 실패해 취소했습니다.
Exact uv 0.12.3 pin은 유지하고 허용된 version metadata suffix만 받아들인 fix head
`3dfeff2a881a3313883729943519896798d92afc`의 run `31329294154`에서 exact 18 required executions이
18/18 성공했습니다. 따라서 Accepted는 local 구현뿐 아니라 Python 3.12.13/3.13.15/3.14.3/3.14.7과
Linux/macOS 네 product 좌표의 hosted acceptance까지 포함합니다. 상세 증거는 EVID-028에 기록합니다.

EVID-028/status head `68b408add3b050d0938ccebc6c83200499f57b2a`의 run `31330601427`은 exact 18 중
16 success/2 macOS product normal failure였습니다. Helper readiness는 atomic temp-write/rename으로,
actual SIGINT E2E는 cold private build와 early exit/kill/reap을 포함하는 bounded harness로 보강했습니다.
Race audit에서 드러난 directly-reaped child와 delayed Wait-result publication window는 product coordinator가
queued result, `Signal(0)`/`os.ErrProcessDone`, synchronous publication consumption 순으로 조정한 뒤 group
signal 여부를 결정하도록 닫았습니다. Final fix `385382efffd1872ae7fb427192bab27b95dc57e2`의 run
`31332208055`는 exact 18/18 성공했습니다. EVID-029/status patch 자체의 후속 exact-head CI는
commit/push 뒤 별도로 검증합니다.
