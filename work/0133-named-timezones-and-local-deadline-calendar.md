# GDJ-0133: 명시적인 시간대와 업무 지역별 기한 달력

- 상태: 고정 데이터·값·AST·typed/dynamic API와 양 backend 경로 작성; 생성 소비자·Helpdesk·독립 관측·실제 검증 미완료
- 시작: 2026-10-19
- 기준: `c0c61979cc7bcf5787f5de4cc8fecb120e4288ea` — 문서 변경 commit; 제품 source `c12b5c0f`의 전체 통합 검증 완료
- 근거: [헌장](../docs/CHARTER.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md), [로드맵](../docs/ROADMAP.md), [개발 기준](../docs/DEVELOPMENT_CRITERIA.md)

## 필요한 기반과 소비자

현재 Helpdesk 기한 달력은 저장한 UTC instant의 날짜로 일·주·월·분기·연을 묶고 결과도 UTC 경계로 검증한다.
업무 지역의 날짜가 UTC와 다르면 다른 기간에 표시된다. 명시적인 IANA 시간대를 받아 같은 DB query에서 지역 날짜와
기간을 계산하고 HTML/API·OpenAPI·독립 client·브라우저에 선택한 시간대를 유지한다. 집계 후 애플리케이션에서
행을 재분류하거나 현재 페이지의 행만으로 합계를 바꾸지 않는다.

DateTime의 지역 날짜/시계·구성요소 추출과 절삭을 공통 AST에 연결한다. Locale 번역과 표시 형식, 사용자별 설정 저장,
naive 시간 입력·일반 wall-time 산술은 이 조회와 별도로 남아 있는 전체 카탈로그의 요구다.

## 유지할 계약

- 시간대는 expression에 명시하는 불변 값이다. 생략한 기존 DateTime 함수는 UTC를 유지한다. Process의 `TZ`·`ZONEINFO`,
  `time.Local`이나 DB session timezone을 변경하지 않는다. 임의 offset 문자열·경로·알 수 없는 이름을 UTC로 대체하지 않는다.
- Go/SQLite에서 사용하는 IANA 데이터는 고정한 출처·버전·hash를 보유하고 바이너리에 포함한다. 이름/alias 목록을 특정
  업무 지역 몇 개로 제한하지 않는다. 공개 lookup 결과·목록·location 변경은 query나 내부 cache에 영향을 주지 않아야 한다.
- Typed/dynamic query는 같은 function AST·source/cardinality·selected scope·NULL·자원 예산을 사용한다. 명시적 시간대는
  DateTime 입력에만 허용한다. Date/Time/Duration 자체에 없는 날짜·시간대를 추측해 채우지 않는다.
- 실제 원본 DateTime의 물리 타입·전체 모델 범위를 변환 전에 검사한다. 잘못된 timezone·손상 값·변환 범위 초과를 NULL이나
  작은 추출 결과로 숨기지 않는다. 선택되지 않은 CASE/FILTER의 기존 평가 경계와 쓰기/rollback·context·cursor 의미를 유지한다.
- 지역 날짜/시계/각 구성요소와 TruncYear/Quarter/Month/Week/Day/Hour/Minute/Second를 함께 다룬다. DST overlap/gap,
  30분 전환·날짜선 이동·과거 초 단위 offset·연도 경계를 검증 대상에서 빼지 않는다. 절삭은 `fold=0`처럼 전환 전 offset을
  선택한다. PostgreSQL도 같은 embedded 전환표를 SQL 안에서 조회한다. 이 설계의 고정 source·독립 관측·실제 실행을
  대조한 뒤 완료로 기록한다.
- Helpdesk는 명시적 `time_zone` 입력과 UTC 기본값, 현재 Category·ViewTicket admission·하나의 읽기 snapshot·DB pagination을
  유지한다. `as_of`와 저장한 UTC instant는 그대로 두고 그룹 키와 기간 소속만 선택한 지역 기준으로 계산한다.
  NULL 기한·범위/정렬·페이지 링크·출력 검증·전체 저장/관계/audit 보존과 실패 응답을 함께 연결한다.
- 새 embedded 데이터와 runtime package는 현재 source binding에 포함한다. 테스트·source 재생성·출처 확인은 같은 데이터
  버전으로 결합하고 다른 source/환경의 성공을 전이하지 않는다.

## 구현과 검증 순서

1. 고정 IANA 데이터와 불변 timezone 값·동시 cache·공개 결과 소유권을 구현하고 source binding을 연결한다.
2. 공통 AST·typed/dynamic 함수·양 backend의 추출과 절삭, 같은 행 UPDATE·관계/집계/후속 결과 및 오류 경계를 연결한다.
3. 구조가 다른 생성 앱과 독립 Django/native 관측에 시간대·DST·domain·실패/rollback·compile 부정 대조를 추가한다.
4. Helpdesk의 지역별 기한 달력과 공통 입력/출력·OpenAPI/client·브라우저를 연결한다.
5. 한 변경 묶음의 포맷·최소 compile 후 제출하고 원격에서 필요한 native·race·process·전체 플랫폼 검증과 원본 대조를 수행한다.

## 완료 조건

- [ ] 데이터 출처·불변 값·cache·source binding과 공통 AST의 시간대 의미를 연결한다.
- [ ] 추출/절삭·typed/dynamic·양 backend·생성 소비자·독립 비교와 실패/rollback을 구현한다.
- [ ] 업무 시간대별 기한 달력의 HTML/API·입출력·client·브라우저 및 인가/저장 보존을 연결한다.
- [ ] 같은 source의 필수 실행·원본 증거를 대조하고 현행 지원/미완료 범위를 갱신한다.

## 현재 구현

`timezone.Zone`은 고정 IANA 2025c의 전체 이름/alias를 검증하는 불변 값이고, 공개 Location·이름 목록은 caller가 소유한다.
데이터 ZIP과 출처 JSON 및 runtime package를 두 source binding에 포함했다. 모든 DateTime calendar 함수는
`InZone(zone)`를 통해 같은 AST를 만들며 시간대가 equality·원본 metadata·관계/selected scope·예산에 반영된다.

양 backend는 같은 전환표를 사용한다. PostgreSQL은 parameter로 전달한 정렬 전환 경계를 `width_bucket`으로 조회하고
정수 offset만 timestamp에 더하거나 뺀다. 2400년 이후는 Gregorian 400년 주기를 사용해 9999년까지의 규칙을 유지한다.
전체 역사·미래 경계의 Go 독립 reader 대조와 DST/NULL/domain/CASE/FILTER/group/후속 결과/UPDATE/rollback 테스트를 작성했다.
전환표의 UTC·지역 경계가 정렬되지 않거나 fixed footer가 마지막 offset과 다르면 데이터 오류로 거부한다.

시간대 값·AST·ORM 및 양 backend의 최소 compile만 수행했다. 생성 소비자·독립 Django/native 비교·Helpdesk와 원격 실제 실행은
계속 구현·검증 대상이다. 기존 source의 통합 PASS를 이 변경에 전이하지 않는다.

아직 timezone 쿼리·서비스의 검증 완료를 주장하지 않는다. 세부 실행은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가
소유하며, named annotation·나머지 function/subquery/window와 전체 카탈로그의 나머지 기반/기능도 계속 완성 대상이다.

참조: 고정 [Django DateTime 함수](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/functions/datetime.py),
[PostgreSQL 17 시간대·절삭](https://www.postgresql.org/docs/17/functions-datetime.html),
[Go 1.26.5 timezone 데이터 생성 원본](https://github.com/golang/go/blob/c19862e5f8415b4f24b189d065ed739517c548ba/lib/time/update.bash).
