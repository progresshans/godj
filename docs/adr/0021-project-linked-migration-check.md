# ADR-0021: Project-Linked Migration Check Contract

- 상태: Accepted
- 날짜: 2026-08-10
- 관련 work/contract: [GDJ-0021](../../work/0021-migration-project-check-compatibility-contracts.md),
  MIG-065..MIG-074, Q-010, Q-012
- 선행 결정: [ADR-0004](0004-cli-and-project-binary.md),
  [ADR-0019](0019-versioned-migration-definition-source.md),
  [ADR-0020](0020-migration-definition-loader-product-shape.md)
- 대체하는 ADR: 없음

## 맥락

Accepted ADR-0004는 global `godj`가 project를 찾고 build/orchestration하며 settings/app/model이
필요한 동작은 project-linked binary가 수행한다는 역할만 정했습니다. Descriptor schema, project
selection, private invocation과 wire protocol은 의도적으로 결정하지 않았습니다. Accepted
ADR-0019/0020은 caller-provided strict definition bytes와 bounded pure loader를 제공하지만 file
discovery나 CLI를 소유하지 않습니다.

다음 좁은 사용자 경험은 migration definition catalog가 load 가능한지 DB 없이 검사하는
`godj migrations check`입니다. Global CLI가 임의 project code를 직접 import할 수 없으므로 linked
runner가 semantic root와 product loader를 소유해야 합니다. 동시에 project selection/build/runner
경계를 닫지 않으면 outer marker fallback, shell injection, protocol downgrade, symlink follow,
unbounded output과 orphan child가 서로 다른 구현에서 달라질 수 있습니다.

이 ADR은 MIG-065..074 decision contract와 test-only feasibility로 아래 결정을 검증했습니다.
Activation 당시에는 **Proposed**였고 제품 CLI/package/API가 구현됐다는 뜻이 아니었습니다.
Activation baseline `53729103651bfc34acc5fe07fb4376d5dd78c204`은 Draft PR #1
[run 31310606332](https://github.com/progresshans/godj/actions/runs/31310606332)의 Ubuntu/macOS 두
job을 통과했지만 이 ADR의 채택 근거로 재사용하지 않았습니다. Exact implementation head
`84ddf109c04acd72992b816aa72140c6e748e5f0`의 local evidence와 별도 10-job hosted
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)을 근거로 아래 경계를
Accepted합니다. 이는 전역 `godj` command, public project API나 production runner 구현을 뜻하지
않습니다.

## 결정 기준

- 하위 directory와 explicit descriptor에서 예측 가능한 project selection
- Global orchestration과 linked project semantic ownership의 좁고 versioned한 경계
- Shell/path/symlink/protocol downgrade를 fail-closed하는 안전성
- Existing `migrations/definition.Load`의 exactly-once 재사용과 error ownership 보존
- DB/lifecycle mutation 0과 partial result 0
- Descriptor/catalog/wire acceptance와 retained diagnostics의 11개 cap 및 deterministic precedence
- Child cancellation, reap와 private artifact cleanup
- Django Python file ABI를 복제하지 않는 독립 Go redesign provenance
- Public product API를 먼저 고정하지 않고도 machine-testable한 contract

## 고려한 선택지

### Global CLI가 migration files를 직접 탐색하고 해석

Global binary만으로 빠르게 구현할 수 있지만 project-linked settings/app semantics와 future custom
command ownership이 갈라집니다. Root config public ABI를 global tool에 복제하고 library/version
mismatch를 process boundary에서 검증하지 못합니다.

### Project binary의 public `migrations check` command만 직접 실행

Production command와 가까우나 global CLI가 어떤 installed/stale binary를 선택하는지, build failure와
project selection을 어떻게 구분하는지가 남습니다. Direct production command surface와 installed
binary lifecycle을 이번 contract에 불필요하게 고정합니다.

### Plugin, Go source import 또는 shell command descriptor

Flexible하지만 host toolchain/ABI, arbitrary execution과 quoting/injection을 public contract로 만듭니다.
Cross-platform behavior와 cache/trust 문제가 migration catalog check보다 훨씬 큽니다.

### Closed descriptor + private built runner protocol

Global side는 marker/descriptor/build/process만, linked side는 roots/discovery/loader만 소유합니다.
Strict one-value JSON protocol과 private binary는 component/version failure를 구조화할 수 있고
test-only proof로 먼저 검증할 수 있습니다. Build cost와 user `init()` execution은 명시적 비용입니다.

## 채택한 결정

### 사용자 command, descriptor와 project selection

사용자 namespace는 `godj migrations check`입니다. Exact argv는 executable 뒤 `migrations check` 또는
`migrations check --project <descriptor-file>` 둘뿐입니다. `--project=`, repeated/missing/empty/unknown/
extra/reordered argument는 project search 전 `invalid_arguments`, exit 2입니다.

Implicit selection은 invocation cwd를 physical absolute directory로 한 번 resolve하고 이를 첫
directory로 세어 최대 128 physical ancestor에서 raw entry name이 byte-exact `godj.toml`인 nearest
marker 하나만 선택합니다. Retained parent handle을 enumerate한 뒤 exact entry를 no-follow open하므로
case-insensitive macOS의 `GODJ.TOML`은 marker가 아닙니다. 128번째까지 marker 없이 filesystem root면
`project_not_found`, parent가 더 남으면 `project_search_limit_exceeded`이고 further stat/build/runner는
0입니다. Nearest descriptor가 invalid하면 outer marker로 fallback하지 않습니다. Cwd resolve,
ancestor open/enumeration/stat의 ENOENT/ESTALE/ENOTDIR/permission/I/O와 traversal/selected descriptor
disappearance는 errno와 무관하게 `project_selection_failed`, exit 3, no fallback/build입니다. Normal
marker absence는 successful retained-directory enumeration에서 raw exact entry가 없을 때뿐입니다.

