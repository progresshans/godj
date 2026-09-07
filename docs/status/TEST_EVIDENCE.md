# 테스트 증거

현재 변경의 실행 결과는 이 파일에 한 번만 기록한다. 설계 채택, 코드 존재, 특정 환경에서의 검증은 서로 다른 상태다.
미실행·비대상·환경 실패를 PASS로 표현하지 않으며 다른 source의 성공을 현재 실행 결과로 옮기지 않는다.

## GDJ-0063 — 제품·검증 코드의 책임 정리와 결함 수정

- 작업: [GDJ-0063](../../work/0063-runtime-and-validation-ownership.md)
- 기준: `df19040fc0e9c5a0e966ceada4b2d4484bdc5a57`, 기존 Draft PR #1의 `codex/revision-fenced-migration-lifecycle`.
- 로컬: 2026-09-08 KST, Go 1.26.5 darwin/arm64. 기준 위의 변경 작업 사본에서 아래 집중·통합 검증을 실행했다.
- 구현 소스: `0badd6b369fa599ee5891665602990aaf44df3fe`.
- 상태: 구현·로컬 checkpoint·해당 고정 소스의 Hosted full scope 완료. 2026-09-08 KST에 최종 job 결과와 capture를 확인했다.

### 변경과 보존한 위험

| 항목 | 구현과 검증 소유권 |
|---|---|
| F1 | Store.Touch가 현재 record의 만료·갱신·삭제를 원자적으로 소유한다. real MemoryStore/SQLite Store에서 같은 ID의 detached-read 이후 갱신, 정확한 idle/absolute 만료, rotation, 취소를 barrier로 재현한다. Absolute 사례는 9/18/27초에 idle을 연장한 뒤 30초에 만료시킨다. |
| F2/S1 | `wirejson`의 strict lexical·구조/정수·bounded read와 `projectwire`의 Schema IR 표현/크기/사전 검사를 공유한다. showmigrations가 짝 없는 surrogate를 거부하며 유효 U+FFFD/pair는 보존한다. 각 envelope·resource budget·오류 우선순위·drain 정책, definition loader의 결정적 오류 선택은 별도다. |
| M1/S3 | Loader가 복사한 definition/source와 immutable planner를 게시하고 내부 lifecycle은 빌려 읽는다. 외부 Definitions/Sources는 필요한 view만 복사한다. Raw reconstructor 입력은 검증·복사하며 history check·plan·revision fence는 매 실행 새 snapshot에 적용한다. |
| S2 | 공용 CLI owner가 retained project/workspace/build/child/cleanup을 처리한다. 명령별 완성 응답·durable publication·TTY credential·foreground signal의 terminal policy는 active work 표와 실제 fault/process 회귀가 소유한다. |
| S4/S5 | DB 공통 AST projection/order/key/relation/scalar 검사를 `queryplan`으로 모으고 물리 quoting/schema/parameter/DDL/transaction은 backend에 남겼다. 지원 operation의 non-nil 포인터를 경계에서 값으로 정규화하며 typed nil·unknown/embedded operation 오류와 nested IR 복사를 유지한다. |
| S6 | Private AST/dataflow 해석 대신 import·I/O·공개 진입점 감사를 적용한다. 기존 우회 12개를 실제 global CLI로 빌드·실행하고 init marker로 build 실패와 구별한다. Private rename/import alias의 정상 대조, 두 renderer의 IR 변화, 정상 기대 SQL을 반환하는 stub이 변경 입력 대조에서 거부되는 실행도 확인했다. |
| S7 | Select/object/delete handler가 해당 case만 fresh DB에서 관측한다. Select 세 contract는 sibling을 합쳐 반복하던 18회 query 대신 실제 필요한 5/1/0회만 수행한다. Delete는 네 fixture 실행을 두 개로 줄였다. 전체 상태/trace/cache/rollback과 sibling 오염 대조, 12개 relation contract의 locked-oracle 비교를 유지했다. 전역 cache는 없다. |
| S8 | Pure protocol 7개를 Portable core로 분류하고 실제 CLI platform 선택에서 제외했다. argv 조합은 parser unit에 두고 outer/global/external은 대표 arity·identity·option의 pre-I/O 관측을 유지한다. SQL pipeline 대조는 Portable, PostgreSQL Phase D는 환경·schema·무접속·중단 경계를 소유한다. OS 이미지·arch/race/CGO/32-bit 경계와 필수 sentinel·no-skip·completion 검사는 유지했다. |
| M2 | `scripts/sourceinventory`가 `ast.IsGenerated`와 Git commit/현재 파일 바이트로 겹치지 않는 분류를 생성한다. 생성 머리말을 출력하는 수작업 코드·실제 header·test 우선순위·미완성 Go·untracked/ignored/deleted 파일·고정 commit 대조로 기존 오분류를 재현·차단했다. |

두 attestation의 새 wire/project helper를 source binding에 포함했다. 기존 `db/`·`migrations/` 범위가 새 queryplan/loadeddefinition도
포함하는지 mutation 대조를 추가했고 operator의 새 CLI owner, SYS-020 consumer의 relationstate도 확인했다.
`sessiontest`는 일반 Store 단위 회귀의 지원 코드이며 live capture producer는 아니다. 고정 codec fixture·oracle/expected·profile·lock을
현재 구현 소스에 맞추기 위해 덮어쓰지 않았다.

### 실제 집계와 측정

동일 도구로 기준 commit과 변경 작업 사본을 집계했다. 빈 줄·주석을 포함하고 새 helper·테스트·집계 도구도 포함한다.

| 분류 | 기준 | 구현 후 | 변화 |
|---|---:|---:|---:|
| Go 전체 | 848개 / 313,585줄 | 866개 / 312,019줄 | -1,566줄 |
| test Go | 164,801줄 | 164,724줄 | -77줄 |
| conformance 지원 Go | 54,100줄 | 54,099줄 | -1줄 |
| framework·CLI·generator·support Go | 83,722줄 | 82,234줄 | -1,488줄 |
| generated Go | 45개 / 6,805줄 | 45개 / 6,805줄 | 0 |
| 비-generated examples Go | 4,157줄 | 4,157줄 | 0 |
| Python 전체 | 134개 / 36,330줄 | 134개 / 36,350줄 | +20줄 |

합계는 **1,546줄 순감소**다. 목표 줄 수에 맞춘 테스트 삭제나 줄바꿈 압축은 하지 않았다.
기준/구현 후 Go+Python source digest는 각각 `128890711b9e7c1e6b1bbdb6531475edcda01dfb3512fc434bdbb6902062b8c9` /
`44670e2f2d2f27bed3cc0b581d6c05b064617cfedcd0c0d73018c38eee1ba359`다. 아래 GDJ-0062의 generated 분류도 정정했다.

Operation 0개인 definition 1/100/1,000개로 같은 Digest probe를 실행한 결과 호출당 allocation은 모두 0회/0 B였다.
이전의 2회/160 B, 2회/16,384 B, 2회/약 164 KB와 같은 입력 조건이다. 전체 migration 시간·처리량의 개선율을 뜻하지 않는다.

### 로컬 checkpoint

편집 중 compile 이후 각 묶음의 normal을 실행했다. 세션 3 package와 관련 실제 observation, protocol 10 package,
migration/definition, CLI owner 전체, SQLite/compiler와 relation observation의 정상·부정 대조가 통과했다.
Source 또는 통합 지원 코드가 바뀐 이후 필요한 다음 checkpoint도 실행했다.

A: `./sessions ./systemstate ./web/sessionauth ./migrations/... ./db/sqlite ./db/internal/...`,
`./internal/wirejson ./internal/projectwire ./internal/projectcheck/...`, 두 projectgenerate/projectmigration protocol,
`./conformance/internal/relationstate`와 select/object/delete product.
B: `./project ./internal/projectgenerate/... ./internal/projectmigration/...`, `./conformance/runners/godj`,
`./conformance/cmd/godjcheck`, 두 attestation, `./conformance/relationfixture/...`.
C: `./conformance/{projectmigrateproduct,projectmigratetargetproduct,projectshowmigrationsproduct,projectoperatorproduct,migrationwriterproduct,runserverproduct}`.

