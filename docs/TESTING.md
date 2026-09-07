# 검증 실행

테스트는 중요한 위험을 찾아내는 도구다. 테스트 이름·파일 수·옛 byte roster 자체를 보존하기 위해 제품 개발을 멈추지 않는다.
제품 동작, Go-native 안전성, Django 비교와 실제 프로세스·DB 검증은 목적이 다르므로 필요한 위치에서 실행한다.

## 작업 중

한 가지 설계 변경에 필요한 제품 코드·생성기·테스트를 먼저 함께 정리한다. 편집 중에는 필요한 compile 확인만 하고,
변경 묶음이 완성되면 gofmt와 affected package/test를 모아 실행한다. 아래 package는 예시이며 변경 영향에 맞게 바꾼다.

```sh
go test ./schema/... ./query/...
go test ./orm -count=1
```

실제로 존재하는 selector를 선택하고 테스트가 하나도 실행되지 않은 성공을 근거로 사용하지 않는다.
생성기·IR·모델 선언 변경에는 `make generate-check`와 관련 whole-candidate/external compile을 포함한다.
순수 parsing/validation의 각 입력마다 외부 앱 전체를 다시 build하지 않는다. Public API·generated ABI 변경은 소비자 compile까지 확인한다.

## 검증 범위

| 범위 | 소유하는 위험 | 실행 시점 |
|---|---|---|
| 빠른 feedback | formatting, compile, pure/affected logic, 관련 drift | 편집·PR feedback |
| 관련 integration | SQLite/PostgreSQL 실제 동작, relation/cache, security, rollback와 필요한 race | 관련 변경 통합 |
| CLI/process | 실제 linked build/child, TTY/signal/cancel/reap, private workspace와 output | 명령·프로세스 소유권 변경 |
| Reference | pinned Django/DRF actual, normalizer/comparator와 source authority | 해당 계약·adapter·profile 변경 |
| 전체 platform | OS/arch/mode, cold build, external archive와 전체 기능 간 조합 | 명시한 통합 milestone |
| 장기 검증 | 반복 stress/fuzz, 큰 입력, RSS·성능 | 관련 위험 또는 주기적 실행 |

`make quick`, `make generate-check`, 관련 `go-test-*`는 PostgreSQL capture 없이 실행할 수 있다.
`make ci`는 현재 소스에 맞는 실제 PostgreSQL capture를 추가로 요구하는 전체 로컬 gate다.
Capture가 없으면 시작 단계에서 실패하며, 준비 방법은 아래를 따른다. PR의 빠른 feedback 성공은 전체 CI·다른 DB/platform의 성공이 아니다.
Full/scoped Hosted 검증은 CI workflow의 수동 실행 또는 `ci:full`, `ci:orm`, `ci:cli`, `ci:web`, `ci:reference` 라벨로 선택한다.
대상 커밋을 먼저 push한 뒤 라벨을 추가한다. 라벨 추가 이벤트 시점의 PR head를 검증하므로,
같은 라벨로 다시 실행하려면 기존 라벨을 제거한 뒤 다시 추가한다. 라벨이 붙어 있는 상태에서의 PR push는 빠른 feedback만 실행한다.
Draft PR을 테스트 서버로 쓸 수 있으며 매 docs push가 전체 platform 검증을 다시 요청하지 않게 한다.
Job 선택과 aggregate가 필요한 검증의 누락을 확인한다. 선택하지 않은 그룹은 not-selected이며 PASS로 가장하지 않는다.

Makefile과 workflow가 실제 명령·platform matrix를 소유한다. 이 문서에 명령별 테스트 수·해시·Job/Step 수를 복사하지 않는다.
[Makefile](../Makefile), [workflows](../.github/workflows/)의 현재 설정을 사용한다.

## 반복 실행과 cache

