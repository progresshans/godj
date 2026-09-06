# 검증 실행

테스트는 중요한 위험을 찾아내는 도구다. 테스트 이름·파일 수·옛 byte roster 자체를 보존하기 위해 제품 개발을 멈추지 않는다.
제품 동작, Go-native 안전성, Django 비교와 실제 프로세스·DB 검증은 목적이 다르므로 필요한 위치에서 실행한다.

## 작업 중

변경한 Go 파일은 gofmt하고 affected package/test부터 실행한다. 아래 package는 예시이며 변경 영향에 맞게 바꾼다.

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
라벨은 추가될 때 실행을 요청한다. 같은 라벨로 다시 실행하려면 기존 라벨을 제거한 뒤 다시 추가한다.
Draft PR을 테스트 서버로 쓸 수 있으며 매 docs push가 전체 platform 검증을 다시 요청하지 않게 한다.
Job 선택과 aggregate가 필요한 검증의 누락을 확인한다. 선택하지 않은 그룹은 not-selected이며 PASS로 가장하지 않는다.

Makefile과 workflow가 실제 명령·platform matrix를 소유한다. 이 문서에 명령별 테스트 수·해시·Job/Step 수를 복사하지 않는다.
[Makefile](../Makefile), [workflows](../.github/workflows/)의 현재 설정을 사용한다.

## 반복 실행과 cache

일반 CI는 dependency/build cache를 재사용할 수 있다. 실제 DB/process 검증은 `-count=1`로 test-result cache를 끄더라도
다운로드와 compile cache를 재사용한다. Cold build나 private workspace 격리가 검증 대상일 때만 그 경계를 따로 실행한다.
Credential·사용자 입력·secret을 포함한 임시 workspace는 공유 cache로 저장하지 않는다.

외부 build가 많은 conformance runner/godjcheck와 명령별 제품 흐름은 순수 core loop에서 분리한다.
각 위험에 주 실행 경로를 두고 같은 test/platform/mode의 반복은 새 위험이나 실패를 조사할 때만 추가한다.
DB schema/port/temp 디렉터리는 lane별로 분리하고 무거운 DB/process suite의 동시 실행 수를 제한한다.

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

## 실제 source의 증거

PostgreSQL actual은 검증할 source·observer·환경에서 생성하고, source/profile/scenario identity와 digest를 확인한 consumer가 사용한다.
Oracle·expected로 actual을 만들지 않고, 다른 source의 actual이나 stale attestation을 current proof로 인정하지 않는다.
같은 신뢰된 CI 실행의 artifact를 생성 job에서 소비 job으로 전달한다. 장기 기록은 source·환경·명령·결과와 불변 artifact 위치를 남긴다.

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
