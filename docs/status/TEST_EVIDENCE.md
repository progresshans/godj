# 테스트 증거

현재 변경의 실행 결과는 이 파일에 한 번만 기록한다. 설계 채택, 코드 존재, 특정 환경에서의 검증은 서로 다른 상태다.
미실행·비대상·환경 실패를 PASS로 표현하지 않으며 다른 source의 성공을 현재 실행 결과로 옮기지 않는다.

## GDJ-0058 — 관계 조회 정리와 eager First

- 작업: [GDJ-0058](../../work/0058-eager-first-ticket-detail.md)
- 기준: `0b9955ec0ef3b013e59fd185038d38e57582d008`, 원래 작업 디렉터리와 `codex/revision-fenced-migration-lifecycle` 브랜치.
- 로컬 환경: 2026-09-06, Go 1.26.5 darwin/arm64. 아래는 해당 기준에 GDJ-0058 변경을 적용한 checkout에서 실행했다.
- 상태: 로컬 checkpoint 완료. 고정 source의 관련 Hosted 검증은 후속 기록이 소유한다.

| 실제 명령·범위 | 결과 |
|---|---|
| `go test ./orm ./codegen -run 'SelectRelated\|ForwardSelect\|ProjectRelationFacade' -count=1` | PASS, First 추가 전 동작 보존 정리의 기존 회귀 |
| `go test -count=1 -timeout=8m ./orm ./codegen ./examples/helpdesk ./internal/compiletest ./internal/projectgenerate ./internal/projectcheck` | PASS, First 구현·생성·출판·외부 Go module compile |
| `go test -count=1 ./orm ./examples/helpdesk` | PASS, 마지막 공통 rows 획득 이관 후 전체 ORM 및 Helpdesk 재검증 |
| `go test -race -count=1 -timeout=8m ./orm ./codegen ./examples/helpdesk ./internal/compiletest` | PASS |
| `CGO_ENABLED=0 go test -count=1 -timeout=8m ./orm ./codegen ./examples/helpdesk ./internal/compiletest` | PASS |
| `make generate-check` | PASS, Helpdesk·Article·relationdeleteproduct 전체 산출물 drift 없음 |
| `go vet ./orm ./codegen ./examples/helpdesk` | PASS |
| `python3 scripts/check_docs.py`, `git diff --check` | PASS |

검증 내용: First의 최대 1회 scan·기존 Offset/Limit/Distinct/JOIN 유지, cold/warm/empty cache, required와 nullable
관계·객체 독립 소유권, binding/context/backend/scan/rows/close 오류와 재시도, 외부 typed/dynamic First 호출을 확인했다.
Helpdesk의 실제 SQLite HTTP 상세 요청은 티켓과 Category를 1회 JOIN으로 읽고, 다른 Category와 없는 티켓에 404를 반환했다.
인증·ViewTicket 거부 시 application data Query는 0회였다. Category id/name 출력 정책과 ViewCategory의 별도 Admin 정책도 확인했다.

편집 중 삭제한 private discriminator·context probe를 요구하던 테스트와 상세 응답 필드 수 기대값을 수정했다.
공통 rows 함수로 옮길 때 남은 두 projection 호출부의 compile 오류도 수정하고 위 검증을 통과했다.
로컬 Docker daemon이 실행 중이지 않아 PostgreSQL sentinel은 로컬에서 skip됐다. PostgreSQL 실행 결과는 Hosted 기록 전까지 미검증이다.
이번 단계에서 전체 `make ci`, 전체 플랫폼·32비트·Django differential oracle을 로컬에서 다시 실행하지 않았다.

## 이전 증거