일반 CI는 dependency/build cache를 재사용할 수 있다. 실제 DB/process 검증은 `-count=1`로 test-result cache를 끄더라도
다운로드와 compile cache를 재사용한다. Cold build나 private workspace 격리가 검증 대상일 때만 그 경계를 따로 실행한다.
Credential·사용자 입력·secret을 포함한 임시 workspace는 공유 cache로 저장하지 않는다.

외부 build가 많은 conformance runner/godjcheck와 명령별 제품 흐름은 순수 core loop에서 분리한다.
생성기의 byte/schema 단위 검사는 `codegen`, 생성된 별도 Go module의 compile·runtime·잘못된 조합 거부는
`codegen/consumertest`가 소유한다. 후자는 integration과 relation platform 범위에서 실행하며 `make quick`에는 포함하지 않는다.
순수 schema/codegen 검사는 Portable Go의 각 mode에서 실행한다. 관계 matrix는 생성 소비자와 실제 DB 동작의 플랫폼 차이를 검증한다.
각 위험에 주 실행 경로를 두고 같은 test/platform/mode의 반복은 새 위험이나 실패를 조사할 때만 추가한다.
DB schema/port/temp 디렉터리는 lane별로 분리하고 무거운 DB/process suite의 동시 실행 수를 제한한다.

`internal/projectcheck`의 다섯 protocol과 projectgenerate/projectmigration protocol은 Portable core가 문법·resource 검사를
소유한다. 공용 `wirejson`과 `projectwire`도 core에 속한다. CLI platform owner는 이들을 사용하는 실제 outer command와
linked runner를 실행하며 protocol 패키지 전체를 다시 선택하지 않는다. 32-bit compile은 정수 범위와 architecture 경계를 위해 유지한다.

| 변경 위험 | 주 검증 위치 | 추가 실행 경계와 보존할 관측 |
|---|---|---|
| Strict JSON·Schema IR wire | 공용 primitive와 명령별 protocol unit | duplicate/trailing/Unicode·정확한 budget·transport precedence, 실제 linked wire의 정상/실패 연결 |
| SQLMigrate argv 전체 조합 | `TestParseSQLMigrateArgumentsRejectsInvalidForms` | outer Run과 global dispatch의 대표 arity/identity/option 실패, absent cwd·poison descriptor·build/init 0 |
| CLI 공용 수명 | `internal/projectcheck` fault/process 회귀 | retained root·workspace 정리·완료 뒤 취소, 외부 migrate/writer/operator/server의 durable/TTY/signal 경계 |
| SQL projection의 실제 위임 | `TestSQLProductRunnerPipelineExecutionControls` | compiled bypass·private rename·IR 변경 대조. 별도 Phase D는 Hosted PostgreSQL 환경 격리·DB 접속 0·중단/reap |
| 세션 동시 접근 | real MemoryStore·durable SQLite의 `AtomicAccess` | 같은 ID의 갱신/만료/rotation/취소 interleaving, multi-runtime PostgreSQL·restart는 DB owner |
| Loaded definition·operation | migration/definition unit·lifecycle | 입력/결과 mutation·동시 replay·typed nil, fresh history·revision fence·durable prefix는 실제 DB owner |
| 공통 Query AST 의미 | backend compiler unit | SQLite와 PostgreSQL 실제 SQL·identifier/NULL·rollback은 각각 DB owner |
| 관계 actual 생성 | contract별 fresh observer | 관계별 query/cache/rollback 회귀, 고정 oracle 대조와 sibling case 미실행 대조 |

Portable의 ubuntu-24.04와 관계/CLI의 ubuntu-22.04는 다른 OS 이미지다. Linux/architecture/CGO/race의 동일 이름만으로
그 실행을 제거하지 않는다. 실제 SQLite·생성 소비자·process 경계의 matrix는 유지하고 pure protocol 반복만 owner를 옮긴다.

