# 테스트 증거

현재 변경의 실행 결과는 이 파일에 한 번만 기록한다. 설계 채택, 코드 존재, 특정 환경에서의 검증은 서로 다른 상태다.
미실행·비대상·환경 실패를 PASS로 표현하지 않으며 다른 source의 성공을 현재 실행 결과로 옮기지 않는다.

## GDJ-0090 — Float의 독립 기준 준비

- 활성 구현의 독립 기준: [GDJ-0090](../../work/0090-floating-point-models.md), branch `feature/floating-point-models`.
- [ADR-0068](../adr/0068-binary64-field-and-finite-json-boundaries.md)는 설계를 채택했으며 GoDj Float 제품 구현·runtime 검증의 완료를 뜻하지 않는다.
- Runner·reference test·raw JSON 세 파일의 정렬된 SHA256 manifest는 `e31576a2e07038176618272c40a6c0eba57b7474e0441f6c3f178f64ca0217ba`다.
  [Raw 관찰](../../internal/floattest/testdata/django61.json)의 SHA256은 `43b1abf4b53b8d81fb89f55d80b5325998895150a0762c770b287e56844ccfd4`다.
- 고정 Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5의 public API를 실행했다. Model 89·Form 136·serializer 360·
  실제 JSON number 16, SQLite add/reopen/update/rollback/reverse·root/relation query 각 9를 보존했다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 fresh reference test를 각각 **1 test PASS**, skip/warning/exception 0으로 확인했다.
  Python과 SQLite source fingerprint는 실제 runtime과 대조하고 나머지 관찰을 모두 비교했다.
- SQLite는 NaN을 NULL로, -0을 +0으로 저장했다. DRF의 non-finite 입력은 field validation을 통과한 뒤 JSONRenderer에서 ValueError가 났다.
  해당 관찰을 정상적인 GoDj 입력·저장 성공으로 채택하지 않는다.
- 별도 **Go 1.26.5 / pgx v5.10.0 / PostgreSQL 17.5(Homebrew)** probe는 transaction의 임시 table에 finite 극값·최소 subnormal·
  ±0·NaN·±Infinity를 binary parameter로 넣었다. 반환된 float64 bits와 native float8send bits가 같았으며 NaN=NaN·NaN>Infinity·-0=0을 확인했다.
  Probe transaction은 rollback했고 임시 table은 남기지 않았다. 고정 Hosted PG나 GoDj FloatField의 실행 증거로 표시하지 않는다.

## GDJ-0089 — Duration의 모델·DB 범위와 소비자 연결

- 완료 작업: [GDJ-0089](../../work/0089-duration-models.md), branch `feature/duration-models`, baseline `1e04a854f1d9439e87d62e274ece93901684d888`.
- Duration 값·IR/default·typed/dynamic AST·generator·SQLite BIGINT/PG INTERVAL·Form/Admin·Helpdesk elapsed·OpenAPI/client를 연결했다.
  [ADR-0067](../adr/0067-duration-model-range-and-number-input.md)은 모델 범위·저장 한도·exact JSON number와 pinned numeric coercion을 구분한다.
- 로컬 runtime 검증 source는 Markdown 제외 **127개** 변경 파일이다. 정렬된 `<sha256>  <relative-path>\n` manifest의 SHA256은
  `a9a368834092ca313abbcf35063588c7774806f86787f2bbe1b3d58e6ccaad75`다. 아래 실행 전후 같은 파일 바이트를 확인했다.
  로컬 runtime 검증의 제품 commit은 `f06bc7a01060b014a129631f60f5d677978adcef`다.
  이후 schema/query/ORM의 GoDoc 주석 세 곳만 바로잡았고 Go scanner의 non-comment token 열이 동일함을 대조했다.
  이 주석 수정은 로컬 runtime 실행 source와 구분하며 Hosted는 실제 후속 commit에서 실행한다.

### 독립 기준과 초기 보완

Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3 UTC/en-us의 실제 public API와 file SQLite를 사용했다.
[Raw 관찰](../../internal/durationtest/testdata/django61.json)의 SHA256은
`e5cda610c3b48c31c2c9e788db77acaa5954d31cce61149b7c9913017596580e`다.
Model **67**·Form **106**·serializer **272**·실제 JSON number **15**, DB add/reopen/update/reverse·root/relation query 각 **9**·Min/Max를 보존했다.
SQLite signed int64 microsecond 양 끝과 한계를 1 넘는 write의 OverflowError·기존 행 보존도 실제 관찰했다.
숫자 전용 관찰 추가 전 raw는 `5c9eca13f8632e3f2ea79a8d0a017c5905a432da8b75073730ff4fb6cb969dba`이며 현재 reference로 표시하지 않는다.

위 최종 reference를 **Python 3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**의 fresh subprocess에서 각각 재생했다.
각 **1 test PASS**, skip/warning/exception 0이며 Python/SQLite fingerprint와 전체 관찰을 대조했다.
Go Form은 **106**개의 cleaned/errors/changed(초기 null/zero/소수초)를, serializer는 **268**개의 field 결과와 JSON number **15**개를 직접 대조한다.
NUL **4**개는 기존 global JSON `invalid_document` 경계를 별도로 assert하며 DRF field parity나 skip으로 합치지 않는다.

초기 focused 실행에서 공통 DB value-kind·migration loader의 Duration 등록, 생성 relation의 configuration error 전달 누락을 확인했다.
또한 별도 consumer fixture의 잘못된 함수 이름, Helpdesk의 embedded migration 목록과 Admin 입력 rendering 누락을 보완했다.
Required 실행이나 비교 기준을 제거하지 않았으며 아래 최종 source의 통합 실행을 새로 수행했다.

### 로컬 통합 checkpoint 완료

환경은 **Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5(Homebrew)**다. 작업 전용 DB·독립 schema를 사용하고
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다. 실행 종료 후 전용 DB의 연결 0개를 확인하고 삭제했으며 기존 service는 유지했다.

- Affected 일반 **30 packages / 8,866 pass events**, 동일 범위 race **30 packages / 8,804 pass events**가 terminal PASS다.
  Time 작업의 27개 범위에 Duration, API parser와 Article API를 더해 JSON number 변경의 기존 소비자도 검사했다.
- 차이 **62 events**는 `internal/compiletest`의 명시적 `!race` source에 속한 **7 root**와 subcase다. Test event 집합과 각 source build tag를 대조했다.
  Duration-vs-표준 time.Duration/clock.Time/calendar.Date의 predicate/write/F 컴파일 거부 3개를 일반 실행에서 확인했다.
- 양 모드의 helper-only skip 2개는 `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`다.
  실제 parent `TestPostgresRevisionFenceCrossProcessIntegration`, `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 모두 PASS다.
- CGO=0 focused 검증은 **4 packages / 5 root tests PASS, skip 0**이다. Generated Duration consumer·독립 OpenAPI client·Helpdesk SQLite/PG·
  PostgreSQL 전체 모델 범위와 외부 interval 거부를 정확한 selector로 실행했다.
- Generated model은 zero/NULL/default, 음수·microsecond·int64 양 끝, 기존 행 추가·reopen·reverse, typed/dynamic/F/IN·projection·Min/Max,
  forward/eager 관계와 cache/Unwrap 복사, 실패 Save·선택 필드·취소를 확인했다. 별도 module의 SQLite/PG child terminal receipts를 요구한다.
  SQLite 한계를 넘는 유효 모델 값은 거부하고 PostgreSQL은 저장·조회하며 probe transaction rollback 뒤 기존 행을 보존한다.
- PostgreSQL은 전체 모델의 ±999999999일 경계 저장·정렬·fresh reopen·transaction query를 확인했다. Native month/infinity/모델 범위 밖 값은 오류이고,
  nullable scanner의 이전 Valid도 해제한다. 오염된 pooled IntervalStyle은 물리 session 검증에서 교체된다.
- Helpdesk는 실제 로그인/Admin·HTML escaping·CSRF/permission·validation-before-transaction, numeric 입력, canonical no-op PATCH·PUT 생략/null,
  rollback·fresh reopen을 확인했다. `0010_ticket_elapsed` 이전 migration 파일은 변경하지 않았다.
- Actual OpenAPI 문서 3개와 고정 **ogen v1.24.0** 생성물을 갱신하고 별도 module의 **26개 필수 receipt**·HTTP·최종 DB를 검사했다.
  Module/tool lock은 유지했고 response domain validation과 명시적 request.Validate를 구분한다.
- Affected vet, generated drift, Helpdesk migration `candidate_count:0`, **Linux 386 / CGO=0** generated model/project build PASS.
  전체 **147 packages compile-only**도 완료했으며 이 결과를 전체 runtime PASS로 표시하지 않는다. CI Python tooling **37 tests PASS**.
- Pinned openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**으로 실제 문서 세 개를 검증했다. 모두 PASS다.

### Hosted 통합 milestone

Date·Time·Duration과 JSON number 기반의 통합 milestone으로 Hosted **full**을 선택했다. 기존 Draft PR #1에 통합한
source `7e338bf28d27d12516d6732e7ae5f38f7b19bda5`, attempt 1에서 [Hosted full](https://github.com/progresshans/godj/actions/runs/35478903468)을 실행했다.
로컬 runtime source `f06bc7a01060b014a129631f60f5d677978adcef`와의 차이는 위 GoDoc 주석·Markdown이다.
첫 실행의 Python compatibility 네 job은 각각 `PYTHON_SUITE_VERIFIED tests=293 skips=4`를 출력했으나 전체 scenario digest의 byte 수 검사에서 실패했다.
기대 `1,081,058` bytes와 실제 `1,081,069` bytes의 차이는 `b7269d9`의 순환 migration 구현 때 이미 갱신한
`godj.migration.writer.unsupported_delta_fail_closed` 하나다. 해당 관찰의 `self_or_cyclic_relation / relation_cycle`이
`required_field_without_backfill / unsupported_delta`로 바뀌었으나 workflow의 고정 기준은 갱신하지 않았다.
311개 관찰을 생성하고 이 시나리오만 이전 source의 함수로 치환했을 때 기존 byte 수와
SHA256 `b8d53e874169009fcd4650c79f2a007e18307d2fddd07a07d970f28bce2ed3f5`가 정확히 재현됐다.
현재 시나리오의 byte 수는 3,273, 이전은 3,262이며 구조화된 결과의 차이는 위 case/code 두 값뿐이다.

Workflow의 예상 byte 수와 digest 두 값만 수정했다. 수정된 workflow의 inline Python을 그대로 추출해
고정 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 각각 **311 scenarios / 1,081,069 bytes /
SHA256 `92bd2eb410e09ca3d046b0ca048c723376c3bfa55a9eb82f25276adc88be3450` PASS**를 확인했다.
각 runtime와 Django/DRF/asgiref/sqlparse version을 실제 실행에서 assert했고 CI tooling **37 tests PASS**, diff 검사도 완료했다.
첫 Hosted 실행은 terminal `cancelled`이며 62개 job 중 success 46·failure 5(Python 네 개와 scope 집계)·cancelled 11이다.
실패 원인을 보존하고 수정 source의 full로 대체한다. 다른 job의 중간 성공이나 이전 Time ORM·Text/DateTime full을
현재 source의 Hosted PASS로 표시하지 않는다.

대체 실행은 source `79637ef3f5943c9490027723527fb5074b01411f`, attempt 1의
[Hosted full](https://github.com/progresshans/godj/actions/runs/35479740366)이다. 첫 실행 source와의 차이는 위 CI 기준 두 값과 Markdown이다.
대체 실행은 **terminal success**, **62개 unique job 전부 success**, cancelled/skipped job 0이다.
Run API의 source/attempt/status와 attempt 1의 전체 jobs API를 대조했고 모든 job의 head SHA와 run attempt가 일치했다.
최종 `CI result (full)`은 `full_platform_verified:true`, `scope:full`과 다음 8개 owner의 완료를 출력했다.
`command-product-matrix`, `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
`product-project-check-matrix`, `python-compatibility-matrix`, `relation-product-matrix`다.

이 결과는 Date·Time·Duration과 exact JSON number를 포함하는 **79637ef** 통합 source에 적용한다.
후속 Float 독립 reference 준비 `e5104dc`와 별도 worktree의 Float 제품 구현은 이 Hosted source에 포함되지 않는다.
Duration 작업을 완료하고 Float 모델·소비자 구현과 해당 source의 검증을 이어간다.

## GDJ-0088 — Clock Time의 모델·소비자 연결

- 완료 작업: [GDJ-0088](../../work/0088-clock-time-models.md), branch `feature/clock-time-models`.
- Time 값·IR/AST/default·generator·양 DB TIME·Form/Admin·Helpdesk `0009_ticket_service_at`·OpenAPI/client를 연결했다.
  의미와 명시적 Python/Form 경계는 [ADR-0066](../adr/0066-clock-time-field-and-precision-boundaries.md)에 있다.
- 제품·검증 source: `9f0ffa8aeea143dd0da48789761235004dd4fa59`. 기존 Draft PR #1에 통합하고 push했다.
- 로컬 affected checkpoint를 완료했다. 제품·테스트·생성물·reference를 포함한 Markdown 제외 126개 파일의 정렬된
  `<sha256>  <relative-path>\n` manifest SHA256은 `2585525d6bf20dbb23a15d0b35f1868341f15eeb69847c44e3e8b55b85e20845`다.
  아래 최종 실행 중 이 source를 보존했다.

### 독립 기준과 초기 통합

고정 Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3 UTC/en-us의 실제 public API와 file SQLite를 사용했다.
Raw 관찰은 model 85개·기본 Form 152개·microsecond Form 152개·serializer 344개, 실제 DB add/reopen/update/reverse·root/relation query 각 9개·Min/Max다.
[Raw 파일](../../internal/clocktimetest/testdata/django61.json)의 SHA256은
`0d57d17153ef6060a5088b807ca706ac1c5eb4ba5e2792acc6ced933eab106c0`다.
Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 fresh subprocess의 pinned reference 재생을 각각 실행해 **1 test PASS**, skip/warning/exception 0을 확인했다.
Python/SQLite fingerprint는 실행 runtime과 대조하며 구버전의 model 11개·serializer 44개 차이는 explicit profile로 검사한다.

첫 27-package 통합 실행은 3 package가 실패했다. 기본 Django TimeInput이 초기 소수초를 제거해 Go의 precision 보존과 changed 결과
10개가 달랐다. 기본 관찰을 보존한 채 supports_microseconds=true 위젯의 별도 실제 관찰을 추가하고 Go를 후자와 비교했다.
이 precision 보존 결정은 [DEV-0014](../DEVIATIONS.md#dev-0014--timeinput의-초기-microsecond와-변경-감지를-보존)의 정확한 selector로 제한한다.
또한 Helpdesk registry fixture의 필드 수를 새 allowlist에 맞추고, 독립 client test에서 request encoder가 Validate를 자동 호출한다는
잘못된 가정을 제거해 명시적 Validate와 서버 검증을 구분했다. 제품의 소수초·JSON 보안·권한·transaction 경계를 약화하지 않았다.
수정 후 Form·Helpdesk SQLite/PG·독립 OpenAPI client·생성 Clock model의 focused **4 packages / 160 pass events**, skip 0을 확인했다.
그 뒤 추가한 import-name collision fixture와 Time config 오류 코드 정리를 포함해 다음 checkpoint를 실행했다.

### 로컬 통합 checkpoint 완료

환경은 **Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5(Homebrew)**다. 작업 전용 DB와 독립 schema를 사용하고
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다. 전용 DB는 모든 실행이 끝난 후 연결 0개를 확인하고 삭제했으며 기존 DB service는 유지했다.

- Affected 일반 실행 **27 packages / 8,390 pass events**, 같은 범위 race **27 packages / 8,331 pass events**가 terminal PASS다.
  Clock, schema/IR, query/ORM, codegen/consumer, migrations/definition, project wire/spec/generation/resource/autodetect,
  Form/model, serializer, Admin/OpenAPI/client, 양 DB/queryplan, compile boundary, Helpdesk와 두 source attestation package를 포함한다.
- 차이 **59 events**는 `internal/compiletest`의 명시적 `!race` source에 있는 7 root test와 subcase다. 일반 실행의 terminal event와
  각 source build tag를 대조했다. 새 Time-vs-Date/DateTime predicate/write/F 컴파일 거부 6개가 일반 실행에 포함된다.
- 양쪽 helper-only skip 2개는 `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`다. 실제 process parent인
  `TestPostgresRevisionFenceCrossProcessIntegration`, `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 normal/race 모두 PASS다.
- CGO=0 focused 실행은 **3 packages / 4 root tests PASS, skip 0**이다. Generated Clock consumer, Helpdesk SQLite/PG,
  독립 OpenAPI client를 정확한 root selector로 실행했다.
- Generated consumer는 자정 default·NULL·microsecond·invalid mutation/동적 입력·원본/cache/projection 복사·F·empty aggregate·
  취소·실패 Save와 update mask·기존 행의 추가/재연결/역방향·root/forward relation 관찰을 양 DB에서 확인했다.
  SQLite/PG child의 terminal PASS를 필수 receipt로 검사했다. `Time` model과 `Time`/`TimeValue` 필드 이름도 import와 충돌하지 않는다.
- Go Form은 microsecond를 보존하는 독립 관찰 **152개**의 cleaned/errors/changed를 대조한다. Serializer **328개**는 직접 field 결과와 대조하고,
  NUL **12개**는 GoDj JSON parser의 invalid_document 경계를 별도로 assert한다. Python typed aware time **4개**는 raw 관찰을 확인하고
  대응하는 zone-free Go 값이 없다는 경계로 구분했다. 344개 전체를 동일한 field parity로 표시하지 않는다.
- Helpdesk는 실제 로그인/Admin·CSRF/permission·validation-before-transaction·canonical no-op PATCH·PUT omission/null·rollback·fresh reopen을 확인한다.
  Ogen v1.24.0은 실제 builtin Session/CSRF 문서에서 재생성했고 독립 module의 compile/HTTP/DB·24개 required receipts,
  canonical clock wire·required response/domain validation과 명시적 request.Validate를 확인했다. Module/tool lock은 바꾸지 않았다.
- Affected vet와 `make generate-check` PASS, Helpdesk `makemigrations`는 **candidate_count:0**이다.
  `go test -exec /usr/bin/true ./...`는 **144 packages compile-only**이며 runtime PASS가 아니다.
  Generated Helpdesk model/project의 **GOOS=linux GOARCH=386 CGO_ENABLED=0 go build**도 PASS다.
- CI Python tooling **37 tests PASS**. 별도 pinned openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**으로
  실제 Article Bearer/Session·Helpdesk Session 문서 세 개를 검증했고 모두 PASS다. 문서 링크·diff·format도 검사했다.

현재 로컬 결과는 위 source·환경·범위에 적용한다. Hosted ORM과 전체 platform/cold-build 증거를 대신하지 않는다.

### Hosted ORM 완료

Source `9f0ffa8aeea143dd0da48789761235004dd4fa59`, attempt 1의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35475136652)이 terminal **success**다.
Run의 head SHA·attempt와 전체 **48개 고유 job / success 44 / scope skip 4**를 확인했다. 필수 job의 실패·누락은 없다.
최종 `CI result (orm)`의 실제 출력은 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

Portable Go, Linux/macOS의 command·relation 제품 normal/race/CGO0, PostgreSQL 17.10 실제 제품을 포함한다.
Python compatibility, exact darwin/arm64 profile, product project check, references/current captures의 네 job은 ORM 범위 밖으로 skip했다.
고정 Python reference의 별도 로컬 결과는 위와 같다. 이 ORM success를 새 전체 platform/cold-build 완료로 확장하지 않는다.
후속 Markdown만 바뀐 commit은 제품 source를 바꾸지 않으며, Duration 작업 사본의 준비 코드는 이 실행에 포함하지 않는다.

## GDJ-0087 — Calendar Date의 모델·소비자 연결

- 작업: [GDJ-0087](../../work/0087-calendar-date-models.md), branch `feature/calendar-date-models`, integration baseline
  `13937986914317d365f580f2adead277d73e2ce1`.
- 제품·로컬 검증 source: `b2b01f80f9a8b04c1893f8dc5d24e9b19ba4b087`. 기존 Draft PR #1에 fast-forward로 통합하고 push했다.
- Markdown 제외 109개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `1a410b39897b27aa75016a1e22b5ac3e5bff6dafd6f698131e7b4f1696498289`다. 아래 실행 중 제품·생성물·reference·test bytes를 유지했다.
- `calendar.Date`·IR/default/strict wire·typed/dynamic AST·generator·양 DB DATE·Form/Admin·Helpdesk 방문 예정일
  `0008_ticket_service_on`·PUT/PATCH·OpenAPI/독립 client를 연결했다. 설계는 [ADR-0065](../adr/0065-calendar-date-field-and-input-boundaries.md)다.

### 독립 기준

Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3, UTC/en-us의
[독립 runner](../../conformance/runners/django/calendar_date_reference.py)를 실제 public API와 file SQLite로 실행했다.
[Raw 관찰](../../internal/calendardatetest/testdata/django61.json)의 SHA256은
`4b3560af943bec1bacc4799ace4ce8c2d6b4053ebc09fe9ba84c878e883071fd`다.
Model 67개·Form 120개·serializer 272개, 날짜 추가 전 기존 행·재접속·갱신·역방향, root query 9개·forward relation 9개·Min/Max를 보존했다.
모델의 datetime coercion은 Python 참조 관찰이며 Go 모델의 지원 기능으로 세지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with djangorestframework==3.18.0
--with asgiref==3.12.1 --with sqlparse==0.5.5 python -W error -m unittest
conformance.runners.django.tests.test_calendar_date_reference`를 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 각각 실행했다.
각 **1 test PASS, skip·warning·exception 0**이다. Fresh process의 모든 관찰을 대조하고 Python/SQLite fingerprint는 실행 runtime과 비교한다.

Go Form은 120개 cleaned/error/changed 관찰, serializer는 268개 field 결과를 대조한다. NUL 4개는 기존 GoDj JSON parser의
`invalid_document` 거부를 별도로 assert한다. DRF field 오류 코드와 같다고 세거나 필수 실행에서 skip하지 않는다.

### 로컬 통합 checkpoint

환경은 Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)다. 이 작업의 전용 DB와 각 테스트의 독립 schema를
사용했고 `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다. 임의로 UTC process 환경에 의존하지 않는지도 확인한다.

- `go test -count=1 -json`의 affected **21 packages / 7,095 pass events**. Calendar, schema/ir, query/orm, codegen/consumer,
  migration definition, project wire, autodetect, Form/model, serializer, Admin/OpenAPI/client, SQLite/PG/queryplan, compile boundary,
  Helpdesk가 포함된다. Helper-only skip 1개는 `TestPostgresRevisionFenceHelperProcess`이며 이를 호출하는 실제
  `TestPostgresRevisionFenceCrossProcessIntegration`은 PASS다.
- 같은 범위의 `-race`: **21 packages / 7,042 pass events**, 같은 helper-only skip 1개. 일반 실행과의 53 event 차이는
  `internal/compiletest`의 명시적인 `!race` source에 있는 7 root test와 subcase다. 양쪽의 terminal event 집합과 build tag를 대조했다.
- `CGO_ENABLED=0` focused 실행: **3 packages / 4 root tests PASS, skip 0**. 생성 Date consumer, 독립 OpenAPI client,
  Helpdesk SQLite·PostgreSQL을 포함한다. 처음 selection에 없던 Helpdesk SQLite는 정확한 root test 이름으로 별도 실행했다.
- 새 generated Date consumer는 root/nullable default·invalid Create/Patch·기존 행 추가·fresh reopen·root/relation query와 independent
  DB 관찰·same-model F·projection/Min/Max·cache/Unwrap 복사·Save 선택 필드·실패 보존·취소·역방향을 양 DB에서 실행한다.
  Parent는 SQLite child와 PG URL이 주어진 경우 PostgreSQL child의 terminal PASS를 필수로 요구한다.
- Helpdesk는 실제 인증·Admin·CSRF/permission 경로, 날짜 validation-before-transaction, canonical no-op PATCH·PUT omission/null,
  실제 변경 뒤 강제 rollback과 새 연결의 저장 값을 검사한다. 기존 Boolean/DateTime/choices 동작도 함께 실행했다.
- Ogen v1.24.0 client를 실제 builtin Session/CSRF의 OpenAPI 문서에서 다시 생성했다. 별도 module의 lock은 보존했고,
  실제 HTTP·DB와 독립 transport의 required date response 오류·canonical wire·null/생략·연도 경계를 검사했다.
- `go test -exec /usr/bin/true ./...`: **141 packages compile-only**. 이 명령은 테스트 본문을 실행한 PASS가 아니다.
  Generated Date model/project의 `GOOS=linux GOARCH=386 CGO_ENABLED=0 go build`도 PASS다.
- affected `go vet`: PASS. `make generate-check`의 모든 checked-in generated source가 clean이고,
  Helpdesk `makemigrations` 재실행은 `candidate_count:0`이다. CI Python tooling **37 tests PASS**.

첫 checkpoint의 Date relation 생성 consumer는 `WithConfigurationError` 누락으로 compile에 실패했다. 메서드를 연결한 뒤 다시 실행했다.
같은 checkpoint의 serializer NUL 4개는 테스트가 global JSON 거부 전에 field binding을 기대해서 실패했다. 보안 규칙을 바꾸지 않고
reference 대조와 document-boundary assertion을 구분했다. 수정 후 focused 실행과 위 일반 checkpoint가 통과했다.

### Hosted에서 발견한 dependency closure와 guard 보완

첫 [Hosted ORM 실행](https://github.com/progresshans/godj/actions/runs/35471559786)은 source `b2b01f80f9a8b04c1893f8dc5d24e9b19ba4b087`다.
Portable normal/CGO0 integration의 namespace/publication-recovery test가 격리 module에 새 `calendar` dependency를 연결하지 않아,
의도한 recovery 단계 전에 readonly compile이 실패했다. 격리 fixture의 dependency 목록에 calendar를 추가했다. Candidate compile나
namespace/recovery 조건은 완화하지 않았다. 같은 run의 macOS Intel race 작업 하나는 `raw.githubusercontent.com`와 `go.dev`의
DNS ENOTFOUND로 Go 도구 준비 전에 실패했다. 이 환경 실패를 제품 PASS나 제품 원인으로 세지 않는다. 첫 run은 **cancelled**로 종료했다.

새 scalar가 통과하는 다른 경계도 확인해 `internal/irresource`, project spec, loaded migration의 Date default/choice payload를
문자열·aggregate 바이트 한도에 포함했다. System-state/operator source binding은 `calendar`, date input과 기존 temporal/Boolean
input helper를 포함하도록 보완했다. 해당 파일이 바뀌면 실행 증거의 source binding이 달라지는 negative control을 추가했다.

Guard/fixture 보완 source는 `8aa3c477e9ef5cfa733d0a2dea1d33c6d402d3b0`이며 별도 **11 files**다. 정렬된 파일 manifest SHA256은
`bce9fbb16b7df441b607c9661536bb649b02685fe5aa5c62d7f52c4f83399d06`이다. 이 추가 source에서:

- Namespace/publication-recovery의 실제 실패 test는 normal/race/CGO0 각각 **1 package / 3 pass events**, skip 0으로 확인했다.
- Project generation·project spec·IR resource·migration·두 attestation package의 전체 affected normal/race는 각각
  **6 packages / 775 pass events**다. Helper-only skip 1개는 `TestPublicationCrashHelper`이며 실제 crash parent
  `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 양 모드에서 PASS다.
- Date generated consumer·Helpdesk SQLite/PG·독립 client를 재실행했다. Normal/race/CGO0 각각 **3 packages / 4 root tests PASS**, skip 0이다.
  PG required 환경을 적용했고 이 재검증에 만든 두 번째 전용 DB도 실행 후 삭제했다.
- 추가 6 package의 vet와 문서·diff 검사도 통과했다. 앞의 21 package 결과는 원래 b2b01f8 source의 결과이며 이 보완을 포함한
  모든 패키지를 다시 실행했다고 표시하지 않는다.

Loaded migration의 oversized Date 테스트는 최초에 개별 payload path를 기대해 실패했다. 기존 오류 우선순위에서는 enclosing
`definition_bytes`가 먼저 반환된다. 그 우선순위를 유지하고 실제 resource 거부를 assert하도록 테스트를 수정한 뒤 위 checkpoint가 통과했다.

### Hosted ORM 완료