Explicit `--project <descriptor-file>`은 directory가 아닌 exact basename `godj.toml` descriptor
file이며 ancestor search를 0회 실행합니다. Relative 값은 invocation cwd 기준으로 resolve/clean하고
descriptor parent를 physical absolute project root/child cwd로 사용합니다. Implicit/explicit final
descriptor 모두 retained parent의 raw exact-name check → Lstat regular non-symlink → no-follow
open/Fstat → bounded read → same-file Lstat 재확인을 요구합니다. Initial stable symlink/non-regular와
stable oversized/lexical/schema bytes는 `invalid_project_descriptor`이며 target을 따라가지 않습니다.
Initial regular exact entry를 선택한 뒤 open/read/same-file 단계의 disappearance, identity change,
symlink swap 또는 permission/I/O는 errno와 무관하게 `project_selection_failed`/3입니다. Race와 stable
descriptor fault가 결합되면 observed identity/file-type/disappearance race가 우선합니다. Stable opened
inode의 concurrent in-place write/truncate는 atomic snapshot을 보장하지 않습니다. Bounded result가
invalid/oversized면 invalid descriptor, 우연히 valid면 success할 수 있어 producer atomic replace 또는
external synchronization이 필요합니다.
Explicit initial parent/entry probe가 supplied path의 absent/non-directory를 확인하면
`invalid_project_descriptor`/2이고, initial parent probe의 permission/ESTALE/other I/O는
`project_selection_failed`/3입니다.

Descriptor v1은 dependency 없는 GoDj canonical TOML-shaped subset이며 full TOML 1.0 호환을 주장하지
않습니다. 64 KiB 이하, BOM 없음, document 전체 LF 또는 CRLF 한 mode, mixed/bare CR 거부, exact final
line ending을 요구합니다. Physical line은 ASCII SP/TAB blank, optional SP/TAB 뒤 `#` printable
ASCII/TAB comment 또는 semantic line입니다. Inline comment/escape/single·multiline string은 없습니다.
Blank/comment를 제외하면 다음 세 semantic line이 exact order로 한 번씩만 나타납니다.

```toml
format_version = 1

[project]
package = "./cmd/mysite"
```

Key/`=` 주위 ASCII SP/TAB은 허용합니다. Parser는 full lexical/closed shape와 package grammar를 먼저
검사하므로 shape+version combined fault는 `invalid_project_descriptor`입니다. 그 뒤
`format_version`은 canonical decimal `0|[1-9][0-9]*`, range 0..65,535이고 supported value exact 1;
다른 in-range value만 `project_descriptor_incompatible`입니다. Package는 escape 없는 simple printable
ASCII string이며 literal leading `./`을 먼저 분리하고 non-empty remainder의 `path.Clean` equality를
요구합니다. Remainder segment는 non-empty이며 `.`/`..`/`...`, glob meta, backslash/NUL이 없어야
합니다. 따라서 `./cmd/mysite`는 valid지만 `cmd/mysite`, `./`, `./cmd/../site`, `./cmd//site`,
`./...`는 invalid인 단일 argv입니다.

### Build와 private invocation

Global side는 project root cwd에서 shell 없이 다음을 실행하는 경계로 채택합니다.

```text
go build -mod=readonly -o <private-0700-temp-dir>/godj-project-runner <project.package>
```

Child env 변경 전에 non-empty original `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`을 capture하고 각
configured protected root를 existing directory로 physical resolve합니다. Resolve/stat failure는
`project_temporary_storage_failed`입니다. Ambient `TMPDIR`(없으면 platform default)도 physical
resolve/Lstat한 existing real directory여야 하고 project root 또는 protected HOME/XDG root와 같거나
그 descendant면 거부합니다. 따라서 `TMPDIR=$HOME/tmp`와 XDG subtree mutation은 build 전 실패합니다.
검증한 explicit base 아래 invocation-private `0700` root를 만들고 같은 outside-project/protected-roots
조건을 다시 확인하므로 default `MkdirTemp`가 ambient protected tree를 따르지 않습니다.
이 containment/non-mutation claim은 static final-base symlink/physical containment check 뒤 same-user
concurrent temp-base rebind/rename이 없는 fixture에 한정합니다. Validation과 create/build 사이
path-resolution race를 retained handle로 fence하지 않으므로 그 race의 write redirection 방지는 product
hardening으로 남깁니다.