관계 product의 Author/Post 생성 모델과 프로젝트는 `conformance/relationfixture`를 공유한다.
이 패키지가 whole-project drift, 생성물 없이 declaration runner를 만드는 bootstrap, 앱 간 의존성과 observer의 oracle-blind 경계를 검증한다.
기능별 product는 실제 query/object/reverse/prefetch/select/delete 결과와 cache·취소·rollback 검증을 소유한다.
옛 fixture별 파일 수·이전 내부 ABI의 복제본을 유지하지 않는다. 현재 생성 조합의 일관성·잘못 섞인 snapshot 거부는
`codegen/consumertest`, 소비자 타입 오류와 publication 실패 시 기존 결과 보존은 각각 compile·projectgenerate 검증이 맡는다.

SQLite/migrations 전체 normal·race·CGO-disabled와 vet는 관계 matrix의 같은 OS/CPU 좌표가 소유한다.
Full scope에서 conformance Go runner 전체를 project-check matrix가 실행하면 관계 matrix는 같은 runner subset을 다시 실행하지 않는다.
해당 owner가 없는 ORM scope에서는 관계 matrix가 subset을 실행한다. 필수 sentinel과 no-skip 검사는 실제 실행 owner에 적용하고,
aggregate는 선택한 owner의 실패·취소·누락을 거부한다. Full scope의 Darwin CGO-disabled lifecycle도 이 두 matrix가 소유하며,
reference-only scope에서는 exact Darwin job이 직접 실행한다.

## 남겨야 하는 검증

- Migration의 per-step atomicity, durable prefix, restart, revision fence와 outcome unknown
- Codegen의 stale/mixed candidate 거부, 실패 시 이전 결과 보존과 중단 후 recovery
- Typed/dynamic 의미 일치, nullable 값, relation/cache 복사·소유권
- 권한 거부, CSRF, token/password 비노출, session rotation/logout/revocation
- Context cancellation, signal, pipe ownership와 child reap
- Oracle-blind actual, comparator negative control, 필수 실행 누락·skip·잘린 로그 거부

테스트를 삭제하거나 통합하면 위 위험을 현재 어느 test가 검증하는지 확인한다.
전체 이름·개수·payload 길이의 영구 잠금 대신 실행한 scope, required capability/sentinel과 실제 실패/skip를 검사한다.
Test 파일의 문장이나 work 일지에 같은 prose가 남아 있는지는 runtime 안전성의 대체 검증이 아니다.
Python compatibility는 현재 발견한 testcase의 시작·종료와 허용된 exact-profile skip을 대조한다. 전역 테스트 수를 고정하지 않고
실패·expected failure·임의 skip·중단된 실행을 거부하며 고정 reference 의미의 별도 digest 검증을 유지한다.

고정 reference 파일의 size/hash는 protocol의 공통 artifact catalog에서 대조한다. 각 계약의 phase·payload·provenance와
부정 대조는 해당 계약 테스트가 맡는다. 구현 파일 자체의 과거 SHA를 보존하기 위해 현재 테스트의 구조를 고정하지 않는다.
공통 fixture는 호출마다 새 mutable 입력을 만들며 actual 관찰과 expected 로딩은 별도로 유지한다.
공통화한 환경 준비에서도 각 실행의 timeout·출력 제한·cleanup·필수 DB 조건을 유지한다.
Select/object/delete 관계 handler는 자신이 맡은 case만 관측한다. 같은 DB에서 sibling case 전체를 실행한 결과의 전역 cache는
사용하지 않는다. 검증 편의를 위한 test-only 결합은 각 case의 fresh DB 결과와 DB state 일치도 따로 확인한다.

## 실제 source의 증거

PostgreSQL actual은 검증할 source·observer·환경에서 생성하고, source/profile/scenario identity와 digest를 확인한 consumer가 사용한다.
Oracle·expected로 actual을 만들지 않고, 다른 source의 actual이나 stale attestation을 current proof로 인정하지 않는다.
같은 신뢰된 CI 실행의 artifact를 생성 job에서 소비 job으로 전달한다. 장기 기록은 source·환경·명령·결과와 불변 artifact 위치를 남긴다.
관찰자나 attestation I/O를 공통 helper로 옮기면 그 helper도 사용하는 attestation의 source binding에 포함한다.
JSON/file 읽기 구현을 공유해도 각 attestation의 source inventory·크기 제한·schema는 독립적으로 검증한다.

