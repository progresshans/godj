# GDJ-0130: 달력 구간 계산과 기간별 기한 조회

- 상태: 구현 묶음과 원격 실패의 DATE scan·CASE/집계 비교 보완 완료, 수정 source의 실제 동작·전체 통합 미검증
- 시작: 2026-10-18
- 기준: `153cf7652578db28fbad7bc9fae4bf107109cfce`
- 근거: [기능 카탈로그](../docs/CAPABILITY_CATALOG.md), [개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [함수 AST](../docs/adr/0090-query-update-and-scalar-expressions.md)

## 필요한 기반과 소비자

Helpdesk의 날짜별 기한 조회를 일·주·월·분기·연 단위로 확장한다. 공통 Query AST의 구간 시작일로 DB에서
그룹·건수·범위·페이지를 계산한다. 조회 결과에는 구간 시작일과 실제 최초/최종 기한 시각을 제공한다.
여러 날짜가 한 구간에 들어가므로 시계만 비교해 최초/최종 기한을 표현하지 않는다.

## 계약

- 닫힌 unary 함수 AST에 `TruncYear/Quarter/Month/Week/Day/Hour/Minute/Second`를 추가한다.
  DateTime은 모든 단위를 지원하고 UTC DateTime을 반환한다. Date는 연·분기·월·주·일을, Time은 시·분·초를
  지원하며 입력과 같은 타입을 반환한다. 주는 월요일부터 시작하고 작은 구성요소는 0, 월/일은 필요한 경우 1이 된다.
  기존 `.Date()`/`.Time()`의 DateTime→Date/Time 변환과 구분한다. 임의 SQL 단위·문자열이나 암묵 타입 변환을 받지 않는다.
- Typed/dynamic·관계·collection 집계·selected result·projection·조건·정렬·그룹·집계·같은 행 UPDATE가 같은 AST를 쓴다.
  원본 metadata와 nullable·source/cardinality·결과 scope·불변 소유권·깊이/node/SQL/parameter 한도를 보존한다.
- 각 실제 기여 operand의 물리 타입과 전체 날짜/시각/시계 범위를 절삭 전에 검사한다. 잘못된 값·24:00·infinity·
  범위 밖 중간값이 유효한 구간 시작으로 감춰지면 안 된다. 선택되지 않은 CASE와 FILTER 제외 행은 평가하지 않는다.
  Native 원인·context·statement/transaction rollback·cache/cursor 정리를 유지한다.
- PostgreSQL은 입력 타입과 UTC를 명시해 Date/Time이나 DateTime 절삭이 세션 timezone에 의존하지 않게 한다.
  SQLite는 statement 안의 결정적 함수로 Gregorian 날짜와 microsecond clock을 계산한다. 원 epoch를 float로 바꾸지 않는다.
  고정 Django 6.1의 실제 양 DB 관측에서 정상 값·원시 SQL/type·변환 오류·저장 보존과 의도적 차이를 대조한다.
  PostgreSQL의 Trunc를 감싼 CASE/집계가 보존하는 aware/naive datetime 차이는 [DEV-0023](../docs/DEVIATIONS.md#trunc를-감싼-식의-반환-타입)의
  명시적 UTC·native 타입/값 검사로만 비교하며 원 관측 결과에 timezone을 붙이거나 다른 오류를 면제하지 않는다.
- Helpdesk는 `period=day|week|month|quarter|year`를 받고 생략 시 day를 사용한다. 범위는 구간 시작일의 inclusive
  `from_date`/`until_date`이며, 부분 기간 포함 여부를 추측하지 않는다. NULL 기한은 마지막 별도 그룹이고 범위 지정 시 제외한다.
  응답의 `period_start`는 nullable Date, `first_due_at`/`last_due_at`는 nullable UTC DateTime이다. 실제 티켓 시각의 MIN/MAX를 사용한다.
  명시적 `as_of`, 현재 Category·ViewTicket, 한 읽기 snapshot·DB 페이지·전체 저장 보존을 HTML/API·독립 client·브라우저에서 유지한다.

## 완료 조건

- [x] 공통 AST·typed/dynamic·양 backend에 타입별 구간 계산과 원본 값 검사를 연결한다.
- [ ] 실제 양 DB의 생성 소비자·compile 부정 대조·관계/집계/후속 결과·쓰기/실패/rollback과 고정 Django 비교를 연결한다.
- [x] Helpdesk 구간 선택·기간별 출력·범위/페이지·HTML/API·OpenAPI/client·브라우저와 보안/저장 보존을 연결한다.
- [ ] 영향에 맞는 원격 필수 실행과 원본 증거를 확인하고 구현·환경별 검증·미완료 범위를 기록한다.

체크한 항목은 코드 연결 범위다. 최소 compile·재생성 이후의 실제 DB/HTTP/브라우저 검증은 남아 있다.

GDJ-0129 source의 성공은 이 변경의 실행 증거가 아니다. 일반 timezone/locale·Date/Time 이동·Duration scaling/division·
named annotation·나머지 function/subquery/window와 전체 카탈로그는 계속 완성 대상이다.
