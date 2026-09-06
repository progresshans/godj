# 테스트 증거

현재 변경의 실행 결과는 이 파일에 한 번만 기록한다. 설계 채택, 코드 존재, 특정 환경에서의 검증은 서로 다른 상태다.
미실행·비대상·환경 실패를 PASS로 표현하지 않으며 다른 source의 성공을 현재 실행 결과로 옮기지 않는다.

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
- 상태: 구현 완료 후 통합 검증 중. 아래에 실제 실행한 명령과 결과만 추가한다.

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

이 수정의 새 commit에서 전체 CI를 다시 실행하며, 첫 실행의 부분 성공을 새 source의 전체 증거로 재사용하지 않는다.