로컬에서 전체 gate를 재현하려면 검증할 소스의 성공한 CI run과 attempt를 선택하고 두 artifact를 내려받는다.
아래 `RUN_ID`, `ATTEMPT`, 절대 경로를 실제 값으로 바꾼다. Artifact는 90일간 보관하므로 만료됐다면 같은 소스에서 새 CI 실행이 필요하다.

```sh
ci_run=RUN_ID
ci_attempt=ATTEMPT
capture_root=/absolute/path/to/godj-evidence
gh run download "$ci_run" --name "systemstate-postgres-$ci_attempt" --dir "$capture_root/systemstate"
gh run download "$ci_run" --name "operator-postgres-$ci_attempt" --dir "$capture_root/operator"
```

두 `provenance.json`의 `checkout`과 같은 commit을 별도 작업 사본에서 사용한다. PR 실행의 checkout은 PR head와 다른 merge commit일 수 있다.
그 작업 사본에서 아래를 실행한다. 다른 저장소·run·attempt·payload·checkout은 envelope 검증이 거부하고,
Go consumer는 checksum과 실제 source/profile/scenario binding도 확인한다. `testdata`의 고정 codec fixture는 이 입력으로 사용할 수 없다.

```sh
export GITHUB_REPOSITORY=progresshans/godj GITHUB_RUN_ID="$ci_run" GITHUB_RUN_ATTEMPT="$ci_attempt"
python3 scripts/ci/capture_artifact.py verify "$capture_root/systemstate" postgresql-17.10-two-process-v1.json
python3 scripts/ci/capture_artifact.py verify "$capture_root/operator" postgresql-17.10-sqlite-external-operator-v1.json
ATTESTATION_DIR="$capture_root" make ci
```

이 로컬 gate는 현재 OS에서의 재현이다. 다른 OS/arch와 PostgreSQL service 실행 결과까지 새로 만든 것으로 기록하지 않는다.

실행 로그는 build-output/build-fail과 dependency ImportPath/FailedBuild, test failure, timeout, malformed/truncated JSON과
stderr-only failure를 구분해야 한다. 원래 test exit code와 유용한 원인을 보존하고 bounded/redacted diagnostics를 출력한다.
검증을 리팩터링할 때는 대표적인 실제 결함·필수 실행 누락·위조 actual이 여전히 실패하는지 확인한다.

## 문서와 과거 증거

문서-only 변경은 local link·상태 일관성과 `git diff --check`를 검증한다. 제품 입력을 바꾸지 않은 실행 기록 추가 때문에
전체 product matrix를 반복하지 않는다. 현재 source에서 실행한 결과는 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)에 한 번 기록한다.
명령, source, 환경, 결과, 실패/skip, 미실행 범위를 남기면 된다. 작은 수정마다 여러 activation/checkpoint/terminal EVID를 만들지 않는다.

코드 규모는 `go run ./scripts/sourceinventory`로 집계한다. 현재 작업 사본의 Git 추적 파일과 무시되지 않은 새 Go/Python 파일을
포함하고 삭제된 파일은 제외한다. `-revision COMMIT`은 고정 commit의 바이트를 읽는다. Go는 `ast.IsGenerated`로 판정하고
test → generated → conformance 지원 → examples → framework/CLI/generator/support 순서로 중복 없이 분류한다.
생성 머리말을 문자열로 출력하는 수작업 생성기는 generated가 아니다. 빈 줄·주석도 줄 수에 포함하며 source digest와 파일별
SHA-256을 함께 출력한다. 이 집계는 품질이나 성능을 대신하는 목표값이 아니다.