| 실제 실행 | 결과 |
|---|---|
| A `go test -json -race -count=1 -p=2 -timeout=20m` | 24 packages, run/pass/skip 2,399/2,398/1, fail 0 |
| A `CGO_ENABLED=0 go test -json -count=1 -p=2 -timeout=20m` | 24 packages, run/pass/skip 2,399/2,398/1, fail 0 |
| B `go test -json -count=1 -p=1 -timeout=25m` | 15 packages, run/pass/skip 1,029/1,028/1, fail 0 |
| C `go test -json -count=1 -p=1 -timeout=25m` | 6 packages, run/pass/skip 88/81/7, fail 0. DSN 미제공 PostgreSQL은 Hosted owner에 남음 |
| PostgreSQL `-run '^Test(Compile\|Compiler)'` normal/race/CGO-disabled | 각 30 top-level tests / 64 run/pass, skip/fail 0. 실제 PostgreSQL I/O 검증은 아님 |
| SQLMigrate 외부 전체 normal | 4 top-level / 43 run/pass, skip/fail 0. 이후 추가한 fixed-output 대조와 공용 성공 비교를 포함한 최종 controls normal은 31 run/pass, skip/fail 0 |
| 변경 argv parser/outer/dispatch normal | 4 top-level / 7 run/pass, skip/fail 0 |
| 고정 Python exact | 274 tests, error/failure 0, DRF 미설치 skip 3. Hosted DRF 환경이 해당 identity의 실행을 소유 |
| 고정 Django/DRF oracle check | 27개 실제 재생성 결과와 고정 바이트 일치 |
| generated drift | Helpdesk·Article·공통 relation bundle 및 별도 generated relation 회귀 PASS |
| affected vet·CI 도구 | PASS. CI Python 도구 22 tests, 실제 platform 선택은 project/projectcheck/linked만 포함 |
| 문서·format·diff·Actions 문법 | PASS. actionlint 1.7.12, ShellCheck/Pyflakes는 실행하지 않음 |

A의 skip은 `TestMakemigrationsCrashHelper`, B는 `TestPublicationCrashHelper`의 직접 진입 guard다. 실제 부모 crash/publish 회귀는
각각 성공했다. C의 skip은 migrate PostgreSQL 2개, targeted migrate 1개, showmigrations 1개, operator 2개, runserver 1개다.
고정 Python의 skip은 `APIAuthenticationScenarioTests`의 DRF missing/invalid token, permission/CSRF/profile,
full-reference deterministic/raw-bearer-free 세 검사다. 이 미실행 환경을 로컬 PASS로 세지 않는다.
Go 통합·외부 명령·최종 SQL controls의 stderr는 0 bytes이며 JSON 시작/종료 inventory도 대조했다.

고정 Python은 `GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uvx --from uv==0.10.12 uv run --frozen python -m unittest discover -s conformance/runners/django/tests -v`,
oracle은 같은 고정 uv 환경에서 `make oracle-check`를 실행했다. DRF 하위 프로젝트는 자신의 `.venv`를 사용했다.

중간 실패도 보존했다. CLI 공통화 첫 normal은 build 직전 취소 barrier가 한 번 줄어든 두 사례에서 실패했고 실제 build 직전에
barrier를 복원한 전체 normal·A race/CGO-disabled가 통과했다. 첫 SQL 우회 대조는 test fixture가 marker 경로를 공유해 실패했고
case별 경로로 고친 뒤 실제 우회·rename·입력 변화 대조가 통과했다. 최초 actionlint는 offline Go module metadata 조회가 막혀
실행되지 않았으며 고정 버전의 정상 module 조회로 실행한 두 workflow 문법 검사가 통과했다.

### 초기 Hosted 실패와 수정