[Hosted ORM](https://github.com/progresshans/godj/actions/runs/35472148411)은 보완 source
`8aa3c477e9ef5cfa733d0a2dea1d33c6d402d3b0`, attempt 1에서 terminal **success**로 종료했다.
48개 unique job의 run ID·attempt·head SHA·terminal 상태를 대조했고 **44 success / 4 scope skip**을 확인했다.
Command product·portable Go·PostgreSQL product·relation product matrix가 검증 범위다. 최초 실패했던 portable normal/CGO0
integration과 macOS relation race도 이번 source에서 성공했다.

Skip 4개는 exact darwin/arm64 profile·product project check matrix·Python compatibility matrix·reference/product capture gate다.
필수 ORM 작업을 생략한 결과가 아니다. 최종 summary는 다음 scope를 명시한다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

검증 source 이후의 Date 통합 변경은 상태 문서뿐이다. 위 로컬 Python reference 재생과 Hosted ORM은 각자의 실행 source와
범위에 적용하며, 전체 platform/cold-build 검증이나 전체 프레임워크 완성을 뜻하지 않는다. GDJ-0087의 구현·검증은 완료했고
다음 기능은 [GDJ-0088 clock Time](../../work/0088-clock-time-models.md)이다.

## GDJ-0086 — Nullable Boolean의 모델·Form/Admin/API 연결

- 작업: [GDJ-0086](../../work/0086-nullable-boolean-models.md), baseline `6d3d42bd4023b9bc8bbe4588fd5645a949b4df9f`,
  구현 branch `feature/nullable-boolean-models`.
- Markdown을 제외한 69개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `ff7c94220331f4a80f09bdcd12c5a2e5b7fd0ef803c78ed08fbf501819650f52`다.
- 제품·생성물은 일반/race/CGO0에서 같다. 넓은 일반·race 실행 뒤 추가한 관계 NOT/IN reference와 test helper,
  필수 child receipt 및 CI 목록은 최종 focused 실행과 정적 CI 도구 검사로 확인했다. 이를 기존 실행에 소급해 합산하지 않는다.

### 로컬 실행

Go 1.26.5 darwin/arm64, 전용 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.
일반/race 범위는 `./schema/... ./codegen ./codegen/consumertest ./orm ./forms/... ./admin ./examples/helpdesk
./serializers ./api/openapi/... ./db/sqlite ./db/postgres ./migrations/definition ./internal/migrationautodetect ./internal/compiletest`다.

| 범위 | 결과 |
|---|---|
| affected normal | **17 packages, 6,477 PASS events**, 직접 helper skip 1 |
| affected race | **17 packages, 6,427 PASS events**, 직접 helper skip 1 |
| focused CGO_ENABLED=0 | generated nullable Boolean·Helpdesk SQLite/PG·외부 OpenAPI client **3 packages, 4 tests PASS**, skip 0 |
| 최종 nullable Boolean Form/reference/생성 소비자 | normal/race/CGO0 각각 **2 packages, 33 PASS events**, skip 0; child의 SQLite·PG 완료를 부모가 필수 확인 |
| 전체 compile-only | **138 packages**, `go test -json -exec /usr/bin/true ./...`; 전체 runtime PASS는 아님 |
| affected vet | PASS |
| 생성 재현성 | Article·Helpdesk·relation fixture drift 없음, checked-in relation product PASS, Helpdesk makemigrations `candidate_count:0` |
| Python CI 도구 | **37 tests PASS**, skip 0 |
| 문서·format·diff | 로컬 링크 118개 문서, gofmt, `git diff --check` PASS |

모든 test 시작/종료·package terminal과 빈 stderr를 대조했다. 일반과 race의 50 event 차이는 `internal/compiletest`의
`!race`로 선언된 파일의 7개 root test와 그 하위 case다. 해당 compile 검증은 일반 실행이 소유한다.
직접 skip 하나는 기존 `TestPostgresRevisionFenceHelperProcess`이며 parent의 cross-process 검사가 실제 child를 실행했다.
Helper skip이나 nested Go event 수를 별도 제품 기능 수로 세지 않는다.

생성 소비자는 실제 serialized migration의 기존 행 추가·역방향·새 연결, default nil/false/true·명시적 null/false,
typed/dynamic root 및 nullable forward 관계의 exact/isnull/IN/NOT, scalar projection·query cache와 eager Unwrap 복사를 비교한다.
Related facade 객체의 pointer identity는 보존하고 caller에게 복사한 raw snapshot을 검증한다. Save mask·취소·실패 입력도 포함한다.
Helpdesk는 기존 0001 행을 0007까지 성장시키고 Form/Admin의 초기값·재검증·저장, PUT/PATCH 생략·default·명시적 null/false,
권한·CSRF·category 범위·실패 transaction rollback과 재접속을 실제 SQLite/PG에서 실행한다.
외부 ogen client는 실제 API 문서 byte 일치·offline 재생성·독립 compile·HTTP·최종 DB와 **20개 필수 receipt**를 확인한다.

초기 실행의 Admin null 거부/재검증 coercion을 수정했다. 초기 PostgreSQL URL의 host 누락은 테스트 환경 설정을 바로잡았다.
생성 소비자가 facade identity를 raw snapshot 복사로 오해한 assertion도 수정했다. 이 초기 실패들은 최종 PASS가 아니다.

### 독립 기준

Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3의
[독립 runner](../../conformance/runners/django/nullable_boolean_reference.py)가 public model·Form/widget·serializer·schema editor/ORM을 실행한다.
[Raw 관찰](../../internal/nullablebooleantest/testdata/django61.json)의 SHA256은
`ce9b6c827c8de2d449c4cba0f245592fa8969667f895d77879798d853d01ff8e`다.
Required/optional widget 30개 입력 관찰, direct field와 JSON serializer의 별도 coercion·생략/default/partial,
기존 table 추가·재접속·update·remove 및 root/nullable 관계 각각 6개 query를 보존한다. Python의 넓은 coercion을 Go JSON에 채택하지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with djangorestframework==3.18.0 --with asgiref==3.12.1
--with sqlparse==0.5.5 python -W error::ResourceWarning -m unittest conformance.runners.django.tests.test_nullable_boolean_reference`는
Python **3.12.13·3.13.15·3.14.3·3.14.7 각각 1 test PASS**, skip·warning·exception 0이다. SQLite fingerprint는 각 runtime에서
직접 읽어 비교하고 의미 관찰은 고정 raw와 대조한다. DB connection은 `closing`으로 종료해 GC 경고를 성공으로 숨기지 않는다.

### Hosted 상태

제품 source `2ea0735c7d811dd4e07862506de7643abc6073f9`의
[Hosted ORM run 35467983458](https://github.com/progresshans/godj/actions/runs/35467983458)은 attempt 1에서 완료했다.
API의 source·attempt·모든 terminal job을 대조했고 **48개 unique job 중 44 success·요청 범위 밖 skip 4개**를 확인했다.
Job 목록과 skip owner는 기존 ORM 계획과 일치하며, 이번 workflow 변경은 새 consumer를 기존 실행과 필수 receipt에 추가한 것이다.
최종 `CI result (orm)` job `105967047083`의 실제 로그는 다음과 같다.

```json
{"full_platform_verified": false, "scope": "orm", "verified_jobs": ["command-product-matrix", "portable-go-matrix", "postgresql-product", "relation-product-matrix"]}
```

새 generated consumer는 relation matrix의 SQLite 실행과 PostgreSQL product의 각 normal/race/CGO0 실행에서 필수다.
이는 해당 source의 ORM scope 결과이며 현재 source의 full이나 Date 준비 작업의 PASS가 아니다.
후속 통합 상태·완료 기록 commit은 Markdown만 바꾸며 제품 source는 같다. 기존 full의 source는 아래 기록과 CURRENT에서 구분한다.

## GDJ-0085 — Self/cyclic 자동 migration과 재개 가능한 게시

- 작업: [GDJ-0085](../../work/0085-relation-autodetection.md), 의미: [ADR-0052](../adr/0052-project-linked-deterministic-makemigrations.md),
  [ADR-0064](../adr/0064-historical-relation-graphs-and-sqlite-remakes.md).
- Baseline `7d9106bce77dfca422b38e5f3347108f57485740`의 `feature/relation-autodetection`에서 구현했다.
  Markdown을 제외한 51개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `6c0fb835c357137b957a5559e8f3d13c70e9c6623840a53ad2a75e251b6c5398`이다.
- 일반 실행과 race/CGO0의 제품·test·자동 graph fixture bytes는 같다. 일반 실행 뒤 갱신한 MIG-107 policy/artifact/provenance는
  별도 현재 writer conformance로 검증했다. 두 JSON의 기존 formatting 복원은 parsed payload가 같은지 대조했다.
  CI의 필수 receipt 추가는 CI 도구 검사에 포함했다.

### 로컬 실행

Go 1.26.5 darwin/arm64, SQLite 3.53.3과 전용 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.
`go test -count=1 -json ./migrations/... ./internal/migrationgraph ./internal/migrationautodetect
./internal/projectmigration/... ./internal/projectcheck ./db/sqlite ./db/postgres ./conformance/migrationwriterproduct`를 실행했다.

| 범위 | 결과 |
|---|---|
| affected normal / race / CGO_ENABLED=0 | 각 **11 test packages, 5,861 PASS events**, 1 no-test package; 세 test roster 동일 |
| 현재 writer conformance | runner/checker/protocol의 `MigrationWriter\|GDJ0050` 검사 **3 packages, 20 PASS, skip 0**; 실제 MIG-099..110 12개 비교 포함 |
| 전체 compile-only | **136 packages**, `go test -json -exec /usr/bin/true ./...`; 전체 runtime PASS는 아님 |
| affected vet | PASS |
| generated drift | Article·Helpdesk·relation fixture clean, checked-in relation product 검사 PASS |
| Python CI 도구 | **37 tests PASS, skip 0** |
| 문서·format·diff | 117개 문서의 로컬 링크, gofmt와 `git diff --check` PASS |

Go JSON의 모든 test 시작/종료·package terminal, 새 graph/연결 초기화/중단 복구 필수 receipt와 빈 stderr를 대조했다.
직접 skip 둘은 기존 `TestPostgresRevisionFenceHelperProcess`, `TestMakemigrationsCrashHelper`이며 실제 child는 parent
process 검사가 실행한다. Helper skip과 Go event 수를 제품 기능 PASS 수로 세지 않는다.

별도 module의 public CLI는 세 cyclic 후보의 preview/write/hash 일치·DB-free 게시·repeat clean을 실행한다. 실제 migrate,
생성 모델의 저장·조회·명시적 순서의 First, 재접속과 잘못된 FK 저장 거부, populated reverse와 재적용·sequence를 검증한다.
동시 writer는 한 게시자와 한 clean 결과로 끝나며, 후보 세 개 각각의 partial-temp write/directory fsync 뒤 SIGKILL을 주입한
여섯 case는 이미 게시한 inode/bytes를 보존하고 동일한 나머지 후보를 재개한다.

실제 양 DB에서 same-app/cross-app 자동 정의를 load/apply/reopen/zero/reapply한다. 명시적 PK가 중간에 있는 선언과
여러 위치의 FK·integer·text를 보존하고 SQL projection, inbound/self 관계·기존 행과 sequence 상한을 검사한다.
Required FK를 미룬 중간 table에 행이 있으면 정확한 empty-table guard에서 거부하며 그 step의 column/recorder는 게시하지 않는다.
별도 column drift와 rollback·revision fence·quarantine 회귀도 유지한다.

### 독립 기준과 발견한 결함

고정 Django 6.1 / asgiref 3.12.1 / sqlparse 0.5.5를 사용한 실제 autodetector/loader/executor/schema editor/recorder 관찰을
[raw fixture](../../internal/migrationgraphtest/testdata/django-autodetect61.json)에 보존했다. SHA256은
`87487f710612204635c0b90d72b6939bb1013a23d07e8bb367acf3e482cb88fb`이며 기록 환경은 Python 3.14.7 / SQLite 3.53.1이다.
File discovery만 fixture seam이다. 두 DB의 이름별 field/constraint graph와 실제 관계 값을 대조하며 GoDj field 순서는
독립 선언 순서와 대조한다. Django historical field 순서·파일 이름·remake 후 ID 차이는 raw 그대로 보존하고
[DEV-0010/DEV-0013](../DEVIATIONS.md)에 설명한다. Exact file/field-order/ID parity를 주장하지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
python -m unittest conformance.runners.django.tests.test_migration_autodetect_reference
conformance.runners.django.tests.test_migration_writer_decisions`를 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 실행했다.
각 **6 tests PASS, skip 0**을 확인했으며, 아래 연결 종료 수정 뒤 네 환경에서 경고·exception 없는 완료를 다시 확인했다.
Runtime fingerprint는 실행 환경과, 관찰 본문은 raw와 대조한다.
MIG-107의 Go-owned 거부 case는 독립 Python decision의 현재 실행으로만 갱신했다. 같은 writer oracle의 나머지 11개 관찰과
Django profile은 canonical bytes가 동일하며, 현재 Go-owned 다섯 decision의 재생 일치를 검사한다.

외부 재접속 소비자가 드러낸 SQLite 기본 FK OFF 결함은 per-physical-connection connector에서 ON/readback을 수행하도록 수정했다.
Pool growth·replacement·reopen, DSN의 OFF 옵션, 초기화/rows/close 오류와 취소를 검사했다. 이전 slice alias, fixture의 무정렬
First·SQLite double-quoted string fallback·Django in-memory 연결 재사용 실패는 PASS로 세지 않았다.

첫 통합 source `b7269d98c6d838fca2fda532d6d8a699dfe0a963`의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35463368487)에서
일반 artifact byte-lock 검사가 갱신되지 않은 MIG-107의 manifest/deviation/oracle size·hash와 SHA256SUMS를 검출했다.
로컬의 이름 기반 writer 검사는 이 공통 검사를 포함하지 않았으므로 Hosted 성공으로 처리하지 않는다.
독립 decision 재생과 나머지 11개 관찰 보존을 다시 확인하고 checksum catalog의 정확한 변경분만 갱신했다.
제품 코드는 바뀌지 않았다. 수정한 protocol **전체 package 904 PASS, skip 0**을 실제 실행했다.
수정 후 Markdown을 제외한 54개 파일의 manifest SHA256은
`7bd95d43b576066e32622c8880f475634cd728411c49dc3cc425f99341dad020`다. 관련 catalog 수정 외의 로컬 runtime 검증 bytes는 그대로다.

후속 로그 검토에서 Python 3.13/3.14의 종료 시 `ResourceWarning`을 발견했다. `sqlite3.Connection`의 context manager는
transaction을 종료하지만 연결을 닫지 않으므로, fingerprint 보조 연결을 `contextlib.closing`으로 명시적으로 닫았다.
종료 코드 0과 unittest OK만으로 경고 없는 완료를 판단했던 최초 Python 결과는 최종 증거로 사용하지 않는다.
같은 네 버전에서 6 tests씩 다시 실행해 stderr의 warning/exception 부재까지 확인했다. 수정한 Python test SHA256은
`839739b5a0c7ab1626502b9c5008fbc08b34071117e988066647a74530151e87`이며, Hosted ORM 대상 Go·fixture·workflow source는 변경하지 않았다.