- [GDJ-0055 마지막 제품 통합 증거](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/status/TEST_EVIDENCE.md#evid-20260905-179--gdj-0055-explicit-operator-provisioning-terminal-acceptance):
  제품 source `0b5b6fc6ec60e1704e5cebfaebd771b682d001ee`의 local/backend/platform 결과다. 현재 변경의 검증이 아니다.
- [GDJ-0056 checkpoint를 포함한 정리 전 전체 기록](https://github.com/progresshans/godj/blob/da1bfc524c4f205075fc7fac7f00b437473a5e1f/docs/status/TEST_EVIDENCE.md):
  EVID-001..182의 명령·source·환경·실패·산출물을 보존한다. EVID-182는 corrected attestation checkpoint이며
  GDJ-0056 전체 Hosted 완료를 뜻하지 않는다.

고정 commit은 현재 브랜치의 조상이다. 네트워크 없이 원문을 보려면 저장소에서 다음을 실행한다.

```sh
git show da1bfc524c4f205075fc7fac7f00b437473a5e1f:docs/status/TEST_EVIDENCE.md
```

과거 본문의 복제 archive는 만들지 않는다. 검증을 인용할 때는 실행한 source, 환경, 검증 범위와 해당 항목을 함께 가리킨다.

## GDJ-0057 — 개발 구조 정리

- 시작 기준: `da1bfc524c4f205075fc7fac7f00b437473a5e1f`
- 작업: [GDJ-0057](../../work/0057-development-simplification.md)
- 상태: 구현·통합 검증 완료. 아래에 실제 실행한 명령과 결과만 기록한다.

### 실행 기록

2026-09-06, darwin/arm64 Go 1.26.5, 기준 `da1bfc5`의 `feature/development-simplification` 작업 사본에서 실행한 구현 checkpoint다.
아래는 최종 commit의 전체 플랫폼 검증을 뜻하지 않는다. 별도 기록이 없는 PostgreSQL service 경로는 이 로컬 검사에서 실행하지 않았다.

| 명령·범위 | 결과 |
|---|---|
| `make quick` (문서 링크·gofmt·CI 도구·core package) | PASS |
| `python3 -m unittest discover -s scripts/ci -p 'test_*.py'` | PASS, 빌드 오류/timeout/잘린 로그/필수 skip와 CI scope·capture provenance 부정 회귀 포함 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/feedback.yml` | PASS, YAML/Actions 표현식 검사. ShellCheck/Pyflakes는 이 명령에 미포함 |
| `go test ./codegen -count=1 -timeout=8m` | PASS |
| `go test ./orm -count=1 -timeout=5m`, 같은 범위 `-race` | PASS |
| `go test ./internal/compiletest -run TestCheckedInRelationFacadeV2CannotHybridizeCurrentBundle -count=1` | PASS, 과거/현행 생성물 혼합 거부 |
| Article와 relationdeleteproduct `godj generate --check` | PASS |
| `go test ./conformance/internal/protocol -count=1 -timeout=5m` | PASS, 문서·workflow 모양 잠금 정리 후 contract/oracle/실행 누락 guard |
| `go test -count=1 ./conformance/systemstate/attestation ./conformance/projectoperatorproduct/attestation` | PASS |
| godjcheck `TestLoadRunnerInputs*`, `TestAttestationRepositoryRoot*`, `TestProjectOperatorAttestationAccepts*`, `TestRequireExactResolvedPath*`, `TestRunRejectsCrossArtifactSYS029*`, `TestRunRequiresPublishedSYS029*` | PASS |
| Form/Admin/serializer 선택·초기값·출력과 operator 권한 CAS의 affected normal 회귀 | PASS; 권한 경쟁 1승, session revoke, old runtime 거부, rollback·unknown outcome 보존 |
| Helpdesk 공개 API를 사용하는 외부 Go test package의 SQLite/Admin/API 흐름 | PASS. 별도 Go module 설치 증거는 아님 |

추가 구현 checkpoint:

- Form/Admin/serializer/systemstate/Article adapter/Helpdesk의 normal/race/CGO-disabled PASS. PostgreSQL sentinel은 로컬에서 명시 skip.
- `internal/gobuild`, `internal/projectcheck`, `internal/projectgenerate` 전체 normal PASS; gobuild/projectcheck race와 cmd/godj 전체 CGO-disabled PASS.
- `GODJ_COLD_BUILD=1 go test ./cmd/godj -run '^TestActualGodjMigrationCheckProcess$/^implicit_success$' -count=1 -timeout=5m` PASS.
- SYS-023 세 PTY 사례의 기존 oracle 비교 PASS. SYS-020의 live SQLite/injected PostgreSQL facts 및 SQL-rendering 회귀 PASS.
- 빌드 원인 누락을 보강한 실제 SQLite 두 프로세스·Article restart·operator known-created response-write-failure 회귀 PASS.
  PostgreSQL 전용 두 helper는 compile만 확인했다.
- Query typed-nil Error/Is/Unwrap panic 재현 후 query/schema 회귀 PASS. SQLite Q-019·migration outcome/durable-prefix 및 내부 migration writer 검증 PASS.
- `TestSQLiteExecutorCompetingCommitStopsTailAndReturnsOwnDurablePrefix`, `TestSQLiteExecutorRejectsRecorderCorruptionAfterValidTransition` normal/race PASS.
- 선택 부모 identity 교체+invalid/oversized descriptor 3건을 실패로 재현한 뒤 수정. 실제 selection/linked/protocol 전체 normal/race PASS.
- 실제 workspace parent 교체, launch failure/reap 0, 동시 cancel/interrupt와 Wait 직후 interrupt 재확인 normal/race PASS.
- `go test -count=1 -run '^$' ./...`, `go vet ./...` PASS (이후 옮긴 실제 테스트는 해당 패키지 normal/race로 추가 검증).
- Helpdesk 선언 runner를 연결한 뒤 `make generate-check` 세 프로젝트 모두 PASS.
- CI 도구 회귀는 17개 PASS. 실패/정상 discovery를 실제 Make에 모두 주입해, macOS GNU Make 3.81에서도 일부 목록 실패와 앞선 gofmt parse 오류가 뒤 성공에 가려지지 않음을 확인했다.

최종 제출 source의 로컬/Hosted 기록은 아래에 이어 적는다. 위 checkpoint의 elapsed 값이나 결과를 전체 플랫폼의 성능·성공으로 일반화하지 않는다.

### 첫 통합과 잔여 검사 정리

2026-09-06, source `1393624ed56782951b0b114c946b9bfab5ceebe9`에서 실행했다.

- darwin/arm64: `make quick generate-check go-vet` PASS (65.10초), `make go-test-conformance` PASS (136.43초),
  `go test -count=1 -timeout=20m ./conformance/runners/godj` PASS (133.52초). PostgreSQL service 미설정 경로는 이 로컬 성공에 포함하지 않는다.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34027839749) PASS.
- [첫 전체 CI](https://github.com/progresshans/godj/actions/runs/34027880576)에서 `internal/compiletest`의 남은
  생성 파일 byte/hash, facade 전체 함수 목록, tool 전체 직접 import 목록 검사 실패를 확인했다. 이 실행은 전체 PASS가 아니다.
  로컬의 과거/현재 생성물 혼용 거부 focused 검사만으로 전체 compiletest 성공을 대신할 수 없음을 확인했다.
- 잔여 모양 잠금을 제거하고 실제 외부 compile/type misuse·생성물 drift·혼용 거부·금지 의존 방향을 유지했다.
  Sealed selector 위조 compile-negative와 JSON value/pointer 누출·unmarshal 무변경 실행 검사를 보강했다.
- CI 라벨과 무관한 PR 라벨이 현재 검증을 취소하지 않도록 concurrency group을 분리했다.
  변경 후 actionlint PASS, CI 도구 회귀 17개 PASS. 로컬 capture 다운로드·provenance 검증·전체 gate 사용법도 보완했다.
- 후속 변경을 동결한 작업 사본: `go test ./internal/compiletest -count=1 -timeout=5m` normal/race/CGO-disabled 모두 PASS
  (각 10.916/11.709/10.629초), `make go-test-integration` PASS (52.64초), `make format-check docs-check`와 `git diff --check` PASS.

첫 실행은 수정 소스의 전체 CI가 시작된 뒤 남은 작업을 취소했다. 실패를 해결한 이전 실행의 부분 성공을 새 source의 전체 증거로 재사용하지 않는다.

### 최종 통합 소스

- 제품·검증 도구 source: `0b8235ce010f971470d344281bc51fee84fb73fa`.
- [전체 CI](https://github.com/progresshans/godj/actions/runs/34028776113), attempt 1: PASS, 78개 작업 모두 success.
  PR checkout `42dc5ea032fc687bbd6ad4ec5488f4088a5efe30`의 tree가 제출 source와 같음을 Git 객체로 확인했다.
- 최종 `CI result (ci:full)`은 `scope: full`, `full_platform_verified: true`로 10개 실행 그룹 모두의 성공을 확인했다.
  4 OS/arch × normal/race/CGO-disabled, cold CLI 경로, 고정 darwin/arm64 기준 비교와 Python 4개 버전 검증을 포함한다.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34028766526) PASS.
- 실제 PostgreSQL producer 6개(normal/race/CGO-disabled × 두 shard), 같은 attempt의 capture 소비·conformance·32비트 후속 검사 PASS.
  두 archive를 별도로 읽어 GitHub archive digest, repository/run/attempt/checkout, payload SHA256과 `SHA256SUMS` 일치를 확인했다.

| 같은 실행에서 생성·소비한 artifact | 불변 artifact ID |
|---|---|
| `systemstate-postgres-1` | `9987975595` |
| `operator-postgres-1` | `9987957182` |

위 artifact의 실제 사용법과 보관 기한은 [TESTING](../TESTING.md#실제-source의-증거)에 있다.
로컬 전체 `make ci`는 Hosted 전체와 중복 실행하지 않았다. 새로운 Helpdesk의 외부 test package 검증은 별도 Go module 설치 증거가 아니며,
Hosted의 기존 외부 archive·생성물 consumer 검증과 구분한다.

완료 상태를 기록한 후속 변경은 Markdown만 포함한다. 제품·workflow·lock을 바꾸지 않는 이 기록 때문에 전체 matrix를 반복하지 않는다.
2026-09-06 완료 문서 6개에 `make docs-check`, `git diff --check`와 검증 source 이후 Markdown-only diff 검사를 실행해 PASS를 확인했다.