최초 구현 `293d556fc591beb5dc4e1eb8e90d11092dede145`의 [full CI 34155216966](https://github.com/progresshans/godj/actions/runs/34155216966)와
[PR feedback 34155192867](https://github.com/progresshans/godj/actions/runs/34155192867)에서 `admin/site_test.go`의 Store wrapper 한 곳이
이전 `Touch(...)(Record,bool,error)`를 유지해 컴파일 실패했다. 로컬 집중 패키지 선택에서 누락한 테스트 소비자이며 환경 오류가 아니다.
Wrapper를 `TouchStatus`로 수정하고 전체 저장소의 Store 구현·호출을 다시 검색했다. `go test -run '^$' ./...`의 121 packages와
`go vet ./...`가 통과했다. Admin 전체 normal/race/CGO-disabled는 각각 86 run/pass, skip/fail 0이다.
집계의 줄 수는 같고 source digest는 위의 수정 후 값으로 갱신했다. 이 수정 소스로 Hosted full scope를 새로 실행해 아래 결과를 확인했다.
최초 `293d556` 실행은 수정 소스의 실행을 시작하면서 취소됐으며 완료 근거로 사용하지 않는다.

### 고정 소스 Hosted 완료

[CI 34155714775](https://github.com/progresshans/godj/actions/runs/34155714775)의 대상은
`0badd6b369fa599ee5891665602990aaf44df3fe`이며 최종 conclusion은 success다. 74개 고유 job의 최종 결과가 모두 success이고,
aggregate가 `scope: full`, `full_platform_verified: true` 및 아홉 필수 owner의 완료를 확인했다.
Linux/macOS amd64·arm64의 normal/race/CGO-disabled, PostgreSQL 17.10, 32-bit Linux, cold external build, 고정 reference와
Python 3.12.13/3.13.15/3.14.3/3.14.7을 포함한다. 로컬 전체 `make ci`는 중복하지 않았다.

Attempt 1은 72개 job이 성공했지만 macOS 26 race의 targeted-migrate 준비에서 pgx 모듈의 checksum 서버
`proxy.golang.org/sumdb/sum.golang.org/supported` 접속이 timeout돼 해당 job과 aggregate가 실패했다.
코드·checksum 정책을 바꾸지 않고 `gh run rerun 34155714775 --failed`로 실패 job과 aggregate만 재실행했다.
Attempt 2의 해당 product는 33 run/pass, skip 0이며 최종 aggregate도 통과했다. 이전에 성공한 72개 검증은 attempt 1에서 실행한
같은 소스의 결과다. 전체를 attempt 2에서 다시 실행했다고 주장하지 않는다.

PostgreSQL의 core/operator-target 두 그룹은 세 mode 모두 required/no-skip 검사를 통과했다. Core는 각 11 packages·44 run/pass,
operator-target은 각 2 packages·12 run/pass, 모두 skip 0이다. 로컬의 PostgreSQL skip 7개는 이 필수 roster에 포함돼 실행됐다.
Python 3.13.15의 실제 log에서 로컬 DRF skip 3개가 각각 `ok`인 것을 대조했다. 네 compatibility job도 discovery/completion guard를
통과했다. Compatibility의 exact-profile skip 4개는 별도 exact Darwin owner가 담당한다.

두 capture의 producer와 consumer는 모두 **attempt 1**이다. 성공한 normal PostgreSQL job이 만든
`systemstate-postgres-1`·`operator-postgres-1`을 같은 attempt의 conformance job이 받아 사용했다.
로컬에서도 `capture_artifact.py verify`로 repository/run/attempt/checkout·payload checksum을 확인하고,
두 Go consumer의 `Load`로 canonical format·profile·checksum·현재 behavioral source binding을 확인했다.

| Capture | Payload SHA-256 | Source binding |
|---|---|---|
| SYS-020 two-process | `f74d40b4ba59b91f3239bfc0a8c7e940aa4c1bf64f80ee51dbb5a84d03d827f1` | 307 files / 3,779,434 bytes / `822288d6cff1f0733ffebee3ed8317e543c718359a26a67bca3b056d18a525fa` |
| SYS-029 external operator | `a893c981d11b3b740b7bd941424e5ccb34d9bdf2442a4a07ef109644f4a64e76` | 365 files / 3,578,563 bytes / `6a27b8f4f199276ec0406e12674b9adcca54e1d3c776276edc6390302dc09f41` |

SYS-020은 두 writer의 동일 schema·barrier·restart 보존과 divergence/loss/drift/secret 0을 관측했다.
SYS-029는 PostgreSQL/SQLite 각각 provisioning 1 process, runtime 2 processes와 credential 1 row,
실제 Admin/API 인증·독립 restart 및 secret/state loss/schema drift 0을 관측했다.
[수정 구현 PR feedback](https://github.com/progresshans/godj/actions/runs/34155695860)도 통과했다.
완료 기록은 위 구현 소스 이후 Markdown에만 추가하며 source binding과 코드 집계를 바꾸지 않는다.

## GDJ-0062 — 테스트·검증 지원 코드 중복 점검

- 작업: [GDJ-0062](../../work/0062-validation-duplication-audit.md)
- 기준: `6ecf0b628465014ee1b1260454a08ce67713bd1a`, `codex/revision-fenced-migration-lifecycle`.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 기준에 아래 구현을 적용한 checkout에서 실행했다.
- 구현 소스: `c5b91c812af87c1450f5fdb881cb751491eb76be`.
- 상태: 구현·관련 로컬 checkpoint·고정 소스의 Hosted full scope 검증 완료. 2026-09-08 KST에 최종 결과와 capture를 확인했다.

### 검색 범위와 남긴 경계

Git 관리 Go 841개 파일(테스트 397개, 실제 generated 45개)과 Python 129개 파일 전체를 검색했다. Generated Go는 중복 삭제 대상으로
보지 않았다. Go 함수 10,029개를 파싱하고, 12줄 이상 본문의 exact-token/identifier·literal-normalized 후보 32/115개 그룹을
검토했다. Python은 같은 길이 기준 885개 함수의 AST 후보 10/29개 그룹을 검토했다. 파일 해시 목록·입력 로더·작은 포인터 복사·
DB seed·경로/환경 준비는 별도 검색으로 확인했다. 구조가 같다는 사실만으로 의미가 같은 것으로 판단하지 않았다.

| 검토 영역 | 처리와 남은 검증 소유자 |
|---|---|
| 고정 artifact 바이트·반복 입력 로딩 | `protocol/artifact_catalog_test.go`에 기존 기대 hash/size를 모았다. 각 계약 테스트는 phase·payload·provenance·mutation 비교를 유지하고 로더는 매번 새 값을 읽는다. |
| 관계 DB 준비·actual 스냅샷 | `internal/relationstate`가 여섯 관찰기의 동일 record/read를 소유한다. 다섯 seed와 네 표준 provisioning 경로를 공유하며 nullable key를 복사한다. 각 product의 DB·cache·정렬·취소·typed/dynamic 회귀는 유지했다. |
| 외부 프로젝트 준비 | `internal/testfixture`가 symlink 해석·별도 root·hash·민감값·환경 구성·동일 source import 감사를 공유한다. 환경 제거의 순서/중복 정책, timeout·출력 한도·command result·kill/reap 흐름은 각 owner에 남겼다. |
| attestation 읽기 | `internal/attestationio`가 duplicate-key/trailing JSON 거부와 bounded regular/source file 읽기를 공유한다. SYS-020/SYS-029의 schema·inventory·각각 4,096/8,192 files, 128/256 MiB 제한은 독립적으로 유지했다. |
| 일반 테스트 준비 | generator와 ORM의 fresh Schema fixture를 `internal/testschema`로 이동했다. CLI 결정성·oracle 입력은 table로 묶고 동일 negative-control/cookie/SQLSTATE-redaction helper만 공유했다. |
| Python 준비·표현 | 원자적 파일 교체 7곳, 같은 관찰 payload/row 변환, test-only decoder를 통합했다. 일반 strict decoder·PK decoder·permissive semantic decoder의 서로 다른 허용 범위는 유지했다. |
| 합치지 않은 구조 후보 | generated 프로그램 문자열 속 서로 다른 타입/실패 검증, 공개/내부 package 경계의 typed fake, DB 종류·실패 단계·savepoint SQL 분류·서로 다른 Article 모델 adapter, 실제 runtime과 테스트의 별도 관찰 구현을 유지했다. |

후속 검색은 새 파일을 포함했다. Go exact/shape 후보는 12/84개 그룹, Python은 1/14개 그룹이다. 남은 Python exact 본문은
호출하는 SQL statement classifier가 달라 결과 의미도 다르다. Go의 동일 본문도 owner별 resource bound, typed fake의 다른 package
계약, 실제 구현과 검증 사이의 독립성 등을 확인해 유지했다. 이 검색은 임의의 모든 부분 중복이 0이라는 주장이 아니다.

### 제거한 반복과 보존한 위험

- 180개 반복 reference file 대조를 **97개 고유 파일**의 기대 hash/size로 통합했다. 기대값은 기존 검사에서 복사했고 현 파일을
  해시해 새 기대값으로 덮어쓰지 않았다. SHA256SUMS 목록/형식 검증과 메모리에서 복원한 역사적 manifest의 checksum은 별도 의미라 유지했다.
- retired migration-relation의 구현 파일 8개를 특정 과거 SHA에 묶던 잠금을 제거했다. 관련 고정 manifest·NI·oracle 3개의 기존
  바이트 잠금과 실제 Django/Go 동작 검증은 유지했다. 구현 파일을 변경할 때 기대 SHA를 다시 적는 방식으로 처리하지 않았다.
- Python의 oracle 선택 20개·regeneration 대상 16개·동일 프로세스 결정성 14개·두 hash seed 결정성 5개 입력은 그대로 table에 남았다.
  마지막 5개는 서로 다른 hash seed `17`/`982451653`으로 **독립 자식 프로세스 10개**를 실제 실행하고 서로 및 고정 oracle과 대조한다.
- Go CLI 결정성 4개 입력도 각각 독립 actual을 두 번 생성한다. oracle 성공 3개의 count/첫 ID/마지막 ID assertion을 모두 유지했다.
  bundle accessor caller-owned-view 중복은 기존 별도 ownership test가 소유한다.
- 관계 fixture의 oracle-blind source 검사에 공통 actual helper를 추가했다. fresh seed의 slice/nullable pointer 오염 대조를 추가했다.
  REL-004의 orphan FK 거부와 delete의 physical FK/rollback fixture는 특수 조건을 보존했다.
- 두 attestation 모두 공통 I/O helper를 source binding에 넣었다. SYS-020은 restart가 사용하는 공통 test fixture도 포함한다.
  새 helper의 변경이 binding을 바꾸는 부정 대조를 기존 mutation test에 추가했다. 다른 source의 과거 capture를 현재 PASS로 사용하지 않는다.

빈 줄·주석 포함, 기준/현재에 같은 방식으로 집계했다. Go 분류는 `_test.go` → generated → conformance 지원 → examples →
framework/CLI 순으로 서로 겹치지 않는다. 프레임워크 runtime/API·생성 Go·고정 reference/profile/lock에는 변경이 없다.

| 분류 | 기준 | 구현 후 | 변화 |
|---|---:|---:|---:|
| Go 전체 | 315,925줄 | 313,585줄 | -2,340줄 |
| `_test.go` | 166,681줄 | 164,801줄 | -1,880줄 |
| conformance 지원 Go | 54,560줄 | 54,100줄 | -460줄 |
| Python 전체 | 37,863줄 | 36,330줄 | -1,533줄 |
| Python `test_*.py` | 13,771줄 | 12,289줄 | -1,482줄 |
| generated / framework·CLI·generator·support Go | 6,805 / 83,722줄 | 6,805 / 83,722줄 | 0 |

GDJ-0063에서 `scripts/sourceinventory`의 `ast.IsGenerated` 판정으로 기준과 구현 후를 재집계해 위 분류를 정정했다.
이전 generated 57개/12,614줄에는 생성 머리말을 출력하는 수작업 생성기 12개/5,809줄이 잘못 포함됐다.
실제 generated는 45개/6,805줄이며 나머지 5,809줄은 framework·CLI·generator·support로 옮겼다.
이 정정은 아래 전체 줄 수·순감소량을 바꾸지 않는다. 당시 함수 중복 검색 결과는 당시 검색의 관측으로 남긴다.

Go/Python 코드 합계는 **3,873줄 감소**했다. Go의 테스트+conformance 지원 비중은 70.030% → 69.806%다.
최상위 Go `Test*`는 2,213 → 2,192개(TestMain 제외), Python reference suite의 unittest method는 325 → 274개다. 합친 입력과 부정 대조는 위와 같이
유지했으며 이 개수를 새로운 영구 잠금으로 만들지 않는다. 코드 절대량과 반복 준비를 줄인 결과이며 실행 시간 개선율을 주장하지 않는다.

### 로컬 checkpoint

아래 A/B와 exact Python/oracle 검증의 구현 소스는 `8c47cbd2ebf0510cf5fb4ba1f42920e8598dc5c6`다. 이후 변경은 아래 CI 완료 검사와
그 source inventory에 한정하며 해당 변경의 별도 검증을 이어서 기록한다.

A: `./codegen/... ./internal/testschema ./orm`, `./conformance/internal/{protocol,relationstate,testprocess}`,
`./conformance/relationfixture/...`, 여섯 relation product, `./conformance/runners/godj ./conformance/cmd/godjcheck`,
`./conformance/systemstate/attestation ./conformance/projectoperatorproduct/attestation`.
A의 race/CGO-disabled는 `./codegen/...` 대신 `./codegen`을 사용했다. 외부 generator consumer의 해당 mode는 Hosted가 소유한다.
B: `./conformance/{migrationwriterproduct,projectmigrateproduct,projectmigratetargetproduct,projectshowmigrationsproduct,projectsqlmigrateproduct,runserverproduct,systemstate/restart}`.

| 실제 실행 | 결과 |
|---|---|
| A, `go test -json -count=1 -timeout=15m` | PASS, run/pass 2,850, skip/fail 0 |
| B, `go test -json -count=1 -timeout=30m` | PASS, run 104 / pass 97 / skip 7 / fail 0 |
| A, `go test -json -race -count=1 -timeout=15m` | PASS, run/pass 2,778, skip/fail 0 |
| A, `CGO_ENABLED=0 go test -json -count=1 -timeout=15m` | PASS, run/pass 2,778, skip/fail 0 |
| 최초 `make python-test-exact` | 환경 FAIL: installed uv 0.12.3과 고정 0.10.12 불일치. 274 tests, error 21, skip 3 |
| 고정 uv 0.10.12로 exact Python unittest 재실행 | PASS, 274 tests, skip 3. 기본 Django 환경에 없는 DRF 의존 검사이며 Hosted DRF 환경이 실행을 소유한다. |
| 고정 uv 0.10.12의 `make oracle-check` | PASS, Django/DRF 27개 세트의 실제 재생성 결과와 고정 바이트 일치 |

위 Go 실행의 stderr는 모두 0 bytes다. B의 skip 7개는 DSN 미제공 PostgreSQL 검사이며 Hosted PostgreSQL 세 mode에서 확인한다.
편집 중 compile에서 발견한 남은 helper 호출/미사용 import는 실제 회귀 묶음 실행 전에 수정했다.
Python 고정 실행은 `GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uvx --from uv==0.10.12 uv run --frozen python -m unittest discover -s conformance/runners/django/tests -v`다.
Oracle 대조는 같은 uv 환경에서 `make oracle-check`를 실행했다. DRF 하위 프로젝트는 자기 `.venv`를 사용했으며 root VIRTUAL_ENV를 무시한다는
도구 경고가 있었지만 고정 DRF profile 검증과 전체 checksum 대조는 통과했다.

`make generate-check`는 Helpdesk·Article·공통 relation 프로젝트 및 별도 metadata relation fixture에서 PASS다. A/B의 `go vet`,
`make ci-tools-test`(17 tests), `make docs-check format-check`(83 documents), `git diff --check`도 PASS다.
전체 OS/arch·cold build·PostgreSQL·외부 process의 모든 mode는 마지막 Hosted 실행이 소유한다. 로컬 전체 `make ci`를 중복하지 않았다.


### 초기 Hosted 실패와 CI 완료 검사 수정

[CI 34100953054](https://github.com/progresshans/godj/actions/runs/34100953054), source `8c47cbd2ebf0510cf5fb4ba1f42920e8598dc5c6`의
Python 3.12.13/3.13.15/3.14.3/3.14.7 job은 모두 `Ran 274 tests`, `OK (skipped=4)`까지 통과했지만 후속 shell이 이전
325 tests/21 skips 수량을 요구해 실패했다. 관련 job ID는 `101675085715` / `101675085702` / `101675085617` / `101675085712`다.
이 실행의 남은 job을 취소했다. 최종 49 success / 5 failure(네 Python 완료 검사와 aggregate) / 20 cancelled이며 full PASS가 아니다.

고정 수량 grep을 `scripts/ci/python_tests.py`로 교체했다. 현재 발견한 각 testcase가 정확히 한 번 시작·종료했는지 확인하고,
실패·expected failure·예상 밖 skip과 subtest skip을 거부한다. 별도 exact profile job이 실행하는 네 검사만 portable skip을 허용한다.
성공 marker는 전체 확인 후에만 출력하며 workflow의 pipefail과 terminal marker 검사로 중단된 실행도 거부한다.
새 일반 회귀를 추가할 때 전역 test-count를 갱신하지 않는다. 새 CI 실행 코드는 두 attestation의 source inventory에도 포함했다.

수정 후 검증:

- `make ci-tools-test`: PASS, 21 tests. empty/duplicate/missing discovery, dropped execution, missing stop, failure/xfail 및 임의 skip 거부 포함.
- 두 attestation package의 `go test -count=1`, `go test -race -count=1`, `CGO_ENABLED=0 go test -count=1`: 모두 PASS.
- CI와 같은 isolated Python 3.13.15 + Django 6.1/DRF 3.18.0 환경에서 새 runner: PASS, 274 tests / 4 exact-profile skips,
  `PYTHON_SUITE_VERIFIED tests=274 skips=4`. 기존 reference 코드나 고정 입력을 변경하지 않았다.
- 같은 환경에서 workflow의 semantic digest 코드 실행: PASS, 311 scenarios / 1,081,058 bytes /
  `b8d53e874169009fcd4650c79f2a007e18307d2fddd07a07d970f28bce2ed3f5`.
- actionlint v1.7.12: PASS. ShellCheck/Pyflakes는 미포함.

### 고정 소스의 Hosted 통합 검증

- [CI 34102953736, attempt 1](https://github.com/progresshans/godj/actions/runs/34102953736): PASS, source
  `c5b91c812af87c1450f5fdb881cb751491eb76be`, **74개 job 모두 success**, 실패·취소 job 0.
- 최종 집계 job `101690984587`은 `scope: full`, `full_platform_verified: true`와 선택한 9개 owner의 성공을 확인했다.
  Linux/macOS amd64·arm64의 normal/race/CGO-disabled, PostgreSQL 17.10, Python compatibility와 고정 reference를 포함한다.
- 대표 relation job `101681409513`(Linux arm64 normal), `101681409311`(Linux amd64 CGO-disabled),
  `101681409602`(macOS amd64 race), `101681409606`(macOS arm64 CGO-disabled)는 각각 26 packages, run/pass 3,213, skip 0과 필수 package/sentinel 검사를 통과했다.
- Linux amd64 project-check의 normal/race/CGO-disabled job `101681409232` / `101681409159` / `101681409181`은
  Go runner 전체 run/pass 387, skip 0과 필수 relation sentinel을 확인했다. 별도 runserver의 skip 1개는 PostgreSQL DSN
  미제공 검사이며 아래 PostgreSQL owner에서 실행했다. normal의 별도 cold CLI build milestone도 PASS다.

PostgreSQL 17.10의 필수 selector·no-skip 검사는 여섯 조합 모두 PASS다. 로컬 B에서 DSN 미제공으로 건너뛴 일곱 검사도 이 owner들이 실행했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 mode의 집계 |
|---|---|---|
| core | `101681409085` / `101681408990` / `101681409168` | 11 packages, run/pass 57, skip 0 |
| operator-target | `101681409063` / `101681408976` / `101681409116` | 2 packages, run/pass 12, skip 0 |

동일 실행의 `systemstate-postgres-1`(artifact `10011349612`)과 `operator-postgres-1`(`10011333769`)을 다운로드했다.
두 provenance의 repository/run/attempt는 `progresshans/godj` / `34102953736` / `1`, checkout은
`add43c2a27594ec51889070e0f8831da81916885`다. GitHub commit API의 tree `2793b9bef9a843b28fe5a3299010a8f266b5e129`는
로컬 구현 소스 `c5b91c8`의 tree와 일치한다. payload SHA-256을 다시 계산해 provenance·SHA256SUMS에 일치함을 확인했다.

| Capture | payload SHA-256 | 현재 source binding |
|---|---|---|
| SYS-020 `postgresql-17.10-two-process-v1.json` | `8a6aaf5c46e4091962f657c6ab57f60c7543cc6464d85cc2f521c88a0dc3dca0` | 295 files / 3,754,305 bytes / `768cd230ad2a3fdd98170f44b00c916f6bec3f0df4950dbe098e99cd707b764d` |
| SYS-029 `postgresql-17.10-sqlite-external-operator-v1.json` | `54872237de7953453fb595a7e3694510b64074d0dd0de0315a281990ea8134ec` | 354 files / 3,624,447 bytes / `c79a582482a101cc563e25e43a0c19cc2c8e48584cc8927b98836830fa7263b5` |

두 source binding은 현재 checkout에서 계산한 scope·file count·payload bytes·digest와 일치한다. Reference consumer
job `101683454026`도 같은 실행의 provenance·현재 source binding·actual 비교를 통과했고, 32비트 Linux compile/관계 product
실행과 고정 Django/DRF checksum·reference 미변경 검사를 통과했다.

Exact Darwin job `101681408911`은 고정 profile·oracle 대조와 Python 274 tests를 통과했다(DRF 의존 3개 skip).
Python 3.12.13/3.13.15/3.14.3/3.14.7 job `101681408958` / `101681408952` / `101681408942` / `101681408955`는 모두
`PYTHON_SUITE_VERIFIED tests=274 skips=4`와 311 scenarios의 고정 semantic digest 검사를 통과했다. Portable의 exact-profile
skip 4개는 exact에서, exact의 DRF skip 3개는 네 portable 환경에서 모두 `ok`임을 testcase identity로 대조했다.
Full scope에서 exact job의 중복 Go lifecycle step은 비대상이며 relation/project-check matrix가 해당 검증을 소유한다.

구현 이후 완료 기록은 Markdown 세 파일에 한정한다. 문서 변경에는 문서·링크·상태·diff 검사를 적용하며 전체 제품 검증을 반복하지 않는다.


## GDJ-0061 — 검증 fixture와 CI 실행 소유권 정리

- 작업: [GDJ-0061](../../work/0061-validation-fixtures-and-ci-ownership.md)
- 기준: `ddb8c5135533f9fb7fc280d0446f2688b7b6b649`, `codex/revision-fenced-migration-lifecycle`.
- 구현 소스: `21ceeb56021e65c7c718eef93a898150812b6c32`.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 기준에 이번 구현을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 소스의 Hosted full scope 검증 완료.

### 변경과 검증 소유권

관계 query/object/reverse/prefetch/select/delete product는 `conformance/relationfixture`의 동일 Author/Post 프로젝트를 소비한다.
중복 생성 Go 파일 42개를 없앴으며 whole-project drift·declaration bootstrap·앱 의존성·observer 경계 검사를 공통화했다.
기본 metadata-only relation fixture는 다른 생성 계약을 검증하므로 남겼다. 각 product의 실제 DB·cache·취소·rollback·typed/dynamic
회귀는 유지했다. Go AST 대조에서 이들 runtime test 본문은 변경되지 않았다.

Go runner의 oracle 일치 9개와 결정성 6개 테스트는 9개 subtest로 통합했다. 결정성을 검증하던 6개 입력은 독립 actual을 두 번
생성하며 첫 actual로 고정 oracle도 대조한다. 별도 준비로 세 번 만들던 중복을 제거했고 나머지 3개 입력은 기존처럼 한 번 생성한다.
이전 미배포 facade v2의 1,060줄 복제본과 전용 검사를 제거했다. 현재 full union의 **모든 generated file**을 다른 snapshot과 섞어
컴파일이 실패하는 검사, 기능별 prerequisite 실패, bundle 복사·순열 결정성, publication 실패 시 기존 결과 보존은 계속 실행한다.

Go 파일을 기준/현재에서 동일하게 집계했다. 빈 줄·주석 포함, `_test.go` → generated marker → conformance 지원 → examples →
framework/CLI 순으로 중복 없이 분류했다. 비교하는 양쪽 소스에서 직접 집계했다.

| 분류 | 기준 | 현재 | 변화 |
|---|---:|---:|---:|
| Go 전체 | 321,999줄 / 882개 파일 | 315,925줄 / 841개 파일 | -6,074줄 / -41개 파일 |
| `_test.go` | 168,081줄 | 166,681줄 | -1,400줄 |
| generated Go | 17,288줄 | 12,614줄 | -4,674줄 |
| conformance 검증 지원 Go | 54,560줄 | 54,560줄 | 0 |
| framework/CLI Go | 77,913줄 | 77,913줄 | 0 |

테스트와 conformance 지원 Go의 합은 222,641줄에서 221,241줄로 줄었다. 비중은 생성 코드라는 분모도 줄어 69.14%에서 70.03%가
됐다. 비중 하락을 성과로 주장하지 않는다. 실제 AST의 최상위 `Test*` 함수는 2,237개에서 2,213개로 줄었다(30개 삭제·6개 추가).
문자열 내부의 예제 `func Test...`와 TestMain은 세지 않았으며 이름·개수 자체를 새로운 영구 잠금으로 만들지 않았다.

### 로컬 checkpoint

공통 affected 집합 A:
`./conformance/relationfixture/...`, `./conformance/relationqueryproduct`, `./conformance/relationobjectproduct`,
`./conformance/relationreverseproduct`, `./conformance/relationprefetchproduct`, `./conformance/relationselectproduct`,
`./conformance/relationdeleteproduct`, `./conformance/runners/godj`, `./internal/compiletest`, `./internal/projectgenerate`,
`./conformance/internal/protocol`. 명령은 아래 flag와 해당 package를 `go test`에 직접 전달했다.

| 실제 실행 범위 | 결과 |
|---|---|
| 초기 affected `go test -run '^$'` | compile-only PASS, 테스트 본문 미실행 |
| A 및 `./codegen/... ./conformance/postgresproduct`, `-json -count=1 -timeout=15m` | 첫 checkpoint FAIL: run 2,243 / pass 2,233 / skip 2 / fail 8. 공통 source 검사기의 주석 오탐과 옛 CI include/SQLite job 가정 두 원인 |
| 수정한 `./conformance/relationfixture/... ./conformance/internal/protocol`, `-json -count=1 -timeout=15m` | PASS, run/pass 1,190, skip/fail 0. 나머지 package는 첫 checkpoint에서 PASS |
| A, `-json -race -count=1 -timeout=15m` | PASS, run 1,859 / pass 1,858 / skip 1 / fail 0 |
| A, `CGO_ENABLED=0`, `-json -count=1 -timeout=15m` | PASS, run 1,859 / pass 1,858 / skip 1 / fail 0 |
| `make generate-check`, A의 `go vet` | PASS. Helpdesk·Article·공통 relation 프로젝트와 별도 metadata fixture drift 없음 |
| `make ci-tools-test` | PASS, 17 tests. package 분류·scope 누락/skip/실패·malformed output 거부 포함 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/feedback.yml` | PASS. ShellCheck/Pyflakes 미포함 |
| `make docs-check format-check`, `git diff --check` | PASS, 문서 82개 |

수정한 observer 검사는 comment를 제외한 식별자·decoded string/import를 확인하며 escaped oracle 경로, file reader, NI shortcut과
문법 오류의 부정 대조를 통과했다. 설명 주석의 문구를 보존하기 위해 runtime 동작이나 oracle 경계를 완화하지 않았다.
normal의 PostgreSQL E2E skip은 로컬 DSN 미제공이며 최종 Hosted PostgreSQL mode들이 실행을 소유한다. publication crash helper의
직접 진입 skip은 부모가 별도 자식 프로세스로 실행하며 부모 회귀는 PASS다. 테스트 없는 generated/support package는 소비자 검증으로
확인하며 test pass 수에 넣지 않는다. 위 Go 실행의 stderr는 모두 0 bytes다.

### CI 실행 경계 확인

SQLite 전용 네 job은 관계 matrix와 같은 Linux/macOS amd64·arm64에서 같은 migrations/SQLite package의 normal·race·CGO-disabled를
반복했다. 이 job 정의를 삭제하고 관계 matrix가 전체 package와 normal vet를 소유한다. 네 주요 matrix의 include 반복을 platform 객체와
mode 축으로 정리했다. 변경 전후 YAML을 별도로 파싱·전개해 48개 좌표의 OS/CPU/mode와 timeout 값이 같음을 확인했다.
순수 schema/codegen 검사는 Portable Go의 각 mode가 소유하고 외부 consumer·DB 동작은 relation platform matrix에 남는다.

Full scope의 Go runner 전체 실행은 project-check matrix가 소유하고 동일 좌표의 relation subset은 생략한다. ORM scope에서는 relation
matrix가 subset을 직접 실행한다. 필수 sentinel과 no-skip 검사를 실제 runner owner에 적용했다. 같은 Darwin CGO-disabled lifecycle은
Full scope에서 두 matrix가 소유하고 reference-only scope에서는 exact job이 직접 실행한다.

실제 workflow의 relation/project-check shell을 추출해 synthetic Go JSON을 공급했다. 세 mode의 relation 두 분기와 project-check
9개 실행이 성공했으며 runner 누락·shared drift sentinel 누락·runner sentinel skip은 모두 거부했다. 원래 Go 실패 exit 42도 보존했다.
이는 shell 분기와 로그/필수 실행 검사의 검증이며 제품 테스트 실행을 대체하지 않는다. scope unit tests도 선택 owner의 실패·취소·skip·
누락을 거부했다. 최종 Hosted full scope가 실제 환경의 통합 실행과 PostgreSQL source-bound capture를 소유한다.

### 고정 소스의 Hosted 통합 검증

- source: `21ceeb56021e65c7c718eef93a898150812b6c32`, 2026-09-07 KST.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34088858221): PASS.
- [CI 34088869887, attempt 1](https://github.com/progresshans/godj/actions/runs/34088869887): PASS, 재시도 없이 74개 job 모두 성공.
- 최종 집계 job `101644441697`은 `scope: full`, `full_platform_verified: true`와 선택한 9개 owner의 성공을 확인했다:
  `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
  `product-project-check-matrix`, `project-operator-product-matrix`, `python-compatibility-matrix`,
  `relation-product-matrix`, `targeted-migrate-product-matrix`.

대표 relation job `101638169902`(Linux arm64 normal), `101638169908`(Linux amd64 CGO-disabled),
`101638169919`(Linux arm64 CGO-disabled), `101638169934`(macOS amd64 race), `101638169994`(macOS arm64 CGO-disabled)는 각각 26 packages,
run/pass 3,132, skip 0과 `--required`·`--packages`·`--no-skips` 검사를 통과했다. 공통 fixture의 drift/bootstrap sentinel을
검사하고 `RUNNER_COVERED=true`일 때 runner를 중복 실행하지 않는 경로를 실제로 확인했다.

Linux amd64 project-check의 normal/race/CGO-disabled job `101638169779` / `101638169880` / `101638169772`는
각각 Go runner 전체 run/pass 387, skip 0과 relation sentinel no-skip 검사를 통과했다. 별도 runserver package의 skip 하나는
PostgreSQL DSN 미제공 경로이며 해당 E2E는 아래 PostgreSQL owner에서 실행했다. normal의 별도 cold CLI build milestone도 PASS다.

PostgreSQL 17.10 검증 여섯 조합 모두 필수 selector·no-skip 검사를 통과했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 모드의 집계 |
|---|---|---|
| core | `101638169744` / `101638169758` / `101638169808` | 11 packages, run/pass 57, skip 0 |
| operator-target | `101638169850` / `101638169803` / `101638170626` | 2 packages, run/pass 12, skip 0 |

같은 실행의 `systemstate-postgres-1`(artifact `10006236076`)과 `operator-postgres-1`(`10006219408`)을 다운로드했다.
두 provenance의 repository/run/attempt는 `progresshans/godj` / `34088869887` / `1`이며 실제 checkout은
`6d06397c8007a0040a250d1fe9120e0ea0a7fbcf`이다. GitHub commit API의 tree `6557451b642de6750d63b009a5d29e6b05ccc774`는
로컬 구현 소스 `21ceeb5`의 tree와 같다. payload SHA-256을 다시 계산해 provenance와 SHA256SUMS에 일치함을 확인했다.

- `postgresql-17.10-two-process-v1.json`: `25c4467b34f65cc59635ad78792d798b1a6579f52832bb68b9f73961dc2cd13f`
- `postgresql-17.10-sqlite-external-operator-v1.json`: `7e178c6639ca6cdb92d8a03e6f6e1ef8998537bf5f545063b0ee88b5a331c82f`

Exact Darwin job `101638169661`은 고정 Python profile·oracle 대조를 통과했다. Full scope의 중복 Go lifecycle step은
실행하지 않았으며, 해당 Go 검증의 실제 결과는 관계/project-check matrix가 소유한다. Python은 325개 중 DRF 의존 3개가 skip됐다.
Python 3.12.13/3.13.15/3.14.3/3.14.7 job `101638169698` / `101638169708` / `101638169723` / `101638169728`은 모두 PASS다.
각 portable Python 실행의 skip 21개와 exact의 skip 3개를 테스트 identity로 대조해 반대 환경에서는 모두 PASS임을 확인했다.
각 환경의 skip을 그 환경에서 실행한 PASS로 세지 않는다.

Reference consumer job `101639545181`은 같은 실행의 두 provenance 검증, conformance actual 비교, 32비트 Linux compile과
관계 product 실행을 모두 통과했다. 공통 fixture는 32비트에서 실제 테스트를 실행했고 generated drift·Django/DRF oracle checksum·
reference artifact 미변경 검사도 통과했다.

### 실행 비용 관측과 완료 기록

| Hosted 실행 | 성공 job | 생성 시점부터 최종 job 완료 | 개별 job 실행 시간의 합 |
|---|---:|---:|---:|
| 이전 소스 `d1115c7`, run `34080294179` | 78 | 35분 26초 | 362분 18초 |
| 이번 소스 `21ceeb5`, run `34088869887` | 74 | 30분 27초 | 345분 27초 |

두 값은 서로 다른 실행의 관측이다. 전체 경과 시간에는 대기열이 포함되고, job 시간 합에는 병렬 실행이 중복 합산된다.
삭제한 SQLite 네 job의 이전 실행 시간 합은 5분 20초였다. 동일 cache·부하를 고정한 비교 실험이 아니므로 전체 속도 개선율로
일반화하지 않는다. 확정된 변화는 중복 네 job·runner subset·fixture compile/준비와 테스트 코드의 제거다.

전체 platform 검증은 위 Hosted 실행이 소유하며 로컬 전체 `make ci`를 반복하지 않았다.
구현 소스 이후 완료 기록은 Markdown만 변경했다. `make docs-check format-check`(82개 문서), `git diff --check`,
CURRENT·work 상태·검증 소스의 일치와 Markdown-only 변경 경계 검사를 통과했다.

## GDJ-0060 — 생성기 검증의 실행 경계 정리

- 작업: [GDJ-0060](../../work/0060-codegen-validation-boundaries.md)
- 기준: `0ffce7029b80988d6bc28391dca2f5d8967d65c7`, `codex/revision-fenced-migration-lifecycle`.
- 구현 소스: `d1115c7d8371cd52627eb4e1a6c7888b64b981fa`.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 아래는 기준에 이번 구현을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 소스의 Hosted full scope 검증 완료.

| 실제 명령·범위 | 결과 |
|---|---|
| `go test -run '^$' ./codegen/...` | compile-only PASS, 이 단계에서 테스트 본문은 실행하지 않음 |
| `go test -json -count=1 -timeout=15m ./codegen/... ./internal/compiletest ./internal/projectgenerate ./conformance/internal/protocol` | PASS. test/subtest run 1,767 / pass 1,766 / skip 1 / fail 0, stderr 0 bytes |
| `go test -json -race -count=1 -timeout=15m ./codegen/...` | PASS. run/pass 383, skip/fail 0, stderr 0 bytes |
| `CGO_ENABLED=0 go test -json -count=1 -timeout=15m ./codegen/...` | PASS. run/pass 383, skip/fail 0, stderr 0 bytes |
| `make generate-check` | PASS, checked-in 생성물 drift 없음 |
| `go vet ./codegen/...` | PASS |
| `python3 -m unittest discover -s scripts/ci -p 'test_*.py'` | PASS, 17 tests. 새 package 분류, 포맷 오류·공백 경로·tracked deletion·partial Git listing의 실패 보존 포함 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/feedback.yml` | PASS. ShellCheck/Pyflakes는 이 명령에 미포함 |
| `make quick`, `python3 scripts/check_docs.py`, `git diff --check` | PASS, 문서 81개. quick 실행 출력에 외부 consumer package가 포함되지 않음을 확인 |

normal의 skip 하나는 부모가 자식 프로세스로 실행하는 `TestPublicationCrashHelper`의 직접 진입이다. 부모 crash 회귀는 통과했다.
테스트가 없는 `codegen/internal/testfixture`는 호출하는 검사로 검증하며 별도 테스트 실행으로 세지 않는다.
제품 runtime·생성 ABI·고정 reference artifact에는 변경이 없다. PostgreSQL·전체 OS/arch/cold matrix는 최종 Hosted가 소유하며
이번 로컬 실행에서 전체 `make ci`를 반복하지 않았다.

Go AST로 기준과 변경 파일을 대조했다. 기존 최상위 test 함수 107개는 삭제·중복 없이 남았고, 생성 Go fixture literal
40종의 값과 등장 개수가 같았다. 순수 `codegen`의 직접 외부 Go 실행은 27곳에서 0곳으로 분리했다. 현재 외부 명령 생성은
consumer helper 한 곳이 소유하며 원래 각 테스트의 command argument와 결과·실패 검증은 유지한다.
이 개수는 이번 이관의 점검 결과이며 CI의 영구 roster나 제품 계약으로 잠그지 않는다.

### 실행 비용 관측

- 위 normal 실행에서 순수 `codegen`은 0.423초, 분리한 `codegen/consumertest`는 50.947초였다. package별 값이며 병렬 실행의 총 시간을 합산하지 않는다.
- 외부 검증 분리 후 최초 `make quick`은 14.40초, 포맷 배치 적용 후 실행은 2.91초였다. 캐시 상태도 다를 수 있어 전체 개선 비율로 일반화하지 않는다.
- 포맷 처리만 같은 현재 파일 집합·동일 머신에서 비교했다. 기준 commit의 Makefile로 `format-check`를 실행한 결과 2.879초,
  현재 `make format-check`는 0.179초였으며 둘 다 PASS였다. 이전 checkout의 제품 테스트를 실행한 결과가 아니다.
- Hosted의 `Fast Go feedback` 단계는 [이전 소스 `623ce53`](https://github.com/progresshans/godj/actions/runs/34046122604)의
  52초에서 [이번 소스 `d1115c7`](https://github.com/progresshans/godj/actions/runs/34080359289)의 12초로 관측됐다.
  두 실행 모두 PASS, Ubuntu 24.04·Go 1.26.5다. 서로 다른 실행·cache의 관측이며 전체 CI 속도의 비교 실험은 아니다.
- Go 라인은 새 package의 import·보조 코드와 추가 회귀를 포함해 기준보다 67줄 늘었다. 이번 개선은 빠른 경로에서 외부 빌드를 분리하고
  동일한 파일 집합의 포맷 프로세스를 줄인 것이며, 전체 검증을 제거하거나 전체 CI 시간 감소를 입증한 결과가 아니다.

### 고정 소스의 Hosted 통합 검증

- 날짜: 2026-09-07 KST, source `d1115c7d8371cd52627eb4e1a6c7888b64b981fa`.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34080359289): PASS. 빠른 실행에서 외부 consumer가 제외됐고,
  CI 도구 17개 테스트는 한 번 실행됐다.
- [CI 34080294179, attempt 1](https://github.com/progresshans/godj/actions/runs/34080294179): PASS, 재시도 없이 78개 job 모두 성공.
- 최종 집계 job `101619694472`은 `scope: full`, `full_platform_verified: true`와 선택된 10개 소유자의 성공을 확인했다:
  `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
  `product-project-check-matrix`, `project-operator-product-matrix`, `python-compatibility-matrix`,
  `relation-product-matrix`, `sqlite-matrix`, `targeted-migrate-product-matrix`.

관계·project-check·operator·targeted migration의 Linux/macOS amd64·arm64 normal/race/CGO-disabled matrix,
portable Go·SQLite 및 Python compatibility를 통과했다. 세부 package·환경·필수 실행 조건은 위 CI의 고정 workflow와 job 로그가 소유한다.

`codegen/consumertest`는 portable integration의 normal/race/CGO-disabled job
`101614191299` / `101614191225` / `101614191231`에서 실제 package 실행을 통과했다.
relation matrix는 `./codegen/...`를 실행하고, 새 package 위치의 mixed-snapshot 거부 테스트를 필수 sentinel로 검사한다.
Ubuntu amd64 세 모드 `101614191128` / `101614191176` / `101614191131`에서 각각 47 packages,
run/pass 3,495, skip 0과 `--packages`·`--required`·`--no-skips` 검사를 확인했다.

exact darwin/arm64 job `101614190974`은 SQLite lifecycle·고정 Python profile·oracle 재생성 대조를 통과했다.
Python suite는 325개 중 DRF 의존 테스트 3개가 skip됐으며, 이 세 개는 DRF를 설치한 Python
3.12.13/3.13.15/3.14.3/3.14.7 compatibility 작업 네 개에서 모두 PASS임을 실제 로그로 확인했다.
반대로 portable Python suite의 skip 21개는 exact darwin 로그에서 전부 PASS였다. skip 이름과 실행 결과를 대조했으며,
각 환경의 skip을 그 환경에서 통과한 테스트로 세지 않는다. DRF oracle 세 종류도 별도 고정 환경에서 대조를 통과했다.

PostgreSQL 17.10 제품 검증은 여섯 조합 모두 PASS다. 각 로그의 필수 selector와 `--no-skips` 검사를 확인했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 모드의 실행 집계 |
|---|---|---|
| core | `101614191080` / `101614191095` / `101614191096` | 11 packages, run/pass 57, skip 0 |
| operator-target | `101614191057` / `101614191047` / `101614191069` | 2 packages, run/pass 12, skip 0 |

같은 실행의 `systemstate-postgres-1`(artifact `10003548153`)과 `operator-postgres-1`(`10003519540`)을 다운로드해
provenance의 repository/run/attempt가 `progresshans/godj` / `34080294179` / `1`임을 확인했다.
실제 checkout은 PR merge commit `9f1ce653a2cfe1f39e7b7a4f96f26f67c543d66c`이며, GitHub commit API와 로컬 Git에서
확인한 tree `2f63a9a9279ff403a1c5dfddcf40a58bbf74f300`가 위 PR source의 tree와 같다.
payload의 SHA-256을 다시 계산해 provenance와 `SHA256SUMS`에 일치함을 확인했다:

- `postgresql-17.10-two-process-v1.json`: `0a988dfbb2fa6f58a787ef82246066bee672115a27cf913ce67054aee9fb443c`
- `postgresql-17.10-sqlite-external-operator-v1.json`: `f8020379725ebfe46601916b0c03270e710798c43b8c546e69663224f6b03f6a`

reference consumer job `101615278509`은 같은 실행의 두 캡처 provenance를 검증하고 conformance,
32비트 Linux compile·관계 product, 고정 oracle checksum과 reference artifact 미변경 검사를 통과했다.

최종 Hosted full scope가 전체 플랫폼 검증을 소유한다. 제품 API·구현 상태의 변경이 없어 구현 현황과 ADR은 수정하지 않았다.
위 구현 소스 이후 완료 기록은 Markdown만 변경한다.

완료 상태를 반영한 세 Markdown은 2026-09-07 KST에 `python3 scripts/check_docs.py`(81개 문서),
`git diff --check`, frontmatter·CURRENT 상태 일치와 검증 소스 이후 Markdown-only 변경 검사를 통과했다.

## GDJ-0059 — 테스트와 검증 코드 공통화

- 작업: [GDJ-0059](../../work/0059-test-validation-compaction.md)
- 기준: `257e593309721bb0da888a3cbdef9b93c2b52083`, 원래 작업 디렉터리와 `codex/revision-fenced-migration-lifecycle` 브랜치.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 아래는 기준에 이번 구현을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 source의 Hosted full scope 검증 완료.

관련 19개 package는 다음과 같다. 제품 runtime·공개 API·생성 ABI·고정 oracle/profile에는 변경이 없다.

```sh
gdj_compact_packages=(
  ./conformance/internal/generationtest ./conformance/internal/relationschema
  ./conformance/internal/testprocess ./conformance/internal/protocol
  ./conformance/relationproduct ./conformance/relationobjectproduct ./conformance/relationqueryproduct
  ./conformance/relationreverseproduct ./conformance/relationprefetchproduct
  ./conformance/relationselectproduct ./conformance/relationdeleteproduct
  ./conformance/projectmigrateproduct ./conformance/projectmigratetargetproduct
  ./conformance/projectshowmigrationsproduct ./conformance/projectsqlmigrateproduct
  ./conformance/runners/godj ./internal/compiletest ./internal/projectgenerate ./codegen
)
```

| 실제 명령·범위 | 결과 |
|---|---|
| 위 package의 `go test -count=1`을 process/fixture/protocol/runner/consumer 묶음으로 실행 (`-timeout=5m/10m/12m`) | PASS, 실제 SQLite·외부 process·consumer compile·생성/출판·oracle 비교·부정 회귀 |
| `go test -json -race -count=1 -p=2 -timeout=20m "${gdj_compact_packages[@]}"` | PASS, 19개 package. test/subtest run 2,353, pass 2,348, skip 5, fail 0; stderr 0 bytes |
| `CGO_ENABLED=0 go test -json -count=1 -p=2 -timeout=20m "${gdj_compact_packages[@]}"` | PASS, 19개 package. test/subtest run 2,353, pass 2,348, skip 5, fail 0; stderr 0 bytes |
| `make generate-check` | PASS, Helpdesk·Article·relationdelete 및 별도 관계 fixture 6개의 byte drift 없음 |
| `go vet` — 위 목록의 conformance package 16개 | PASS |
| `python3 scripts/check_docs.py`, `git diff --check` | PASS |

race/CGO-disabled의 skip은 PostgreSQL 접속 설정이 없는 전용 제품 테스트 네 개와, 부모 테스트가 자식 프로세스로만
실행하는 `TestPublicationCrashHelper`의 직접 진입 한 개다. 부모 publication crash 회귀는 통과했다.
로컬 PostgreSQL 실행을 주장하지 않으며 실제 DB 검증은 최종 Hosted scope가 소유한다.
JSONL의 test/subtest 건수는 실행 기록이며 제품 계약이나 고정 roster로 추가하지 않는다.

이관 중 남은 미사용 import로 compile-only 및 일부 normal package가 실패했다. import를 제거한 후 해당 package를
다시 실행하고 위 전체 관련 race/CGO-disabled를 통과했다. 최초 compile 실패를 성공 기록으로 재사용하지 않았다.

정적 변경 비교에서 기존 test 함수 삭제는 없다. 동일 helper 통합과 공통 입력의 상태 격리·generated inventory·process
안전성·출력 상한 회귀를 포함해 전체 Go 라인은 323,137에서 321,932로 1,205줄 감소했다. 실행시간 개선을 측정한 결과는 아니다.

### 고정 소스의 Hosted 통합 검증

- 날짜: 2026-09-07 KST(2026-09-06 UTC), source `623ce53e52187c7d2ab656775e356ba5e0ce5117`.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34046122604): PASS.
- [CI 34046136824, attempt 2](https://github.com/progresshans/godj/actions/runs/34046136824): PASS, 최종 78개 job 성공.
- attempt 1의 Python 3.14.7 job `101521418575`는 `Set up uv`에서 manifest 다운로드가 `fetch failed`로 실패했다.
  해당 Python 테스트와 semantic digest는 실행되지 않았다. 나머지 76개 job은 성공했고, 이 실패를 반영한 집계 job
  `101526253688`도 실패했다. 소스·lock을 변경하지 않고 `gh run rerun 34046136824 --failed`로 두 실패 작업을 재시도했다.
- attempt 2의 Python job `101526358026`은 도구 설치·portable suite·전체 scenario semantic digest를 통과했다.
  portable suite는 325개 tests, skip 21개로, 모두 별도 exact darwin/arm64 profile에서 실행하는 검증이다.
  최초 실행의 실패를 성공으로 바꾸어 기록하지 않으며 통과한 76개 작업은 같은 소스의 결과로 유지했다.
- 최종 집계 job `101527678459`은 `scope: full`, `full_platform_verified: true`와 선택된 10개 소유자의 성공을 확인했다:
  `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
  `product-project-check-matrix`, `project-operator-product-matrix`, `python-compatibility-matrix`,
  `relation-product-matrix`, `sqlite-matrix`, `targeted-migrate-product-matrix`.

관계·project-check·operator·targeted migration의 Linux/macOS amd64·arm64 normal/race/CGO-disabled matrix,
portable Go·SQLite 및 Python 3.12.13/3.13.15/3.14.3/3.14.7 compatibility를 통과했다.
각 실행의 세부 package·환경·필수 실행 조건은 위 CI의 고정 workflow와 job 로그가 소유한다.

PostgreSQL 17.10 제품 검증은 아래 여섯 조합 모두 PASS다. 각 로그의 필수 selector와 `--no-skips` 검사를 확인했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 모드의 실행 집계 |
|---|---|---|
| core | `101521418310` / `101521418292` / `101521418302` | 11 packages, run 57 / pass 57 / skip 0 |
| operator-target | `101521418308` / `101521418305` / `101521418336` | 2 packages, run 12 / pass 12 / skip 0 |

같은 attempt 1의 system-state 캡처 `systemstate-postgres-1`(artifact `9993227200`)과 operator 캡처
`operator-postgres-1`(`9993208641`)을 생성했다. reference consumer job `101522152376`은 두 캡처의 provenance를
검증하고 conformance·32비트 Linux compile/관계 product·고정 oracle checksum 검사를 통과했다.
exact darwin/arm64 job `101521418181`도 고정 Python profile·SQLite lifecycle·reference 검증을 통과했다.

다운로드한 두 provenance의 repository/run/attempt는 `progresshans/godj` / `34046136824` / `1`이다.
실제 CI checkout은 PR merge commit `915a718477c4642ae156d095e758743d0633c63d`이며, GitHub commit API와 로컬 Git에서
확인한 tree `fa9ad08184eeea92828474ce74922b160dceca2a`가 위 PR source의 tree와 같다. commit ID를 혼동하지 않는다.
payload를 다시 SHA-256으로 계산해 provenance와 일치함을 확인했다:

- `postgresql-17.10-two-process-v1.json`: `3935e5aeeba3d78e6636bdc82185bf24abfcb1ffe004f0cef01118605f5afa69`
- `postgresql-17.10-sqlite-external-operator-v1.json`: `0608758c67f8cbbaa2569e04f1fb76a05f275b27a62fff4ef49118b9f512bef3`

전체 `make ci`와 같은 전체 플랫폼 matrix를 로컬에서 추가 실행하지 않았다. 최종 Hosted full scope가 해당 범위를 소유한다.
제품 API·구현 상태의 변경이 없어 구현 현황과 ADR은 수정하지 않았다. 이후 완료 기록은 Markdown만 변경한다.

완료 상태를 반영한 세 Markdown은 2026-09-07 KST에 `python3 scripts/check_docs.py`(80개 문서),
`git diff --check`, frontmatter·CURRENT 상태 일치와 검증 source 이후 Markdown-only 변경 검사를 통과했다.

## GDJ-0058 — 관계 조회 정리와 eager First

- 작업: [GDJ-0058](../../work/0058-eager-first-ticket-detail.md)
- 기준: `0b9955ec0ef3b013e59fd185038d38e57582d008`, 원래 작업 디렉터리와 `codex/revision-fenced-migration-lifecycle` 브랜치.
- 로컬 환경: 2026-09-06, Go 1.26.5 darwin/arm64. 아래는 해당 기준에 GDJ-0058 변경을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 source의 관련 Hosted 검증 완료. 아래 각 기록이 해당 source와 범위를 소유한다.

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
로컬 Docker daemon이 실행 중이지 않아 PostgreSQL sentinel은 로컬에서 skip됐다. 최종 PostgreSQL 검증은 아래 Hosted 실행에서 수행했다.
이번 단계에서 전체 `make ci`, 전체 플랫폼·32비트·Django differential oracle을 로컬에서 다시 실행하지 않았다.

### Hosted 실패 후 generated fixture 보정

첫 고정 소스 `d594c9547fa0a57f6da28e704a9613ba6e2336f2`의
[PR feedback](https://github.com/progresshans/godj/actions/runs/34039980956)은 PASS였다.
[ORM scope CI](https://github.com/progresshans/godj/actions/runs/34040015705)는
`conformance/relationselectproduct/project/zz_godj_relation_select_related.go`가 새 생성기 결과와 달라 실패했다.
대표 job `101504961156`, macOS race `101504961226`, portable conformance normal `101504961243`과 CGO0 `101504961253`에서
같은 `TestCheckedInGeneratedSelectRelatedProjectMatchesElevenDeterministicCandidates` 실패를 확인했다. 이 실행은 완료 증거가 아니다.
원인을 보정하고 다른 완료 결과를 확인한 뒤 남은 작업을 취소했으며, 첫 실행의 최종 conclusion은 `cancelled`다.

누락된 companion을 재생성하고 `make generate-check`에 manifest가 없는 관계 fixture 여섯 개의 기존 drift 검사를 추가했다.
보정 checkout에서 `go test -count=1 ./conformance/relationselectproduct`, 같은 범위 `-race`, `CGO_ENABLED=0` 모두 PASS.
확장된 `make generate-check`, `python3 -m unittest discover -s scripts/ci -p 'test_*.py'`(17개), 문서 링크·diff 검사도 PASS였다.
보정 후 새 고정 source와 Hosted 결과를 별도로 확인하며 첫 실행의 성공한 일부 job을 재사용하지 않는다.

### 보정 소스의 최종 관련 검증

- 날짜: 2026-09-07 KST(2026-09-06 UTC), source `aca9115223b3d4703c36553e580c3ee60f7d2c42`.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34040428257): PASS.
- [CI 34040585667, attempt 1](https://github.com/progresshans/godj/actions/runs/34040585667): PASS, 48개 job 성공·5개 범위 외 그룹 skip.
- 최종 집계 job `101508656253`은 `scope: orm`, `full_platform_verified: false`와 다음 소유자의 성공을 확인했다:
  `portable-go-matrix`, `postgresql-product`, `relation-product-matrix`, `sqlite-matrix`, `targeted-migrate-product-matrix`.
- 관계 product의 Linux/macOS amd64·arm64 normal/race/CGO-disabled와 portable Go core/integration/conformance/product,
  SQLite 및 targeted migration의 선택된 조합을 통과했다. 각 세부 조합은 위 실행의 job 및 workflow가 소유한다.
- PostgreSQL 17.10 실제 product는 core/operator-target × normal/race/CGO-disabled 6개 조합이 모두 성공했다.
  Helpdesk의 `TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance`는 core 세 모드에서 필수 selector와
  `--no-skips` 실행 검사를 통과했다. 해당 job은 normal `101506541054`, race `101506541056`, CGO0 `101506541046`이다.
- 같은 attempt의 `systemstate-postgres-1`(artifact `9991617191`)과 `operator-postgres-1`(`9991598099`)이 생성됐다.
  이번 scope는 reference consumer를 선택하지 않았으며 그 실행·소비를 주장하지 않는다.

선택하지 않은 그룹은 `conformance-validation`, `exact-darwin-validation`, `product-project-check-matrix`,
`project-operator-product-matrix`, `python-compatibility-matrix`다. 이 결과는 GDJ-0058의 관련 검증이며 전체 프로젝트
platform/reference 검증으로 확대하지 않는다. 완료 기록은 Markdown만 변경하며 동일 제품 소스의 전체 matrix를 반복하지 않는다.

완료 문서는 2026-09-07 KST에 `python3 scripts/check_docs.py`(79개 문서), `git diff --check`와
`aca9115` 이후 변경이 모두 Markdown인지 확인하는 검사를 통과했다. 제품·도구·생성물은 최종 CI 소스와 동일하다.

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