전체 로그·source manifest·event audit는 `/tmp/godj-0085-position-path`가 가리키는 로컬 scratch에 있다.
통합 source `4320eba32a0dcb3a1e21b6244c87e32a89dad5b6`의
[Hosted ORM run 35463646580](https://github.com/progresshans/godj/actions/runs/35463646580)은 **attempt 2에서 완료**했다.
Attempt 1의 macOS-26 race command가 `go mod tidy`의 sumdb TLS handshake timeout으로 실제 제품 실행 전에 실패했고,
source 변경 없이 실패 job만 재실행했다. 최종 API에서 같은 run ID/head SHA의 48개 unique job을 대조하여
**44 success, 4 expected scope skip**과 모든 selected coordinate의 완료를 확인했다. 최종 `CI result (orm)` job은
`105956145302`이며 실제 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10의 normal/race/CGO0·core/operator-target 여섯 조합과 선택한 Linux/macOS의 관계·명령·portable 검증을 완료했다.
예를 들어 PG normal core는 12 packages·1,863 runs/pass·skip 0, Linux arm64 CGO0 relation은
27 packages·4,910 runs/pass·skip 0을 실제 job log에서 대조했다. 이후 Python fingerprint 보조 연결 종료 수정은 위 네 버전의
로컬 Python 재생으로 검증한 별도 source다. 이를 Hosted 실행 파일로 표시하지 않는다.
Full-platform·배포·전체 프레임워크 완성의 증거는 아니다.

## GDJ-0085 — 자동 relation 계획의 field insertion 기반 checkpoint

- [활성 work](../../work/0085-relation-autodetection.md)의 `feature/relation-autodetection`, baseline `7d9106bce77dfca422b38e5f3347108f57485740`에서 실행했다.
  아래는 자동 cycle 분할을 연결하기 전의 **field insertion 기반** 검사이며 GDJ-0085 전체 완료 결과가 아니다.
- Markdown을 제외한 25개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `261aad3632e9b0d83c1d5c0c3f639b7223f51ddec806a24cae7024ecb7d363a2`다. 최초 self Create/nullable self Add 탐지 변경을 포함한다.
- Go 1.26.5 darwin/arm64, 전용 PostgreSQL 17.5(Homebrew)와 SQLite를 사용했다.
  `go test -count=1 -json ./migrations/... ./internal/migrationgraph ./internal/migrationautodetect
  ./internal/projectmigration/... ./db/sqlite ./db/postgres`를 normal, `-race`, `CGO_ENABLED=0`에서 실행했다.
  각 lane **9 test packages, 5,377 PASS events**, 1 no-test package이며 세 실행의 test roster가 정확히 일치한다.
  유일한 직접 test skip은 `TestPostgresRevisionFenceHelperProcess`; 실제 helper는 기존 parent process integration이 실행한다.
  모든 test 시작/종료·package terminal과 빈 stderr를 대조했다. Go event 수는 제품 기능 수가 아니다.
- 새 실 DB 검사는 명시적 PK가 중간에 있는 self/inbound graph에 여러 FK·integer·text를 삽입한다. Serialized definition →
  SQL projection → forward/reopen/no-op → 여러 populated reverse/remake → zero/reapply를 실행하며 정확한 logical field order,
  기존 행/NULL/참조와 삭제된 ID의 sequence 상한을 보존한다. Retained column 이름 drift는 다음 schema/recorder 게시 전에 거부한다.
- 위치의 codec roundtrip/canonical digest, 잘못된 anchor/type/과대 문자열, retained field 변경·순서 변경, physical column 중복/
  ordinal/타입/constraint source 변조와 기존 rollback·contention·quarantine/process 검사를 포함한다.
  첫 실행의 slice 삽입/삭제가 borrowed Before 배열을 바꾼 실패는 PASS로 세지 않았다. 복사 경계 수정 후의 위 source만 채택했다.
- 원본 JSON·stderr·source manifest와 audit는 `/tmp/godj-0085-position-path`가 가리키는 로컬 scratch에 보관했다.
  이후 자동 candidate 분할·부분 게시 재개, Go-owned MIG-107 갱신과 CLI/생성 소비자 검증이 남았다. 새 Hosted/full-platform/배포
  결과나 기존 Draft PR에 통합된 source를 뜻하지 않는다.

## GDJ-0084 — Historical relation graph와 순환 migration

- 작업: [GDJ-0084](../../work/0084-relation-migration-graphs.md), 의미: [ADR-0064](../adr/0064-historical-relation-graphs-and-sqlite-remakes.md).
- Baseline `af49707c1530b454a06b4aefa57534a3774517f7`의 별도 `feature/relation-migration-graphs`에서 구현했다.
  Markdown을 제외한 35개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `c568dfdb26ab7be34978fa123f288c2c061acb5e0ac454c3407c35b968c25cb8`이다.
  Go·fixture·CI 33개는 아래 세 Go lane과 동일 bytes다. Python runner/test 두 파일의 portable runtime fingerprint 처리는
  별도 최종 4-version 재생으로 검증했다.

### 독립 관찰과 의도적 차이

고정 Django 6.1 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.7의 fresh process에서
[runner](../../conformance/runners/django/migration_graph_reference.py)의 실제 SQLite migration executor/loader/schema editor로
10단계의 전체 historical field/choices, 실제 행·FK와 recorder를 수집했다. File discovery만 fixture seam으로 교체했다.
[Raw fixture](../../internal/migrationgraphtest/testdata/django61.json) SHA256은
`6a6d21dead891f1e3f9c2e9967eb87bdfcc449124c54c627ef974736d767983f`다.

Self Create → 관계 두 개 Add → self Add → choices → close/reopen → self/choices 역방향 → 두 관계 역방향 → zero →
reapply → zero를 비교한다. SQLite remake 뒤 Django의 다음 ID는 3, GoDj의 다음 ID는 101이다. 기존 sequence 상한 보존을
[DEV-0013](../DEVIATIONS.md#dev-0013--sqlite-migration-remake에서-삭제된-id의-sequence-상한을-보존) 하나로 명시하며,
나머지 모든 관찰은 동일하게 대조한다. Raw oracle의 값을 바꾸거나 전체 exact parity로 표현하지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
python -m unittest conformance.runners.django.tests.test_migration_graph_reference`를 Python
**3.12.13 / 3.13.15 / 3.14.3 / 3.14.7 각각 1 test PASS, skip 0**으로 재생했다. Python fingerprint는 실행 버전과 대조하고
관찰 본문은 raw artifact와 일치해야 한다. Django PostgreSQL의 독립 reference 실행을 뜻하지 않는다.

### 로컬 실행과 source

Go 1.26.5 darwin/arm64, SQLite 3.53.3과 전용 PostgreSQL 17.5(Homebrew)를 사용했다.
실행 목록은 `./migrations/... ./internal/migrationgraph ./db/sqlite ./db/postgres ./internal/migrationautodetect ./internal/migrationgraphtest`다.

| 범위 | 결과 |
|---|---|
| affected normal / race / CGO_ENABLED=0 | 각 **7 test packages, 5,283 PASS events**, 2 no-test packages; 세 test roster 동일 |
| generated nested forward/eager 소비자 | normal/race/CGO0 각각 **2 top-level PASS**, 실제 생성 module과 strict child harness |
| 전체 compile-only | **136 packages PASS**, `go test -exec /usr/bin/true ./...`; 전체 runtime PASS는 아님 |
| affected vet | PASS |
| Python CI 도구 | **37 tests PASS, skip 0** |
| 문서·format·diff | 116개 문서의 로컬 링크, 변경 Go gofmt와 `git diff --check` PASS |

모든 Go JSON의 test 시작/종료, package terminal, 새 필수 receipt와 stderr를 대조했다. 유일한 직접 test skip은 기존
`TestPostgresRevisionFenceHelperProcess`이며 실제 child는 해당 parent process integration이 실행한다. Helper skip을
기능 PASS로 세지 않는다. Test event 수는 제품 기능 수가 아니다. Generator/생성 ABI의 변경은 없으며 기존 generated 파일에
drift가 없다. 두 소비자는 이제 수작업 DDL 대신 정확한 Schema IR을 serialized definition으로 load하여 실제 migrate한다.

### 보존·거부·실패 경계

- Self와 3-model cycle, 같은 source의 여러 Add/Remove, cross-app 같은 model 이름의 back edge를 실제 양 DB에서
  apply/unapply/reopen한다. 기존 inbound/self 참조 값, NULL, 전체 행과 sequence 상한을 보존한다.
- Direct target의 PK뿐 아니라 transitive target의 전체 physical schema를 확인한다. Target에 미기록 column을 주입하면
  다음 step의 schema/recorder successor를 남기지 않고 거부하며, 외부 drift를 제거한 fresh 호출은 성공한다.
- Transitive metadata의 누락·reserved table·caller alias와 seal 이후 변조, graph의 불연속·미래 target·reverse collision,
  깊이 2,048 cycle과 큰 field/app 이름·aggregate resource 한도를 검사한다. DB 밖의 planner/reconstructor import 경계를 유지한다.
- SQLite의 FK suspension 전후/readback, BEGIN, 첫째/둘째 copy, drop/rename, 마지막 FK 검사, recorder, COMMIT 전후,
  복원 write/read와 취소를 주입한다. 물리 discard 실패 3개를 포함한 **19개 case**에서 rollback/committed/unknown과
  row/FK·pool readback·terminal quarantine·명시적 Close 뒤 file reopen의 실제 durable 결과를 대조한다.
- 기존 contention/stale/fork/rollback/process와 recorder 회귀도 위 DB package 실행에 포함한다. 폐기한 flat-target 전용 제한
  검사는 closed graph의 양방향 실행과 whole-plan capability gate로 대체했다. Reverse ownership/resource 보장은 공통 graph와
  실제 큰 boundary fixture가 계속 검증한다.

초기 checkpoint의 기존 flat-target 기대값 실패, shared app의 중복 byte 계산, 순수 core의 backend import, Django sequence
차이와 cross-app target 선택 fixture 오류는 PASS로 세지 않았다. 최종 위 source의 완료 결과만 채택했다.
통합 source는 `d6db513aba479ebec6a5f256bebac9348a0a32ce`이며 35개 파일의 실제 bytes를 통합 사본에서도 대조했다.
[Hosted ORM run 35456370913](https://github.com/progresshans/godj/actions/runs/35456370913)은 attempt 1에서 완료했다.
48개 unique job의 run ID·head SHA·terminal 상태를 실제 API 목록과 대조했으며 **44 success, 4 expected scope skip**이다.
최종 `CI result (orm)` job `105935247654`의 실제 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10의 normal/race/CGO0·core/operator-target 여섯 조합, 선택한 Linux/macOS의 관계·명령·portable 검증을 완료했다.
제외 항목은 Python compatibility, exact Darwin profile, product project check, reference/current capture다.
전용 `godj_0084_normal` DB는 로컬 검증 후 제거했다. 이후 문서 기록 commit은 실행 source로 표시하지 않는다.
새 full/platform·Windows runtime·배포와 전체 프레임워크 완성은
이 로컬 기록의 범위가 아니다. 새 self/cyclic 선언의 자동 `makemigrations` 계획은 후속 요구다.

## GDJ-0083 — Nested eager graph와 하위 cache

- 작업: [GDJ-0083](../../work/0083-nested-forward-eager-graphs.md), 의미: [ADR-0029](../adr/0029-one-hop-forward-select-related.md#상태와-범위).
- Baseline은 GDJ-0082 통합 source `3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0`다.
  별도 `feature/nested-forward-eager`의 구현 commit은 `401d3e9fe5b1c07030d3633178b63f2f1041b305`이며,
  기존 Draft PR의 제품 통합 source는 `a49b592be1896d02d73a9657fe360623ffa296d8`다. 통합 뒤 89개 Go 검증 파일과
  CI receipt 두 파일의 hash가 검증 사본과 일치함을 확인했다. 충돌은 CURRENT·active work의 이전 진행 설명 두 곳뿐이었다.
- Python 3.14.7, Django 6.1, asgiref 3.12.1, sqlparse 0.5.5의 fresh process로
  [runner](../../conformance/runners/django/nested_eager_reference.py)의 **440개 관찰**을 수집했다.
  [fixture](../../orm/testdata/nested-eager-django61.json) SHA256:
  `eb638880bfaeffd4c51d1e2a57bb0270e12d5d57e2068efa648f26745b96a782`.
- `uv run --no-project --isolated --python 3.14.7 --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
  python -m unittest conformance.runners.django.tests.test_nested_eager_reference`: 최종 재생 **1 test PASS, skip 0**.
  이름 440개·selection 집합 8개, 모든 selected prefix의 전체 scalar 값, cold/warm Count/First/All·warm SQL 0을 대조한다.
  독립 reference는 Django SQLite이며 Django PostgreSQL 실행 증거로 확대하지 않는다.

### 로컬 실행과 source

Go 1.26.5 darwin/arm64, 실제 SQLite와 전용 PostgreSQL 17.5(Homebrew)에서 같은 43 package 목록을 실행했다.
패키지 이름 목록, 전체 JSON의 시작·종료·실패·skip과 필수 case를 대조했다. Test completion event 수는 제품 기능 수가 아니다.

| 범위 | 결과 |
|---|---|
| affected normal | **28 test packages, 6,032 PASS events**, 15 no-test packages |
| affected CGO_ENABLED=0 | **28 test packages, 6,032 PASS events**, normal과 같은 test roster |
| affected race | **28 test packages, 5,982 PASS events**, 나머지 roster는 normal과 동일 |
| 전체 compile-only | **134 packages PASS** (`go test -exec /usr/bin/true ./...`); 전체 runtime 실행을 뜻하지 않음 |
| affected vet / generated drift | PASS / Helpdesk·Article·relationfixture 세 프로젝트 PASS |
| Python CI 도구 | **37 tests PASS, skip 0**; 새 필수 receipt 7개를 실제 normal 완료 event와 대조 |
| 문서·format·diff | 로컬 링크 114개 문서 검사, 변경 Go gofmt drift 없음, `git diff --check` PASS |

세 Go lane은 **89개 변경 제품·검증 파일의 동일 SHA256 manifest**로 묶었다. Manifest SHA256:
`0d1406ef181fe092a655b821369eb783c83b3df06a3e3b9ecddd8ff01a0c700a`.
이후 CI 필수 receipt 등록 두 파일은 별도로 검증했다. 현재 non-document 변경은 이 89개와 CI 두 파일뿐이며 각각 현재 bytes를 대조했다.
Race에서 제외된 50 events는 기존 `internal/compiletest`의 `!race` 7개 top-level test와 하위 case로, normal·CGO0가 실행한다.
세 lane의 유일한 직접 skip은 `TestPostgresRevisionFenceHelperProcess`다. 실제 자식 process는 해당 parent integration tests가 실행하며
이 helper skip 자체를 기능 PASS로 세지 않는다. 생성 소비자의 child race 모드·전체 종료·필수 흐름·출력 잘림도 기존 strict harness가 검사한다.
전용 `godj_0083_normal`·`godj_0083_cgo0` DB는 검증 후 제거했다.

### 기능과 실패 검증

- 양 DB에서 440개 관찰의 전체 source·target 값, Count/First/All, 실제 조회 수와 LEFT JOIN 수를 비교했다.
  별도 generated SQLite module은 같은 440개를 facade typed/dynamic와 object-builder typed/dynamic 네 경로로 실행하고
  전체 하위 graph 접근의 추가 SQL 0·warm 반복·취소를 검사한다. Filter 입력의 별도 typed parity는 기존 GDJ-0082 회귀가 소유한다.
- 공통 prefix의 병합·부모 우선 정렬·불변 복사·유한 self-cycle occurrence를 확인했다. Raw AST source/filter metadata 충돌
  **12개씩**을 양 DB에서 일반·LIMIT 0 입력으로 pre-I/O 거부한다. SQLite의 63 selected JOIN은 실제 scan하고 64 JOIN은
  일반·빈 조회에서 SQL 전에 거부한다. Typed tree의 깊이 64/65와 입력 node 1024/1025 경계도 검사한다.
- 실제 generated scanner와 DB Rows에 scan/iteration/close 오류·취소·child PK 불일치·필수 child absence·partial child·
  없는 ancestor 아래 present/partial child를 주입한다. All/First가 부분 결과를 반환하지 않고 rowset을 한 번 닫으며,
  같은 query의 재시도와 이후 warm graph 접근이 성공함을 확인했다.
- 서로 다른 행·반환·동시 caller의 전체 graph 복제, 12개 동시 All의 SQL 1회, snapshot/Fresh/파생 query,
  선택하지 않은 하위 관계의 lazy cache·backend affinity, NULL/FK assignment와 바뀌지 않은 형제 cache를 확인했다.
  FK 직접 변경과 `With...ID` 모두에서 아직 접근하지 않은 형제 선택도 보존한다.
- `SelectedGraph`의 no-I/O·context·Fresh·absent 의미, `FromSelected`의 복사 객체·foreign binding 거부,
  다른 facade origin의 root/child selector 거부, 잘못된 중간 Go type의 compile 실패와 최초 configuration cause 보존을 검사했다.
  기존 stale generated handle·forged selector 음성 검증도 새 공통 factory selector 경로에 맞춰 유지했다.

초기 checkpoint는 통과로 세지 않았다. Selection-only 필수 child가 proven-present parent 아래에서 불필요하게 LEFT JOIN을 유지하던
경로를 수정했고, filter OR 경로의 기존 optional ancestry는 보존했다. 생성물·golden·폐쇄 selector 음성 fixture와 저장용 모델의
PK-presence를 잃던 테스트도 정리했다. 추가 검토에서 찾은 미접근 형제 cache 유실은 수정 전 생성 코드에서 두 assignment 방식의
실패를 재현한 뒤, 반환 전 하위 facade cache 준비로 해결했다. 정상 결과만으로 완료 처리하지 않고 최종 source의 세 lane을 대조했다.

제품 통합 source `a49b592be1896d02d73a9657fe360623ffa296d8`의
[Hosted ORM run 35425015186](https://github.com/progresshans/godj/actions/runs/35425015186)은 attempt 1에서 완료했다.
48개 unique job의 run ID·head SHA·terminal 상태와 예정된 제외 항목을 대조했으며 **44 success, 4 expected scope skip**이다.
최종 `CI result (orm)` job `105851855780`의 실제 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10의 normal/race/CGO0와 두 shard, 선택한 Linux/macOS의 portable·relation·command product를 검증했다.
제외된 범위는 exact Darwin profile, Python compatibility, product project check, reference/current capture다.
이후 문서 기록 commit을 실행 source로 표시하지 않는다.
새 full/platform·Windows runtime·배포 검증과 전체 프레임워크 완성은 이 기록에 포함하지 않는다.
Reverse/ManyToMany eager·무인자 자동 선택·일반 self/cyclic migration과 임의 cycle identity 공유는 후속 요구다.
Query 소비자는 명시적 FK-enforced DDL 뒤 generated Create/Save를 사용하며 cyclic migration 지원 증거로 확대하지 않는다.

## GDJ-0082 — Nested forward 경로의 독립 관찰

- 작업: [GDJ-0082](../../work/0082-nested-forward-relation-paths.md), 의미: [ADR-0023](../adr/0023-symbolic-relation-binding-and-shared-relation-ast.md#상태와-범위).
- 제품 baseline `7e5a933db69435154842162287ef86ef7172bc17`의 별도 작업 사본이다. Markdown을 제외한 변경·새 파일 91개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은
  `6c6b09512a07bb5506c199d39ff7633ef61f297816b6a3502de5bd7db1e57f25`다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)의 전용 DB·개별 schema를 사용했다. 완료 뒤 이번 작업이 만든 두 전용 DB를 제거했다.

### 독립 reference

- Python 3.14.7, Django 6.1, asgiref 3.12.1, sqlparse 0.5.5의 fresh process에서
  [runner](../../conformance/runners/django/nested_forward_reference.py)를 실행해 **146개 관찰**을 수집했다.
  [fixture](../../orm/testdata/nested-forward-django61.json)의 SHA256은 `2a7164ce3079f51227fbd60b134d2ba034ef42a9fb591b698ad45ce68741b53a`다.
- `uv run --no-project --isolated --python 3.14.7 --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
  python -m unittest conformance.runners.django.tests.test_nested_forward_reference`는 **1 test PASS, skip 0**다.
  전체 JSON 일치, unique case 이름, count/ids·cold/warm First·warm cache의 추가 SQL 없음과 실행 수를 확인했다.
- 동일 Django 설치의 `django.db.models.sql.query`에서 `Query.setup_joins`(1890–2005), `trim_joins`(2007–2037),
  `build_filter`(1487–1657), `JoinPromoter.update_join_types`(2842–2897) source를 확보했다(BSD-3-Clause).

이 결과는 Django SQLite 관찰이다. 아래 PostgreSQL 증거는 GoDj를 실제 DB에서 실행한 결과이며 Django PostgreSQL 관찰로 세지 않는다.

### 구현과 실제 검증

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=10m`과 같은 패키지의 `CGO_ENABLED=0` 실행을 완료했다.
범위는 GDJ-0081과 같은 43 packages다. `query`, `orm`, `db/...`, `codegen`·외부 생성 소비자, compile-only typed 소비자,
관련 conformance/relationfixture·query·object·select·reverse·prefetch·delete·product와 examples를 포함한다.

- Normal·CGO0 각각 **28 test packages, 5,118 test 완료 event PASS**, no-test package 15개다. 시작/종료와 package
  terminal을 대조했다. 유일한 testcase skip은 부모가 별도 프로세스로 실행하는 `TestPostgresRevisionFenceHelperProcess`다.
- Race도 **28 test packages, 5,068 test 완료 event PASS**다. Normal/CGO0가 실행한 `internal/compiletest`의
  `!race` 최상위 7개·하위 포함 50개 event를 제외한 test roster가 일치한다. 세 lane의 제품 source manifest가 같다.
  이 source의 Hosted ORM 완료는 아래 통합 checkpoint에 별도로 기록한다. 전체 platform PASS로 확대하지 않는다.
- 양 DB에서 **146개 독립 관찰**의 All·First·Count·selected target field 값·SELECT 수·LEFT JOIN 수를 대조했다.
  Nullable ancestor 아래 required tail, self-cycle·공유 prefix·서로 다른 route, DateTime/정수/문자열/Boolean·IN·AND/OR/NOT,
  reverse 중복·Distinct·slice·direct eager 조합을 포함한다.
- 세 app의 외부 생성 모듈에서 generated Create/Save·typed/dynamic query·object builder·facade와 실제 SQLite를 실행했다.
  Dynamic/facade는 146개, typed는 명시적 NULL list member를 표현하지 않는 8개를 제외한 **138개**다.
  Selected 73개에서는 object builder도 대조하며, typed가 가능한 69개는 같은 AST를 비교한다.
  Cold/warm terminals·관계 접근의 추가 SQL 없음, 취소, cache 복제·Fresh·파생 query의 새 평가를 검사했다.
- Generated consumer의 physical fixture에는 실제 FK 제약이 있다. 기존 migration lifecycle은 self-reference CreateModel을
  거부하므로 이 fixture는 명시적 DDL로 준비했다. 이번 결과를 self/cyclic migration 지원·검증으로 확대하지 않는다.
- 두 DB에서 route 사이 FK column/nullability/target PK/table/root identity 충돌과 각 LIMIT 0 변형 **10개 plan**을 I/O 전에 거부했다.
  SQLite는 63 JOIN과 64-hop source-key trim을 실제 실행하고, 64 JOIN 초과와 LIMIT 0 초과도 I/O 전에 거부했다.
- Foreign snapshot composition, 잘못된 intermediate Go type, zero/과도한 깊이, policy 복제·오류 우선순위와 부분 batch 거부,
  초기/지연 generated group binding 실패의 원래 오류, private metadata 위조, concurrent compiler의 SQL 인자 분리를 검사했다.
- 최종 source의 전체 134 package compile-only, affected vet, 세 프로젝트 generated drift, format·113개 문서 local links·diff 검사 PASS.

### 첫 실행에서 수정한 사항

OR의 공통 nullable parent가 INNER로 바뀔 때 아래 다른 분기의 required JOIN까지 잘못 INNER로 바꾸던 계획을 수정했다.
선언상 optional ancestry와 최종 JOIN 종류를 별도로 보존하며 독립 SQL 관찰을 다시 통과했다.
Raw 관찰 helper의 빈 projection 처리, PostgreSQL fixture 관리 connection의 UTC timezone, 생성 소비자의 명시적 정렬과
structured error 기대값을 바로잡았다. 없어진 per-edge query 타입과 충돌하던 옛 schema는 이제 허용하는 양성 검증으로
전환했고, 실제 generic member/type namespace 충돌과 원자적 실패 검증은 유지한다.

### 통합 checkpoint

Feature `3cfecb9`를 기존 Draft PR #1에 통합한 source는 `3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0`다.
제품 91개 파일이 로컬 검증 manifest와 일치함을 확인했다. 두 문서 충돌은 이미 반영한 GDJ-0081 Hosted 완료와
새 구현 상태를 유지하여 해결했다. [Hosted ORM run 35421304637](https://github.com/progresshans/godj/actions/runs/35421304637)의
48개 unique job의 `head_sha`·run ID·attempt 1·terminal 상태를 대조했다. **44 success, 4 expected scope skip**으로 완료했으며
최종 `CI result (orm)` job `105841846273`의 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10 여섯 mode/shard와 선택된 Linux/macOS를 검증했다. 제외 항목은 product project check,
exact Darwin profile, Python compatibility, reference/current capture다. 최신 full은 별도 source `8fd8936d`의
run `35384697050`이며, 이 source의 full·Windows runtime·배포 결과로 사용하지 않는다.

## GDJ-0081 — 여러 direct forward target 동시 선택

- 작업: [GDJ-0081](../../work/0081-multiple-forward-eager-selections.md), 의미: [ADR-0029](../adr/0029-one-hop-forward-select-related.md#상태와-범위).
- Source: `1c71610ec705c29fae85417be4800068473003bd` 기반 feature 작업 사본. Markdown을 제외한 변경·새 파일·삭제 97개의
  `<sha256 또는 deleted>  <relative-path>\n` 정렬 manifest SHA256은
  `d7384cf7b3b3a660d93aa52ca6ed656ac432a8ec3439d9d2839ee3c1b561034f`다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)의 전용 DB·개별 schema에서 실행했다.
  최종 lane 종료 후 이번 작업이 만든 두 전용 DB를 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=10m`의 영향 범위는 다음과 같다.
같은 패키지 목록을 `-race`, `CGO_ENABLED=0`에도 사용한다.

```text
./query ./orm ./db/... ./codegen ./codegen/consumertest ./internal/compiletest
./conformance/nullableforwardproduct ./conformance/relationfixture/...
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationqueryproduct ./conformance/relationreverseproduct
./conformance/relationprefetchproduct ./conformance/relationdeleteproduct
./conformance/relationproduct ./examples/...
```

Normal·CGO0 각각 **28 packages, 4,792 test 완료 event PASS**, race는 **28 packages, 4,742 event PASS**다.
각 lane의 no-test package는 15개다. `internal/compiletest`의 `!race` build tag로 제외하는 7개 compile-only 최상위 검사와
그 하위 event 50개는 normal·CGO0에서 완료했다. Race에서 이 50개를 실행한 것으로 세지 않았으며 나머지 test roster가 일치함을 확인했다.
시작/종료 event와 package 완료·필수 sentinel을 대조했다. 직접 진입 skip 한 개는 부모가 자식 프로세스로 실행하는
`TestPostgresRevisionFenceHelperProcess`이며 기능 PASS로 세지 않는다. 최초 시도는 테스트 PostgreSQL URL의 hostname 누락으로
실패했다. 이어 기존 dynamic 전용 zero 상태의 오류 기대값과 단일 selector private field를 주입하던 fixture를 현재 공통
runtime/복수 selection 구조에 맞게 고쳤다. Resolver를 바꿔 잘못된 생성물을 만드는 음성 검증도 현재 생성 위치로 갱신했고,
원래의 오류 cause·pre-I/O 거부를 계속 검사한다. 위 완료 결과는 이 fixture 보완을 포함한다.

### 확인한 의미

- FK 이름으로 정렬한 immutable projection 집합과 하나의 `ForwardSelectQuery[S]` runtime을 사용한다. Source와 모든 target을
  한 번의 Rows.Scan으로 읽으며 구체 target Go type은 닫힌 adapter에 남는다. 입력 순서·같은 선택의 반복이 결과를 바꾸지 않는다.
- 고정 Django 6.1의 **220개 독립 관찰**을 실제 SQLite·PostgreSQL의 rows·First·Count·SELECT 수·LEFT JOIN 수와 비교했다.
  같은 Person을 가리키는 두 FK와 별도 Team FK, nullable target, reverse 중복·Distinct·Offset·Limit·빈 결과를 포함한다.
- 별도 생성 모듈의 migration·SQLite·typed/dynamic object builder·model별 variadic facade를 실행했다.
  Dynamic/facade는 220개, typed는 explicit NULL-only IN 입력을 제외한 215개다. 선택 후 Filter·OrderBy·Distinct·Offset·Limit과
  Fresh가 전체 선택을 보존하며 cold/warm terminal과 각 relation accessor의 추가 I/O 없음도 검사했다.
- 같은 PK의 서로 다른 FK, reverse JOIN으로 중복된 source와 query cache는 각각 독립 소유한다. 단일→복수 builder 파생과 caller의
  selector slice 변경이 이전 query를 바꾸지 않는다. Concurrent All의 단일 평가와 모든 target cache의 독립 복제를 검사한다.
- 두 번째 target의 잘못된 key·부분 NULL·부재, scan/rows/close/취소 실패는 All·First에서 부분 결과/cache를 게시하지 않으며
  다음 All이 재시도한다. Nil scanner·잘못된 destination 수·typed nil destination, 다른 binding·충돌한 중복 selection도 거부한다.
- 양 DB에서 selected/filter/source-key provenance 충돌과 존재하지 않는 두 번째 selected source key **30개 plan**을 SQL 전에 거부한다.
  LIMIT 0도 이 검증을 생략하지 않는다. 기존 GDJ-0080의 정상 self-reference와 실패 경로도 같은 실행에서 유지한다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 fresh Django runner를 다시 실행해 각각 **1 test PASS, skip 0**을 확인했다.
  별도 invalid-selector 관찰 8개에서 Django Count는 selected 이름을 무시하지만 GoDj는 Count에도 기존 binding/configuration 검증을
  적용한다. 이를 동등성 PASS로 세지 않는다. Django oracle은 SQLite profile이고 PostgreSQL은 위 GoDj 실제 DB 결과다.
- 전체 134 package compile, affected vet, 세 프로젝트 generated drift, CI script unittest 37개, format·docs·diff 검사 PASS.

### 통합 checkpoint

Feature `876595d7dd0a0425cf9a062e7f332f38e63f3e3e`를 통합한 source `7e5a933db69435154842162287ef86ef7172bc17`의
[Hosted ORM run 35417711000](https://github.com/progresshans/godj/actions/runs/35417711000) attempt 1이 **44 success·4 expected scope skip**으로 완료했다.
48개의 고유 job ID·run ID·head SHA·종료 상태를 대조했으며 최종 job `105831664789`의 보고서는
`scope=orm`, `full_platform_verified=false`, command/portable/postgresql/relation owner 검증 완료다.
PostgreSQL 17.10 여섯 mode/shard와 선택된 Linux/macOS 환경을 포함한다. Product project-check, Python compatibility,
exact Darwin profile, reference/current-capture owner는 이 scope에서 제외했다. 통합한 97개 source manifest는 로컬 검증과 같았다.
이전 GDJ-0080 source `7397a73b933eef4d30c5a8fa12c84a79fc7945e9`의 완료 결과를 이 변경의 PASS로 재사용하지 않는다.
전체 플랫폼·Windows runtime·배포 검증은 이 로컬 결과에 포함하지 않는다.

## GDJ-0080 — Eager materialization과 filter JOIN 조합

- 작업: [GDJ-0080](../../work/0080-eager-filter-join-composition.md), 의미: [ADR-0029](../adr/0029-one-hop-forward-select-related.md#상태와-범위), [빈 조회 경계](../adr/0062-scalar-membership-and-empty-query-execution.md).
- Source: `408c4179d52c5ea8925f503a000ef69d9f892289` 기반 작업 사본. Markdown을 제외한 변경·새 파일 21개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `2e21f11bd6e759910e172ae4bf1c1471e0f51f8766a431913ec5a1b21c4ea93b`다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)의 전용 DB·개별 schema에서 실행했다.
  최종 lane 종료 뒤 이번 작업의 전용 PostgreSQL DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`과 같은 범위의 `-race`, `CGO_ENABLED=0` 실행을 완료했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./conformance/nullableforwardproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationqueryproduct ./conformance/relationreverseproduct ./examples/...
```

Normal·race·CGO0 각각 **22 packages, 3,828 test 완료 event PASS**, no-test package 11개다.
모든 test의 시작/종료·package 완료와 필수 sentinel을 대조했다. 각 lane의 직접 진입 skip은 부모가 자식 프로세스로
실행하는 `TestPostgresRevisionFenceHelperProcess` 한 개이며 이를 기능 PASS로 세지 않았다.
초기 실행은 LIMIT 0의 불필요한 SQL과 새 무결성 테스트의 오류 code 기대값을 보정하기 전 실패했다.
별도 생성 모듈의 컴파일·실행으로 초기 소비자 실패 원인도 확인했다. 최종 검토에서는 같은 root FK의 forward/reverse view가
서로 다른 target을 선언해도 compile되던 self-reference 경계를 재현하고 보완했다. 위 결과는 이 보완까지 포함한 전체 영향 범위를
normal·race·CGO0로 다시 실행한 결과다.

### 확인한 의미

- 하나의 required/nullable selected edge와 다른 forward/reverse filter JOIN을 함께 compile·materialize한다.
  선택한 alias의 target columns만 root columns 뒤에 붙이며 reverse filter의 중복 행을 유지한다.
- 고정 Django 6.1의 **88개 독립 관찰**에서 양 DB의 All·First·Count, Distinct·Offset·Limit, nullable target와
  실제 SELECT 수·LEFT JOIN 수를 비교했다. PostgreSQL tracer는 production physical-session 검증을 유지했다.
  알려진 빈 결과는 PostgreSQL 전체 connection query 수와 SQLite query counter가 늘지 않음을 확인했다.
- Generated model·migration·typed/dynamic selector·facade를 별도 module의 실제 SQLite에서 실행했다.
  Dynamic/facade는 88개, typed는 explicit NULL-only member의 두 경우를 제외한 86개를 검사했다.
  Cold/warm All·First·Count, selected relation 접근의 추가 I/O 없음, 취소 우선순위와 typed/dynamic AST 일치를 대조했다.
- 중복 source object·FK pointer·selected target cache의 수정이 다른 반환 객체나 query cache에 전파되지 않음을 검사했다.
  여러 JOIN의 scan/Rows.Err/Rows.Close/취소/행 무결성 실패 후 부분 cache를 게시하지 않고 retry가 성공함을 확인했다.
- 다른 root identity, 같은 FK의 conflicting target/table/PK, nonselected FK·source-key proof 충돌을 포함한 **28개 잘못된 plan**을
  양 DB에서 pre-I/O 거부했다. LIMIT 0으로 감싸도 검증을 생략하지 않는다. 정상 self-reference는 동일 FK의 forward/reverse view로
처리하며 실제 양 DB에서 NULL parent와 child/grandchild가 만든 중복 `[1, 2, 2]`를 보존했다. 공유 plan의 concurrent compile과 detached arguments도 확인했다.
- `LIMIT 0`을 공통 empty-source 분석에 포함했다. Backend/session은 전체 compile·context/lifetime 검증을 먼저 실행하고
  model은 빈 결과, COUNT는 0을 반환한다. 조기 반환으로 metadata/capability 검사를 건너뛰지 않는다.
- 기존 generated Count 22개·nullable-forward 73개·scalar-lookup 748개 관찰에서도 서로 다른 eager/filter edge의 All을
  미지원 기대값 대신 실제 rows와 warmed relation/cache 결과로 검증했다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 독립 eager/filter runner를 다시 실행해 각각 **1 test PASS, skip 0**을 확인했다.
  Django reference는 SQLite profile이며 PostgreSQL 결과는 위 GoDj actual DB 검증이 소유한다.
- 전체 compile, affected vet, 최종 generated drift, CI script unittest 37개, format·docs·diff 검사 PASS.

### 통합 checkpoint

초기 통합 source `629f0a0deaf9d8c2beed157ef9b51fcd464fcb6d`의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35413019895)을
attempt 1은 48개 job의 source·run ID·완료 상태를 대조해 **44 success, 4 scope skip**으로 완료했다.
최종 `CI result (orm)` report는 `scope=orm`, `full_platform_verified=false`이며 command/portable/postgresql/relation owner를 검증했다.
PostgreSQL 17.10의 core/operator-target × normal/race/CGO0 여섯 조합과 선택된 Linux/macOS를 포함한다.
그 뒤 위 self-reference 보완이 추가됐으므로 이 실행을 보완 후 source의 결과로 재사용하지 않는다.
최종 보완 source `7397a73b933eef4d30c5a8fa12c84a79fc7945e9`의 [Hosted ORM run 35414363995](https://github.com/progresshans/godj/actions/runs/35414363995),
attempt 1도 **44 success, 4 scope skip**으로 완료했다. 48개 job의 유일한 ID·run ID·head SHA·terminal 상태를 대조했다.
`CI result (orm)` job `105822544681`의 report는 `scope=orm`, `full_platform_verified=false`와
command/portable/postgresql/relation owner 완료를 기록한다. 실제 PostgreSQL 17.10의 여섯 mode/shard 조합과
선택된 Linux amd64/arm64·macOS arm64/amd64의 관계·command 검증을 포함한다.
네 skip은 project-check matrix·Python compatibility·exact Darwin profile·reference/current-capture job이며 해당 범위를 PASS로 계산하지 않는다.
여러 selected projection·nested traversal·reverse OR/NOT·새 full/platform·배포·전체 ORM 완료는 이 결과에 포함하지 않는다.

## GDJ-0079 — Direct forward scalar lookup

- 작업: [GDJ-0079](../../work/0079-forward-scalar-lookups.md), 의미: [ADR-0040 추가 결정](../adr/0040-composable-typed-boolean-predicates-and-article-search.md#직접-forward-대상의-scalar-lookup), [IN 의미](../adr/0062-scalar-membership-and-empty-query-execution.md).
- Source: `c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b` 기반 작업 사본. Markdown을 제외한 변경·새 파일 33개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `308bd105930855cca56e99da76444c2c542588111f8ce53206365e6899e667a0`이다.
  독립 reference·generated consumer 입력·CI 필수 완료 sentinel·최종 테스트 보정을 포함한다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5 (Homebrew)의 전용 DB/개별 schema에서 실행했다.
  최종 lane 종료 뒤 이 작업에서 만든 전용 PostgreSQL DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`으로 다음 영향 범위를 실행했다.

```text
./query ./orm ./db/... ./codegen/... ./internal/compiletest
./conformance/nullableforwardproduct ./conformance/relationqueryproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationreverseproduct ./conformance/relationprefetchproduct
./conformance/relationdeleteproduct ./examples/... ./internal/projectgenerate/...
```

최종 normal은 **29 packages, 4,199 test 완료 event PASS**, no-test package 12개다.
처음 실행 뒤 남은 두 package `codegen/consumertest`, `internal/compiletest` 전체를 보정 후 다시 실행했다.
최초 normal source와 최종 source의 차이는 이 두 package의 `_test.go` 세 파일뿐임을 hash로 대조했다.
그 외 제품·테스트·fixture bytes는 그대로다. Package별 마지막 완전한 실행을 사용하며 실패 event를 PASS에 합치지 않는다.

다음 범위는 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.
CGO0는 public generic API의 외부 compile 검사를 위해 `./internal/compiletest`도 포함했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./examples/...
./conformance/relationselectproduct ./conformance/relationqueryproduct
./conformance/relationobjectproduct ./conformance/relationreverseproduct
./conformance/relationprefetchproduct ./conformance/relationdeleteproduct
```

Race는 **24 packages, 3,614 test 완료 event PASS**, CGO0는 **25 packages, 3,668 test 완료 event PASS**다.
각 no-test package는 10개다. 모든 test의 시작/종료와 필수 consumer, 양 DB의 748개 하위 case를 대조했다.
Normal의 직접 진입 skip은 PostgreSQL revision-fence·generated publication crash helper 두 개, race/CGO0는 PostgreSQL helper 한 개다.
부모의 process 실행과 구분하며 이 skip을 기능 PASS로 계산하지 않는다.

### 확인한 의미와 수정

- Forward target의 Integer·Char/Text·DateTime nullable/non-null과 Boolean을 typed/generation에 연결했다.
  Scalar kind별 exact·비교·icontains·isnull·IN과 명시적 dynamic suffix, policy-before-value와 원자적 실패를 검사했다.
- 같은 immutable AST에서 declared field nullability와 optional forward path를 함께 계산한다. Target isnull의 JOIN promotion과
  explicit NULL member의 홀수 부정, 빈 목록의 SQL 생략을 확인했다. 원래 field metadata·입력 목록·accessor 반환 목록은 공유하지 않는다.
- 고정 Django 6.1의 **748개 독립 관찰**에서 행 ID·Count·실제 SELECT 수와 named-field JOIN 종류를 양 DB에서 대조했다.
  PostgreSQL은 production connection profile/physical guards를 유지한 tracer로 조회 수를 측정했다. Known-empty case는
  전체 connection query 수도 늘지 않음을 확인했다. SQLite도 실제 backend query counter를 비교했다.
- 별도 module의 generated model·migration·relation adapter·facade가 748개 dynamic 결과와 **typed로 표현하는 628개의 AST 일치**를 검사했다.
  나머지 120개는 explicit NULL member가 있는 dynamic 입력이며 typed parity로 세지 않는다. Cold/warm Count·eager cache·nullable target scan과
  invalid/canceled empty query의 pre-I/O 거부를 확인했다. 다른 eager/filter edge의 All은 기존 미지원 오류를 계속 검사한다.
- 실제 Helpdesk 양 DB 재연결 뒤 category 이름의 IContains+IN, dynamic suffix와 eager Count/All을 연결했다.
  서술형 검색 예시는 README에 추가했다. 일반 HTTP 검색 규칙이나 인가 정책을 바꾸지 않았다.
- 생성 후보 compile에서 공유 terminal 필터가 reverse의 nullable/Boolean까지 넓어지는 문제를 발견했다. Reverse 생성 범위를 명시하고
  세 프로젝트의 후보 compile·생성을 다시 완료했다. Reverse non-exact/OR/NOT은 계속 미지원이다.
- 새로운 sealed ReferenceField signature에도 과거 진단 문자열을 요구하던 negative compile 기대값과 nullable target 미지원 기대값을 갱신했다.
  실제 다른 model field의 사용은 계속 compile에 실패하며 타입 구분을 검사한다.
- 748개 child test의 JSON이 compiler 진단용 128 KiB 캡처를 초과했다. Test 결과에는 별도의 bounded 4 MiB 캡처를 사용하고
  초과분 drain·총량 대조·truncation/진단/skip/missing completion 거부를 유지했다. 잘린 첫 결과를 PASS로 세지 않고 package 전체를 다시 실행했다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 forward-lookup·nullable-forward·eager-count reference 각각을 다시 관찰했다.
  각 버전 **3 tests PASS, skip 0**다. 이 reference profile은 SQLite이며 PostgreSQL 비교는 위 실제 GoDj 실행이 소유한다.
- 전체 compile·vet, 최종 보정 package의 vet, generated drift, CI script unittest, docs·format·diff 검사 PASS.

### 통합 검증 소유자

이 작업의 구현과 필수 로컬 영향 범위를 완료했다. OS/process 구현이나 새 backend를 추가하지 않았다.
통합 Hosted ORM은 GDJ-0080과 함께 source `7397a73b933eef4d30c5a8fa12c84a79fc7945e9`에서 완료했다.
위 GDJ-0080 checkpoint가 source·scope와 terminal 근거를 소유한다. 위 baseline의 GDJ-0078 Hosted는
이 새 lookup source의 실행 결과가 아니다. 새 full·Windows runtime·배포·전체 ORM 완료를 주장하지 않는다.

## GDJ-0078 — Nullable forward 대상 필터와 Boolean JOIN

- 작업: [GDJ-0078](../../work/0078-nullable-forward-relation-predicates.md), 의미: [ADR-0040 추가 결정](../adr/0040-composable-typed-boolean-predicates-and-article-search.md).
- Source: `6eb40412501b8945dad06a75f95e3b39510e2fe0` 기반 작업 사본. Markdown을 제외한 변경·새 파일 36개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `289ba29a040db517ae8675c28163911dd6edb600902887219a7596473df6befc`다.
  독립 Django runner/JSON, 실제 생성 소비자 입력, 양 DB 실행 fixture와 CI 필수 완료 sentinel을 포함한다.
  검증 후 `ForwardRelation`·`BindForward`의 GoDoc 두 곳만 현재 nullable 지원에 맞춰 고쳤다. 실행 코드는 그대로이며
  통합할 36-file manifest는 `14ad182cc754178cbbbf8151775394c653c9481bfee6da50b5411d985e710342`다.
- Go 1.26.5 darwin/arm64, modernc SQLite, 실제 PostgreSQL 17.5 (Homebrew)의 전용 DB와 테스트별 schema를 사용했다.
  최종 lane이 모두 끝난 뒤 이 작업에서 만든 전용 DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`으로 다음 영향 범위를 실행했다.

```text
./query ./orm ./db/... ./codegen/... ./internal/compiletest
./conformance/nullableforwardproduct ./conformance/relationqueryproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationreverseproduct ./conformance/relationprefetchproduct
./conformance/relationdeleteproduct ./examples/... ./internal/projectgenerate/...
```

최종 normal은 **29 packages, 2,699 test 완료 event PASS**, no-test package 12개다.
다음 범위를 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./examples/...
./conformance/relationselectproduct ./conformance/relationqueryproduct
./conformance/relationobjectproduct ./conformance/relationreverseproduct
./conformance/relationprefetchproduct ./conformance/relationdeleteproduct
```

Race·CGO0는 각각 **24 packages, 2,114 test 완료 event PASS**, no-test package 10개다.
모든 lane의 JSON 전체에서 test 시작/종료를 대조하고 양 DB의 73개 하위 case, generated nullable/eager Count 소비자와
불일치하는 존재 증명의 사전 거부를 필수 완료로 확인했다. Normal의 직접 진입 skip은 PostgreSQL revision-fence·generated publication
crash helper 두 개, race·CGO0는 PostgreSQL helper 한 개다. 부모의 process 실행과 구분하며 이 skip을 기능 PASS로 계산하지 않는다.

### 확인한 경계와 수정

- Required/nullable source FK의 직접 대상 exact를 typed/dynamic 같은 AST와 generated relation adapter에 연결했다.
  기존 non-null Integer·Char/Text·DateTime과 대상 PK, root scalar·source-key isnull의 AND/OR/NOT·중첩 부정을 검증했다.
- 필터가 반드시 요구하는 대상 존재를 공통 planner가 계산한다. Nullable edge의 INNER/LEFT JOIN과 홀수 부정의 joined 대상 column
  IS NOT NULL 보정으로 null source 행을 보존한다. Source-key 존재 증명은 실제 predicate edge와 정확한 hop metadata 일치를 요구한다.
- 고정 Django 6.1/SQLite를 별도 process로 실행해 **73개 독립 관찰**의 행 ID·Count를 실제 SQLite와 PostgreSQL에서 대조했다.
  Named target-field JOIN 종류도 비교했다. Django target-PK JOIN 생략 최적화는 구현·parity 주장 대상이 아니다.
  SQLite의 실제 query 수와 별도 생성 module의 cold/warm Count·eager cache·typed/dynamic AST를 확인했다.
- 이전 GDJ-0077의 Count 관찰 22개 중 미지원이던 nullable target-field case도 이제 실제 generated consumer에서 지원한다.
  현재 source에서는 **22개 전체의 Count를 검증**한다. 서로 다른 eager/filter edge의 All 6개는 계속 명시적 미지원이다.
- 하위 Query AST의 기존 Boolean·nullable target scalar 허용과 SQLite quoted identifier 정책을 보존했다.
  이것이 typed/dynamic nullable target scalar API 확장을 뜻하지 않는다. Reverse OR/NOT·다른 relation lookup·다중/중첩 eager는 미지원이다.
- 초기 runtime 검사에서 남아 있던 nullable 거부 기대값과 sparse generated binding 기대값을 갱신했고, 테스트의 SQLite DateTime 저장 형식을
  production의 고정 UTC microsecond text에 맞췄다. 두 Boolean case에서 source-key isnull의 존재 증명도 JOIN 계획에 반영했다.
  검토 후 compiler validation의 불필요한 AST 축소와 혼합 metadata를 수정하고 위 normal 전체를 최종 source로 다시 실행했다.
  중간 재실행의 PostgreSQL URL 환경 변수 오기는 실패로 남기고 올바른 필수 환경으로 다시 실행했다. 이전 실패를 PASS에 합치지 않았다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 nullable-forward와 eager-count 독립 reference를 각각 **2 tests PASS, skip 0**으로 확인했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...` 및 최종 공통 compiler 수정 후 `go vet ./db/...`, generated drift,
  CI script unittest, docs·format·diff 검사 PASS. Generated/Python/CI 입력은 해당 검사 뒤 변하지 않았다.

### 통합 검증 소유자

로컬 영향 범위와 GDJ-0077 Count를 포함한 Hosted ORM 통합 검증을 완료했다.

- 통합 source `c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b`, [run 35407175164](https://github.com/progresshans/godj/actions/runs/35407175164), attempt 1:
  **completed/success, 고유 job 48개 중 44 success·4 의도한 scope skip**. 모든 job의 source·run identity·terminal 상태를 대조했다.
- 최종 job `105802085743`의 report는 `scope=orm`, `full_platform_verified=false`이며 소유자는
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix`다.
  실제 PostgreSQL 17.10 core/operator-target의 normal·race·CGO0와 선택된 Linux amd64/arm64·macOS arm64/amd64 제품/command를 포함한다.
- 비대상 네 owner는 product project-check matrix, Python compatibility matrix, exact darwin/arm64 reference profile,
  reference/current-capture 통합이다. 새 full·Windows runtime·배포 결과로 표시하지 않는다.
- 이후 GDJ-0079의 새 scalar lookup 구현은 이 source에 포함되지 않는다. 위 PASS를 다음 구현의 결과로 재사용하지 않는다.

GDJ-0076의 과거 Hosted와 Text+DateTime의 과거 full은 각각의 source를 증명한다. 전체 프레임워크의 완료나 출시 결정이 아니다.

## GDJ-0077 — 관계 조회 Count와 캐시 의미

- 작업: [GDJ-0077](../../work/0077-eager-count-and-query-cache-semantics.md), 의미: [ADR-0029 추가 결정](../adr/0029-one-hop-forward-select-related.md).
- Source: `d0f079481d660682c7a18884ce2c305234f6ad38` 기반 작업 사본. Markdown을 제외한 변경·새 파일 24개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `f88b777ce1d44f94620828e5f198d7c82884269e1f73e8da73e985d53f1f80e0`이다.
  별도 generated module의 test 입력, Python runner와 독립 JSON fixture를 포함한다.
- Go 1.26.5 darwin/arm64, modernc SQLite, 실제 PostgreSQL 17.5 (Homebrew)의 전용 DB와 테스트별 schema를 사용했다.
  모든 로컬 lane 뒤 이 작업이 만든 전용 DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`으로 다음 영향 범위를 실행했다.

```text
./query ./orm ./codegen/... ./internal/compiletest ./db/... ./examples/helpdesk
./conformance/relationselectproduct ./conformance/relationproduct ./conformance/relationqueryproduct
./internal/projectgenerate/...
```

최종 normal은 **16 packages, 2,371 test 완료 event PASS**, no-test package 2개다.
최초 실행에서 새 독립 consumer가 기존 generated relation API의 범위를 잘못 사용해 compile에 실패했다.
Reverse adapter를 올바르게 사용하도록 고치고 nullable target-field lookup은 기존 미지원 오류를 검사하도록 수정했다.
해당 `codegen/consumertest` package 전체를 다시 실행했다. 다른 Go 제품·테스트·fixture bytes는 그대로이며,
추가 변경한 Python runner의 version guard는 아래 독립 reference lane에서 검증했다. 실패한 event는 PASS로 계산하지 않았다.

다음 범위를 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./examples/helpdesk ./conformance/relationselectproduct
```

Race·CGO0는 각각 **9 packages, 1,775 test 완료 event PASS**, no-test package 1개다.
모든 test의 start/terminal event를 대조했고 필수 generated consumer·실제 Helpdesk 양 DB·Count 실패/동시성 검사가 완료됐다.
Normal의 직접 진입 skip은 PostgreSQL revision-fence·generated publication crash helper 두 개, race/CGO0는 PostgreSQL helper 한 개다.
부모 process 검증과 구분하며 이 skip을 기능 PASS로 세지 않는다.

### 확인한 경계

- Eager projection만 제거하고 filter·order·Distinct·Offset·Limit을 보존한다. Cold Count는 SQL 집계를 사용하며
  두 번 호출하면 각각 평가한다. Eager All 완료 뒤에는 그 cache의 길이를 재사용하고 원래 QuerySet의 cache는 빌려 쓰지 않는다.
- Context/typed-nil/zero query/binding 오류의 사전 거부, scan/rows/close/backend 실패의 원인 보존·정확한 Close·재시도,
  실행 중인 All과 독립적인 cold Count를 검사했다. Count는 related object를 materialize하거나 그 row 무결성을 검사하지 않는다.
- 별도 module의 실제 generated project에서 typed·dynamic·facade Count, 필수·nullable FK, 빈 IN·slice·Distinct와
  reverse filter의 중복 행을 검증했다. 고정 Django 6.1의 22개 관찰 중 **21개 count·cold SQL 수·JOIN 수를 실제 SQLite에서 대조**했다.
  Nullable target-field filter 1개는 기존 미지원 오류를 확인했고 parity로 세지 않는다. 서로 다른 eager/filter JOIN의 All 6개도 미지원 상태다.
- 실제 SQLite/PostgreSQL Helpdesk에서 기존 DB 재연결 후 eager Count→페이지 All→cache Count와 관계 접근을 확인했다.
  기존 외부 `.go.txt` compile 소비자에도 typed/dynamic/facade Count를 연결했다.
- 독립 reference는 Python 3.12.13·3.13.15·3.14.3·3.14.7에서 각각 **1 test PASS, skip 0**로 다시 관찰했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.

### 검증 소유자

이 변경의 필수 로컬 영향 범위를 완료했다. DB별 SQL 또는 platform/process 구현은 변경하지 않았다.
새 Hosted ORM은 위 GDJ-0078과 묶은 source `c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b`에서 완료했다.
GDJ-0076의 Hosted는 그 기준 source만 증명하며, 이번 통합 ORM을 전체 플랫폼·새 full·배포 결과로 표시하지 않는다.

## GDJ-0076 — 모델 선택값과 metadata-only migration

- 작업: [GDJ-0076](../../work/0076-model-choices-and-metadata-migrations.md), 의미: [ADR-0063](../adr/0063-model-choices-and-metadata-only-migrations.md).
- Source: `adb3ea62f7c8f9a57c623634e2c11b60f04374bc` 기반 작업 사본. Markdown을 제외한 변경·새 파일 144개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `601ac03fd9d9a7742e69194444b628670c0bdf9374308ffd3bd4934ebc237037`이다.
  독립 Django runner/fixture, generated model 입력과 별도 OpenAPI client module을 포함한다.
- 환경: Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5 (Homebrew). 새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했다.
- 로컬 lane 종료 후 이 작업에서 생성한 전용 DB만 제거했다.
- 로컬 제품 bytes를 통합한 source는 `e5688068ea64ac55493ccbbe0d622ccdd084847b`이다. 이후 외부 migration 어댑터의 텍스트 compile fixture 한 파일을 수정한
  `d0f079481d660682c7a18884ce2c305234f6ad38`에서 아래 Hosted ORM 검증을 완료했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`의 영향 범위는 다음과 같다.

```text
./schema/... ./forms/... ./serializers ./api/openapi/... ./admin
./migrations/... ./db/... ./codegen/...
./internal/projectwire ./internal/projectspec ./internal/irresource ./internal/migrationautodetect
./internal/projectgenerate/... ./internal/projectmigration/... ./internal/projectcheck/...
./examples/... ./conformance/choicesproduct ./conformance/migrationrelationproduct ./conformance/runners/godj
```

최종 normal은 **46 packages, 4,355 test 완료 event PASS**, no-test package 14개다.
첫 broad normal 뒤 남은 실패는 새 capability의 기대값과 바뀐 Helpdesk 입력에 대한 parent fixture 기대값이었다.
이를 반영하고 capability 없는 choices 변경의 사전 거부 검사를 추가한 뒤 `migrations`, `migrations/backend`, `db/sqlite`,
`api/openapi/consumertest` 전체를 다시 실행했다. 최초 broad normal과 최종 source의 차이는 해당 네 package의 `_test.go` 네 파일뿐이며,
나머지 package의 제품·테스트·fixture bytes는 그대로임을 hash로 대조했다. 실패한 event를 PASS로 계산하지 않고 package별 최종 실행을 사용했다.

다음 범위를 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.

```text
./schema/... ./forms/... ./serializers ./admin ./api/openapi/...
./migrations/... ./db/... ./codegen/consumertest ./conformance/choicesproduct ./examples/helpdesk
./internal/projectwire ./internal/projectspec ./internal/migrationautodetect
```

Race·CGO0는 각각 **21 packages, 2,334 test 완료 event PASS**, no-test package 2개다.
JSON 전체를 읽어 test 시작/종료·실패·필수 소비자 완료를 확인했다. Normal의 직접 진입 skip은 부모가 process로 실행하는
PostgreSQL revision fence·makemigrations crash·generated publication crash helper 세 개이며, race/CGO0에는 PostgreSQL helper 한 개다.
이 skip을 기능 PASS로 세지 않는다. 필수 choices·actual DB·generated model·OpenAPI client는 skip 없이 완료했다.

### 수정과 검증한 의미

- 초기 실행에서 AlterField가 이전 field slice를 공유해 변경 전 상태까지 바꾸는 문제를 발견했다. 요소를 교체하기 전에 slice를 분리해
  정확한 Before/After와 역방향 복원을 보존했다. PostgreSQL은 같은 step의 relation target 선택값 변경을 허용하면서, 순서별 metadata의
  정확한 일치는 별도로 검사하고 초기 물리 catalog 비교에서만 choices를 제외했다. 위조된 target label은 양 renderer에서 거부한다.
- Project wire scan·크기 계산·resource scan, definition encode/decode·digest·loaded intent, SQLite seal에 choices와 scalar 전체 payload를
  연결했다. 검토 중 확인한 DateTime default의 scan/size/seal 누락도 함께 보완했다. 기존 migration definition golden bytes는 유지했다.
- 고정 Django 6.1/DRF 3.18.0의 독립 19개 입력을 Form/serializer에서 비교했다. [DEV-0012](../DEVIATIONS.md)의 JSON type 차이는
  명시적 assertion으로 검사하며 parity로 세지 않는다. Python model clean 관찰은 GoDj 모델 validation 구현 증거가 아니다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7에서 각각 **1 test PASS, skip 0**로 reference 전체를 다시 관찰했다.
- 별도 module의 실제 generated model은 string·Text·int64 choices의 metadata 소유권, Form Select·공백·null/0, serializer,
  ordinary ORM Create/Update/Save·typed/dynamic query와 목록 밖 저장 값을 확인했다. child test 두 개의 완전한 종료를 요구한다.
- Helpdesk의 이전 0001..0004 파일을 보존하고 실제 makemigrations로 0005 선택값 추가와 0006 label/order 변경을 작성했다.
  양 DB에서 기존 priority 99를 전진/역방향/재적용 동안 보존하고, Admin/API에서 허용값을 검증하며 기존 int64 극값을 조회·표시했다.
- SQLite는 metadata 왕복 뒤 schema_version 불변과 revision의 정확한 증가를, PostgreSQL은 table OID/heap 불변을 검사했다.
  혼합 AlterField/AddField, FK 무결성, 물리 drift 거부와 recorder 보존을 실제 DB에서 확인했다. 순수 SQL projection은 choices에 0개,
  물리 변경과 혼합된 step에는 물리 SQL만 반환한다.
- 고정 ogen을 통해 request의 nullable integer enum을 실제 생성했다. 처음의 anyOf 바깥 enum은 생성기가 보존하지 않아 non-null branch로
  옮겼다. 별도 client의 실제 HTTP는 선택값·null·생략과 잘못된 enum cast의 서버 거부를, 독립 wire 응답은 목록 밖 값과 int64 극값을 검증한다.
  Parent는 DB의 최종 행·관계·정수·Text·시각을 별도로 검사한다. Client의 encoder는 Validate를 자동 호출하지 않는다.
- Admin의 option/value/label escaping, 목록 밖 초기값 보존, 거부 입력의 무변경, 저장·audit의 raw 값 보존을 검사했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.

### Hosted ORM 통합 검증

- 최초 [run 35401098373](https://github.com/progresshans/godj/actions/runs/35401098373), source `e5688068ea64ac55493ccbbe0d622ccdd084847b`는 실패를 발견한 뒤
  수정 source의 실행으로 대체되어 최종 cancelled다. 외부 migration adapter의 `.go.txt` fixture에 새 `AlterField` 메서드가 빠져
  Linux/macOS normal·CGO0 관계 작업 8개와 최종 scope 검사 1개가 실패했다. 기존 전체 compile은 테스트가 동적으로 만드는 이 소비자를 실행하지 않았다.
- 이 fixture에 명시적 미지원 오류를 반환하는 메서드를 추가했다. `go test -json -count=1 -timeout=10m ./internal/compiletest`와
  동일한 CGO0 명령에서 각각 **54 test 완료 PASS, skip 0**을 확인했다. 수정 commit은 이 fixture 한 파일의 3줄 추가뿐이다.
- 수정 source `d0f079481d660682c7a18884ce2c305234f6ad38`, [run 35401719591](https://github.com/progresshans/godj/actions/runs/35401719591), attempt 1:
  **completed/success, 고유 job 48개 중 44 success·4 의도한 skip**. 모든 job의 terminal 상태와 정확한 source를 확인했다.
- 최종 job `105787183637`의 report는 `scope=orm`, `full_platform_verified=false`이며 소유자는
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix`다.
  실제 PostgreSQL 17.10, Linux amd64/arm64 및 macOS arm64/amd64의 선택된 normal·race·CGO0 제품/command 검증을 포함한다.
- 비대상 네 owner는 product project-check matrix, Python compatibility matrix, exact darwin/arm64 reference profile,
  reference/current-capture 통합이다. Windows runtime 또는 새 full 검증으로 표시하지 않는다.

### 통합 검증 소유자

이 작업의 로컬 및 수정 source의 Hosted ORM 검증을 완료했다.
전체 플랫폼·reference·cold-build의 새 full 검증이나 배포 증거는 아니다. Callable/grouped choices, 다른 scalar choice,
Python enum 내부 ABI, general physical AlterField와 전체 모델 validation은 이 작업으로 완료되지 않는다.

## GDJ-0075 — Scalar IN과 빈 조회의 실행

- 작업: [GDJ-0075](../../work/0075-scalar-membership-and-empty-query-semantics.md), 의미: [ADR-0062](../adr/0062-scalar-membership-and-empty-query-execution.md).
- Source: `8fd8936d634b5038a534936c15a2b1cfac4b853b` 기반 작업 사본. Markdown을 제외한 변경·새 파일 29개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `e3394ceec710e1e56f6271519673e2028a2121db48941ac9e22351b832450863`이다.
  실제 generated consumer 입력·Python runner·독립 JSON fixture를 포함하고 모든 checkpoint 동안 이 bytes를 유지했다.
- 환경: Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5 (Homebrew). 새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했다.
  로컬 lane 종료 뒤 이 전용 DB만 제거했다. 기존 개발 DB와 checked-in generated Go·migration은 변경하지 않았다.

### 로컬 실행과 실패 수정

`GODJ_REQUIRE_POSTGRES=1`로 실제 PostgreSQL을 필수화하고 `go test -json -count=1 -timeout=15m`을 실행했다.

```text
./query ./orm ./db/... ./codegen/...
./conformance/relationproduct ./conformance/relationqueryproduct ./conformance/relationprefetchproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct ./conformance/relationreverseproduct
./conformance/relationdeleteproduct ./conformance/postgresproduct ./examples/...
```

첫 checkpoint는 SQLite 일반 Atomic 경로에서 빈 조회가 실제 SELECT를 실행하는 누락을 발견했다. 해당 실행 경계에도 전체 compile 뒤
empty rows 처리를 연결했다. 빈 조건과 미지원 relation의 결합은 compiler보다 앞선 AST 구성에서 이미 오류이므로 새 회귀의 기대 시점을
그 계약에 맞췄다. 추가 검토에서 synthetic cursor를 원래 transaction lifetime에 묶어 detached Query context가 취소를 우회하거나
transaction 종료 뒤 결과를 읽는 일을 막았다. 실패한 중간 실행을 PASS로 합치지 않고 완성된 묶음의 위 normal 범위를 다시 실행했다.

최종 normal은 **27 packages, 2,273 test 완료 event PASS**, no-test package 11개다. 같은 source의 다음 범위를 race와 CGO0로 실행했다.

```text
./query ./orm ./db/... ./codegen/...
./conformance/relationprefetchproduct ./conformance/relationqueryproduct ./examples/helpdesk
```

`go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`은 각각
**11 packages, 2,089 test 완료 event PASS**, no-test package 2개다. 모든 JSON event의 시작/종료·필수 test·package를 대조했고 fail·잘린 로그는 없다.
세 mode의 유일한 test skip은 부모가 별도 process로 실행하는 `TestPostgresRevisionFenceHelperProcess`의 직접 진입이다.
필수 membership·generated consumer는 skip 없이 완료됐고 helper skip은 기능 PASS로 세지 않는다.

### 검증한 의미와 한계

- 독립 Django 6.1/UTC/SQLite의 여섯 field × 다섯 목록 × 네 Boolean 구성, **120개** 결과·SELECT 수를 보존했다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7의 locked Django/DRF/asgiref/sqlparse 환경에서 각 **1 test PASS, skip 0**로 fixture 전체를 다시 관찰했다.
- 외부 module에 실제 생성한 모델의 dynamic 120개·typed 84개를 실제 SQLite 결과와 QueryCount로 대조했다. Typed concrete slice에 없는
  explicit NULL member는 dynamic으로 검사하며 이를 typed 실행으로 세지 않는다. 필수 child test 네 개가 각각 정확히 한 번 완료돼야 통과한다.
- 실제 PostgreSQL의 120개 결과·model SELECT 수가 같은 reference와 일치했다. Trace는 SQL/credential을 보관하지 않고 호출 수만 센다.
  Empty source는 checkout/profile SQL까지 0회였으며 COUNT 0·MIN/MAX NULL도 driver 호출 없이 반환했다. 일반 nonempty query의 profile 검증 SQL은
  model SELECT 수와 구분했다. 고정 Django의 PostgreSQL 관찰 전체를 재현했다는 주장은 아니다.
- Caller slice·accessor·cached nullable pointer 소유권, derived query의 독립 cache, int64·UTC 연도 경계, NULL/empty와 nullable NOT을 확인했다.
  All/Count/Exists/ordered First/At/Iterate/projection/aggregate, invalid list·policy 우선순위·field/order/relation 오류·취소·closed/quarantine을 검증했다.
- SQLite Atomic/CoordinatedAtomic/AtomicRelation과 PostgreSQL Atomic/CoordinatedAtomic의 empty query·expired session·detached context 취소·
  종료 후 synthetic cursor 차단을 실제 transaction에서 확인했다. BEGIN/COMMIT/coordination lock 자체의 무 I/O를 주장하지 않는다.
- 0/NULL aggregate scanner는 실제 `database/sql` SQLite와 numeric alias·pointer·Scanner·string/bytes·지원하지 않는 destination의 오류를 대조했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.

### 통합 검증 소유자

통합 source는 `adb3ea62f7c8f9a57c623634e2c11b60f04374bc`다. Merge 뒤 로컬 checkpoint의 29개 파일 hash가 모두 같음을 확인했다.
[Hosted ORM 실행](https://github.com/progresshans/godj/actions/runs/35392098111)의 attempt 1은 **completed/success**다.
서로 다른 job 48개 중 **44개 success**, scope 밖 4개는 계획된 skip이다. 실패·취소·미완료 job은 없다.
`CI result (orm)` 실제 로그의 `scope=orm`, `full_platform_verified=false`와 네 owner
`portable-go-matrix`·`relation-product-matrix`·`command-product-matrix`·`postgresql-product`의 성공을 확인했다.

실제 범위는 Linux/macOS amd64·arm64의 선택된 relation/command 제품과 Linux portable normal/race/CGO0, PostgreSQL 17.10의 실제 제품이다.
비대상 skip은 project-check matrix·Python compatibility matrix·exact Darwin reference·current capture reference 통합이다.
이 네 범위를 이번 실행의 PASS로 표현하지 않는다. Windows runtime과 전체 reference/platform/cold-build를 요청한 `full`은 아니며,
기존 Text+DateTime full은 아래 GDJ-0074의 source에만 적용된다. 후속 GDJ-0076 작업 사본의 choices 구현도 이번 PASS에 포함하지 않는다.

## GDJ-0074 — DateTimeField와 UTC 시각 값

- 작업: [GDJ-0074](../../work/0074-datetime-field-and-model-time-values.md), 의미: [ADR-0061](../adr/0061-datetime-field-and-canonical-instant-values.md).
- Source: `d3cb0a9cf1294efbddc7aee34cdef8cafecc9837` 기반의 2026-09-19 작업 사본. Markdown을 제외한 변경·새 파일 98개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `d26a9de41607bb9e0fadfdce3b169c82d2e92ad39b790bdb91c4aaefa0f39b38`이다.
  HTML template, Python 관찰기/fixture, migration JSON과 실제 생성 Go/client도 포함한다.
- 환경: Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5 (Homebrew), locked Django 6.1/Python 3.14.3.
  새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했고 모든 로컬 lane 종료 뒤 전용 DB만 제거했다.
  기존 개발 DB와 Helpdesk 0001·0002·0003 migration은 변경하지 않았으며 Git 원본 bytes와 대조했다.

### 로컬 실행과 실패 수정

`GODJ_REQUIRE_POSTGRES=1`과 전용 DB URL로 다음 관련 범위의 `go test -count=1 -json -timeout=15m`을 실행했다.

```text
./schema/... ./internal/temporal ./query ./orm ./codegen/... ./migrations/... ./db/...
./forms/... ./serializers ./admin ./api/... ./examples/helpdesk/... ./examples/article/...
./internal/migrationautodetect ./internal/projectgenerate/... ./internal/projectmigration/...
./conformance/definitionload ./conformance/relationproduct ./conformance/runners/godj
```

첫 checkpoint는 Form 공백/24시 처리 불일치, nullable OpenAPI branch를 잘못 읽은 새 테스트, 외부 client의 소수초 손실을 발견했다.
빈 문자열만 NULL로 처리하고 Form의 유효한 24시를 다음 날 자정으로 정규화했다. OpenAPI 테스트는 실제 null/string branch를 확인하도록
수정했다. 고정 ogen v1.24.0의 `json.EncodeDateTime`은 RFC3339 layout으로 소수초를 생략하므로, 표준 date-time과 지원되는
`x-ogen-time-format` RFC3339Nano를 실제 문서에 게시하고 재생성했다. Consumer 기대값을 초 단위로 완화하지 않았다.

영향받은 temporal/forms/query/OpenAPI/Helpdesk/client 패키지 전체를 재실행했으며 최종 패키지별 normal 결과는
**42 packages, 3,984 test 완료 event PASS, fail 0**, no-test package 13개다. 뒤이어 ordered scalar 오류 설명의 잘못된 Integer/String
표현만 정리했고 query 패키지는 normal/race/CGO0 모두 다시 통과했다.

다음 관련 범위를 `go test -race -count=1 -json` 및 `CGO_ENABLED=0 go test -count=1 -json`으로 실행했다.

```text
./schema/... ./internal/temporal ./query ./orm ./codegen/... ./migrations/definition
./db/sqlite ./db/postgres ./forms/... ./serializers ./admin ./api/openapi
./api/openapi/consumertest ./examples/helpdesk ./internal/migrationautodetect
```

각 mode는 **18 packages, 2,690 test 완료 event PASS, fail 0**, no-test package 1개다. Event 수는 하위 test를 포함한다.
Normal의 `TestPublicationCrashHelper`·`TestPostgresRevisionFenceHelperProcess`, race/CGO0의 PostgreSQL helper는 직접 실행하지
않는 부모 진입에서 skip한다. 실제 부모가 별도 helper process를 실행해 통과했고 이 skip은 기능 PASS로 세지 않았다.

### 검증한 의미와 한계

- IR/default/historical codec: offset·monotonic 정보와 미세 자릿수를 제거한 default identity, UTC 연도 1·9999, 잘못된 scalar arm/비정규
  문자열 거부. SQLite DATETIME와 PostgreSQL timestamptz DDL/catalog, application default와 영속 SQL DEFAULT의 분리를 확인했다.
- 실제 생성 외부 model consumer: required/default/nullable/time.Time zero, epoch 이전 시각·양 끝 연도·microsecond ordering, typed/dynamic/F
  query, nullable projection/scan·Min/Max/empty aggregate, cache pointer 분리, Create/Patch 정규화·Save mask·취소·I/O 전 invalid 거부를
  실제 SQLite에서 확인했다. `Time`·`TimeValue` 필드가 Go import 이름과 충돌하지 않고 필수 child test 2개가 각각 완료되어야 한다.
  Linux/386/CGO0 generated model cross-compile도 통과했으며 386 runtime을 실행한 것은 아니다.
- 생성 forward/reverse 관계의 nonnullable DateTime exact binding을 실제 consumer에서 확인했다. 새 arbitrary relation lookup이나
  nullable terminal 지원을 주장하지 않는다.
- Helpdesk 양 DB: 0001의 기존 행 → 0003 → 새 0004 → 0001 reverse → 0004 재적용, 기존 값·NULL backfill·reopen 보존.
  API offset/nanosecond 입력과 canonical 응답, Admin의 연도 1 표시·연도 9999 변경·blank NULL·invalid calendar 뒤 DB 보존,
  실제 nullable MIN/MAX와 기존 권한·CSRF·4096 byte 제한을 확인했다. 실제 브라우저 전체 E2E는 별도다.
- 독립 ogen module의 **15개 필수 check**가 실제 HTTP·DB에서 offset·precision·연도 1/9999·생략/null을 포함해 완료했다.
  실제 문서와 locked generator의 생성물 drift를 확인했으며 Article 문서·client lock·ogen 설정은 보존했다.
- 고정 Django live fixture 비교 **1 test PASS**. Go Form의 **46개** 입력은 값·오류 code·widget을 비교했고, NUL suffix **2개**는
  실제 reference 결과를 보존한 [DEV-0011](../DEVIATIONS.md#dev-0011--datetime-입력의-nul을-거부하고-문자열-전체를-해석) invalid 회귀로 구분했다.
  이 2개는 Django parity PASS가 아니다. Locale/DST 전체·date transform·자동 시각 default는 여전히 미완료다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.
  Helpdesk `makemigrations`는 `status=clean`, candidate 0이었다.

### 누적 통합 검증

GDJ-0073 Text와 GDJ-0074 DateTime의 Hosted full milestone을 선택했다. 이 실행이 전체 플랫폼·고정 PostgreSQL 17.10·
process/reference·cold-build를 소유한다. 첫 구현 source `cea93c5dd00b50e9d0256ba764faa3554ff58b13`의
[Hosted 실행](https://github.com/progresshans/godj/actions/runs/35383582028)은 Python 3.13.15 lane에서 DateTime reference 비교가 실패했다.
고정 3.14.3에서 관찰한 24시 수용을 호환성 runtime에도 그대로 요구한 테스트의 profile 오류다.

동일한 고정 Django/DRF/asgiref/sqlparse 의존성으로 Python 3.12.13·3.13.15·3.14.3·3.14.7을 각각 직접 실행했다.
3.12/3.13은 required/optional `24:00:00` 2개를 invalid로 거부하고, 나머지 46개는 고정 fixture와 같았다. 3.14.3/3.14.7은 48개 모두 같았다.
Compatibility test는 알려진 두 runtime의 2개 expected 결과만 명시적으로 선택하고 전체 roster를 계속 비교한다.
수정 뒤 네 버전 모두 해당 unittest **1 test PASS, skip 0**이다. Go 제품·고정 reference fixture는 바꾸지 않았으며 기존 Go 실행 bytes도 유지한다.
실패를 확인한 이전 run의 남은 작업은 취소하고 수정 source로 full을 다시 실행한다. 이전 run이나 b43552a의 결과를 새 source의 PASS로 사용하지 않는다.
수정 source는 `8fd8936d634b5038a534936c15a2b1cfac4b853b`이며 [재실행 full](https://github.com/progresshans/godj/actions/runs/35384697050)의
attempt 1은 **completed/success**다. 서로 다른 **62개 job**이 모두 completed/success이고 실패·취소·skip job은 없다.
`CI result (full)`의 실제 report에서 `full_platform_verified=true`와 전체 선택 owner의 성공을 확인했다.
같은 run의 `systemstate-postgres-1`·`operator-postgres-1` capture 두 개가 게시됐고, reference job이 현재 source와 producer provenance를 검증해 소비했다.

이 full은 누적 Text/DateTime의 Linux/macOS·normal/race/CGO0·32-bit compile·고정 PostgreSQL 17.10·exact Darwin·Python compatibility·
process·cold-build와 reference 통합 범위를 완료했다. Python compatibility 네 버전도 모두 terminal success다.
실제 CI runner는 Linux/macOS이며 Windows runtime 검증은 포함하지 않는다. 이전 완료 기록의 Windows 포함 표현을 실행 roster에 맞게 정정했다.
완료 기록 이후의 Markdown 변경이나 별도 GDJ-0075 작업 사본의 IN 구현을 이 full source의 검증으로 합치지 않는다.

## GDJ-0073 — TextField와 여러 줄 Form/Admin 입력

- 작업: [GDJ-0073](../../work/0073-text-field-and-multiline-model-forms.md), 의미: [ADR-0060](../adr/0060-text-field-and-form-widget-semantics.md).
- Source: `b43552a1f88259babe97ec9fe83951f8cd205261` 기반의 2026-09-19 작업 사본. 중간 `fa74d0e`는 문서만 바꾼 commit이다.
  최종 변경·새 파일 중 Markdown을 제외한 73개 파일의 `<sha256>  <relative-path>\n` 정렬 manifest SHA256은
  `589be36af37d844dc66a5b5b2314da1a735011ddd4ccb334623712820285fb7c`다. HTML template, Python 관찰기·fixture와 생성 JSON·Go도 포함한다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite, PostgreSQL 17.5. 새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했고
  검증 뒤 작업 전용 DB만 제거했다. 기존 개발 DB와 Helpdesk 0001·0002 migration은 바꾸지 않았다.

### 관련 실행과 실패 수정

`GODJ_REQUIRE_POSTGRES=1`, 전용 `GODJ_TEST_POSTGRES_URL`로 normal은 다음 범위를 실행했다.

```text
./schema/... ./orm ./query ./codegen/... ./migrations/... ./db/sqlite ./db/postgres
./forms/... ./admin ./serializers ./api/... ./examples/helpdesk/...
./examples/article/adminapp ./examples/article/apiapp ./internal/migrationautodetect
./internal/projectgenerate/... ./internal/projectmigration/...
```

첫 실행은 DB 주소에 host가 빠져 backend URL 검증에 거절됐고, 새 테스트가 기존 관계 terminal에 없는 IContains와
serializer default의 공백 보존을 가정해 실패했다. DB 주소를 명시적 localhost URL로 수정하고 기존 implicit-exact 및
serializer normalization 의미에 맞게 테스트를 고쳤다. 제품 정책을 테스트에 맞춰 완화하지 않았다.
실패한 `db/postgres`, Helpdesk, serializers, codegen/consumertest 패키지를 각각 전부 재실행했다.
추가로 raw textarea의 선행 newline·HTML escaping·invalid Form 뒤 DB 보존과 실제 본문 수정을 보강해 Helpdesk를 세 모드로 재실행했다.
각 패키지의 최종 결과로 normal은 **29 packages, 3,427 test 완료 event PASS, fail 0**, no-test package 6개다.

다음 범위를 `go test -race -count=1 -json`과 `CGO_ENABLED=0 go test -count=1 -json`으로 실행했다.

```text
./schema/... ./orm ./codegen ./codegen/consumertest ./migrations/definition
./db/sqlite ./db/postgres ./forms/... ./admin ./serializers
./api/openapi/consumertest ./examples/helpdesk ./internal/migrationautodetect
```

두 모드 모두 최종 **15 packages, 2,357 test 완료 event PASS, fail 0**이다. 위 event 수는 하위 test를 포함한다.
Normal의 `TestPostgresRevisionFenceHelperProcess`·`TestPublicationCrashHelper`, race/CGO0의 PostgreSQL helper는 직접 호출하지
않는 parent 진입에서 skip했다. 실제 부모 테스트는 별도 helper process를 실행해 통과했고 이 skip을 기능 PASS로 세지 않았다.

### 검증한 의미

- Schema/codec의 Text kind·nullable·긴 문자열/빈 default 보존, 잘못된 length/default/PK 거부. 양 DB TEXT DDL에는 영속 DEFAULT가 없고
  PostgreSQL catalog는 varchar·length·nullability·identity·persistent default drift를 거절했다.
- 실제 생성된 별도 module의 Text consumer는 required/default/empty/null, 긴 Unicode 본문 저장, typed/dynamic/F query,
  nullable projection·Max aggregate, cache 포인터 분리와 Create/Patch/Save mask를 확인했다. 필수 child test 두 개가 각각
  정확히 한 번 완료되어야 하며 skip·실패·잘린 출력·stderr는 거절한다. Forward/reverse 생성 관계의 nonnullable Text exact binding도 검증했다.
- Helpdesk 양 DB: 0001에 기존 행 입력 → 0002 → 0003 → 0001 reverse → 0003 재적용, 기존 값과 NULL backfill 보존.
  권한·CSRF·재시작, 잘못된 Text 타입/NUL·4096 bytes 초과 거부, 긴 본문의 API→DB→textarea→Admin 수정,
  빈 Form Text의 빈 문자열과 integer Null 구분을 확인했다. 실제 브라우저 전체 E2E를 실행했다는 주장은 아니다.
- 외부 ogen v1.24.0 module: 실제 HTTP와 DB를 확인하는 **14개 필수 check**, 문서·생성물 drift, multiline/HTML 본문과
  생략/null/empty string을 검증했다. Article 문서와 client 의존성 lock은 변경하지 않았다.
- 고정 Django 6.1 Char/Text × nullable × required의 **104개 관찰값**과 Go cleaning·widget·오류 code가 일치했다.
  Python live fixture 비교 1 test PASS. 같은 locked 환경의 Python suite는 **276 tests 중 269 PASS, 7 skip**이다.
  4개 capture/profile 요구와 설치되지 않은 DRF를 요구하는 3개 test는 이 실행의 검증 범위가 아니다. 전체 reference 통합 PASS로 쓰지 않는다.
- `go test -run '^$' ./...` 전체 compile, `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.
  Helpdesk `makemigrations` 재실행은 `status=clean`, candidate 0이었다.

최종 구현 commit은 `fca8cbfa38c072c9f8825f000a90f206ce291ddf`이며 로컬·원격 source가 일치했다.
[해당 source의 Fast feedback](https://github.com/progresshans/godj/actions/runs/35377057526)은 completed/success다.
문서-only 선행 commit 위로 통합하면서 제품·테스트 파일의 위 manifest가 그대로 유지됨을 확인했다.
이번 작업의 새 Hosted full은 실행하지 않았다. 이전 `b43552a` full 결과는 GDJ-0072까지의 근거이며 위 Text 변경의 platform PASS가 아니다.
후속 구현과 누적 변경의 영향에 맞춰 별도 통합 milestone을 선택한다.

## GDJ-0072 — 일반 정수와 기존 Helpdesk 모델의 성장

- 작업: [GDJ-0072](../../work/0072-integer-field-model-growth.md), 의미: [ADR-0059](../adr/0059-signed-integer-field-and-model-growth.md).
- 로컬 source: `8ade467afe918474e9c42fd066edbfe7972ee600`에 이번 변경을 적용한 2026-09-19 작업 사본.
  변경·새 파일 중 Markdown을 제외한 100개 파일의 `<sha256>  <relative-path>\n` 정렬 manifest SHA256은
  `e06a8538d34c60b175ea14e7f4376ade3fe479575b21b2d602ea3a9ce55f7c8a`다. HTML template과 생성 JSON·Go도 포함한다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite, 로컬 PostgreSQL 17.5. 새 전용 PostgreSQL DB와 테스트별 schema,
  임시 SQLite를 사용했다. 기존 개발 DB와 Helpdesk `migrations/0001_initial.godj.json`은 변경하지 않았다.
  검증을 마친 작업 전용 PostgreSQL DB는 제거했다.

### 로컬 관련 검증

다음 범위를 `GODJ_REQUIRE_POSTGRES=1`, 전용 `GODJ_TEST_POSTGRES_URL`로 `go test -count=1 -json -timeout=15m` 실행했다.

```text
./schema/... ./orm ./forms/... ./serializers ./admin ./codegen/...
./migrations/... ./db/... ./query ./internal/migrationautodetect ./internal/compiletest
./api/... ./examples/article/... ./examples/helpdesk/... ./conformance/relationproduct
```

첫 실행에서 standalone 관계 product의 생성물 갱신 누락과 새 관계 consumer 테스트의 지원 밖 비교 연산 사용을 발견했다.
실제 generator로 누락 파일을 재생성하고 기존 implicit-exact 의미에 맞게 테스트를 수정했다. 역방향 정수 terminal compile도 보강한 뒤
`./codegen/consumertest ./conformance/relationproduct` 전체를 재실행했다. 나머지 패키지 소스에는 이후 동작 변경이 없다.
최종 패키지별 결과는 **35 packages, 3,174 test 완료 event PASS(하위 test 포함), fail 0**이다. 13 packages는 `[no test files]`다.
유일한 test skip은 직접 실행용이 아닌 `TestPostgresRevisionFenceHelperProcess`의 parent 진입이다. 실제 교차 process 부모 테스트는
helper 전용 환경·pipe로 child를 실행하고 통과했다. 이 skip을 기능 검증 PASS로 세지 않았다.

- 신규 generated integer consumer: 별도 module에서 required/default/min/max/null/zero, typed·dynamic·F query, nullable projection·Min/Max,
  cache 포인터 분리, Create/Patch/Save mask·취소·쓰기 전 실패를 실제 SQLite와 검증했다. 필수 child test 두 개가 각각 정확히 한 번
  완료되어야 하며 skip·실패·잘린 출력·stderr는 거부한다. linux/386/CGO0 generated model **cross-compile**도 통과했다. 386 runtime 주장은 아니다.
- Helpdesk: SQLite·PostgreSQL에서 실제 0001 schema에 행을 만든 뒤 0002 적용·reverse·재적용, reopen·권한 교체와 Admin/API 흐름을
  검증했다. 기존 row의 값과 새 nullable priority, int64 양 끝·0·null 입력, 잘못된 JSON 숫자·타입과 권한 거부 뒤 DB 보존을 확인했다.
  Admin의 정확한 정수 렌더링과 null/zero 변경도 포함한다.
- 외부 ogen v1.24.0 consumer: 실제 문서·offline 재생성·독립 module build·HTTP와 DB 결과를 함께 확인했다. Helpdesk 생성 요청의
  생략/null/0/최소/최대 정수와 관계 범위·권한을 추가해 **13개 필수 check**를 확인한다. Article 문서와 client 의존성 lock은 변하지 않았다.
- 고정 Django 6.1의 `BigIntegerField.formfield()`에서 required/optional **90개 관찰값**을 생성했다. Go Form이 같은 입력·정수·null·오류
  코드를 통과했고 Python의 live 관찰/저장 fixture 비교 **1 test PASS**를 확인했다. 기존 전체 conformance corpus에 새 정수 contract가
  등록됐다는 주장은 아니다.
- `make generate-check`: Helpdesk·Article·관계 fixture 및 standalone 관계 product drift PASS.
  Helpdesk `makemigrations` 재실행은 `status=clean`, candidate 0이었다.
- 관련 `go vet`, `make docs-check format-check`, `git diff --check` PASS. 현재 Markdown 99개 local link를 검사했다.

### 통합 검증 소유권

누적 GDJ-0070/0071/0072를 묶은 Hosted full이 OS·race·CGO0·고정 PostgreSQL·process/reference 통합을 소유한다.
로컬 전체 matrix는 반복하지 않는다. 첫 [Hosted 실행](https://github.com/progresshans/godj/actions/runs/35369545141)은
source `48165d8fc7b7d3d8412fe75784ac10e7c4ed1873`에서 이전 GDJ-0071 API 변경의 네 consumer 갱신 누락을 발견했다.
`apiapp.Middleware()`가 제거됐는데 reference fixture·distinct-process worker·외부 operator runner가 남은 호출을 사용해 compile에 실패했다.
같은 원인 확인 후 나머지 실행을 취소했으며 이 run은 통합 PASS가 아니다.

네 consumer 모두 실제 API 인스턴스의 `Middleware()`로 연결하고 불필요한 wrapper를 제거했다. 호환 shim을 추가하지 않았다.
수정 후 `go test -run '^$' ./...` 전체 compile과 `go vet ./...`을 통과했다. 관련 GDJ-0044/0047 API/auth reference,
process worker·외부 SQLite operator의 기존 실제 흐름을 재실행해 **3 packages, 63 test PASS, skip/fail 0**을 확인했다.
수정 source `b43552a1f88259babe97ec9fe83951f8cd205261`의
[Hosted full 35370184198](https://github.com/progresshans/godj/actions/runs/35370184198)은 최종 attempt 2에서 **completed/success**다.
62개 고유 job 모두 같은 source·run에 묶인 completed/success이며, 최종 aggregate는 `scope=full`,
`full_platform_verified=true`와 8개 필수 owner를 확인했다. 로컬·원격 브랜치 source도 일치했다.

Attempt 1에서는 macOS Intel normal command job의 `go mod tidy`가 `proxy.golang.org`의 checksum 서버 연결 timeout으로 실패했다.
소스를 바꾸거나 checksum 검사를 끄지 않고 `gh run rerun --failed`로 실패 항목 재실행을 요청했다. 재실행한 command job은
operator·targeted migrate 양쪽의 필수 실행을 확인했고 최종 aggregate도 성공했다. GitHub의 최종 attempt job 목록에는 이전 성공 결과가
포함되므로 모든 성공 검사를 새로 중복 실행했다고 표현하지 않는다.

- Portable normal/race/CGO0, 관계·project-check·명령의 Linux/macOS·amd64/arm64 matrix와 PostgreSQL 17.10 여섯 조합이 완료됐다.
- API/client의 normal/race/CGO0, 정수 generated consumer와 32-bit compile, 실제 DB·process와 필수 sentinel은 해당 실행 owner가 검증했다.
- 고정 Darwin Python **275 tests·skip 0**, normal reference **275 tests·profile 소유 skip 4**, 네 Python 버전의 compatibility 검증이 통과했다.
- 현재 run/source의 system-state와 operator capture를 reference job이 검증·소비했다. 두 producer는 성공한 attempt 1이며,
  최종 attempt에서 그 provenance를 보존한다. 다른 source의 capture나 과거 `b74a79e` 결과로 대체하지 않았다.
- 후속 TextField 작업은 별도 worktree의 미완성 변경이며 이 full PASS의 대상이 아니다.

## GDJ-0071 — schema 정체성·JSON 정책과 실제 생성 client

- 작업: [GDJ-0071](../../work/0071-api-schema-identity-and-generated-client.md), 설계: [ADR-0058](../adr/0058-model-derived-openapi-and-operation-ownership.md).
- 기준 commit: `f7db3ed1ab0e7fcdedd9f4af0c1f760893bad823`에 이번 변경을 적용한 2026-09-12 작업 사본이다.
  해당 기준 commit 자체나 이전 Hosted 결과를 이 변경의 PASS로 표시하지 않는다.
- 최종 변경 source 입력 80개의 SHA256 manifest digest: `b452bb6f6eb748d206434bace1a81c83cba40b12d98540ce1e7a30e1e012f69e`.
  기준 commit 대비 변경·새 Go/Python/JSON/YAML 파일과 Makefile·go.mod·go.sum 경로를 정렬하고,
  각 `<file-sha256>  <relative-path>\n` 행을 이어 SHA256으로 계산했다. 문서·license는 이 digest에서 제외했다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite, 로컬 PostgreSQL 17.5. 새 전용 PostgreSQL DB와 테스트별 schema,
  임시 SQLite 파일을 사용했다. 기존 개발 DB를 변경하지 않았고 작업 전용 PostgreSQL DB는 검증 후 제거했다.

### 구현과 위험의 소유권

- `JSONPolicy`의 middleware와 문서가 실제 subtree 적용을 공유한다. Zero policy와 Helpdesk에는 자동 406이 없고,
  dynamic route 일부에만 적용되는 prefix는 명시적으로 실패한다. Article의 기존 협상·routing error 동작을 유지한다.
- 명시적 named schema와 local reference를 그대로 출력한다. 중복·미해결·순환·지원 외 참조, depth/node/byte budget,
  property 이름과 참조의 구분, 불변 snapshot과 결정성을 검증했다. 공통 오류 component의 재선언은 거부한다.
- 네 schema 연결 위치의 참조 검사, 공유 auth/406 실패 status의 필수 application header 거부,
  root error alias와 공통 오류의 일치, 빈 문서/부분 게시 방지를 확인했다.
- Helpdesk는 단일 API 구성에서 route와 문서를 만든다. 실제 encoder/input Spec, bare list·nested category,
  선택 category 밖 ticket의 404, 생성 default·null·빈 문자열, 권한 선행과 기존 category/ticket 보존을 검증했다.

### 관련 Go와 실제 외부 client 실행

통합 범위는 `./api/... ./web/... ./examples/article/... ./examples/helpdesk`다. Test가 있는 **18 packages**에서
각 모드 **555개의 test 완료 event PASS(하위 test 포함), fail/실제 test skip 0**을 확인했다.
별도의 5개 package는 Go 소스만 있어 `[no test files]`이며 실행 누락된 test를 의미하지 않는다.

```sh
make api-client-dependencies
GODJ_REQUIRE_POSTGRES=1 go test -json -count=1 ./api/... ./web/... ./examples/article/... ./examples/helpdesk
GODJ_REQUIRE_POSTGRES=1 go test -race -json -count=1 ./api/... ./web/... ./examples/article/... ./examples/helpdesk
GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -json -count=1 ./api/... ./web/... ./examples/article/... ./examples/helpdesk
```

`GODJ_TEST_POSTGRES_URL`은 전용 DB로 설정했다. Normal 첫 실행에서는 generator의 일반 stderr 경고를 consumer runtime과
똑같이 실패로 처리한 harness 때문에 외부 consumer가 실패했다. 경고는 `WWW-Authenticate`를 Go의 canonical casing으로
다루는 도구 진단이었다. Tool의 일반 진단과 실행 consumer의 엄격한 stderr 계약을 구분하고 generation drift를 그대로 유지했다.
최종 document 연결 회귀도 포함해 `./api/openapi ./api/openapi/consumertest`를 다시 실행하여 **2 packages, 125 PASS**를 확인했다.
그 외 변경되지 않은 **16 packages, 430 PASS**는 첫 normal 실행 결과다. Race/CGO0는 최종 제품/test 소스로 전체 관련 범위를 실행했다.

외부 consumer는 GoDj를 import/replace하지 않는 별도 module에서 **ogen v1.24.0**으로 생성한 세 client를 사용한다.
실제 API 문서 세 개의 byte 일치, 고정된 schema/config/tool/lock의 offline 재생성, 정확한 generated 파일 집합·내용,
별도 executable build와 실제 HTTP를 같은 테스트에서 확인했다. 부모 race 실행에서는 consumer executable도 race로 빌드했다.

- 실제 HTTP: Article Bearer CRUD·PATCH omitted/null/empty/false·PUT default/보존·인증/인가 오류, Session cookie·CSRF CRUD와
  잘못된 CSRF, Helpdesk selected relation·생성 default·읽기 전용 거부, 사전 취소 요청과 최종 DB effects.
- 별도 wire fixture: int64 최대 path/response와 overflow 거부, 필수 nullable/read-only 필드 누락과 추가 응답 필드 거부,
  PATCH의 실제 serialized omitted/null/empty/false. 각 mock 응답은 정확히 한 HTTP 교환을 요구한다.
- 완료 보고: 12개 필수 check의 정확한 집합, 중복 JSON member·잘린/후행 보고·race 불일치 거부. 빈 파일도 경로 이름을
  검사하며 subprocess 실패·취소·출력 초과·성공 종료의 runtime stderr를 성공으로 취급하지 않는 부정 대조군을 포함한다.
- Session은 실제 adapter와 CSRF 교환을 쓰지만 parent가 메모리 session을 준비한다. 생성 SDK의 로그인 기능 검증은 아니다.
  생성기의 template 출처와 Apache-2.0 license는 client fixture에 보존했다. Root framework의 go.mod/go.sum은 바꾸지 않았다.

### 독립 규격·CI 연결·문서 검증

- 격리 `uv --no-project` 환경의 openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**:
  OpenAPI 3.1.1 문서 **3개**, named schema **17개** 유효성, native local ref를 resolve한 수용/거부 **158사례**
  (수용 69/거부 89; Article profile별 59, Helpdesk 40), default annotation **7검사** PASS.
  `default`는 값을 삽입하지 않고, `x-godj-normalization`·parser lexical/byte·권한/DB 계약은 JSON Schema가 검사하지 않음을 확인했다.
- 검사한 문서 SHA256: Article Bearer `13733fb869ffdd8d2395bd0e457854b80226154401ced9d1d7a143e067d80847`,
  Article Session `a5a951276698455afd214e207a2e6c52681a11468c2f80a05f0be1f92bdaa333`,
  Helpdesk Session `0b09e0a2589fee4e93ba061026114f39a5556820a54138e2cb5980ea1d7ae6fb`.
- CI package/scopes의 Python 회귀 **12개 PASS**. 정확한 모듈 경로를 integration으로 분류하고 normal/race/CGO0 Make target이
  의존성 준비를 소유한다. 실제 `go list`에서도 consumer는 integration, nested client는 root package 목록 밖임을 확인했다.
  의존성 준비는 임시 복사본에서 수행해 lock 변화를 거부한다. Portable Go cache key에 client go.sum을 포함했다.
- 유지보수 export 명령의 별도 compile과 실제 실행 PASS. Article Bearer 24,491 bytes/10 operations,
  Article Session 21,331 bytes/10 operations, Helpdesk Session 7,105 bytes/3 operations가 저장된 입력과 byte-identical이었다.
  비어 있지 않은 출력으로 재실행하면 exit 1이고 기존 세 파일의 SHA256가 유지됐다. 갱신 절차는
  [consumer README](../../api/openapi/consumertest/README.md)에 있다. 이 명령은 기본 Go test package 집합 밖에서 별도로 검증했다.
- 관련 `go vet`, `make format-check docs-check`, `git diff --check`: PASS. 현행 Markdown **97개**의 local link를 검사했다.
- 현행 API와 문서, subprocess/receipt·CI/CLI의 독립 읽기 리뷰를 완료했다. 발견한 empty-file membership와 상속 `GORACE`에
  의한 child false PASS 가능성을 수정하고 해당 부정 대조군까지 실행했다.
- Hosted full matrix·Linux/다른 arch와 PostgreSQL 버전, 새 Django differential contract, 배포형 SDK·다른 언어 generator는
  이번 범위에서 실행하지 않았다. 외부 validator는 repository Python lock을 변경하지 않았다.

## GDJ-0070 — 모델과 실제 API 선언에서 OpenAPI 제공

- 작업: [GDJ-0070](../../work/0070-model-derived-openapi.md), 설계: [ADR-0058](../adr/0058-model-derived-openapi-and-operation-ownership.md).
- 기준 HEAD: `6d30973ae3034e16dc56b9d2b9e0a6faffe1fb49`에 이 작업의 미커밋 변경을 적용한 2026-09-12 작업 사본이다.
  위 HEAD 자체의 PASS나 Hosted 검증을 뜻하지 않는다.
- 최종 변경 Go 파일 20개의 SHA256 manifest digest: `ef9271c9bf993514f82939e087a90d13942b2cdb7bf8ed8f5d7af9dc77014a5a`.
  HEAD 대비 변경·새 Go 파일의 경로를 정렬하고 각 `<file-sha256>  <relative-path>\n` 행을 이어 SHA256으로 계산했다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite와 로컬 PostgreSQL 17.5. 이 작업 전용 임시 PostgreSQL DB와
  테스트별 schema·임시 SQLite fixture를 사용했다. 기존 개발 DB는 변경하지 않았다.

### 구현과 위험 검증

- 같은 serializer Spec에서 full/partial 입력과 ModelEncoder 응답을 투영한다. Read-only·required/default·null·empty·
  Unicode 문자 길이, input trim 이후 제약과 untrimmed output 길이의 차이, field allowlist와 immutable snapshot을 검증했다.
- 실제 operation에서 route·permission·body·response를 연결한다. Web의 이름·경로 문법·교차 route language 충돌,
  OAS template 고유성, 잘못된 media type, profile 소유 header 충돌과 실패 시 부분 게시 없음의 회귀를 포함한다.
- 실제 Session/Bearer adapter의 공개 metadata와 custom cookie/header 정규화, description 중 인증·인가·entropy 작업 없음,
  unsafe Session의 세 조건 AND, Bearer challenge·JSON 오류와 HEAD/204/plain 500을 검증했다.
- 실제 `http.Client`와 임시 HTTP server로 문서의 입력·출력과 Article 생성·PATCH·HEAD 200/403/404/406·빈 query 응답을 대조했다.
  기존 site fixture에서 문서의 익명 403·로그인 후 200·secret 부재와 public-only 404를 확인했다.
  기존 CRUD·missing target 우선순위·CSRF·취소·audit·two-runtime 흐름도 아래 관련 package 범위에서 실행했다.

### 실행

첫 통합은 `./api/... ./serializers ./web/... ./examples/article/...`에서 normal/race/CGO-disabled를 실행했다.
Normal 최초 실행의 PostgreSQL 4개는 환경 미설정으로 skip이었다. 이후 전용 DB를 만들었으나 host 없는 URL은 GoDj의
configuration 검증에서 거부되어 PostgreSQL normal/race 4개가 각각 실패했다. Host를 포함한 URL로 수정하고 해당 네 흐름을
다시 실행해 모두 PASS를 확인했다. 이 설정 실패를 제품 회귀나 미실행 성공으로 세지 않는다.

최종 리뷰에서 발견한 응답 `maxLength` 누락을 고친 뒤 변경이 영향을 주는 OpenAPI와 Article 전체를 다시 실행했다.
아래 세 명령은 각각 **11 packages, 185 tests PASS, fail/skip 0**이다.

```sh
GODJ_REQUIRE_POSTGRES=1 go test -json -count=1 -timeout=10m ./api/openapi ./examples/article/...
GODJ_REQUIRE_POSTGRES=1 go test -race -json -count=1 -timeout=15m ./api/openapi ./examples/article/...
GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -json -count=1 -timeout=15m ./api/openapi ./examples/article/...
```

`GODJ_TEST_POSTGRES_URL`은 실행 전에 전용 DB로 설정했다. 최종 수정에서 바뀌지 않은 `api`, `api/sessionauth`, `api/bearerauth`,
`serializers`, `web`, `web/sessionauth`는 첫 통합에서 각 모드 **6 packages, 312 tests PASS, fail/skip 0**이다.
두 범위를 합쳐 각 모드 497개 test의 관련 위험을 검증했다. Article의 실제 생성물 drift·declaration bootstrap 회귀도 포함한다.

- `openapi-spec-validator==0.9.0` 격리 실행: 실제 Article Session/Bearer 구성에서 생성한 3.1.1 문서 두 개 모두 PASS.
  최종 문서는 각각 35,137/43,433 bytes, 10 operations다. Registry나 DB 내용을 문서에 넣지 않는다.
- 같은 격리 환경의 `jsonschema==4.26.0` Draft 2020-12 validator: 186개 schema 위치의 유효성과 50개 수용/거부 사례 PASS.
  Full/partial, readonly·누락·추가 필드, null·empty, 응답 Unicode 최대 길이, int64 범위, page와 오류 envelope를 확인했다.
  Fixture 사례는 실제 HTTP 검증과 구분하며 parser lexical/byte·trim 정책 전체의 동치 검증을 주장하지 않는다.
- 관련 `go vet`, `make docs-check format-check`, `git diff --check`: PASS. 문서 94개의 local link destination을 확인했다.
- 새 Go dependency·Django oracle lock·generated ABI 변경은 없다. 외부 validator는 프로젝트 Python lock에 추가하지 않았다.
- Hosted full matrix, 다른 OS/arch·PostgreSQL 버전, 새 Django differential contract, 별도 모듈 설치·생성 SDK와 외부 client generator는
  이번 범위에서 실행하지 않았다. 아래 과거 Hosted 전체 성공은 GDJ-0070 소스의 PASS가 아니다.

## 2026-09-12 — 통합 개발 경험 조사와 개발 기준

- 범위: [공식 문서·source 비교 보고서](../research/2026-09-12-framework-developer-experience.md)와
  [개발 판단 기준](../DEVELOPMENT_CRITERIA.md), 관련 현행 문서의 연결·상태 정리.
- 코드 읽기 기준: `6d30973ae3034e16dc56b9d2b9e0a6faffe1fb49`. 제품 코드·생성물·dependency·CI·conformance profile은 변경하지 않았다.
- `PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_docs.py`: PASS, 92개 문서의 local link destination 확인.
- 보고서의 각주 56개: 참조·정의의 누락·중복·미사용 없음. 새 문서의 EOF·trailing whitespace·fence 정합 확인.
- `git diff --check`: PASS. Django/DRF, FastAPI/Template/Litestar, Ninja/Modern REST 비교의 독립 source 리뷰에서
  남은 실질적 오류를 발견하지 않았다. 진단 정보의 공개 대상과 기록 절차의 중복 가능성 두 문구는 보정했다.
- 비교용 앱 구현·실행, 개발 시간·성능 측정, Go/DB/race/platform·Hosted 검증은 이번 문서 작업에서 수행하지 않았다.
  공식 테스트 source의 기대값을 읽은 사실을 로컬 실행 PASS로 표시하지 않는다. 아래 GDJ-0069는 이전 제품 소스의 검증 기록이다.

## GDJ-0069 — 바인딩·쿼리 준비와 감사 후속 개선

- 작업: [GDJ-0069](../../work/0069-boundary-preparation-and-audit-followup.md).
- 기준: `71ba61f0ecf26d397b10ac207212146f27441de7`.
- 상태: F1~F8 구현·전후 측정·관련 로컬 통합과 동일 제품 소스의 Hosted full scope 완료.
- 검증 소유권: 묶음별 affected local checkpoint, 관련 DB·race·CGO-disabled 통합, 최종 제품 소스 Hosted full scope.
- 아래 GDJ-0068은 직전 완료 근거이며 GDJ-0069 변경 소스의 PASS가 아니다.

### 변경과 검증 대상

| 항목 | 최종 변경 | 보존하는 검증 |
|---|---|---|
| F1 | ReverseObject의 정적 검사를 Bind가 소유하며 prefetch는 canonical private field를 공유 | 공개 metadata 변조·descriptor snapshot·zero/nil·PK·callback field 변경·cold cache·취소·16개 동시 소비 |
| F2 | CheckHistory가 private immutable applied map을 읽음 | Plan의 별도 mutable 복사, unknown/history 오류 순서, 64 goroutine의 반복 CheckHistory와 forward/backward Plan |
| F3 | PostgreSQL IN 값을 compile-local leaf에 한 번 준비 | mixed IN/NOT/ISNULL·relation alias·model/projection/aggregate·재컴파일 인자 독립성·기존 오류 우선순위 |
| F4 | Article의 최대 6개 optional predicate를 한 번에 Filter | 기존 typed/dynamic AST와 유효 조건의 batch/chain 의미, 검색·페이지·정렬·SQLite/PostgreSQL HTTP 흐름 |
| F5/F6 | 경로 Count 전환, 미사용 regexp 제거 | 기존 root/malformed/segment bound와 wirejson 숫자 거부 |
| F7 | 두 Article PostgreSQL 준비와 두 CLI assertion helper 공유, 표준 slices.Equal | 독립 schema·flow·fixture hash/LoadReport·required DB·redaction·별도 oracle/actual·exact SQLite snapshot |
| F8 | canonical digest byte 검증으로 임시 decode 할당 제거 | 64개 위치 각각 모든 byte와 stdlib canonical hex 대조, 기존 손상·중복·전체 검증 뒤 만료 1행 삭제·cross-runtime fence |

검증 비용을 줄이기 위해 assertion·필수 DB·오류 검사를 삭제하지 않았다. F1의 storage.Field는 외부 상태를 볼 수 있는
callback이므로 매 Load에서 계속 검사한다. F4는 이미 검증된 최대 6개 predicate를 수집하며 한도 밖의 임의 chain/batch가
같은 오류 우선순위를 갖는다고 일반화하지 않는다. F7은 fixture와 DB 수명만 공유하며 oracle을 actual 생성에 전달하지 않는다.

### F1~F4 전후 측정

2026-09-10 KST, Go 1.26.5 darwin/arm64, Apple M3 Pro. 각 제품 변경 전에 같은 workload를 추가해 기준을 측정했다.
`-benchtime=200ms -count=3`의 중앙값이며 마지막 비교는 `-p=1`로 package를 순차 실행했다. DB·HTTP 전체 성능의 증거는 아니다.

```sh
go test -p=1 -run '^$' -bench 'Benchmark(PlannerCheckHistory|ReverseObjectFrom|ReversePrefetch|PostgresConditionCompilation|ConditionBatching)$' -benchtime=200ms -count=3 ./migrations ./orm ./db/postgres ./query
```

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| ReverseObject.From | 2,093 → 282.4 | 1,408 → 736 | 15 → 7 |
| ReversePrefetch / 20 owners·1,000 rows | 166,038 → 140,910 | 465,117 → 463,482 | 7,390 → 7,372 |
| CheckHistory / 32 applied | 2,391 → 1,383 | 2,728 → 0 | 3 → 0 |
| CheckHistory / 256 applied | 19,231 → 11,700 | 21,800 → 0 | 3 → 0 |
| CheckHistory / 1,024 applied | 90,187 → 51,232 | 98,384 → 0 | 5 → 0 |
| PostgreSQL full compile / exact | 488.9 → 490.9 | 504 → 520 | 13 → 13 |
| PostgreSQL full compile / IN 8 | 827.9 → 755.9 | 1,640 → 1,272 | 17 → 16 |
| PostgreSQL full compile / IN 256 | 10,948 → 9,121 | 43,112 → 29,560 | 180 → 179 |
| PostgreSQL full compile / IN 999 | 48,908 → 42,537 | 168,665 → 119,528 | 1,670 → 1,669 |

PostgreSQL 일반 exact 조건은 leaf 준비 공간이 16 bytes 늘었고 시간은 이 측정에서 비슷했다. IN 999의 49 KB는
전체 할당량이 아니라 제거된 두 번째 public Values 복사량에 해당한다. 공개 getter의 방어적 복사는 유지한다.
각 IN leaf의 snapshot은 컴파일이 끝날 때까지 보유하므로 여러 IN이 있으면 동시에 보유하는 복사 공간은 목록 길이의 합에
비례한다. 표는 한 IN의 총 할당량 측정이며 다중 IN의 peak memory 감소를 증명하지 않는다.

| 유효 조건 수 | chain → batch ns/op | chain → batch B/op | chain → batch allocs/op |
|---|---:|---:|---:|
| 8 | 951.8 → 502.1 | 2,456 → 1,488 | 22 → 12 |
| 64 | 11,110 → 3,278 | 35,416 → 10,896 | 190 → 68 |
| 256 | 78,062 → 12,848 | 353,563 → 43,920 | 766 → 260 |
| 1,023 | 932,708 → 52,908 | 4,764,445 → 172,033 | 3,067 → 1,027 |

이 비교는 같은 유효 Plan의 수집 방식 차이다. Immutable AST의 체인 구성 자체를 변경하지 않았고 새 전역 cache를 추가하지 않았다.

### F8 실제 DB 측정과 정책 결정

SQLite는 modernc v1.56.0의 실제 임시 파일과 `_busy_timeout=5000`, PostgreSQL은 기존 로컬 17.5 서비스에 새로 만든
전용 DB·개별 schema를 사용했다. 모두 framework migration으로 준비하고 실제 coordinated transaction을 실행했다.
PostgreSQL 17.10의 Hosted 환경과 이 로컬 버전을 구분한다. 양쪽 전후 각각 34 workload × 3회, 총 102 sample이 완료됐다.

```sh
GODJ_REQUIRE_POSTGRES=1 go test -run '^$' -bench 'BenchmarkSession(Capacity|Operations)Database$' -benchtime=200ms -count=3 -timeout=15m ./systemstate
```

`GODJ_TEST_POSTGRES_URL`은 실행 전 전용 DB를 가리키도록 설정했다. Capacity는 64/1024/4096 상한 각각 25%·full-live·full-expired를
측정한다. Full-expired의 fixture 복원은 timer 밖이며 측정에는 전체 검증·실제 1행 삭제·commit이 포함된다.

| 4096 상한 / occupancy | 기준 → 변경 ms/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| SQLite / 25% | 1.294 → 1.210 | 181,681 → 148,904 | 6,986 → 5,962 |
| SQLite / full-live | 18.580 → 17.028 | 15,306,836 → 15,044,732 | 241,287 → 233,095 |
| SQLite / full-expired | 18.152 → 18.037 | 15,308,345 → 15,046,251 | 241,303 → 233,111 |
| PostgreSQL / 25% | 0.747 → 0.702 | 183,647 → 150,881 | 7,025 → 6,001 |
| PostgreSQL / full-live | 14.734 → 14.393 | 15,308,844 → 15,046,713 | 241,316 → 233,124 |
| PostgreSQL / full-expired | 14.735 → 14.296 | 15,309,532 → 15,047,401 | 241,333 → 233,142 |

포화 시 두 번의 digest scan에서 4096×2개의 decode 할당을 제거했고 payload 복원 검증은 유지했다.
용량이 남아도 inventory는 O(n)이며 포화 시 추가 O(n) payload 검증이 남는다.

Operations는 1024/4092행에서 독립 backend/Runtime 1개·4개를 사용한다. Create는 직후 Delete까지 한 cycle이며 Rotate는 두 ID를
번갈아 사용한다. 아래 값은 4092행의 cycle당 평균 wall time이며 개별 요청의 대기 시간을 뜻하지 않는다.

| DB / operation / Runtime 수 | 기준 → 변경 ms/cycle | 기준 → 변경 B/cycle |
|---|---:|---:|
| SQLite / create-delete / 1 | 7.730 → 7.824 | 738,567 → 607,590 |
| SQLite / create-delete / 4 | 10.768 → 10.284 | 739,382 → 608,245 |
| SQLite / rotate / 1 | 0.986 → 0.979 | 16,105 → 16,073 |
| SQLite / rotate / 4 | 1.266 → 1.236 | 16,209 → 16,171 |
| PostgreSQL / create-delete / 1 | 3.775 → 3.973 | 742,434 → 611,472 |
| PostgreSQL / create-delete / 4 | 3.773 → 3.499 | 742,973 → 611,930 |
| PostgreSQL / rotate / 1 | 0.769 → 0.767 | 19,558 → 19,525 |
| PostgreSQL / rotate / 4 | 0.681 → 0.626 | 19,604 → 19,571 |

할당 감소는 반복해서 관측되지만 일부 create-delete 시간은 늘었으므로 모든 DB 작업의 속도 개선을 주장하지 않는다.
Rotate는 row 수를 유지하며 ensureCapacity를 호출하지 않는다. Create의 inventory와 두 operation의 digest lookup·DB fence 비용을
구분한다. 모든 workload 종료 후 실제 inventory의 행 수·digest 유효성·중복 없음도 확인했다.

첫 SQLite 동시 benchmark는 busy timeout을 지정하지 않아 SQLITE_BUSY로 실패했다. 지원 계약대로 acquisition 실패를 전파한 것이며
제품에 retry를 추가하지 않았다. 성공 경합 비용 측정을 위해 fixture를 기존 waiting-fence profile로 고친 뒤 전후 전체를 다시 실행했다.

채택한 수정은 canonical lowercase ASCII hex의 동등 byte 검사뿐이다. 64개 위치 × 모든 256개 byte와 길이·대문자·Unicode를
stdlib decode/re-encode oracle로 대조한다. COUNT, 첫 만료 행을 찾자마자 삭제, payload decode 생략은 도입하지 않는다.
전체 스캔 제거에는 DB constraint·만료 metadata·손상 검사 책임을 함께 설계해야 한다. 현행 정책 유지 결정은
[ADR-0048](../adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)에 반영했다.

### 로컬 checkpoint

- F2/F5/F6: `go test -json -count=1 ./migrations ./web ./internal/projectcheck/protocol` — 3 packages·521 test pass, skip 0.
- F1: `go test -json -count=1 ./orm` — 457 test pass, skip 0. 외부 소비자는 아래 통합 checkpoint에서 실행했다.
- F3 초기 normal은 265 pass·PostgreSQL 환경 관련 10 skip, F4 초기 normal은 166 pass·4 PostgreSQL skip이었다.
  실제 DB 설정 뒤 아래 실행으로 해당 integration과 Article 흐름을 확인했다.
- `GODJ_REQUIRE_POSTGRES=1 go test -json -count=1 ./db/postgres ./examples/article ./examples/article/webapp` —
  3 packages·310 pass. 단독 실행용 `TestPostgresRevisionFenceHelperProcess` 1개만 전용 helper marker가 없는 부모 열거에서 skip하고,
  실제 cross-process integration의 자식 경로는 실행됐다. 서비스 필요 테스트의 미실행을 PASS로 세지 않았다.
- F7의 두 CLI 성공 helper 소비자 — 2 pass, skip 0. Separate actual output과 locked oracle을 실제 비교했다.
- `go test -json -count=1 -timeout=15m ./systemstate ./sessions ./db/sqlite ./codegen/consumertest` —
  4 packages·888 pass·skip 0. 실제 외부 Go module의 generated reverse/prefetch 소비자를 포함한다.
- `go test -json -count=1 -timeout=10m -run '^TestMigrationCommand' ./conformance/runners/godj` —
  47 pass·skip 0. Exact SQLite snapshot의 실제 상태·false-green 거부·catalog 손상·read-only·byte identity를 유지했다.
- 아래 관련 10 package는 race와 CGO-disabled 각각 1695 pass다. 두 모드 모두 PostgreSQL 필수 환경을 설정하고 실제 DB 테스트를
  실행했다. 위와 같은 subprocess 전용 helper 한 항목만 부모 열거에서 skip하며 테스트 실패·DB 누락은 없다.

```sh
GODJ_REQUIRE_POSTGRES=1 go test -race -json -count=1 -timeout=15m ./migrations ./orm ./query ./db/postgres ./systemstate ./codegen/consumertest ./web ./internal/projectcheck/protocol ./examples/article ./examples/article/webapp
GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -json -count=1 -timeout=15m ./migrations ./orm ./query ./db/postgres ./systemstate ./codegen/consumertest ./web ./internal/projectcheck/protocol ./examples/article ./examples/article/webapp
```

- `make docs-check format-check generate-check` — PASS. 문서 90개 링크, Helpdesk 12·Article 12·relationfixture 16개와
  별도 relationproduct의 checked-in generated fixture가 모두 현재 소스와 일치한다.
- 변경 영향 package의 `go vet`와 `git diff --check` — PASS.
- 로컬 검증과 benchmark 종료 후 이 작업에서 만든 전용 PostgreSQL DB만 삭제했고 기존 17.5 서비스가 계속 실행됨을 확인했다.
- 자체 diff 검토에서 F1의 callback field 복사/매회 검사, F3의 analysis/emission DFS와 null-negation, F4의 predicate 순서,
  F7의 setup context/cleanup 수명·독립 oracle, F8의 canonical ASCII grammar·전체 검사 후 DML을 대조했다.

### 동일 제품 소스의 Hosted full scope

- 제품·검증 소스: `b74a79eb4948ef6b57ef5271103cf05eb514d23e`.
- [CI 34466299719](https://github.com/progresshans/godj/actions/runs/34466299719), `workflow_dispatch`, `suite=full`, attempt 1:
  2026-09-10 20:04:40 KST `completed/success`. 62개 고유 job 모두 재실행 없이 `completed/success`다.
- 최종 aggregate `102845405925`의 실제 출력은 `scope: full`, `full_platform_verified: true`이며 8개 필수 owner가 모두 있다.
  현재 workflow의 62개 예상 job 이름·OS/arch/mode와 실제 목록을 대조해 누락·중복이 없고 source/run/attempt도 모두 같음을 확인했다.
- Linux/macOS amd64·arm64 × normal/race/CGO-disabled의 relation·project·command 조합과 Portable Go 12개 조합을 모두 통과했다.
- 같은 소스의 [PR feedback 34466280714](https://github.com/progresshans/godj/actions/runs/34466280714)는 `completed/success`다.
- Command 12개 job의 실제 로그를 전부 대조해 각각 operator 15 run/pass·targeted migrate 33 run/pass·skip 0과
  `verified_command_products: [operator, targeted]`를 확인했다.
- PostgreSQL 17.10 core/operator-target × normal/race/CGO-disabled 모두 완료됐다. 각 core는 12 packages·54 run/pass,
  operator-target은 2 packages·12 run/pass이며 여섯 로그 모두 skip 0이다.
- 고정 Darwin reference `102835624565`는 `PYTHON_SUITE_VERIFIED tests=274 skips=0`과 locked oracle 검사를 통과했다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7의 각 실행은 274개와 선언된 exact 전용 4개 skip 및 semantic digest를 통과했다.
- Reference job `102837127710`은 같은 run의 두 capture를 소비해 전체 conformance 대조와 Linux 32-bit compile·관계 실행을 완료했다.
- Project-check normal Linux job `102835625169`의 `GODJ_COLD_BUILD=1` 필수 선택은 1 package·2 run/pass·skip 0으로 완료됐다.
  같은 job의 일반 Runserver 선택에서 PostgreSQL service 미설정으로 skip한 한 항목은 PostgreSQL 전담 owner가 실제 실행했다.

| 실제 PostgreSQL owner | normal job | race job | CGO-disabled job |
|---|---|---|---|
| core | `102835624811` | `102835624837` | `102835624755` |
| operator-target | `102835624756` | `102835624740` | `102835624792` |

### 현재 capture의 독립 확인

공식 resolver로 같은 run의 성공한 producer를 선택하고 GitHub archive digest, repository/run/attempt/checkout,
payload SHA-256·`SHA256SUMS`를 확인했다. 두 공식 Go `Load`도 현재 소스의 profile·canonical JSON·behavioral source binding을 검증했다.

| capture | artifact / producer job / attempt | payload SHA-256 |
|---|---|---|
| SYS-020 | `10147775230` / `102835624811` / `1` | `3c6e2537b8aa456b1e37fef858362609db1096718fcc5bea0cd812d015e3b332` |
| SYS-029 | `10147738561` / `102835624756` / `1` | `1ae68529847f7e9697711bac83b786e73d225ad3f92b7537536f5c0318f8e5f8` |

SYS-020 source binding은 327 files / 3,749,392 bytes /
`ab344c55038c4d40570fa69c8e5c3e0b0288322ea94c6554af9089dac31134ee`,
SYS-029는 389 files / 3,488,968 bytes /
`13404d6eefb717c2067c409510355d695f9f66889323bcae3be76b4bb5bd43af`다.
GitHub archive SHA-256도 각각 `3fe181b8510173668b2dfe6d70eaa07165cdd5c78d9c9556724b40d3585b1921`,
`e874ffcb5170f299c2cb02209d390040aa84eb4baa1b531fc3c35c9c856f7ac5`와 일치했다.

SYS-020은 writer 두 process의 barrier·restart 보존과 divergence/loss/drift/secret 0을 확인했다.
SYS-029는 PostgreSQL·SQLite 각각 세 독립 process, Admin/API 인증·restart 유지와 state loss/schema drift/raw secret 0을 확인했다.

완료 기록은 CURRENT·TEST_EVIDENCE·GDJ-0069 작업 문서의 Markdown만 변경한다. 제품·검증 소스와 두 behavioral source binding은
Hosted 소스와 동일하며 문서 링크·상태·diff와 공식 capture Load를 별도로 확인한다. 기존 PR #1은 Draft로 유지한다.

## GDJ-0068 — 불변 값 전달과 검증 비용 정리

- 작업: [GDJ-0068](../../work/0068-immutable-value-transfer-and-verification-cost.md).
- 기준: `6e826a1acc41720b1df99acb7e488c71f24c2841`.
- 상태: 구현·전후 측정·관련 로컬 통합 검증과 보정 소스의 Hosted full scope 완료.
- 검증 소유권: affected local checkpoint 후 기존 Draft PR의 같은 제품 소스 Hosted full scope.

### 전후 측정

2026-09-10 KST, Go 1.26.5 darwin/arm64, Apple M3 Pro. 각 묶음은 해당 제품 변경 전에 benchmark를 추가해 기준을
측정했다. Generated construction의 기준은 ORM 변경 후 생성기를 바꾸기 직전의 checkpoint다.
마지막에는 다른 로컬 검증이 끝난 뒤 같은 호스트에서 package를 순차 실행했다. 아래는 각각 세 번의 중앙값이며
DB latency·전체 서비스 처리량·CI 시간을 측정한 결과가 아니다.

```sh
go test -p=1 -run '^$' -bench 'Benchmark(PrincipalResolve|ActiveSessionLoad|FormBindErrors|FormResultAccess|SerializerUnknownErrors|JSONDecoding|JSONEncoding|JSONRejectedString|TemplateLoop|TemplateRejectedEscape|ProjectStateEquality|ProjectStateAppChange|WideModelWrite|GeneratedConstruction)$' -benchtime=200ms -count=3 ./auth ./sessions ./forms ./serializers ./templates ./migrations ./orm ./codegen/consumertest
```

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| Principal resolve / 8 permissions | 240.9 → 32.93 | 416 → 0 | 4 → 0 |
| Principal resolve / 128 permissions | 2,371 → 31.04 | 8,952 → 0 | 6 → 0 |
| Active memory session load / 3 values | 1,039 → 361.4 | 1,648 → 0 | 21 → 0 |
| Form bind errors / 16 fields | 9,194 → 2,325 | 37,024 → 6,568 | 300 → 59 |
| Form bind errors / 256 fields | 940,227 → 34,087 | 4,362,293 → 108,072 | 35,220 → 783 |
| Form immutable result access / 16 fields | 866.2 → 16.29 | 4,176 → 0 | 8 → 0 |
| Serializer unknown errors / 16 fields | 2,603 → 937.1 | 10,640 → 3,304 | 22 → 27 |
| Serializer unknown errors / 256 fields | 326,611 → 11,779 | 1,984,533 → 57,832 | 266 → 275 |
| Serializer unknown errors / 1,024 fields | 5,359,130 → 47,784 | 31,876,024 → 242,024 | 1,037 → 1,048 |
| JSON decode / 16 array-valued members | 19,223 → 17,465 | 35,912 → 27,248 | 516 → 497 |
| JSON decode / 256 array-valued members | 297,454 → 271,844 | 573,090 → 436,810 | 7,969 → 7,710 |
| Template loop / 1,000 items | 278,860 → 178,383 | 1,042,235 → 50,704 | 5,757 → 2,759 |
| Template rejected escape / 1 MiB input, 64-byte cap | 1,974,799 → 24,619 | 10,485,856 → 96 | 4 → 2 |
| ProjectState equality / 16 apps | 13,680 → 1,055 | 26,808 → 0 | 135 → 0 |
| ProjectState app replacement / 16 apps | 2,568 → 592.5 | 10,408 → 2,760 | 41 → 6 |
| JSON rejected string / 64-byte document cap | 247,322 → 3,094 | 487,065 → 64 | 6 → 1 |
| JSON encode / 1 row | 1,718 → 883.7 | 1,352 → 744 | 29 → 6 |
| JSON encode / 100 rows | 159,792 → 81,108 | 185,383 → 153,000 | 2,022 → 19 |

Serializer unknown 오류는 작은 임시 collection의 할당 수가 조금 늘었지만 누적 prefix 복사와 총 할당량이 크게 줄었다.
JSON decode의 이득은 이 측정에서 약 9~10%이며 encoder·출력 거부 경로의 큰 변화와 구분한다.
Form/Session/Principal의 불변 반환만 복사를 줄였고 mutable getter, 외부 입력과 callback 소유권은 계속 검증했다.
Template과 JSON의 출력 거부는 입력 검증 자체를 생략하지 않으며 중간 escape 문자열을 만들지 않는다.

`BenchmarkWideModelWrite`는 O(1) field reader와 fake Mutator를 가진 4/32/256개 writable scalar field 모델이다.
쓰기 재사용과 constructor 비용을 따로 측정했다. 공개 metadata 입력은 복사하고 callback에 전달할 field도 매번 분리한다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| fields 4/Create | 812 → 661.5 | 1,872 → 1,392 | 6 → 5 |
| fields 4/Update | 858.1 → 708.6 | 1,968 → 1,488 | 7 → 6 |
| fields 4/Save | 1,065 → 561.2 | 2,944 → 2,464 | 8 → 7 |
| fields 4/SaveMask | 1,199 → 495 | 3,008 → 1,312 | 9 → 5 |
| fields 4/NewManager | 307.5 → 674.8 | 1,360 → 2,368 | 5 → 9 |
| fields 32/Create | 9,534 → 4,302 | 15,544 → 12,344 | 9 → 8 |
| fields 32/Update | 9,583 → 4,445 | 16,536 → 13,336 | 10 → 9 |
| fields 32/Save | 7,813 → 3,012 | 20,768 → 17,568 | 8 → 7 |
| fields 32/SaveMask | 10,108 → 3,444 | 23,112 → 12,488 | 12 → 8 |
| fields 32/NewManager | 1,279 → 3,481 | 7,600 → 14,560 | 5 → 13 |
| fields 256/Create | 361,115 → 33,600 | 122,808 → 95,544 | 9 → 8 |
| fields 256/Update | 334,514 → 34,655 | 132,504 → 105,240 | 10 → 9 |
| fields 256/Save | 257,598 → 22,677 | 168,480 → 141,216 | 8 → 7 |
| fields 256/SaveMask | 344,249 → 26,076 | 186,952 → 100,296 | 12 → 8 |
| fields 256/NewManager | 8,931 → 26,498 | 60,336 → 115,168 | 5 → 13 |

준비된 lookup을 추가해 `NewManager`의 시간과 메모리는 늘었다. 이 변경은 Manager를 반복 사용하는 경로를 위한 것이며,
일회성 생성·쓰기까지 무조건 빨라졌다는 뜻은 아니다. Save의 fallback insert는 전체 필드를 순서대로 한 번 읽고,
forced/masked update는 사용하지 않는 insert plan을 만들지 않는다.

다음은 실제 checked-in Article 생성 코드의 BuildCreate/BuildPatch와 관계 fixture의 storage.Field 호출이다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| Create | 169.8 → 83.08 | 752 → 320 | 3 → 1 |
| Patch | 119.2 → 52.1 | 544 → 112 | 3 → 1 |
| RelationStorage | 490.9 → 277.2 | 1,696 → 384 | 12 → 4 |

Compile fixture는 15개 positive와 29개 negative를 각각 독립 package로 두고 두 외부 module build로 검사한다.
각 package의 start·terminal, negative 자신의 build failure·diagnostic을 확인한다. Dependency failure, 누락·잘린 JSON,
중복 결과나 runtime skip을 fixture 성공으로 인정하지 않는다. No-test-files terminal은 compile-only package에서만 별도 증명한다.
동일한 두 top-level 검사의 단일 관측은 package 7.128→2.736초, negative 2.31→0.12초였다.
이는 반복 성능 실험이나 Hosted 단축 시간의 증명이 아니며, 실제 facade/architecture/부모·자식 race 검사는 별도로 유지했다.

주요 구현 소스 `aaf104b`의 제품·CLI·생성기 Go는 같은 `scripts/sourceinventory` 분류에서 80,619→80,448줄이다. Generated Go는 6,723→6,740줄이고,
회귀·측정 Go는 164,446→165,320줄이다. 순수 줄 수를 줄이기 위한 테스트 삭제는 하지 않았다.

### 로컬 변경 묶음

- `go test ./validation ./auth ./sessions ./forms/... ./serializers ./api/... ./admin ./web/...`: PASS.
  외부 snapshot, 세션 원본·파생값 분리, Form 초기값 동시 overlay·callback 보관 값, invalid permission·반복 Page 응답.
- `go test ./templates ./serializers ./migrations`: PASS. Nested/include loop scope·forloop object 의미,
  escape의 정확한 cap·오류 시 nil, JSON empty-name/duplicate/child/budget 우선순위, ProjectState zero/empty·상태별 소유권.
- `go test ./orm`: PASS. Full field reference, 순서가 다른 PK의 forced/fallback insert,
  callback metadata의 동시 변경 격리와 기존 write/save 회귀.
- JSON 직접 bounded 출력 후 `go test ./serializers`: PASS. `encoding/json`의 `SetEscapeHTML(false)`를 독립 비교값으로
  사용해 ASCII control, quote/backslash, Unicode/U+2028/U+2029와 정확한 document cap을 대조했다.
- `go test ./codegen ./codegen/consumertest ./orm ./internal/compiletest`: PASS. Standalone/bundle의 실제 선행 AST 충돌,
  정상·오용 ABI와 generated module의 실제 소비자 실행을 포함한다.
- SQLite·PostgreSQL assignment compiler의 quoting/duplicate/value 오류 순서와 SQLite ASCII column key 검사: PASS.
  이 checkpoint는 실제 PostgreSQL 서비스 실행 증거가 아니다.
- Testenv·두 attestation profile과 compile/generated environment 검사: PASS. 마지막 env 값·Windows case-fold 의미,
  offline flags·실제 부모 race·취소·helper source 변경에 의한 binding 무효화를 확인했다.
- Django planning/restart의 공유 관측 helper 검사 23개 PASS. Go actual과 Django expected는 합치지 않았다.
- CI 도구 전체 `PYTHONPATH=scripts/ci PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/ci -p 'test_*.py'`:
  36개 PASS. Command owner의 선택·미선택·실패·누락·skip과 CLI output을 확인했다.
- Actionlint v1.7.12로 `ci.yml`·`feedback.yml` PASS. `-shellcheck= -pyflakes=`로 실행했으므로 두 별도 도구 검사는 포함하지 않는다.

### 통합 checkpoint

영향 범위 25개 package를 아래 selector로 normal checkpoint 이후 race·CGO-disabled에서 실행했다.

```sh
go test -race -json -count=1 -timeout=15m ./validation ./auth ./sessions ./forms/... ./serializers ./templates ./migrations ./orm ./api/... ./admin ./web/... ./systemstate ./db/internal/queryplan ./db/sqlite ./db/postgres ./internal/testenv ./internal/compiletest ./codegen ./codegen/consumertest ./conformance/systemstate/attestation ./conformance/projectoperatorproduct/attestation
```

CGO-disabled는 같은 명령에서 `-race`를 빼고 `CGO_ENABLED=0`을 적용했다. 각 exit status와 전체 JSON terminal을 검사했다.
Race는 25 packages·2,955 pass/2,965 run, CGO-disabled는 25 packages·3,006 pass/3,016 run이다.
각각 PostgreSQL integration·helper 10개가 로컬 서비스 환경 부재로 skip됐다. 실제 PostgreSQL 검증은 Hosted 전담 owner가 맡는다.

실제 command 검증은 `go test -p=1 -json -count=1 -timeout=25m`에서 아래 20개 top-level test만 `-run`으로 선택했다.
모든 하위 test를 포함하며 필수 목록을 `go_test_events.py --required ... --no-skips`로 확인했다.
결과는 7 packages·120 run/pass·skip 0이다.

| package (`conformance/` 아래) | 필수 top-level test |
|---|---|
| projectmigrateproduct | `TestGlobalMigrateArticleSQLiteProduct`, `TestGlobalMigrateSQLiteMiddleFailureAndFreshResume`, `TestGlobalMigrateArticleSQLiteFullMIG096Concurrency`, `TestGlobalMigrateAuthenticatedArticleRestartDurability` |
| runserverproduct | `TestRunserverPublicOnlyEnvironmentsDiscardAmbientArticleCredentials`, `TestGlobalRunserverPublishesAuthenticatedArticleAdminAndAPI`, `TestGlobalRunserverArticleSQLiteDevelopmentLoop`, `TestGlobalRunserverRejectsStaleCopiedArticleBeforeRuntime`, `TestRunserverHarnessForcedCleanupIncludesSeparateDescendantGroup` |
| migrationwriterproduct | `TestMigrationWriterExternalProjectSQLitePublicSurface` |
| projectshowmigrationsproduct | `TestGlobalShowMigrationsExternalProjectSQLiteProduct` |
| projectsqlmigrateproduct | `TestGlobalSQLMigrateExternalSQLiteProduct`, `TestSQLProductRunnerPipelineExecutionControls`, `TestSQLProductRunnerSourceBoundaries` |
| projectoperatorproduct | `TestOperatorSanitizeEnvironmentDropsHostOnlyControls`, `TestGlobalCreatesuperuserExternalSQLiteProduct`, `TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity`, `TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation`, `TestOperatorCountRawSecretOccurrencesDetectsAuditMarker` |
| projectmigratetargetproduct | `TestProjectLinkedTargetedMigrateSQLite` |

- 고정 Python/Django/DRF exact: `PYTHONDONTWRITEBYTECODE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv tool run --from uv==0.10.12 uv run --project conformance/reference/drf --frozen python -m scripts.ci.python_tests --profile exact`:
  PASS, `PYTHON_SUITE_VERIFIED tests=274 skips=0`. Lock을 변경하지 않았다.
- `make docs-check format-check generate-check`: 최종 PASS. Helpdesk 12·Article 12·relationfixture 16개와 별도 metadata fixture를 확인했다.
- 최종 영향 package의 `go vet`와 `git diff --check`: PASS.

초기 compile에서 삭제한 namespace validator 호출과 이동한 environment import가 남아 실패했으며 함께 정리한 뒤 통과했다.
초기 `make generate-check`는 별도 `relationproduct`의 main 생성물 두 파일이 오래되어 실패했다. 두 파일을 재생성하고 전체 drift와
해당 소비자의 normal·race·CGO-disabled를 다시 실행해 모두 PASS를 확인했다. 첫 runtime benchmark의 package는 모두 PASS였지만
출력 wrapper가 zsh readonly `status` 대입으로 실패했다. 원래 완료 로그와 측정값을 확인했고 최종 측정 wrapper도 정상 종료했다.

CI는 같은 OS/arch/mode의 operator·targeted command를 한 job에서 차례로 실행해 checkout·Go·의존성 준비를 공유한다.
각 test selector·필수 sentinel·no-skip·timeout·normal vet를 유지하며 첫 제품 실패 뒤에도 다음 제품과 최종 outcome gate를 실행한다.
선택된 제품의 누락·skip은 성공이 될 수 없다. 전체 platform·PostgreSQL·capture·cold CLI·32-bit의 최종 증거는 아래 Hosted 결과다.

### 첫 Hosted 구성 검사 실패와 보정

첫 제출 소스 `aaf104b6ada9444c1683f8d9a13f62cc218618a7`의
[full scope 34447460391](https://github.com/progresshans/godj/actions/runs/34447460391), attempt 1에서
`TestWorkflowRetainsDeclaredCoordinatesAndModes`와 `TestWorkflowRequiredProductSentinelsRemainInventoried`가 실패했다.
두 검사는 통합 전 `project-operator-product-matrix` 이름을 참조하고 있었다. Linux normal job `102775334624`,
arm64 CGO-disabled `102775334610`과 macOS race `102775334557`의 실제 실패 로그로 확인했다.

검사를 새 command owner로 연결하고 각 product step을 분리해 mode·timeout·vet·실행 조건·필수 sentinel·no-skip을
확인하도록 보강했다. 두 step의 outcome을 최종 gate에 전달하는 검사도 유지했다.
보정 뒤 `go test -count=1 -timeout=10m ./conformance/internal/protocol`의 normal·race·CGO-disabled 모두 PASS다.
제품 구현은 바꾸지 않았으나 검증 source binding이 바뀌므로 보정 소스에서 전체 Hosted와 capture를 새로 실행한다.
첫 실행의 일부 성공한 job이나 capture를 최종 전체 검증으로 재사용하지 않는다.
보정 실행을 요청한 뒤 첫 실행의 남은 작업은 취소됐고 최종 conclusion은 `cancelled`다.

### 보정 소스의 Hosted full scope

- 제품·검증 소스: `ffe384492bee0b98aec918d594f6c17531797280`.
- [CI 34447908999](https://github.com/progresshans/godj/actions/runs/34447908999), `workflow_dispatch`, `suite=full`, attempt 1:
  2026-09-10 16:31 KST `completed/success`. 이 새 실행의 62개 고유 job 모두 재실행 없이 `completed/success`다.
- 최종 aggregate `102784486761`의 실제 출력은 `scope: full`, `full_platform_verified: true`다.
  Command·reference·exact Darwin·portable Go·PostgreSQL·project check·Python compatibility·relation의 8개 필수 owner를 확인했다.
- 같은 소스의 [PR feedback 34447900215](https://github.com/progresshans/godj/actions/runs/34447900215)도 `completed/success`다.
- Linux/macOS amd64·arm64 × normal/race/CGO-disabled의 relation·project·command 조합과 Portable Go 12개 조합을 모두 통과했다.
  Command 12개 job의 실제 로그를 전부 대조해 각각 operator 15 run/pass, targeted migrate 33 run/pass, skip 0과
  `verified_command_products: [operator, targeted]`를 확인했다.
- PostgreSQL 17.10 core/operator-target × 세 mode 모두 PASS. 각 core는 12 packages·54 run/pass,
  operator-target은 2 packages·12 run/pass이며 여섯 로그 모두 skip 0이다.
- 고정 Darwin reference는 `PYTHON_SUITE_VERIFIED tests=274 skips=0`과 locked oracle 검사를 통과했다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7은 각각 274개·선언된 exact 전용 4개 skip과 semantic digest를 통과했다.
- Reference job `102778261410`은 같은 run의 두 capture를 소비해 전체 conformance 대조와 Linux 32-bit compile·관계 실행을 완료했다.
  Cold CLI job `102776873840`의 `GODJ_COLD_BUILD=1` 필수 선택은 1 package·2 run/pass·skip 0이다.
  같은 job의 일반 Runserver 선택에서 PostgreSQL service 미설정으로 skip한 한 항목은 PostgreSQL 전담 owner가 실제 실행했다.

| 실제 PostgreSQL owner | normal job | race job | CGO-disabled job |
|---|---|---|---|
| core | `102776873451` | `102776873485` | `102776873467` |
| operator-target | `102776873391` | `102776873456` | `102776873526` |

Full job 수는 직전 74개에서 62개로 줄었다. Workflow 생성부터 마지막 job 완료까지는 직전
[34432064345](https://github.com/progresshans/godj/actions/runs/34432064345)의 36분 21초에서 이번 29분 59초로 줄었다
(07:01:24→07:31:23 UTC). 이는 runner 대기를 포함한 각 한 번의 관측이다. 소스·cache·배정 조건이 다른 실행이므로
6분 22초 전체를 job 통합만의 효과로 해석하지 않는다. 제거한 12개 checkout·toolchain·dependency 준비와 실제 각 검증의 완료는 확인했다.

### 현재 capture의 독립 확인

성공한 normal producer의 artifact를 공식 resolver로 선택하고 GitHub archive digest, repository/run/attempt/checkout,
payload SHA-256과 `SHA256SUMS`를 검증했다. 두 공식 Go `Load`도 현재 소스의 profile·canonical JSON·behavioral source binding을 확인했다.

| capture | artifact / producer job / attempt | payload SHA-256 |
|---|---|---|
| SYS-020 | `10140459061` / `102776873451` / `1` | `2299e5f562775927b6cd3f9e6015844b32875e48c1cba9f4823373bcf9d9267f` |
| SYS-029 | `10140444503` / `102776873391` / `1` | `6a9151f6e8a1bc1e0b58ef4b5aac07380e6e421270e2fe88a9e6443e64786f48` |

SYS-020의 source binding은 327 files / 3,750,356 bytes /
`bd244ee50dea57f95fd5a25dc911ecb3ab941592b2157d69f3c401f4ee08579c`,
SYS-029는 389 files / 3,489,542 bytes /
`a00eb7b61fd71685b7d06e3d15239459d8be7316dca550fd56770ac4559653bf`다.
각 GitHub archive digest는 `95f3c120f51c6007f3335c7cd902cc29e53a7468bc89644d9cdd8c3f51ebb026`과
`6a6683d56946b1d7cbebde754d2351b38e8fe570574430f998be981699ea9b8c`이며 다운로드한 archive와 일치했다.

SYS-020은 writer 두 process의 barrier·restart 보존과 divergence/loss/drift/secret 0을 확인했다.
SYS-029는 PostgreSQL·SQLite 각각 세 독립 process, Admin/API 인증·restart 유지와 state loss/schema drift/raw secret 0을 확인했다.

완료 기록은 CURRENT·TEST_EVIDENCE·GDJ-0068 작업 문서의 Markdown만 변경한다. 제품·검증 소스와 두 behavioral source binding은
Hosted 소스와 동일하며, 문서 링크·상태·diff와 공식 capture Load를 별도로 확인한다. 기존 PR #1은 Draft로 유지한다.

## GDJ-0067 — 불변 준비와 실행 비용 정리

- 작업: [GDJ-0067](../../work/0067-immutable-preparation-and-execution-cost.md).
- 기준 제품: `9c21568dcbd7bb33c817d6e830a44026d2554e32`.
- 상태: 구현·로컬 통합 검증·동일 제품 소스 Hosted full scope 완료. 각 baseline은 해당 제품 변경 전 실행했다.
- 환경: 2026-09-10 KST, Apple M3 Pro, Go 1.26.5 darwin/arm64.

`go test -run '^$' -bench 'Benchmark(JSONEncoding|JSONNestedConstruction|TemplateComposition|TemplateLoop)$' -benchtime=200ms -count=3 ./serializers ./templates`

각 값은 세 실행의 중앙값이다. Encoding은 10개 문자열 필드를 가진 1/100행, construction은 중첩 object 생성이다.
기존 template composition은 1,000개 불변 child 공유, loop는 1,000개 항목 렌더링을 측정한다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| JSON encode / 1 row | 3,258 → 1,740 | 4,169 → 1,352 | 87 → 29 |
| JSON encode / 100 rows | 307,603 → 158,443 | 498,217 → 185,383 | 8,020 → 2,022 |
| JSON nested construction / depth 8 | 1,394 → 829.0 | 3,072 → 3,072 | 24 → 24 |
| JSON nested construction / depth 64 | 51,506 → 6,471 | 24,576 → 24,576 | 192 → 192 |
| Template loop / 1,000 items | 280,251 → 289,060 | 1,046,343 → 1,042,220 | 5,759 → 5,757 |
| Template inheritance / depth 1 | 222.5 → 131.5 | 96 → 56 | 5 → 3 |
| Template inheritance / depth 8 | 1,024 → 169.7 | 320 → 56 | 8 → 3 |
| Template inheritance / depth 32 | 4,116 → 204.4 | 1,088 → 56 | 10 → 3 |
| ORM reused Manager.Using / 4 fields | 2,022 → 23.17 | 3,457 → 48 | 37 → 1 |
| ORM NewManager / 4 fields | 1.773 → 2,070 | 0 → 4,210 | 0 → 40 |
| Reverse prefetch / 20 owners, 1,000 rows | 162,051 → 151,262 | 476,520 → 465,114 | 8,426 → 7,390 |

Inheritance은 같은 `-benchtime=200ms -count=3` 조건의 `BenchmarkTemplateInheritance`를 template 제품 변경 전후에 실행했다.
Template loop는 복사·할당은 줄었지만 이 측정의 시간은 약 3% 증가했다. 전체 render가 빨라졌다고 일반화하지 않는다.
ORM은 `Benchmark(ManagerPreparation|ReversePrefetch)$`를 제품 변경 전후에 같은 조건으로 실행했다.
준비 비용을 `NewManager`로 옮겼으므로 재사용하는 Manager의 `Using`이 측정 대상이며, 일회성 constructor가 공짜라고 주장하지 않는다.
Prefetch는 pointer field가 있는 모델과 fake row source로 복사·그룹·cache 비용을 측정하며 DB latency를 포함하지 않는다.

### 로컬 변경 묶음

- `go test ./auth ./web/sessionauth`: PASS. 난수 panic의 원래 값 전파·후속 병렬 해싱,
  clock/entropy panic을 HTTP 500으로 복구한 뒤 다음 요청의 CSRF token/cookie 발급을 확인했다.
- `go test ./serializers`: PASS. 문자열 escape·독립 동시 출력·공유 subtree의 출력 budget·정수 경계·기존 오류 우선순위.
  초기 compile에서 삭제한 `validObject`의 Spec.Bind 호출이 남아 실패했고, 같은 불변 object 검증 경계로 전환한 뒤 통과했다.
- `go test ./templates ./apps ./forms/... ./web`: PASS. 상속·nested block·include scope·깊이 실패 위치·동시 render와 기존 입력/라우팅 회귀.
- 추가 후보: JSON 정수 출력의 임시 slice 할당과 salt read 뒤 취소된 PBKDF2 계산을 제거했다. 해싱 work profile과 오류 종류는 유지했다.
- `go test ./orm`: PASS. metadata 입력/getter와 쓰기 callback mutation 격리, using별 독립 평가, 기존 relation/prefetch/eager 회귀.
- `go test ./db/sqlite ./db/postgres`: PASS. 각 dialect의 JOIN/nullable/alias/오류·SQL 결과 회귀. PostgreSQL 서비스 의존 테스트는
  로컬 환경에서 skip하며 실제 PostgreSQL 검증은 아직 Hosted owner에 남아 있다.
- `go test -count=1 -timeout=25m ./internal/compiletest ./codegen/consumertest`: PASS. 전체 정상·오용 ABI와 생성 소비자.
  최종 `-count=1`·checksum 복사·완전 offline 설정 뒤 생성 소비자 전체를 normal·race·CGO-disabled로 다시 실행해 통과했다.
- `make python-test ci-tools-test`: normal `PYTHON_SUITE_VERIFIED tests=274 skips=4`, CI 도구 34개 PASS.
  Skip은 선언된 exact 전용 네 검사다. DRF 직접 관측은 모두 실행했다.
- Attestation 두 profile normal과 migration/SQLMigrate/ShowMigrations outer flow normal: PASS.
  공유 source validation에서도 profile별 자원 한도·타입·digest 범위를 유지하고, 명령별 runner code·취소·비정상 process 분류를 확인했다.

### 통합 checkpoint

| 실제 실행 | 결과와 범위 |
|---|---|
| `go test -run '^$' ./...`, `go vet ./...` | PASS. 전체 패키지 compile·정적 분석. 전체 테스트 실행을 의미하지 않는다. |
| API·Admin·SystemState·Sessions·Article·Helpdesk normal | PASS. 새 Manager metadata 준비를 사용하는 실제 읽기·쓰기·HTTP·인증 흐름. |
| Forward object·prefetch·select·delete 제품 normal | PASS. 프로젝트에 연결된 생성 모델과 실제 SQLite 관계·cache·NULL·rollback 관측. |
| Migrate SQLite 외부 제품·중간 실패/재개·인증 restart | PASS. 세 필수 sentinel과 하위 항목을 `go_test_events.py`로 검증: 1 package, 10 run/pass, skip 0. |
| Auth·SessionAuth·Serializer·Template·ORM·SQLite/PG compiler·Attestation 두 profile·생성 소비자 race | 각 패키지 PASS. 초기 합동 실행은 `internal/compiletest`의 공용 `repositoryRoot`가 `!race` 파일에 남아 compile 실패했다. 공통 파일로 옮긴 뒤 해당 패키지의 race·normal·CGO-disabled 재검증 PASS. |
| 같은 영향 범위 CGO-disabled | PASS. 외부 ABI 오용과 생성 소비자 전체 포함. PostgreSQL 서비스 I/O는 로컬에서 검증하지 않았다. |
| Migration command 공통 분류와 세 outer 흐름 race | PASS. runner code·취소·cleanup·잘못된 process failure 거부 유지. |
| 고정 Python/DRF exact | uv 0.10.12, Python 3.14.3, Django 6.1, DRF 3.18.0. `PYTHON_SUITE_VERIFIED tests=274 skips=0`; 고정 byte/hashseed 관측 포함. |
| `make docs-check format-check generate-check`, actionlint v1.7.7 | PASS. Helpdesk 12·Article 12·relation 16개 생성 파일 clean, checked-in 생성물 검사와 Actions 문법 검증. |

Exact Python은 `uv tool run --from uv==0.10.12 uv run --project conformance/reference/drf --frozen python -m scripts.ci.python_tests --profile exact`로
실행했고 `PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC`를 적용했다. Host uv가 다른 버전이어도 lock을 변경하지 않는다.
새 JOIN helper와 CI 필수 실행 목록 변경이 두 behavioral source binding을 무효화하는 mutation test도 통과했다.
PostgreSQL projection 충돌은 기존 category/code를 유지하고 상세 메시지에 해당 edge 이름을 추가했다.

실제 제품·생성기·CLI Go 코드는 기준 80,658줄에서 80,619줄로 줄었다(`scripts/sourceinventory`의 같은 분류).
회귀·성능 측정 코드는 추가했으며 생성된 45파일/6,723줄은 바꾸지 않았다. 대규모 줄 수 절감이나 전체 서비스 처리량의 개선을 주장하지 않는다.

### Hosted 통합 검증

- 제품 소스: `56303abeefd1c911ad5954cd062a2e4ed67ce41e`.
- 실행: [34432064345](https://github.com/progresshans/godj/actions/runs/34432064345), `workflow_dispatch`, `suite=full`, attempt 1.
- 최종 결과: 2026-09-10 12:43 KST `completed/success`. 74개 고유 job이 모두 completed/success이며 attempt 1에서 재실행 없이 통과했다.
  최종 aggregate job `102736316913`의 실제 출력은 `scope: full`, `full_platform_verified: true`이며 아홉 필수 owner를 확인했다.
- Reference·현재 capture 소비, 전체 OS/arch/mode와 Linux 32-bit compile·ORM/관계·runner 실행을 확인했다.
  macOS Intel relation normal은 26 packages·2,906 run/pass·skip 0, race는 26 packages·2,871 run/pass·skip 0이다.
  마지막 targeted migration normal은 1 package·33 run/pass·skip 0으로 끝났다.
- PostgreSQL 17.10 core/operator-target의 normal·race·CGO-disabled 여섯 작업은 completed/success이며 실제 job 로그의
  완료 gate도 각각 core 12 packages·54 run/pass, operator 2 packages·12 run/pass, 모두 skip 0이다.
- Python compatibility 3.12.13·3.13.15·3.14.3·3.14.7은 각각 `PYTHON_SUITE_VERIFIED tests=274 skips=4`와 semantic digest를
  통과했다. Hosted exact Darwin은 274개·skip 0과 locked oracle 검사를 통과했다. 네 skip은 해당 exact owner가 실제 실행했다.
- Cold CLI 전담 선택은 `GODJ_COLD_BUILD=1`에서 1 package·2 run/pass·skip 0으로 통과했다. 같은 job의 Runserver 일반
  선택에서 PostgreSQL 환경 부재로 skip한 한 항목은 위 PostgreSQL 전담 여섯 작업 중 core가 실제 실행했다.
- 정상 producer가 게시한 두 capture를 현재 run의 artifact ID·producer attempt/job으로 선택했다. Python의 checkout/run/
  provenance/payload checksum 검사와 공식 Go `Load`의 profile·behavioral source 검사가 모두 통과했다.

| capture | artifact / producer job / attempt | payload SHA-256 |
|---|---|---|
| SYS-020 | `10134915349` / `102729493640` / `1` | `6f86d690ce1fcf66a5b06cb997cb1fefa5d08c7cd13fd3b31f80f8bdae1df9fe` |
| SYS-029 | `10134892644` / `102729493575` / `1` | `290c7ae34481a0635f393f4b8c0bfb10369a5dbe3af49f17cd7238cf06ac28b8` |

SYS-020의 source binding은 325 files / 3,746,834 bytes /
`8d22988e05578d6da4ca54284c41543e7b2ae538848dbdaaf43ac2cda44fc083`,
SYS-029는 386 files / 3,496,180 bytes /
`ed8afb62d161a0366fc4901cd59764bc14f162bfddbb2d99b7f28cb5d17b94bc`다.
둘 다 고정한 제품 소스의 로컬 계산과 일치한다. SYS-020은 barrier/restart 유지와 divergence/loss/drift/secret 0,
SYS-029는 PostgreSQL·SQLite의 세 독립 process, Admin/API 인증·restart 유지와 state loss/schema drift/raw secret 0을 확인했다.

제품 검증 뒤 마감 변경은 CURRENT·TEST_EVIDENCE·GDJ-0067 작업 문서 세 Markdown 파일이다. 제품 source diff는 없고
두 behavioral source binding·capture Load 결과가 검증 당시와 같다. 문서 링크·상태·diff 검사를 별도로 적용한다.


## GDJ-0066 — 실행 비용과 중복 책임의 측정 기반 정리

- 작업: [GDJ-0066](../../work/0066-measured-runtime-and-validation-optimization.md).
- 기준 제품: `d6443fa048ffdf4e22e0d817f4e8d0aaeb83e8da`, 기존 Draft PR #1의 동일 작업 브랜치.
- 상태: 구현·비교 측정·관련 로컬 검증과 최종 Hosted full scope 완료. Baseline은 제품 변경 전 비교 benchmark와 문서만 추가한 작업 사본이다.
- 환경: 2026-09-10 KST, Apple M3 Pro, Go 1.26.5 darwin/arm64.

### 변경 전 측정

`go test -run '^$' -bench 'Benchmark(PlanDerivation|LoadedAncestorIndex|AuditPruneFullSQLite|ActiveSessionLoad)$' -benchtime=200ms -count=3 ./query ./migrations ./systemstate ./sessions`

각 행은 세 번 실행한 ns/op의 중앙값이다. DB/prune 준비와 migration graph 구성은 benchmark loop 밖이다.
Audit는 실제 file-backed SQLite에 보존 한도만큼 ID를 채운 뒤 prune query를 반복한다. HTTP 처리량 또는 전체 CI 시간의 측정은 아니다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| Plan order / 64 fields | 776.1 → 25.53 | 9,120 → 80 | 4 → 1 |
| Plan filter / 64 fields | 782.1 → 25.93 | 9,040 → 0 | 3 → 0 |
| Plan order / 256 fields | 2,453 → 25.62 | 35,616 → 80 | 4 → 1 |
| Plan filter / 256 fields | 2,446 → 25.77 | 35,536 → 0 | 3 → 0 |
| Ancestor index / 128 chain | 113,634 → 13,195 | 42,920 → 18,264 | 140 → 8 |
| Ancestor index / 2,048 chain | 28,482,495 → 394,881 | 1,229,330 → 803,025 | 2,078 → 14 |
| Full audit prune / 10,000 rows | 1,246,068 → 595,376 | 319,593 → 2,752 | 29,782 → 53 |
| Full audit prune / 100,000 rows | 12,535,509 → 6,096,642 | 3,199,626 → 2,768 | 299,782 → 53 |
| Active memory session Load | 1,275 → 1,039 | 2,112 → 1,648 | 26 → 21 |
| Eager ready-related cache / one row | 2,177 → 200.6 | 3,921 → 584 | 45 → 5 |

변경 후에는 위 selector에 `ForwardSelectedReadyCache`와 `./orm`을 추가해 같은 조건으로 실행했다.
Eager의 기준은 Query storage 변경 후·eager 최적화 전 작업 사본에서 별도로 측정한 값이다.
시간·할당 모두 세 실행의 중앙값이며 개선 비율은 이 microbenchmark 범위다. 감사 prune은 DB 내부 bounded scan을 계속 수행한다.
관계 Count는 실제 JOIN multiplicity·Distinct·slice·NULL fixture에서 한 aggregate 행을 소비함을 확인했고,
영속 Manager.Load는 transaction 1회·행 조회 1회, timestamp 변화 시 write 1회·동일 값일 때 write 0회를 확인했다.

### 로컬 checkpoint

| 실제 실행 | 결과와 보존한 검증 |
|---|---|
| 전체 `go test -run '^$' ./...` | PASS. 새 Query/Store API와 checked-in 소비자 전체 compile. 테스트 실행 PASS를 의미하지 않는다. |
| Query/ORM/SQLite/PostgreSQL compiler/systemstate normal | PASS. 생성 시점 오류·불변 소유권·관계 Count와 MIN, rows/cardinality/close/cancel과 audit rollback. PostgreSQL 서비스 필요 10개는 로컬 skip이며 Hosted가 소유한다. |
| `./sessions ./systemstate ./admin ./web/sessionauth` normal | PASS. atomic access interleaving, 저장 전 정책 검증, missing-before-clock, 시계·entropy panic cleanup, 한 transaction/한 read와 실제 인증 흐름. |
| eager 변경 뒤 `./orm` normal | PASS. 독립 ready cache·Fresh·nullable·projection·취소·모델 복사 회귀. |
| `./query ./orm ./migrations ./sessions ./systemstate ./db/internal/queryplan ./db/sqlite ./internal/gobuild` race | PASS. 실제 테스트가 없는 공통 queryplan 패키지는 compile만 수행하며 호출 backend의 검사가 해당 경로를 검증한다. |
| Query/ORM/migrations/sessions/systemstate/SQLite/PostgreSQL CGO-disabled | PASS. PostgreSQL 서비스 의존 10개 skip은 Hosted 검증 전까지 미실행이다. |
| 다섯 외부 SQLite 제품 sentinel normal | writer·targeted migrate·operator·showmigrations·sqlmigrate 모두 실제 build/child/DB 실행 PASS. `go_test_events.py`가 필수 sentinel·package 완료·no-skip을 확인했다. |
| `make generate-check` | PASS. Helpdesk 12·Article 12·relation fixture 16개 생성 파일 clean, checked-in generated test PASS. |
| PostgreSQL raw catalog negative control | PostgreSQL 17.5 별도 임시 cluster에서 PASS. 컬럼 null/default, expression/partial index, sequence, internal FK trigger, policy/view와 cancellation을 확인하고 cluster를 종료·정리했다. 요구 profile 17.10은 Hosted에서 검증한다. |
| 고정 Python/DRF reference 환경 | uv 0.10.12, Python 3.14.3, Django 6.1, DRF 3.18.0으로 `GODJ_EXACT_PROFILE=1` 및 complete discovery gate 실행: `PYTHON_SUITE_VERIFIED tests=274 skips=0`. 고정 reference byte/hashseed 대조 포함. |
| CI 도구·Actions | `scripts/ci` unittest 33개 PASS; actionlint v1.7.7 PASS. 새 PostgreSQL catalog sentinel을 core required/no-skip inventory에 포함했다. |

초기 Query API 전환에서 기존 unchecked 입력 테스트가 실패해 생성자 오류 경계로 수정했다. 초기 Python 실행은 host uv 0.12.3
profile 불일치와 DRF 없는 root 환경으로 실패·skip했으며, 고정 uv와 별도 DRF 환경의 완료 gate로 위 결과를 얻었다.
예전 reference/oracle/lock·생성물은 바꾸지 않았다. 초기 실패와 아래 확정된 실행 결과를 구분한다.

### 제품 소스의 Hosted 검증

[CI 34385261400](https://github.com/progresshans/godj/actions/runs/34385261400)는 제품 소스
`b028b77fa27f680c82f4cb188d5af1088dbb61c2`를 workflow dispatch의 `full` scope로 검증했다.
이벤트 source와 실제 checkout은 같은 commit이다. 2026-09-10 03:26 KST의 최종 conclusion은 success이며,
74개 고유 job의 최신 결과가 모두 completed/success다. 최종 aggregate job `102592262213`은 `scope: full`,
`full_platform_verified: true`와 아홉 필수 owner의 성공을 확인했다. Linux/macOS amd64·arm64의 normal/race/CGO-disabled,
PostgreSQL 17.10·cold build·고정 oracle·Python 호환성 및 선택한 32-bit Linux compile/관계 실행을 포함한다.

Attempt 1의 macOS Intel relation normal job `102579851988`은 `TestExternalConsumerCompiles`의
`project_external_consumer.go.txt`에서 module checksum 확인 중 `proxy.golang.org` DNS `i/o timeout`으로 실패했다.
변경하지 않은 compile harness의 의존성 조회 실패이며, 이 실행을 제품 PASS로 계산하지 않는다. 최초 aggregate도 그
owner 실패를 정확히 거부했다. 실행 종료 후 제품 변경 없이 실패 job과 aggregate만 attempt 2로 재실행했다.
재시도 job `102590752409`는 26 packages·2,903 run/pass·skip 0으로 성공했다. 기존 72개 성공 결과는 유지했으며,
API의 시작/종료 시각 대조로 두 job만 실제 재실행된 것을 확인했다.

PostgreSQL 17.10 core/operator-target의 normal·race·CGO-disabled 여섯 작업은 모두 success다.
각 core는 12 packages·54 run/pass, operator-target은 2 packages·12 run/pass이며 모두 skip 0이다.
공통 catalog 관측기의 물리 변경 대조와 관계 Count/MIN의 실제 DB 실행을 포함한다.
Linux amd64 normal의 cold external CLI milestone도 `GODJ_COLD_BUILD=1`, 1 package·2 run/pass·skip 0으로 완료했다.

Python 3.12.13/3.13.15/3.14.3/3.14.7 호환성 작업은 각각 `PYTHON_SUITE_VERIFIED tests=274 skips=4`로 완료했다.
그 4개는 고정 profile 전용 검사이며 exact darwin/arm64 작업이 별도로 실행했다. Exact 작업의 root Python suite는
274 tests·DRF 전용 3 skips, locked oracle 대조는 성공했다. DRF 검사는 별도 의존성을 갖춘 호환성 작업과
위 로컬 고정 DRF 환경의 274 tests·skip 0 결과가 검증한다. 비대상 skip을 실제 실행으로 계산하지 않는다.
Linux reference 작업의 root Python suite는 같은 고정 profile 4개와 DRF 3개를 제외한 274 tests·7 skips였다.
그 작업의 reference catalog·GoDj product expectation 대조, generated drift 및 32-bit Linux 검사는 모두 성공했다.

두 capture는 같은 run의 성공한 attempt 1 normal producer가 생성했다. `capture_artifact.py resolve`로 실제 producer job과
artifact ID를 확인하고, `verify`로 repository/run/attempt/checkout·payload checksum을 검증했다.
두 Go consumer의 `Load`도 현재 제품 작업 사본에서 canonical format·profile·checksum·behavioral source binding을 검증했다.
Hosted conformance consumer도 같은 artifact ID와 producer attempt 1을 확인해 제품 동작 대조를 완료했다.
최종 run attempt 2에서도 resolver와 provenance 검사는 성공한 producer attempt 1을 올바르게 선택·검증했다.
SYS-020의 loss/divergence/drift/secret은 모두 0이며 restart와 barrier 조건을 만족한다. SYS-029의 실제 PostgreSQL·SQLite
두 backend 모두 Admin/API 인증과 서로 다른 세 process의 provision/runtime/restart를 확인했다.

| Capture | Artifact / producer job | Payload SHA-256 | Source binding |
|---|---|---|---|
| SYS-020 two-process | `10117570038` / `102579851272` | `58c41d0f78e08b21f6cdc642516ba88cf3376f889c1822bb3bcb072b4a536305` | 322 files / 3,742,404 bytes / `2482656983e81f20966666af7de5a7a1728b1c4460524b7e02811a63e4e8b04e` |
| SYS-029 external operator | `10117537379` / `102579851285` | `1ac5c01249a676fd5750cafd97be876256fc78d01d6ca1dc4b6d94862591cd43` | 383 files / 3,492,733 bytes / `b20cb7120a6a9ca958bdf66424d0132f475dfb7fc4dbdcece7100ea9731defd8` |

최종 제품 검증 이후에는 ADR-0039·0044, CURRENT, 이 실행 증거와 work의 Markdown 다섯 파일만 정리했다.
제품 소스 diff가 없고 두 behavioral source binding이 위 값과 같음을 확인했다. 후속 문서에는 링크·상태·diff 검사를 적용한다.

## GDJ-0065 — 코드와 검증 체계의 중복 정리

- 작업과 감사 항목별 처리: [GDJ-0065](../../work/0065-codebase-refactoring.md).
- 기준: `341659d290ed0d344e1db51de086c5d4baac5f4e`, 기존 Draft PR #1의 동일 작업 브랜치.
- 로컬: 2026-09-08 KST, Go 1.26.5 darwin/arm64, 기준 위 변경 작업 사본.
- 상태: 영역별 구현·affected 검증·process smoke·독립 리뷰와 수정 소스의 Hosted full scope를 완료했다.

### 로컬 checkpoint

| 실제 실행 | 결과와 범위 |
|---|---|
| `./templates ./serializers ./admin ./examples/article/articleapp` normal | PASS. 외부 값·metadata 변경 격리, forloop first/last와 중첩 scope, 준비된 encoder/projector, Article no-op·update/patch·rollback을 확인했다. 후속 loop scope와 Admin index 정리 뒤 해당 package를 다시 실행해 PASS했다. |
| `./schema/... ./query/... ./orm ./codegen ./codegen/consumertest` normal | PASS. Generated namespace와 receiver 분리, dynamic relation, At limit/offset/cache, 전체 consumer 조립. 소비자 fixture의 facade 인자 누락 수정 뒤 해당 두 테스트를 다시 실행했다. |
| `./migrations/... ./db/... ./systemstate ./internal/migrationautodetect ./internal/projectmigration/... ./internal/irresource` normal | PASS. Built-in 경계, loaded graph·base-state 격리, budget·hash, rows/prune, operation seal과 변조 검증을 포함한다. PostgreSQL 서비스가 필요한 실제 검사는 이 로컬 실행의 성공으로 주장하지 않는다. |
| projectcheck의 다섯 protocol normal | PASS, 281 test pass event, skip/fail 0. 공통 code 집합과 명령별 exit 차이·strict wire·오류 우선순위. |
| Article/Helpdesk 전체와 `./internal/projectcheck ./internal/projectcheck/linked ./internal/projectgenerate/... ./cmd/godj ./project` normal | 관련 패키지를 실행하고 발견된 세 원인을 수정했다. 실패했던 adminapp/projectgenerate/linked 전체를 다시 실행해 PASS했다. PostgreSQL 서비스·helper entry point·macOS deleted-cwd 조건의 skip은 미실행으로 남긴다. |
| `make generate-check` | PASS. Helpdesk·Article·relationfixture의 현재 generated bytes/manifest와 checked-in relation product 일치. |
| `go test -run '^$' -p 2 ./...` | 저장소 전체 127 package compile PASS. 테스트 본문 실행이나 다른 platform의 compile 성공을 뜻하지 않는다. |
| 검증 helper·attestation·protocol·CI tools | 관련 Go packages PASS, Python CI tools 33 tests PASS. Source binding·complete inventory·suite 선택·owner/sentinel 이전을 확인했다. |
| Django·DRF·reference | Django 274 tests PASS, 7 skips(DRF 전용 3개·exact-profile 전용 4개). Locked DRF 환경에서 API-auth 5/5 PASS로 해당 3개를 실행했다. Reference catalog는 54회 독립 contractcheck PASS. Exact-profile oracle 실행은 Hosted가 소유한다. |
| 공유 helper의 실제 SQLite process 소비자 | targeted-migrate, showmigrations, migrate, operator, runserver의 관련 normal smoke PASS. Runserver 강제 descendant cleanup도 실행했다. |
| 문서·형식·workflow | `make docs-check format-check`, `git diff --check`, actionlint v1.7.12의 두 workflow 검사 PASS. |

초기 통합 검사에서 세 원인을 발견했다. Article Service의 과거 Admin sentinel 기대값을 현재 core repository 오류로 고쳤고,
실제 Admin callback의 sentinel 변환은 유지했다. Writer의 최초 replay를 Detect로 옮기면서 바뀐 오류 분류는 catalog 실패로
복원했다. External generated-product fixture는 새 공통 internal package 의존성을 포함하도록 수정했다. 제품의 publication
recovery assertion을 약화하지 않았다. 최초 실패 기록을 최종 PASS로 덮어 해석하지 않는다.
Python 공통 normalization을 적용하며 생긴 helper/지역 변수 이름 충돌도 수정한 뒤 전체 affected suite를 재실행했다.

독립 리뷰는 root 제품/CLI 변경, DB/migration 변경, 검증 catalog/CI/process/source binding 변경을 교차 검토했다.
Admin projector에서 남아 있던 행별 field index 재구성을 추가로 제거했다. 리뷰 자체를 DB·race 실행으로 세지 않는다.

### 한정된 성능 관찰

Apple M3 Pro의 짧은 microbenchmark이며 전체 DB/HTTP 처리량이나 장기 메모리 사용량의 보장은 아니다.

| 작업 | 관측 |
|---|---|
| Sparse Audit prune | 보관 한도 10,000과 1,000,000 모두 240 B/op. 이전 코드의 capacity+1 ID 배열은 각각 약 80KB·8MB였으며 현재는 그 선할당이 없다. |
| PostgreSQL 1,024-operation intent 검증 | 현재 operation seal 약 1.37µs·864 B/op, 비교용 전체 seal 약 1.008ms·731,631 B/op. 전체 seal은 preflight와 완료 시 계속 실행한다. |
| Serializer | 준비된 encoder 1,328 B·5 allocs/op, 같은 새 API를 매 row 준비하면 2,384 B·10 allocs/op. 과거 API 전체와의 역사적 benchmark 비교가 아니다. |
| Template 1,000회 loop | 동일 작업 사본에서 map-copy scope 1,686,874 B·6,760 allocs/op, linked scope 1,046,461 B·5,759 allocs/op. 100회 짧은 실행이며 전체 baseline 대비 속도 향상률로 해석하지 않는다. |

### 동일 분류의 소스 집계

`scripts/sourceinventory`로 기준 commit과 변경 작업 사본을 비교했다. 새 helper·테스트, 공백·주석을 포함한다.
초기 감사의 단순 generated marker 분류 대신 같은 AST 기반 분류를 양쪽에 적용했다.

| Go 역할 | 기준 줄 수 | 구현 후 | 변화 |
|---|---:|---:|---:|
| Framework·CLI·generator·support | 82,273 | 80,431 | -1,842 |
| 테스트 | 165,402 | 163,688 | -1,714 |
| Conformance 지원 | 53,478 | 53,541 | +63 |
| 직접 작성한 예제 | 4,157 | 3,968 | -189 |
| 생성물 | 6,805 | 6,723 | -82 |
| **Go 합계** | **312,115** | **308,351** | **-3,764** |

Python은 36,523→36,442줄(-81)이며 **Go+Python 합계 3,845줄 순감소**다. Go는 870→892파일,
Python은 134→139파일이다. 작게 분리한 공통 소유자·회귀 테스트가 늘었으므로 파일 수 감소를 목표로 삼지 않았다.
Makefile·JSON catalog·문서는 이 소스 분류 합계에 포함하지 않는다.
기준/구현 후 source digest는 `039e4f21d3772bf2ba69cbdf9265d72c0ba2385c42086bf1e5d708b3d548988d` /
`3c1c2ba9a6523f16101a0a29b8a060473f22ad74d91ee48065940e0b6f72786c`다.

실행 catalog를 정적으로 대조하면 full scope의 Linux amd64에서 모드당 26개 package 선택 중복을 정리했다.
25개 relation package와 runserver 1개로, normal/race/CGO-disabled 합계의 해당 선택은 156→78이다.
이 중 18개 package에 Linux test 파일이 있고 8개는 fixture 지원 package이므로 이를 테스트 suite 78회 절감으로 표현하지 않는다.
별도로 root test 6개를 선택하던 godjcheck relation subset 명령 3회도 생략한다. Go runner subset은 이전 full scope에서도
생략했으므로 새 절감에 포함하지 않는다. 기존 Portable Ubuntu 24.04와 relation/project-check 22.04를 모두 24.04로 맞춘
환경 정책 변경과 소유권 이전의 결과이며, 같은 과거 환경의 실행 시간·비용을 실측한 절감률이 아니다.

### 초기 Hosted 검사와 수정

최초 구현 `105c09fe76a8f6f8e62608f29d675b6ec87a8f8c`의
[CI 34175399787](https://github.com/progresshans/godj/actions/runs/34175399787)에서 PostgreSQL core의 normal/race/CGO-disabled가
같은 `TestPostgresRevisionFenceCrossProcessIntegration`의 초기 빈 이력 비교에서 실패했다. 공통 Clone은 빈 목록을 일관되게
반환하지만 기존 integration helper가 `reflect.DeepEqual`로 nil과 empty를 구분했다. 이력의 개수·순서·App/Name을 비교하는
`slices.Equal`로 고쳤으며 history/fingerprint unit과 PostgreSQL test package compile이 통과했다. 실행 본문이나 required/no-skip
검사는 제거하지 않았다. 실제 교차 프로세스 검사는 아래 수정 소스의 Hosted에서 다시 실행해 통과했다.

초기 PR feedback은 성공했지만 전체 CI 성공이 아니며, 수정 소스로 전체 실행을 시작하면서 이전 미완료 job은 취소됐다.
로컬 전체 `make ci`와 Hosted 전체는 중복 실행하지 않았다.

### 수정 소스의 Hosted 검증

[CI 34175865564](https://github.com/progresshans/godj/actions/runs/34175865564)는 제품 소스
`ce86851372a0fc29292aca26381482d5f24429eb`의 full scope를 검증했다. PR checkout은
`d7ed3cfaa7341a994e5774f4c52f0e0e4940e2c2`이며 두 commit의 전체 tree diff가 0인 것을 확인했다.
2026-09-08 10:46 KST 최종 conclusion은 success이며, 74개 고유 job이 모두 completed/success다.
Aggregate는 `scope: full`, `full_platform_verified: true`와 아홉 필수 owner의 성공을 확인했다.
Linux/macOS amd64·arm64의 normal/race/CGO-disabled, PostgreSQL 17.10, 32-bit Linux, cold external build,
고정 Django/DRF oracle과 Python 3.12.13/3.13.15/3.14.3/3.14.7을 포함한다. 수정 소스의 전체 실행은 attempt 1에서 완료됐다.

PostgreSQL 17.10의 core/operator-target은 normal·race·CGO-disabled 모두 success다. 각 core는
11 packages·44 run/pass, operator-target은 2 packages·12 run/pass이며 모두 skip 0이다.
초기 실패했던 교차 프로세스 revision-fence 검사도 이 required/no-skip 실행을 통과했다.

두 capture는 성공한 같은 run의 attempt 1 normal producer가 생성했다. `capture_artifact.py resolve`가 실제 producer job과
artifact ID를 확인했고, `verify`가 해당 PR checkout의 별도 작업 사본에서 repository/run/attempt/checkout·checksum을 검증했다.
두 Go consumer의 `Load`도 현재 제품 작업 사본의 canonical format·profile·checksum·behavioral source binding을 검증했다.
Hosted conformance consumer도 같은 두 artifact와 producer attempt 1을 확인해 모든 product suite 대조를 통과했다.

| Capture | Artifact / producer job | Payload SHA-256 | Source binding |
|---|---|---|---|
| SYS-020 two-process | `10037287698` / `101905103605` | `f74395f956d3003fba30068fa3422a40c156fd9d63faac2ce0eac442e07eda02` | 321 files / 3,742,523 bytes / `50e8e106b6019937a04b916aa4b6b2fe85a6e824a85cd04b7e5ad7b377539cc6` |
| SYS-029 external operator | `10037284632` / `101905103693` | `ceb1226265816c78d9ef9ac3daa78865e881d1a6e0e288bcc21376d492479f64` | 382 files / 3,493,366 bytes / `77dcadb47945ef28eb60c46379372f3e398203e981df362f3e5f21ff9150e13f` |

## GDJ-0064 — 추가 결함 수정과 불변 값의 복사 정리

- 작업: [GDJ-0064](../../work/0064-review-fixes-and-immutable-values.md).
- 검증 소스: `863724a06cd6fe75d1b8f6fe3a23b9cf10c8ec1d` 위 GDJ-0064 변경 작업 사본에서 실행했다.
- 환경: 2026-09-08 KST, Go 1.26.5 darwin/arm64.
- 범위: 사용자 요청에 따라 구현 및 간단한 로컬 확인만 완료했다. 전체 Hosted/DB/race/platform 검증은 실행하지 않았다.

| 실제 실행 | 결과와 한계 |
|---|---|
| `go test ./orm ./query` | PASS. panic 중 rows Close·flight 해제·waiter 재시도·partial cache 방지와 plan 파생 격리를 확인했다. |
| `go test ./codegen ./schema/ir -count=1` | PASS. 검증 후 취소 시 기존/첫 파일 publication 방지, canonical schema/hash와 기존 생성물 golden 비교. 별도 consumer matrix는 실행하지 않았다. |
| `go test ./web/... ./api/sessionauth ./api/bearerauth ./serializers ./admin` | 6 packages PASS. sibling/cross-site signed pair 거부, 정상 origin/logout, nil-header cookie, immutable response/serializer와 reverse round trip·escape 후 cap을 확인했다. |
| `go test -run '^$' ./conformance/runners/godj ./examples/article/apiapp` | 두 소비자 compile PASS. 해당 conformance 시나리오를 실행한 결과는 아니다. |
| 미사용 함수 제거 영향 compile | projectmigratetargetproduct, db/postgres, internal/projectgenerate, migrations/definition PASS. 실제 DB·외부 프로세스 검증은 아니다. |
| capture artifact Python unit | 6 tests PASS. producer attempt 1 → consumer attempt 2, repository/run/checkout/payload/producer mismatch, 실패·취소·누락·중복·미래 attempt 거부를 검사했다. GitHub API 응답은 fixture다. |
| Workflow·format·문서 | YAML 및 resolver → artifact ID download → producer-attempt 검증 연결 확인. 변경 Go 파일 gofmt와 `git diff --check`, 로컬 Markdown link 검사 PASS. Hosted 실행·actionlint는 하지 않았다. |

별도 공개 API 재현에서도 SQLite in-memory의 callback panic 후 다음 조회와 동일 QuerySet 재평가가 성공했다.
Nil-header cookie 적용은 panic 없이 204이며, 취소된 WriteFile은 context canceled를 반환하고 기존 파일을 보존했다.
Percent literal의 static/parameter Reverse는 모두 실제 HTTP request로 왕복해 200을 반환했다. 정상 로그인 뒤
sibling Origin과 유효 서명 쌍을 전송한 요청은 403과 mutation 0이었다. 이번 수정 후 Chrome 재실행은 하지 않았다.

동일한 1/100/1000개 flat 값·field 예제를 각 30회 관찰했다. Object.Value/Value.AsObject는 현재 0 bytes·0 allocations,
Plan.WithLimit은 8 bytes·1 allocation이다. 1000개 기준 변경 전은 각각 약 186KB와 57,352 bytes였다.
이 비교는 개별 호출의 할당 관찰이며 workload 처리량·race·최악 입력 성능을 검증한 것이 아니다.
App generation의 구조는 app당 Normalize 11→1회와 schema hash 6→1회로 정리됐고 생성 golden은 변경되지 않았다.

중간 compile에서 병행 ORM 편집의 `finishRowsLifecycle` 잔여 호출을 발견해 수정했으며 최종 영향 패키지 검사는 통과했다.
외부 재현 스크립트의 첫 실행은 정상 로그인에도 `Sec-Fetch-Site: same-site`를 설정하던 fixture가 새 정책에 의해
403으로 차단됐다. 정상 로그인은 same-origin, 공격 요청은 same-site로 구분한 뒤 재실행해 위 결과를 확인했다.

미사용 함수 17개는 선언 범위 679줄이며 주변 공백/import 포함 698줄을 제거했다. 기존 distinct-process registry와
그 실행 테스트는 보존했다. 새 오류·불변성 회귀 테스트가 추가됐으므로 이 수치를 전체 diff 순감소량으로 해석하지 않는다.

미실행: `make ci`, 전체 `make generate-check`, codegen consumer/process matrix, pinned Django/DRF conformance,
PostgreSQL·race·CGO-disabled·다른 OS/architecture·Hosted CI. 아래 GDJ-0063의 성공은 과거 고정 소스의 결과다.

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
