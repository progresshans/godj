---
id: GDJ-0055
status: active
updated: 2026-09-02
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "72e5445616e68ff60ba542345684644d0730c5b3"
depends_on: ["GDJ-0042", "GDJ-0045", "GDJ-0046", "GDJ-0049", "GDJ-0054"]
contracts: ["SYS-021..SYS-030", "Q-022"]
allowed_paths:
  - "go.mod"
  - "go.sum"
  - "cmd/godj/**"
  - "project/**"
  - "systemstate/**"
  - "web/sessionauth/runtime_logout_test.go"
  - "internal/projectcheck/**"
  - "internal/compiletest/**"
  - "examples/article/**"
  - "conformance/contracts/**"
  - "conformance/fixtures/**"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/runners/godj/**"
  - "conformance/runners/django/runner.py"
  - "conformance/runners/django/system_state_decisions.py"
  - "conformance/runners/django/tests/test_scenarios.py"
  - "conformance/runners/django/tests/test_system_state_scenarios.py"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/systemstate/**"
  - "conformance/projectmigrateproduct/**"
  - "conformance/projectoperatorproduct/**"
  - "conformance/runserverproduct/**"
  - "conformance/README.md"
  - "Makefile"
  - ".github/workflows/ci.yml"
  - "docs/adr/0047-explicit-single-runtime-system-state.md"
  - "docs/adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md"
  - "docs/adr/0056-explicit-operator-provisioning-and-open-existing.md"
  - "docs/adr/README.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0055-project-linked-explicit-operator-provisioning.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0055 — Project-linked Explicit Operator Provisioning

## 사용자에게 보이는 결과

다음 개발 흐름으로 현재 project의 단일 durable operator를 한 번 생성하고, 이후 raw startup password 없이 Admin/API를
재기동합니다.

```text
godj migrate
godj createsuperuser
godj createsuperuser --project ./godj.toml
godj runserver
```

`createsuperuser`는 interactive terminal에서 username, password, confirmation을 한 번씩만 받고 password echo를 끕니다. 이미
operator가 있으면 덮어쓰기, password 비교, permission 변경이나 성공 합성을 하지 않습니다.

## 목표

- Current `systemstate.Open`에 결합된 empty-table bootstrap과 restart raw-password verification을 explicit
  `ProvisionOperator`와 raw-password-free `OpenExisting`으로 분리합니다.
- Current system migration과 credential/session/audit physical schema, digest, encoded password profile과 cooperative
  database coordination fence를 그대로 사용합니다.
- Exact 두 public argv만 허용하고 invalid/noncanonical/secret-bearing flag는 terminal, project discovery와 build 전에
  거부합니다.
- Valid project를 선택·build한 뒤에만 TTY input을 읽습니다. Username은 bounded canonical text이고 password와 confirmation은
  echo-off byte input으로 받아 일치할 때만 child에 한 번 전달합니다.
- Generic `Command.Stdin []byte` clone 경로와 분리한 one-shot sensitive process owner, bounded binary request와 strict JSON
  response를 사용합니다. Secret은 argv, environment, filesystem, stdout/stderr, error, report와 artifact에 넣지 않습니다.
- Project가 별도 system-state backend opener와 immutable operator policy를 소유하게 하고 global CLI는 database/auth policy를
  해석하지 않습니다.
- Empty credential state의 exactly-one insert, concurrent cooperative winner 1명, loser의 stable already-provisioned/no-write,
  corrupt/cardinality/missing-schema와 commit-outcome-unknown no-retry를 검증합니다.
- Provisioned Article runtime은 stored encoded credential과 static project policy로 열고, raw admin username/password environment
  requirement를 제거합니다. Not-provisioned만 explicit public-only branch로 허용하며 schema/corrupt/policy failure를 public mode로
  낮추지 않습니다.
- SQLite/PostgreSQL fresh process, Admin/API login, restart, cancellation/reap, known-created cleanup outcome과 repository-external
  public project를 product observation으로 검증합니다.

## 비목표

- 두 번째 user, User/Group model, staff/superuser bit, object permission과 universal wildcard permission
- password change/reset/delete, permission 자동 확장, account disable lifecycle와 session-family revocation
- `--username`, `--password`, `--password-file`, `--noinput`, environment/file/stdin-line fallback 또는 secret-manager integration
- non-TTY automation, Windows, production deployment/proxy/TLS와 remote/non-loopback Admin publication
- automatic migrate/adopt/repair, retry/reopen, non-cooperative writer와 distributed policy negotiation
- Schema DSL/IR, Query AST, migration Definition/ProjectState/digest, generated role ABI 또는 system migration/schema 변경
- Q-019 SQLite unknown-outcome terminal quarantine 구현. 이 P1 hardening은 이번 command의 선행 blocker가 아니며 별도 packet으로
  즉시 뒤따릅니다.
- Go heap/core dump/swap/ptrace에서 password byte가 완전히 사라진다는 보장. Current string-based PasswordHasher 경계에서
  best-effort buffer clear보다 강한 zeroization은 주장하지 않습니다.

## 결정할 경계

### Current-only system-state API

Phase B에서 다음 의미의 current public surface를 고정합니다. Exact 이름은 ADR-0056과 source가 authority입니다.

```go
type CredentialPolicy struct {
    Principal      auth.Principal
    PasswordHasher auth.PasswordHasher `json:"-"`
}

type RuntimeConfig struct {
    CredentialPolicy CredentialPolicy
    SessionLimits    sessions.Limits
    MaxSessions      int
    AuditCapacity    int
}

type ProvisionOperatorConfig struct {
    Username         string
    Password         string `json:"-"`
    CredentialPolicy CredentialPolicy
}

func ProvisionOperator(context.Context, Backend, ProvisionOperatorConfig) error
func OpenExisting(context.Context, Backend, RuntimeConfig) (*Runtime, error)
```

- `BootstrapConfig`과 implicit-bootstrap `Open`은 pre-release current-only policy에 따라 shim 없이 제거합니다.
- `CredentialPolicy`의 immutable `auth.Principal`과 password-hash profile은 project code가 소유합니다. Stored row가 소유하는
  username/encoded password와 섞거나 CLI flag로 열지 않습니다. 세 config의 `String`/`GoString`은 redacted입니다.
- `ProvisionOperator`는 password hash를 coordination fence 밖에서 준비하되 authoritative readiness/cardinality와 insert를 한 번의
  `CoordinatedAtomic` callback에서 수행합니다.
- Exactly one existing valid row는 supplied username/password가 같든 다르든 password 비교 없이 `credential_already_exists`입니다.
  2+는 cardinality, malformed/profile-invalid row는 corrupt, structurally valid하지만 project principal/active/permission policy와
  다른 row는 credential-policy mismatch입니다. 어떤 경우도 overwrite/update/delete하지 않습니다.
- Commit/transaction outcome unknown은 retry하지 않고 stable persistence outcome을 반환합니다. 새 backend의 `OpenExisting` 또는
  login이 reconciliation authority이며 command가 성공을 추측하지 않습니다.
- `OpenExisting`은 stored username/hash/principal/permissions/definition digest를 strict하게 검증하고 authenticator를 구성하지만 raw
  password를 요구하거나 DB write를 하지 않습니다.
- Exact system migration이 없거나 다르면 schema-unavailable입니다. 그 migration이 적용된 뒤 credential/session/audit가 모두 빈
  clean state만 credential-absent이고 Article composition이 이 exact code만 public-only 선택에 사용할 수 있습니다. Unavailable
  table, dependent rows without credential, malformed row와 policy mismatch는 startup failure입니다.

Stable system-state codes는 `credential_absent`, `credential_already_exists`, `credential_policy_mismatch`를 추가하고 기존 raw
password/bootstrap identity를 합쳐 표현하던 `credential_mismatch`를 제거합니다. Transaction/commit outcome marker는 outer
`persistence_failure` cause chain에 보존합니다.

### Project ownership

- `project.Config`는 migration opener와 이름상 혼합하지 않은 `OpenSystemStateBackend` 및 secret-free immutable
  `SystemOperatorPolicy`를 갖습니다.
- Private linked runner가 request를 완전히 검증한 뒤 backend를 정확히 한 번 열고, provision을 한 번 호출하고, close를 한 번
  완료한 뒤 response를 게시합니다.
- Known commit 뒤 backend close나 outer workspace cleanup이 실패하면 generic failure로 creation 여부를 지우지 않습니다.
  Stable `operator_created_*_cleanup_failed` outcome이 no-retry/reconciliation ownership을 보존합니다.
- Linked runner가 insert를 확정했지만 private response를 게시하지 못한 경우 canonical project-runner main은 cause-free
  `project.RunnerExitCode`를 사용합니다. Normal direct-child reserved exit 86은 known-created output failure, exit 87은 그보다 먼저
  발생한 backend cleanup failure를 보존합니다. 이 값들은 global public exit가 아니며 global command는 각각 stable public
  failure와 exit 3으로 다시 닫습니다.
- Started child에 exact private request 전체가 한 번 전달됐지만 strict terminal response나 trusted reserved exit를 얻지 못하면
  생성 여부를 추측하지 않습니다. `operator_provision_outcome_unknown`은 `KnownCreated=false`를 “미생성”이 아니라 “알 수 없음”으로
  표현하고 fresh `OpenExisting`/login reconciliation 전 retry를 금지합니다. Partial request는 strict framing상 mutation을 시작할 수
  없으므로 기존 transport failure를 유지합니다.
- Actual external SQLite project는 reserved exit 86/87, post-commit abort, empty/malformed/over-limit response를 각각 별도 fresh DB에서
  실행합니다. Global process 종료 뒤 runner marker가 정확히 한 줄인지 다시 확인하고, credential row 1개를 새 backend의
  `OpenExisting`과 supplied password 인증으로 reconcile하므로 row shape나 조기 marker만으로 no-retry를 성공 처리하지 않습니다.

### TTY input과 secret pipe

Public argv는 정확히 다음 두 형태입니다.

```text
godj createsuperuser
godj createsuperuser --project PATH
```

- stdin이 terminal이 아니면 `input_not_terminal`로 실패합니다. 이번 packet에는 line/JSON pipe fallback이 없습니다.
- Prompt는 stderr에 `Username: `, `Password: `, `Password (again): ` 순서로 한 번씩만 씁니다.
- Username은 valid UTF-8 1..256 bytes, trim identity, NUL/ASCII control 없음입니다. Password는 valid UTF-8 1..1024 bytes,
  CR/LF/NUL 없음, all-whitespace 아님이며 leading/trailing spaces는 byte-exact하게 보존합니다.
- `golang.org/x/term`을 direct dependency로 사용하고 실제 PTY에서 no-echo와 error/SIGINT 뒤 terminal restore를 검증합니다.
- Private terminal read는 `poll` 뒤 VINTR가 queued input을 flush하는 race에서도 interrupt barrier로 돌아와야 합니다. Darwin/Linux
  모두 noncanonical `VMIN=0`, `VTIME=1`의 bounded read를 사용하고 zero-byte timeout은 failure가 아니라 barrier 재검사로
  처리합니다. Kernel `ISIG`/VINTR 의미를 manual byte fallback으로 대체하지 않습니다.
- Parent는 password와 confirmation을 `[]byte`로 비교하고 confirmation을 child에 보내지 않습니다. 모든 mutable secret buffer는
  사용 직후 best-effort `clear`합니다.
- Private request는 fixed `GODJCSU1` magic, big-endian uint16 username/password lengths, exact payload와 EOF를 가진 최대 1,292-byte
  binary frame입니다. Response만 최대 4 KiB strict canonical JSON을 사용합니다.
- Sensitive child owner는 private stdin pipe를 한 번 쓰고 닫으며 shell/argv/env/file을 사용하지 않습니다. Bounded output drain,
  process-group SIGINT/grace/SIGKILL, direct-child reap와 held-pipe descendant cleanup을 소유합니다.
- Private response fd 1과 global public result fd 1/fd 2의 broken pipe가 Unix 기본 SIGPIPE로 known-created fact나 stable exit를
  지우지 않도록 project facade와 global command는 각각 createsuperuser invocation에서만 buffered SIGPIPE receiver를 설치합니다.
  SIGPIPE는 operator context cancellation로 변환하지 않으며 write는 EPIPE로 돌아옵니다. Private failure는 reserved normal exit로,
  global publication failure는 public exit 3으로 닫힙니다.

### Public result와 error

- 성공 stdout은 정확히 `{"status":"created"}\n`, exit 0, one write/no retry입니다.
- 성공 게시 전의 logical failure stdout은 zero이고 stderr에는 이미 게시된 prompt 뒤 stable `category/code\n`만 한 번 씁니다.
- Invalid argv/input/selection은 exit 2, product refusal은 exit 1, infrastructure/protocol/backend/persistence/cleanup은 exit 3,
  interrupt는 130입니다.
- Raw child stderr, hasher/backend cause, username/password/hash, DB URL/path와 partial private bytes는 public error/unwrap/protocol/report에
  포함하지 않습니다.
- Success publication short-write/error는 durable create를 되돌리거나 retry하지 않으며, stderr에 stable known-created output failure를
  한 번 게시하고 exit 3으로 닫습니다. stderr 자체가 broken pipe여도 SIGPIPE 종료로 바뀌지 않고 exit 3을 유지합니다.
- Complete request 뒤 child exit/signal/cancellation, empty/malformed/over-limit response처럼 durable outcome을 확인할 수 없는 경우는
  `operator_project_process_error/operator_provision_outcome_unknown`, exit 3으로 닫고 reconciliation을 요구합니다.

## Contract 계획

SYS-021..030은 기존 system-state profile을 확장하는 GoDj decision contracts입니다. Existing SYS-001..020과 Django login
semantics를 회귀 authority로 재사용하지만 Django User model/management-command internals를 복제하지 않습니다.

| ID | Required observation |
|---|---|
| SYS-021 | implicit bootstrap 제거, explicit provision과 raw-password-free open-existing의 current-only API |
| SYS-022 | exact 두 argv와 pre-I/O rejection; secret-bearing/noncanonical forms 거부 |
| SYS-023 | TTY-only bounded no-echo input, confirmation, restore와 binary one-shot secret transport |
| SYS-024 | request validation 뒤 project-owned one-open/one-provision/one-close와 migration/readiness gate |
| SYS-025 | empty→one, concurrent exactly-one winner, already-one no mutation과 strict cardinality/corruption |
| SYS-026 | rollback/commit/transport outcome unknown no-retry와 actual subprocess의 known-created backend/workspace/output outcome 보존 |
| SYS-027 | stored encoded credential/profile/policy validation과 raw-password-free authenticator startup |
| SYS-028 | migrated clean credential-absent만 public-only branch; schema/corrupt/policy failure downgrade 0 |
| SYS-029 | SQLite/PostgreSQL distinct-process provision, Admin/API login/restart와 secret occurrence 0 |
| SYS-030 | sensitive child cancellation, process-group cleanup, bounded response/redaction와 direct reap |

Phase A는 exact decision/reference artifact와 not-implemented lock만 게시합니다. Product registration과 `passing` 전환은
oracle-blind actual이 존재한 뒤에만 수행하며 aggregate arithmetic과 artifact hashes는 생성 후 측정합니다.

## 단계

- [x] Activation — clean baseline, Proposed ADR-0056, SYS-021..030, Q-022, allowed paths와 비목표 고정 —
  [EVID-178](../docs/status/TEST_EVIDENCE.md#evid-20260831-178--gdj-0055-explicit-operator-provisioning-activation)
- [x] Phase A — exact decision/reference manifest, not-implemented artifact와 protocol/false-green lock
- [x] Phase B — systemstate provision/open-existing split, atomic/error tests와 current-only callsite refreeze
- [x] Phase C — TTY/private/global command, Article SQLite runtime, external project와 secret/process product flow
- [ ] Phase D — PostgreSQL required actual, SYS-021..030 product publication와 source-bound evidence
- [ ] Phase E — affected/full milestone, Linux/386, external archive, exact submitted-head Hosted와 terminal docs

`web/sessionauth/runtime_logout_test.go`는 retired `systemstate.Open`/`BootstrapConfig`를 사용하던 저장소 내 public consumer
회귀 테스트이므로 Phase B current-only callsite refreeze 범위에 exact-file로 추가했습니다. Session-auth 제품 계약이나 구현은
변경하지 않습니다.

네 개의 exact Django runner/reference 파일은 고정된 SYS-021..030 decision/reference case와 pinned system-state oracle을
재생성하기 위해 추가했습니다. Django 제품 구현이나 기존 Django observable semantics를 넓히지 않습니다.
`conformance/runserverproduct/**`는 Article startup에서 raw startup credential 입력을 제거한 current-only API 전환을 실제 public
consumer로 검증하고 migrated credential-absent branch를 구분하기 위한 회귀 범위입니다. Runserver 제품 계약 자체는 확장하지
않습니다.

## Activation 검증

Activation에서는 product source/API/contract registry를 구현·게시하지 않습니다. 문서 link/frontmatter/status consistency와
`git diff --check`만 실행하며 그 결과를 EVID-178에 기록합니다.

## 구현 체크포인트

Phase A-C와 Phase D의 PostgreSQL product path까지 구현했습니다. Affected package normal/race/CGO-disabled, exact
`projectoperatorproduct` normal/race/CGO-disabled, `go vet`, actual PTY/VINTR count-10, portable Python과 pinned historical exact
Python/oracle no-rewrite가 통과했습니다. Source-bound SYS-020/SYS-029 PostgreSQL attestation은 behavioral source를 clean commit으로
동결한 뒤 독립 A/B capture에서 생성해야 하므로 아직 게시하지 않습니다. 현재 checked SYS-020 evidence가 새 source를 거부하고
SYS-029 evidence가 없는 것은 Phase D publication 전의 의도된 fail-closed barrier입니다. 최종 명령·checkout 식별자·결과는 frozen
milestone이 닫힐 때 `TEST_EVIDENCE.md`에 한 번 기록합니다.

Combined consumer gate에서 발견한 두 stale harness도 current 계약에 맞게 교정했습니다. Unmigrated Article은 listener/HTTP 게시
전 `schema_unavailable`로 닫히며, copied runserver fixture는 합성 module 안의 `internal/**` import만 그 module path로 재기준화해
declaration runner build 뒤의 generated-drift preflight를 계속 실제로 검증합니다. 두 경로는 단독 및 combined
normal/race/CGO-disabled에서 다시 통과했습니다.

## 다음 인수인계

Behavioral source를 clean commit으로 동결하고 SYS-020/SYS-029 PostgreSQL attestation을 독립 A/B 환경에서 byte-identical하게
캡처합니다. 정확히 두 evidence JSON, 두 `SHA256SUMS`와 protocol byte lock만 publication descendant에 반영한 뒤 affected/full,
PostgreSQL required inventory, Linux/386, `.git`-free archive와 exact submitted-head Hosted를 실행합니다. 이 gate가 모두 성공하기
전에는 Phase D/E, ADR-0056 또는 global status를 terminal로 승격하지 않습니다. Q-019 terminal quarantine과 two-app
generalization probe는 서로 다른 후속/병렬 evidence lane이며 이 work의 public API나 global status를 동시에 수정하지 않습니다.