Child environment에서는 ambient `TMPDIR`, `GOWORK`, `GOTOOLCHAIN`, `GOFLAGS`, `GOENV`, `GOCACHE`,
`GOCACHEPROG`, `GOMODCACHE`, `GOTMPDIR`, `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `TEST_TELEMETRY_DIR`를
제거합니다. Exact `GOWORK=off`, `GOTOOLCHAIN=local`, `GOENV=off`, empty `GOFLAGS`와
empty `GOCACHEPROG`, `TMPDIR=<private>/tmp`, `GOTMPDIR=<private>/gotmp`, `GOCACHE=<private>/gocache`,
`GOMODCACHE=<private>/gomodcache`, `HOME=<private>/home`,
`XDG_CONFIG_HOME=<private>/xdg-config`, `XDG_CACHE_HOME=<private>/xdg-cache`,
`TEST_TELEMETRY_DIR=<private>/telemetry`를 설정합니다. 각 directory는 `0700`이고 만들 수 없으면
build 전에 실패합니다. `GOTELEMETRY=off` 환경변수를 유효한 policy switch라고 주장하거나 의존하지
않습니다. Go tool-controlled cache/temp/telemetry write는 protected project/caller HOME/XDG tree 밖의
private temp에 두며 아래의 single-attempt cleanup semantics를 적용합니다. Arbitrary user code side
effect까지 차단한다는 뜻은 아닙니다. Private HOME/XDG 때문에 caller `.netrc`와 HOME/XDG-local Git/SSH
config를 사용할 수 없다는 절대 주장은 하지 않습니다. Default Go HOME/UserConfigDir/netrc lookup만
private로 redirect합니다. Explicit `NETRC`, Git config-count/global/system, Git SSH command, askpass/helper,
`SSH_AUTH_SOCK`, `GOAUTH` command, `CC`/`CXX`/cgo tool과 external ssh의 OS-account home read는
scrub/isolate하지 않으며 network/auth success와 credential confidentiality를 보장하지 않습니다.
`-mod=readonly`는 project metadata rewrite를 막습니다.
Normal return/caller cancellation/handled SIGINT에서 private temp removal을 정확히
한 번 best-effort attempt하고, `RemoveAll` 성공+retained-parent post-check absence인 cleanup success에서만
residue 0을 보장합니다. Persistent runner/module/build cache, cleanup retry와 crash/escaped-descendant 뒤
stale-temp scavenging은 후속입니다.

Build된 binary는 project root에서 exact private argv
`__godj_project_runner_v1` 하나로 실행합니다. 이는 production project-binary command surface가
아니며 GDJ-0021 test-only candidate 밖의 public API/entrypoint shape는 후속 결정입니다.

### Closed runner protocol v1

Request는 UTF-8 JSON one value + EOF이며 exact closed object입니다.

```json
{"protocol_version":1,"command":"migrations.check"}
```

Success와 failure도 각각 one value + EOF의 closed object입니다.

```json
{"protocol_version":1,"status":"ok","result":{"source_count":2,"definition_count":2,"definition_set_digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
```

```json
{"protocol_version":1,"status":"error","error":{"category":"migration_definition_source_error","code":"invalid_definition_document"}}
```

Duplicate/unknown/missing/wrong-type/trailing/non-UTF-8을 거부하고 raw source/definition/inventory를
응답에 넣지 않습니다. Integer는 raw lexeme를 보존해 canonical `0|[1-9][0-9]*`만 허용합니다.
Protocol version range는 0..65,535, count는 각 0..2,048입니다. Request sender의 version은 exact
lexeme `1`입니다.

Request precedence는 byte/framing/duplicate → required version presence/type/lexeme/range → supported
version → exact command/remaining schema입니다. Response는 transport exit → byte/framing/duplicate →
required version coordinate → supported version → remaining closed schema/unknown fields → logical
success cross-field invariants → logical outcome입니다. Duplicate version+mismatch는 invalid response,
otherwise-valid canonical version 2는 `project_protocol_incompatible`, individual count lexeme/range/digest
grammar error는 invalid response입니다. Success는 `source_count == definition_count`, both-zero이면 exact
empty digest `sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`, positive이면 empty digest 금지입니다.
Violation은 `invalid_project_runner_response`/3이고 malformed individual field가 먼저입니다. Raw document가
없어 다른 valid-looking nonempty digest truth는 global이 recompute하지 않고 linked response를 trust합니다.
MIG-071 exact mismatch wire는 다음 one value + EOF이고 transport exit 0, dispatch/Load 0입니다.

```json
{"protocol_version":2,"status":"ok","result":{"source_count":0,"definition_count":0,"definition_set_digest":"sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"}}
```

Runner가 valid success/error를 썼다면 transport exit 0입니다. Nonzero/signal/no response는 transport
failure입니다.

Linked request parser는 byte/framing/duplicate/UTF-8/type/noncanonical/out-of-range/remaining-schema
fault를 `migration_project_protocol_error/invalid_project_runner_request`, otherwise-valid canonical
in-range request version !=1을 `/project_protocol_incompatible`로 분류합니다. 두 pair만 exact
`protocol_version:1`, `status:"error"` response envelope의 logical error로 쓰고 transport exit 0을
사용합니다. Canonical closed objects는 다음과 같습니다.

```json
{"protocol_version":1,"status":"error","error":{"category":"migration_project_protocol_error","code":"invalid_project_runner_request"}}
```

```json
{"protocol_version":1,"status":"error","error":{"category":"migration_project_protocol_error","code":"project_protocol_incompatible"}}
```

이는 response envelope coordinate 자체가 unsupported여서 global parser가 만드는 MIG-071의
`project_protocol_incompatible`와 다른 observation입니다.

Wire의 process/public observation은 위 closed `result`/`error`뿐입니다. Call/publication counter와
`LoadReport` stage/pointer/reason 등 detailed loader/planning context는 test-only oracle instrumentation이며
wire/public API에 없고 global classification에 쓰지 않습니다. Feasibility gate는 linked actual context와
end-to-end wire가 detail을 strip하면서 category/code pair만 보존하는 동작을 따로 검증합니다.
Build stdout/build stderr/runner stderr raw retained prefix도 wire/oracle/user stream에 없고 test-only
`retained_bytes`/`truncated`만 관찰합니다. Cancel은 capture stop/parent pipe close/drainer join, normal
completion은 EOF drainer join 뒤 scalar를 확정하고 raw bytes를 public publication 전에 discard합니다.
Public stderr는 category/code one-line뿐입니다. User-visible raw diagnostic exposure는 후속입니다.

Failure vocabulary와 public exit도 closed입니다.

| Category | Allowed code | Exit |
|---|---|---:|
| `migration_project_command_error` | `invalid_arguments` | 2 |
| `migration_project_selection_error` | `project_not_found`, `project_search_limit_exceeded`, `invalid_project_descriptor`, `project_descriptor_incompatible` | 2 |
| `migration_project_selection_error` | `project_selection_failed` | 3 |
| `migration_project_build_error` | `project_temporary_storage_failed`, `project_build_failed` | 3 |
| `migration_project_protocol_error` | `invalid_project_runner_request`, `project_runner_failed`, `project_protocol_incompatible`, `invalid_project_runner_response` | 3 |
| `migration_project_process_error` | `project_canceled`, `project_cleanup_failed` | 3 |
| `migration_project_process_error` | `project_interrupted` | 130 |
| `migration_definition_discovery_error` | `invalid_project_source_config`, `invalid_source_root` | 2 |
| `migration_definition_discovery_error` | `invalid_source_entry`, `unsafe_source_entry`, `source_catalog_limit_exceeded` | 1 |
| `migration_definition_discovery_error` | `source_discovery_failed`, `source_read_failed` | 3 |
| `migration_definition_source_error` | ADR-0020 exact 9 source code | 1 |
| `migration_graph_error` | `invalid_node`, `duplicate_node`, `invalid_dependency`, `duplicate_dependency`, `dependency_not_found`, `dependency_cycle` | 1 |
| `migration_project_internal_error` | `project_internal_error` | 3 |

Global-owned failure는 위 pair로 생성합니다. Runner logical response는 linked request parser의 exact
`migration_project_protocol_error/invalid_project_runner_request`와 `/project_protocol_incompatible`,
discovery closed pair와 exact definition source/graph category/code pair pass-through만 허용하고 pair를
reclassify하지 않습니다. 그 밖의 global-owned protocol/build/process pair는 runner response에서
거부합니다.
Detailed context는 linked test instrumentation에만 남으며 user-visible file/pointer diagnostic 부재는
protocol v1의 의도적 제한입니다. Global-owned/unknown pair를
runner가 보내면 `invalid_project_runner_response`, exit 3입니다. Root traversal/readdir permission은
`source_discovery_failed`, private temp creation은 `project_temporary_storage_failed`, pipe/drainer/temp
cleanup은 `project_cleanup_failed`, 나머지 invariant breach는 `project_internal_error`입니다. Final user
output은 successful cleanup 뒤에만 publish합니다. Success는 stdout 한 번, error는 stdout 0/bounded
stderr 한 번이고 runner stdout response와 별개입니다. Cleanup failure는 예정 success/interrupted/
canceled만 대체하며 이미 선택된 non-cancel primary는 보존하고 cleanup failure를 metric에 남깁니다.

### Linked flat discovery

Linked project config가 semantic root list를 제공하되 public type/API는 후속으로 둡니다. Root Go
string은 config preflight에서 valid UTF-8이어야 하며 invalid byte는 filesystem/SourceID/Load 전에
`invalid_project_source_config`/2입니다. 그 뒤 clean project-root-relative slash grammar를 적용하고
unique canonical roots를 raw UTF-8 byte order로 처리합니다.
Retained project-root handle부터 각 intermediate/final component의 raw entry name equality를 확인한 뒤
no-follow directory-handle traversal하고 final handle로 entries를 열거합니다. Initial exact-name
absence/wrong-case 또는 stable initial non-directory/symlink는 `invalid_source_root`/2입니다. Initial
real-directory entry 선택 뒤 open/Fstat ENOENT, identity/non-directory/symlink swap, disappearance와
permission/I/O는 `source_discovery_failed`/3입니다. Precheck 뒤 path를 다시 여는 방식은 허용하지
않습니다.

All sorted root-handle traversal/preflight를 먼저 완료한 뒤 canonical root path order로 retained final
handles를 bounded `ReadDir` chunks로 enumerate합니다. Entries+non-EOF error가 같은 call에서 나오면
`source_discovery_failed`/3이 entries/cap보다 우선합니다. Successful entry를 checked-add해 65,537번째에
즉시 `source_catalog_limit_exceeded`/1로 멈추고 later roots/ReadDir을 실행하지 않습니다. Cap 이하로
완료한 뒤만 retained names를 raw full SourceID path order로 sort해 candidate stage로 갑니다.

각 root immediate entry만 검사합니다. Case-sensitive `*.godj.json`만 candidate이고 recursion하지
않습니다. Nonmatching regular file과 ordinary subdirectory는 ignore합니다. Matching
symlink/directory/non-regular는 target을 열지 않고 `unsafe_source_entry`입니다. Hardlink는 regular
file이며 inode alias를 만들지 않습니다. Candidate SourceID는 clean project-relative slash path이고
byte order로 정렬합니다. Retained root handle에서 candidate를 no-follow open/Fstat하고 post-read
same-entry를 재확인합니다. 모든 bounded read outcome 뒤 handle을 close하고 retained root에서 mandatory
post-read check를 먼저 분류합니다. Symlink/non-regular/identity swap은 `unsafe_source_entry`/1,
disappearance/post-check permission·I/O는 `source_read_failed`/3이며 simultaneous original read/cap
outcome보다 우선합니다. Stable identity에서만 original non-EOF read I/O를 `source_read_failed`, 그 다음
document/batch maximum+1을 `source_catalog_limit_exceeded`로 분류하고 clean EOF를 계속합니다.

Candidate suffix와 ordering은 raw filename bytes에 적용합니다. Matching entry는 SourceID byte cap을
먼저 거친 뒤 UTF-8을 검증하며 invalid UTF-8은 open 전 `invalid_source_entry`, exit 1입니다. Metric은
string 대신 lowercase `path_bytes_hex`를 사용합니다. Nonmatching invalid-byte entry는 aggregate entry
cap에만 포함하고 ignore합니다.
Candidate precedence는 entry cap 뒤 full logical path의 SourceID byte cap → UTF-8 → no-follow
regular/identity safety → aggregate source count입니다. Candidate fault 하나라도 source-count보다 먼저
실패하고, 여러 entry는 raw full path winner, 같은 entry는 이 reason order를 사용합니다.

Zero roots/empty roots도 canonical empty slice로 actual `definition.Load()`를 정확히 1회 호출합니다.
Valid discovery 뒤 product loader를 최대·정확히 1회 호출하고 loader error category/code와 atomic
partial-result 0을 보존합니다. Global/linked orchestration의 direct `migrations.NewPlanner`는 0회지만
Load는 decoded graph stage에서 loader-owned NewPlanner를 정확히 한 번 호출합니다. Base LoadReport
planner count는 MIG-065..068=1, MIG-073 document failure=0, Load 0 rows=0이고 graph-stage mutation은
Load 1/planner 1입니다. DB/recorder/`Executor.Migrate`/revision lifecycle execution은 0입니다.

Test-only proof의 global orchestration helper는 product loader를 import하지 않습니다. Linked
fixture/runner만 existing `migrations/definition`을 import해 actual `Load`를 호출하며 production package가
`conformance/projectcheck` harness를 import하지 않습니다. 이는 product API 추가가 아닙니다.

Canonical discovery bases는 exact 다음과 같습니다. MIG-065/066은 root `migrations`의
`0001_initial.godj.json` → SourceID `migrations/0001_initial.godj.json`; MIG-067은 empty
`migrations`; MIG-068은 configured roots `["migrations/z","migrations/a"]`, z의
`0001_initial.godj.json`+ignored `notes.txt`, a의 `0002_fields.godj.json` → sorted SourceIDs
`migrations/a/0002_fields.godj.json`, `migrations/z/0001_initial.godj.json`; MIG-069는 root
`migrations`의 unsafe `link.godj.json`이고 Source handoff가 없습니다. MIG-073은 root `migrations`의
`broken.godj.json` → SourceID `migrations/broken.godj.json`입니다. MIG-068은 root order와 z entry
order를 각각 permute하며 aggregate entries 3/read 2입니다.

Actual LoadReport `(documents,headers,operations,planner,definitions,sets)`는 MIG-065/066
`(1,1,1,1,1,1)`, MIG-067 `(0,0,0,1,0,1)`, MIG-068 `(2,2,3,1,2,1)`, MIG-073
`(1,0,0,0,0,0)`, Load 0 rows all-zero입니다. MIG-073 FailureContext는 stage `document`, SourceID
`migrations/broken.godj.json`, pointer `/migration/name`, reason `duplicate_key`, empty app/name/limit,
operation index -1, maximum/actual 0, empty graph sources입니다. Success failure context는 absent이고
Load 0 rows에는 LoadReport/failure context가 없습니다.

DB-free는 orchestration-direct NewPlanner 0과 **GoDj-owned** DB open/query, recorder,
`Executor.Migrate`/revision lifecycle execution call 0입니다. Loader-owned graph validation NewPlanner는
포함하며 project binary의 arbitrary user package `init()` external side effect까지 없다고 보장하지
않습니다.

### Limits, precedence와 exit

Parsed/accepted input·catalog·wire 또는 retained-output inclusive cap은 descriptor 64 KiB, ancestor
128(start=1), roots 256, aggregate entries 65,536,
sources 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB, request 64 KiB,
response 64 KiB, diagnostic stream 1 MiB의 11개입니다. Diagnostic cap은 build stdout, build stderr,
runner stderr 각각의 **retained prefix**에 독립 적용하고 runner stdout은 64 KiB response cap입니다.
Numeric 합은 overflow-safe입니다. Regular document는 per-file/remaining-batch max+1까지만 읽고 모든
outcome 뒤 close+mandatory same-entry check를 먼저 분류합니다. Post-check safety/I/O가 simultaneous read
error/cap보다 우선하고 stable identity에서만 original read I/O 뒤 cap을 판정하며 EOF까지 읽지 않습니다.
Request/response/diagnostic pipe만 cap 뒤
remainder를 동시에 EOF까지 drain하고 각 diagnostic은 first 1 MiB + stream별 deterministic
truncation만 보존합니다.

이 11개는 build/runner wall time, CPU/RSS/address space, goroutine/thread/process count, binary/private
HOME/cache/temp bytes·inode, module/network transfer나 post-cap pipe drain bytes/time을 제한하지 않습니다.
64 KiB/1 MiB도 total pipe I/O cap이 아닙니다. Sandbox/rlimit/cgroup/quota/hard timeout은 없고 normal
runner는 caller cancel/SIGINT 전 무기한 실행될 수 있으며 host FS/OS limit와 CI timeout에 의존합니다.
65,536 entry cap은 final source-root immediate entries에만 적용합니다. Ancestor/explicit descriptor
parent marker scan과 configured root component-parent raw-name scan은 one-entry-at-a-time stream/no aggregate
retention이지만 cumulative entry count/name bytes/time cap이 없고 host per-entry name bound에 의존합니다.
이 pre-scan hardening은 product follow-up입니다.
Root count 256 외 per-root/aggregate Go-string bytes, component count와 validation time cap은 없습니다.
Empty root/zero-candidate catalog는 SourceID cap을 exercise하지 않으며 root-string hardening을 새 12번째
cap으로 추가하는 결정은 후속입니다.

Global precedence는 arguments → project selection → descriptor byte/framing/full lexical+closed shape →
descriptor version → build → runner transport/framing → protocol coordinate → supported version →
remaining response schema → logical outcome입니다. Linked side는 request byte/framing/duplicate → protocol
coordinate → supported version → remaining request schema → all sorted root-handle preflight → canonical-root
ReadDir I/O/entry cap → SourceID byte cap/UTF-8 → no-follow regular/identity safety → source count → sorted
bounded read provisional outcome → mandatory close/post-read same-entry check → stable original-read
I/O/document+batch cap classification → `definition.Load` once → response입니다.

Public exit은 0 valid, 1 completed-invalid source/definition/graph, 2 arguments/project/descriptor/config,
3 build/filesystem/runner/protocol/internal, 130 Unix SIGINT입니다. Build/runner child는 owned process
group으로 실행하며 cleanup 보장은 normal return, caller context cancellation과 handled Unix SIGINT에
한정합니다. Single coordinator가 각 ordered stage barrier의 advance/terminal commit 직전에 handled
SIGINT, 그 다음 caller cancel을 확인하고 terminal outcome을 한 번 atomic commit합니다. Cancel이 먼저면
later child result를 무시하고 final success/non-cancel primary가 먼저면 later signal이 primary를 바꾸지
않습니다. Active unreaped child가 있을 때만 cancel/handled SIGINT에 group SIGINT를 보내고 exact 2초
grace 뒤 direct child 상태와
무관하게 stored owned pgid에 group SIGKILL을 attempt합니다. Initial forward와 escalation signal에서
ESRCH만 already-gone이며 다른 syscall error는 `project_cleanup_failed`/3입니다. Capture를 멈추고
parent-owned pipe read handle을
강제 close한 뒤 capture goroutine과 독립인 direct OS wait로 owned direct child를 synchronous
`Wait`/reap하고 drainer를 join합니다. 이어 diagnostic scalar를 확정하고 raw prefix를 discard한 뒤
private temp `RemoveAll`을 한 번 attempt하고 retained-parent absence를 post-check합니다. Normal completion도
EOF join → scalar → discard → temp cleanup 순서입니다. Arbitrary process가 waitable해지는
total hard bound는 주장하지 않습니다. Same-pgid 또는 pgid를 이탈한 non-child descendant의
return-time disappearance/reap은 sandbox/subreaper가 없는 이번 contract에서 보장하지 않습니다.
Child가 아직 spawn되지 않았거나 이미 reaped면 group forward/escalation 없이 같은 interrupted/canceled
outcome과 parent/temp cleanup을 수행합니다. Ordered barrier와 both-race mutation gate를 둡니다.

User output 전에 primary error, success 또는 cancellation outcome 하나를 고른 뒤 cleanup합니다.
Cleanup 성공 뒤 success만 user stdout 1회, error는 stdout 0/stderr human message 1회이며 runner
response write는 별도 counter입니다. SIGINT는 applicable active-child escalation/direct reap(if any)과
parent cleanup 뒤 `project_interrupted`/130, 다른 context cancellation은 `project_canceled`/3입니다. Cleanup
failure는 success/interrupted/canceled만 `project_cleanup_failed`/3으로 대체합니다. 이미 선택된
non-cancel primary는 보존하고 cleanup failure를 metric에 남깁니다. `project_internal_error`는 undefined
invariant가 처음 발생한 primary일 때만 사용합니다. Structured return 전 owned direct child/drainer는
남기지 않지만 temp residue 0은 removal+absence post-check success에서만 보장합니다. Injected failure는
`temp_cleanup_attempts=1`, `cleanup_failed=1`, `residual_temp=1`이며 raw path를 output하지 않고 residual이
남을 수 있습니다. Automatic retry/scavenging하지 않습니다.

SIGTERM/SIGHUP/other fatal signal/SIGKILL, parent/host crash와 power loss는 structured exit, group
escalation 또는 temp cleanup을 보장하지 않고 caller context cancellation/3으로 합치지 않습니다.
Signal-specific exit와 stale-temp scavenging은 후속입니다. Exactly-once stdout/stderr와 partial stdout
0은 healthy injected/base sink에서 cleanup 뒤 single bounded write attempt에만 적용됩니다. EPIPE/
SIGPIPE/EBADF, short write, disk-full/caller-closed sink에는 delivery/rollback/zero partial/portable exit을
보장하지 않으며 detectable failure가 exit 3이어도 실패한 stderr로 structured error를 전달한다고
주장하지 않습니다.

### Hosted CI topology

Public-repository implementation evidence는 existing `ubuntu-24.04` x64 full job과 `macos-15` arm64
exact job을 보존하고, 각각 focused project-check normal test를 추가합니다. 별도 required matrix 두 개는
각각 `strategy.fail-fast: false`, leg timeout 20분, Go 1.26.5와 exact labels
`ubuntu-22.04` x64, `ubuntu-24.04-arm` arm64, `macos-15-intel` x64, `macos-26` arm64를 사용합니다.
2026-08-09 현재 이 label/architecture는 official GitHub-hosted public runner table에서 확인했습니다.
각 entry는 expected coordinate `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`를 같은 순서로
가지며 `go env GOOS`/`GOARCH`가 expected와 exact 같은지 assertion해 misrouting을 실패시킵니다.

Project-check matrix의 각 leg는 normal/race/CGO-disabled/vet를
`./conformance/projectcheck`에 실행합니다. SQLite matrix는 같은 네 gate를 current actual packages
`./migrations ./db/sqlite`에 실행합니다. 각 leg는 pinned checkout/setup-go, `go version`과
`go env GOOS GOARCH` fingerprint를 요구하고 `continue-on-error`를 허용하지 않습니다. 따라서 target은
existing 2 + project-check 4 + SQLite 4의 exact **10 hosted job executions**입니다. Existing artifact/
Python/oracle gate는 제거하지 않습니다.
Static topology test는 YAML top-level 4 job definitions를 세는 대신 두 matrix를 expand해 2+4+4=10을
계산하고 exact label/coordinate, fail-fast false, timeout, no-continue-on-error와 command set을 pin합니다.
각 new leg의 마지막 `git diff --exit-code`와 empty `git status --porcelain=v1`도 pin해 tracked rewrite와
untracked/staged/unstaged repository residue를 모두 실패시킵니다.

Windows native process/path contract가 없으므로 Windows runner를 green skip하지 않고 지원 주장을
유보합니다. PostgreSQL/MySQL adapter/product contract가 없는 상태에서 service만 띄우는 job도 false
green이므로 금지합니다. Future backend의 첫 required job은 digest-pinned service image,
health check, UTC timezone과 C locale 또는 명시적으로 승인된 collation, actual query/write/
transaction/schema/migration/recorder/revision-lifecycle 및 durable restart/persistence contract를 모두
실행해야 합니다. Expected contract 수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음,
final clean worktree도 필수입니다. Adjacent versions는 이후 non-required scheduled matrix로만
분리합니다. Exact
implementation head의
existing 2 + project-check 4 + SQLite 4, 총 10-job topology는 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)에서 모두 통과했습니다.
이후 status 7 + general 9의 exact 16-file completion-documentation commit
`34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)의 같은 exact 10 job을
모두 통과했습니다. 현재 EVID-026 append/status 교정의 exact 8-file patch 자체의 hosted CI는
`not run/pending`입니다.

### Compatibility classification

Exact scenario slugs are:

| ID | Slug | Canonical base |
|---|---|---|
| MIG-065 | `godj.migration.project_check.nested_project_success` | Nearer one-model vs outer empty; counts 1/1, digest `07e61f8d...`, build/runner/read/Load 1/1/1/1 |
| MIG-066 | `godj.migration.project_check.explicit_project_override` | Nearer empty overridden by explicit one-model; ancestor 0, same counts/digest/calls |
| MIG-067 | `godj.migration.project_check.empty_catalog` | One existing empty root; read 0/Load 1, counts 0/0, digest `53f20df4...` |
| MIG-068 | `godj.migration.project_check.canonical_filesystem_order` | Two roots/MIG-057 two documents; read 2/Load 1, counts 2/2, digest `5a73e03d...` under permutations |
| MIG-069 | `godj.migration.project_check.unsafe_source_entry` | Matching symlink, `unsafe_source_entry` |
| MIG-070 | `godj.migration.project_check.project_not_found` | Filesystem root reached without marker, `project_not_found` |
| MIG-071 | `godj.migration.project_check.project_protocol_incompatible` | Exact otherwise-valid version-2 success wire above, transport 0/dispatch·Load 0 |
| MIG-072 | `godj.migration.project_check.project_build_failure_atomic` | Syntax-broken main, build 1/downstream 0 |
| MIG-073 | `godj.migration.project_check.definition_load_failure` | MIG-061 duplicate-name document, read 1/Load 1, source error |
| MIG-074 | `godj.migration.project_check.invalid_runner_response` | Duplicate top-level `status`, invalid response |

Exact full digests, failure context and publication/call counters are the completed GDJ-0021 work에 기록한
single-base table과 machine artifact가 함께 고정합니다. Success MIG-065..068만 user stdout 1/error stderr
0이고 MIG-069..074는 stdout 0/stderr 1이며 partial stdout은 모두 0입니다.

Machine oracle `metrics`는 exact 24 fields만 허용합니다:
`build_calls`, `runner_calls`, `runner_response_writes`, `source_reads`, `load_calls`,
`documents_received`, `headers_validated`, `operations_decoded`, `planner_construction`,
`definitions_published`, `definition_sets_published`, `direct_planner_calls`, `godj_db_calls`,
`revision_lifecycle_calls`, `user_stdout_writes`, `user_stderr_writes`, `partial_stdout_writes`, `exit_code`,
`command_dispatches`, `ancestor_directories_inspected`, `descriptor_reads`, `roots_opened`,
`directory_entries_seen`, `failure`; extra field는 거부합니다. `failure`는 항상 존재하고 MIG-073만
completed work에 기록한 full object, success/Load 0 rows는 null입니다. Temp/diagnostic/process scalar는 oracle에 absent인
feasibility-only 값입니다. MIG-070 temp create/cleanup attempt/failure/residual은 `0/0/0/0`, 나머지 base는
`1/1/0/0`; 모든 base group SIGINT/SIGKILL attempt는 0이고 direct-child reap은
`build_calls+runner_calls`입니다. Joined raw diagnostic은 absent이고 host-dependent retained byte/truncation은
harness-only입니다. Cancellation/cleanup/oversize mutation은 관련 scalar를 test에서 exact 고정합니다.

MIG-065..074는 모두 `decision/ADR-0021/derived=false`인 독립 decision oracle입니다. 완료 결과는
11 reference set/115 unique contract/110 ordered cross-binding과 새 10 `oracle_locked`입니다. Product는
10 adapter/105 contract의 `100 passing + 5 deviation`을 유지하며 새 product adapter를 만들지
않습니다. Static comparison은 exit 1/ordered mismatch 10, product `godjcheck`는 새 scenario를
conformance-tool exit 2/no actual로 거부합니다.

## 결과

- Global/project ownership과 source loader 사이에 검증 가능한 최소 process boundary가 생깁니다.
- Project selection, flat path와 failure/exit 의미가 product API보다 먼저 안정됩니다.
- Build/user code 실행 비용과 `init()` side effect 가능성은 남습니다.
- Unix process ownership과 filesystem no-follow 구현 난도가 추가됩니다.
- 이 Accepted ADR과 test-only code만으로 사용 가능한 product command를 제공하지 않습니다.

## 의도적으로 결정하지 않은 것

- Public global CLI/project registration package와 exact Go 타입
- Production binary direct command dispatcher와 installed/persistent runner cache
- Full CLI/library/generator semver negotiation, repair/upgrade
- Recursive/module/embed/remote/watch discovery와 Windows process/path semantics
- Build/runner hard resource/time sandbox, non-handled signal/crash cleanup과 stale-temp scavenging
- Broken/closed user output sink의 atomic delivery와 loader file/pointer detail을 담는 protocol extension
- Captured build/runner raw diagnostic을 user에게 노출하는 policy
- Explicit auth/VCS/helper/agent environment와 OS-account home의 exhaustive isolation
- Same-user concurrent temp-base rebind/rename와 path-resolution race fencing
- Writer/codec v2/custom/data operation, DB/applied history/migration execution
- Multi-DB/non-SQLite, adoption/repair/crash reconciliation

## 검증

- MIG-065..074 independent manifest/oracle/static fixture와 exact outcome/metrics
- Descriptor/marker/package parser table, invalid-nearest/no-fallback와 explicit descriptor test
- Strict request/response protocol mutation, transport/version/schema precedence와 output cap test
- Temp filesystem no-follow/root/source order/hardlink와 post-read safety-over-cap TOCTOU fault tests
- Build argv/env/project-tree/private-output and cancel/process-group/reap tests
- 11 cap 각각 maximum-1/equal/+1, overflow와 combined precedence
- Loader exactly once/LoadReport, orchestration-direct planner 0, DB/revision lifecycle 0와 partial result 0 sentinel
- 11 set/115 contracts/110 cross-binding, static/product fail-closed and checksum append-only gate
- Exact 10-job hosted topology의 Linux/macOS x64/arm64 project-check/SQLite normal/race/CGO-disabled/vet와 independent review

위 검증은
[EVID-20260810-024](../status/TEST_EVIDENCE.md#evid-20260810-024--gdj-0021-project-linked-migration-check-compatibility-contracts)와
[EVID-20260810-025](../status/TEST_EVIDENCE.md#evid-20260810-025--gdj-0021-github-hosted-10-job-implementation-head-ci),
completion-documentation exact-head 재검증은
[EVID-20260810-026](../status/TEST_EVIDENCE.md#evid-20260810-026--gdj-0021-github-hosted-completion-documentation-head-10-job-ci)에
기록했습니다. Local normal/race/CGO-disabled/vet/count-20, `make ci`, exact Python 174/174,
checksum/no-rewrite, 두 independent P0–P3 clean audit와 implementation-head exact 10 hosted jobs가
통과해 이 ADR을 Accepted합니다. Q-010/Q-012는 public CLI/semver/DB-aware lifecycle 전체를 해결하지
않으므로 `Partial`을 유지합니다. Exact 16-file completion-documentation head도 10 hosted jobs를
통과했고, 현재 EVID-026 append/status 교정의 exact 8-file patch 자체의 hosted CI만
`not run/pending`입니다.
