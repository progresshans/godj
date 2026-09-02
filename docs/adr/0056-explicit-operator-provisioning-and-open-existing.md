# ADR-0056: Explicit Operator Provisioning and Open-existing System State

- 상태: Proposed
- 날짜: 2026-08-31
- 관련 work/contract: [GDJ-0055](../../work/0055-project-linked-explicit-operator-provisioning.md), SYS-021..030, Q-022
- 수정하는 ADR: [ADR-0047](0047-explicit-single-runtime-system-state.md)의 implicit bootstrap/restart-password 결정,
  [ADR-0048](0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)의 concurrent bootstrap observation
- 대체하는 ADR: 없음

## 맥락

ADR-0047/0048은 current system migration, durable single credential/session/audit와 cooperative multi-runtime database fence를
검증했습니다. 그러나 public `systemstate.Open`이 empty credential table의 최초 insert와 existing row의 raw-password verification을
동시에 수행합니다. Article authenticated runtime도 매 시작마다 raw username/password environment pair를 요구합니다.

이 shape는 persistence 자체는 검증하지만 관리 명령과 server lifecycle을 뒤섞습니다. 사용자가 `migrate` 뒤 명시적으로 operator를
생성하는 동작, 이미 생성된 credential을 raw startup password 없이 여는 동작, concurrent create와 commit-unknown의 reconciliation
ownership을 각각 표현하지 못합니다. Global command에 password flag/env/file을 추가하면 process metadata와 ambient environment에
secret을 남깁니다. Existing generic child `Command.Stdin []byte`도 clone을 통해 secret copy를 늘립니다.

## 결정 기준

- explicit user intent와 Django-familiar developer loop
- raw secret의 process metadata/environment/filesystem/diagnostic 비노출
- empty→one database-coordinated atomicity와 no retry
- corrupt/cardinality/policy mismatch의 fail-closed startup
- project-owned database/auth policy와 global CLI의 중립성
- cancellation, process-group cleanup와 known-created outcome 보존
- SQLite/PostgreSQL actual과 실제 PTY에서 검증 가능한가
- current schema/migration/generated ABI를 바꾸지 않는 bounded scope

## 고려한 선택지

### 선택지 A — Existing Open bootstrap와 password environment 유지

코드 변경은 작지만 모든 restart가 raw password를 요구하고 최초 생성이 server startup side effect로 남습니다. Operator가 명시적으로
provision됐는지, already-provisioned인지, startup 검증인지 분리할 수 없습니다.

### 선택지 B — Password flag/env/file와 generic stdin child protocol

자동화는 쉽지만 argv/process listing, inherited environment, file lifecycle과 generic stdin clone에 secret copy/retention 경로를
추가합니다. Current packet이 안전한 provider/custody 정책까지 소유하지 못합니다.

### 선택지 C — Explicit provision/open split와 TTY-only one-shot secret pipe

Current schema와 coordination fence를 유지하면서 최초 mutation을 별도 command로 옮깁니다. Valid project build 뒤 actual terminal에서
no-echo input을 받고, 전용 bounded binary pipe로 project child에 한 번 전달합니다. Runtime은 stored encoded credential과 static policy만
검증해 raw startup password 없이 열립니다.

## 결정

선택지 C를 채택합니다.

1. `systemstate`는 `ProvisionOperator`와 `OpenExisting`을 분리합니다. Pre-release current-only policy에 따라 implicit-bootstrap
   `Open`/`BootstrapConfig` compatibility shim은 남기지 않습니다.
2. Project code는 immutable `auth.Principal`과 `PasswordHasher`로 구성한 secret-free `CredentialPolicy`를 소유합니다.
   `RuntimeConfig`는 이 policy와 session/audit limits를 결합하고, provision config는 같은 policy에 username/password만 추가합니다.
   Stored row가 authority인 username/encoded password를 project policy나 CLI flag와 섞지 않습니다. 세 config의
   `String`/`GoString`은 redacted입니다.
3. Provision은 exact system migration을 먼저 요구하고 password hash를 fence 밖에서 준비합니다. Final readiness, session/audit dependent
   state와 credential cardinality 확인 및 insert는 exactly one `CoordinatedAtomic` callback에서 실행합니다.
4. Empty/clean state만 one insert를 허용합니다. Existing one row는 supplied username/password가 같든 다르든 password 비교,
   update와 delete 없이 `credential_already_exists`입니다. 2+는 cardinality, malformed/profile-invalid row는 corrupt,
   structurally valid하지만 project principal/active/permission policy와 다른 row는 `credential_policy_mismatch`입니다.
5. Callback/commit/transaction은 자동 retry하지 않습니다. Outcome unknown은 success/failure를 추측하지 않고 새 backend의
   `OpenExisting`/login으로 reconcile합니다.
6. `OpenExisting`은 stored username, encoded hash, principal, active flag, permissions와 definition digest를 strict하게 검증하고 immutable
   authenticator를 만듭니다. Raw password를 받거나 DB를 쓰지 않습니다.
7. Exact system migration이 없거나 다르면 `schema_unavailable`입니다. 그 migration이 적용되고 credential/session/audit가 모두 빈
   clean state에서만 `credential_absent`를 반환합니다. Article composition만 이 exact code를 public-only branch로 사용할 수 있으며
   wrong history/table, dependent rows, corruption와 policy mismatch를 public-only로 downgrade하지 않습니다.
8. `project.Config`는 separate system-state backend opener와 immutable operator policy를 소유합니다. Global CLI와 private protocol은
   auth permission/database type을 해석하지 않습니다.
