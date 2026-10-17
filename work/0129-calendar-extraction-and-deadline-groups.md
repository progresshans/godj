# GDJ-0129: 날짜·시각 추출과 기한별 업무 조회

- 상태: 전체 통합 검증 중 — 구현 연결과 생성 ORM의 양 DB 대조 완료; 환경별 범위는 TEST_EVIDENCE 참조
- 시작: 2026-10-16
- 기준: `7f42e7473ea85851b60685bff6c3d533aef5c158` (제품 source `012191bf`의 전체 검증과 문서 반영 완료)
- 근거: [개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [QuerySet 기능](../docs/CAPABILITY_CATALOG.md), [시각·계산식](../docs/adr/0090-query-update-and-scalar-expressions.md), [그룹 결과](../docs/adr/0091-grouped-results-and-having.md)

## 필요한 기반과 소비자

Helpdesk의 현재 Category에서 기한을 UTC 날짜별로 묶어 전체·열린·지연 티켓 수와 최초/최종 기한을 조회한다.
DateTime의 UTC 날짜·시간을 추출하고 이를 DB 그룹 키와 집계에 사용할 수 있도록 공통 함수 AST를 확장한다.
조회한 페이지를 Go에서 다시 묶지 않고 공통 Query AST의 함수·그룹·필터·총수·페이지로 처리한다.
기준 시각과 선택 날짜 범위는 입력과 페이지에 유지하며 ViewTicket·한 읽기 snapshot·전체 저장 보존을 요구한다.

## 계약

- 공통 불변 scalar AST에 닫힌 함수 종류·operand·결과 서명을 추가한다. SQL 함수 이름이나 SQL 조각을 입력으로 받지 않는다.
  DateTime에서 UTC Date/Time을 추출하고 DateTime/Date의 year·iso_year·quarter·month·week·week_day·iso_week_day·day,
  DateTime/Time의 hour·minute·second를 int64로 반환한다. Sunday=1인 week_day와 Monday=1인 iso_week_day를 구분한다.
  Second는 소수 초를 버리며 Date/Time의 별도 값 타입과 DateTime의 microsecond 정밀도를 유지한다.
- 명시적으로 typed NULL을 전파한다. 날짜에 clock part를 요구하거나 Time에 달력 part를 요구하는 조합, Duration·JSON·숫자·
  문자열의 암묵 temporal 변환은 거부한다. Typed capability는 불가능한 조합을 제공하지 않고 dynamic은 같은 AST 서명으로 검사한다.
- Field metadata·finite 관계·collection의 집계 전용 cardinality·선택 결과 scope를 함수 안에서도 보존한다.
  깊이·node·stage·SQL·parameter 한도와 불변 입력/복사 소유권을 유지한다. 조회·UPDATE·집계·후속 결과가 같은 AST를 쓴다.
- 각 실제 기여 operand를 추출 전에 검사한다. SQLite의 잘못된 물리 타입·비정규 date/time/datetime, PostgreSQL의 infinity·
  범위 밖 날짜·24:00과 잘못된 중간 값이 작은 정수 결과나 후속 필터로 숨겨져서는 안 된다. 실행되지 않은 CASE와 FILTER로
  제외한 행은 먼저 평가하지 않는다. Native 원인·context·statement/transaction rollback·cache와 cursor 정리를 유지한다.
- UTC는 현재 DateTime 모델의 계약이다. DB session timezone이나 호스트 locale을 사용하지 않는다. 고정 Django 6.1의
  `Extract*`·`TruncDate`·`TruncTime`을 명시적 UTC profile에서 양 DB로 관측하고, 유효한 결과와 손상/범위 오류의 차이를 구분한다.
  Source·module hash·SQL/parameter·native 결과·변환 오류·저장 보존을 기록한다.
- Date/Time의 typed 출력과 canonical parameter는 기존 모델 값·OpenAPI wire와 같은 의미를 사용한다. Helpdesk의 nullable
  날짜·건수·기한과 날짜 범위는 HTML/API·독립 generated client·브라우저에서 일치해야 한다. 권한·입력·DB/계산 실패를 구분하고
  부분 응답이나 저장 변경을 게시하지 않는다.

## 완료 조건

- [x] 함수 AST·signature·source/cardinality와 양 backend의 물리 값 검사·조회/UPDATE를 연결한다.
- [x] Typed/dynamic·생성 소비자·후속 결과/집계와 compile 부정 대조·실패/rollback을 연결한다.
- [x] 고정 Django의 독립 양 DB 관측과 GoDj 차이를 실제 결과로 대조한다.
- [x] Helpdesk 날짜별 기한 조회·canonical 입력·typed 출력·HTML/API·client·브라우저를 연결한다.
- [ ] 같은 source의 필수 전체 실행과 원본 증거를 확인하고 현행 문서에 반영한다.

일반 timezone/locale 선택·달력/시계 이동·truncation·Duration scaling/division·나머지 함수와 named annotation·
subquery/window·전체 카탈로그는 계속 완성 대상이다. 이 작업은 그 전체 범위를 완료로 처리하지 않는다.