9. Public command는 exact `createsuperuser`와 trailing `--project PATH` 두 형태만 지원합니다. Username/password 관련 flags, positional
   identity와 non-TTY fallback은 지원하지 않습니다.
10. Project selection/build 성공 뒤에만 TTY input을 읽습니다. `golang.org/x/term`으로 password/confirmation echo를 끄고 모든 prompt/error
    경로의 terminal restoration을 PTY actual로 검증합니다. Darwin/Linux private profile은 `ISIG`/VINTR를 유지하면서
    noncanonical `VMIN=0`, `VTIME=1` bounded read를 사용합니다. `poll` 뒤 VINTR input flush가 일어나도 zero-byte timeout에서
    interrupt barrier를 다시 검사하며 kernel terminal signal 의미를 manual byte 처리로 대체하지 않습니다.
11. Confirmation은 parent에서만 비교합니다. Child에는 magic/version, bounded lengths, username/password와 exact EOF를 가진 one-shot
    binary frame 하나만 전달합니다. Response는 secret-free strict JSON union입니다.
12. Sensitive process owner는 generic cloned stdin command와 분리합니다. No shell/argv/env/file secret, one pipe write/close, best-effort
    mutable-buffer clear, bounded stdout/stderr drain, process-group interrupt/kill와 direct reap를 소유합니다. Private fd 1 response와
    global public fd 1/fd 2의 broken pipe는 각 createsuperuser invocation에 한정한 buffered SIGPIPE receiver로 EPIPE를 돌려받으며
    operator context를 취소하지 않습니다.
13. Successful insert 뒤 backend/workspace/output cleanup failure는 generic failure로 creation fact를 지우지 않습니다. Stable
    known-created failure code가 retry 금지와 reconciliation ownership을 보존합니다. Canonical project-runner main은 cause-free
    `project.RunnerExitCode`로 output-only와 preceding backend-cleanup을 normal reserved exit 86/87에 구분해 싣고, global command는 이를
    public failure/exit 3으로 변환합니다. Complete request 뒤 trusted response나 reserved normal exit가 없으면 생성 여부를 추측하지 않고
    `operator_provision_outcome_unknown`으로 fresh-backend reconciliation을 요구합니다. Actual external project는 exit 86/87,
    post-commit abort와 empty/malformed/over-limit response 각각을 fresh `OpenExisting`과 password authentication으로 reconcile하고 종료 뒤
    exact one runner attempt를 검증합니다.
14. Success stdout은 canonical one-write JSON line입니다. 성공 게시 전의 logical failure는 stdout zero와 framework-owned
    category/code만 게시합니다.
    Success stdout publication이 실패하면 stderr에 known-created output code를 한 번 시도하고 exit 3으로 닫으며, stderr까지 닫혀
    있어도 Unix SIGPIPE 141로 downgrade하지 않습니다.
15. Stable system-state taxonomy에 `credential_absent`, `credential_already_exists`, `credential_policy_mismatch`를 추가하고 raw
    password/bootstrap identity를 합쳐 표현하던 `credential_mismatch`를 제거합니다. Transaction/commit unknown marker는 outer
    `persistence_failure` cause chain에서 계속 관찰할 수 있어야 합니다.

## 결과

- Startup side effect가 제거되고 `migrate → createsuperuser → runserver`의 명시적 책임이 생깁니다.
- Article Admin/API restart는 raw password environment가 아니라 durable encoded credential을 사용합니다.
- Concurrent cooperative provision은 existing DB fence에서 선형화되며 current physical schema와 migration bytes가 바뀌지 않습니다.
- TTY-only 첫 profile은 automation 폭보다 secret custody의 좁고 검증 가능한 경계를 우선합니다.
- Public systemstate API와 project.Config가 pre-release current-only로 바뀌므로 모든 repository/external compile callsite를 같은 packet에서
  재기준화해야 합니다.
- Go string-based PasswordHasher 때문에 heap/core-dump 수준의 완전 zeroization은 보장하지 않습니다. Mutable buffers만 best-effort로
  지우고 이 제한을 명시합니다.

## 의도적으로 결정하지 않은 것

- Noninteractive secret provider, terminal agent/OS keychain, password stdin/FD convention와 CI provisioning API
- User/Group/staff/superuser model, multiple operators와 username uniqueness IR
- Password change/reset/delete, permission lifecycle와 session-family revocation
- General environment scrub, core-dump/ptrace/swap hardening과 mlock
- Automatic migrate/repair/retry/reopen, non-cooperative writer와 distributed deployment
- Q-019 SQLite terminal quarantine와 general backend health/telemetry
- Windows, production server/security topology와 universal project template

## 검증

- Exact argv/pre-I/O, TTY/no-echo/confirmation/restore/SIGINT와 non-TTY failure actual
- Binary protocol exact-limit/one-over/truncation/trailing/version/fuzz와 malformed-before-open
- Empty/already/concurrent/cardinality/corrupt/policy/commit-unknown systemstate unit/race/fault tests
- Known-created backend/workspace/output failure, fd 1 SIGPIPE→reserved normal exit, complete-request outcome-unknown no retry와
  fresh-backend reconciliation
- Sensitive child pipe/output cap/cancellation/process-group/reap/held-descendant tests
- Raw marker scan over argv, environment, project/temp files, response/error/report and Article restart process
- SQLite/PostgreSQL distinct-process provision, authenticated Admin/API login and raw-password-free restart
- Repository-external public project compile/runtime, affected normal/race/CGO0/vet, final full/386/archive/Hosted gates
