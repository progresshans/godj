# GDJ-0128: 시각·기간 계산식과 기한 변경 미리보기

- 상태: 제품·소비자·검증 코드 연결, 원격 실행 검증 전
- 시작: 2026-10-15
- 기준: `c6e96c41fb981111a11d2a93914ee305b0c513a0` (GDJ-0127 수정 source의 전체 CI는 별도 진행 중)
- 근거: [개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [시각 값](../docs/adr/0061-datetime-field-and-canonical-instant-values.md), [기간 값](../docs/adr/0067-duration-model-range-and-number-input.md), [계산식](../docs/adr/0090-query-update-and-scalar-expressions.md), [집계와 결과 단계](../docs/adr/0091-grouped-results-and-having.md)

## 필요한 기반과 소비자

Helpdesk 티켓의 원래 기한과 기간만큼 이동한 기한, 명시적인 기준 시각까지 남은 기간을 조회한다.
계산한 값의 필터·정렬·총수·페이지를 DB에서 함께 처리한다. 저장된 기한·기간·관계·보고서·audit를 보존하며
현재 Category·ViewTicket과 하나의 읽기 snapshot을 사용한다. 일반 named annotation은 이 흐름의 선행 조건이 아니다.

## 계약

- 같은 불변 Query AST에 Duration의 덧셈·뺄셈·부호 반전, DateTime ± Duration, Duration + DateTime,
  DateTime − DateTime을 연결한다. 연산의 왼쪽·오른쪽 kind와 결과 kind를 구분한다. Typed 전용 capability와
  dynamic 경로는 같은 서명 판정·compiler·decoder를 사용하며 정수·Float·Go `time.Duration`과 암묵 변환하지 않는다.
- 기존 숫자 연산의 bare NULL 추론은 원본을 변경하지 않는 typed NULL로 정규화한다. Temporal NULL에는 명시적인
  kind를 요구하며 각 literal의 타입을 부모 결과 타입으로 덮어쓰지 않는다. NULL 전파와 실제 0 기간을 구분한다.
- DateTime은 UTC 연도 1..9999·microsecond, Duration은 기존 전체 normalized day/subday 모델 범위를 유지한다.
  SQLite의 int64 microsecond 저장·정수 연산 범위를 모델 범위로 축소하지 않는다. 큰 날짜 차이를 Go `time.Duration`이나
  Julian-day float로 계산하지 않는다. Backend의 좁은 표현·범위 초과는 명시적인 오류다.
- 원본·single-valued 관계·선택 결과의 scope, collection의 집계 전용 cardinality와 명시적 결과 stage를 유지한다.
  계산한 Duration의 SUM/AVG·MIN/MAX·DISTINCT/FILTER와 후속 결과 단계에도 값의 종류와 정밀도 출처를 보존한다.
- SQLite Duration AVG의 native fractional microseconds는 Duration 산술·부호 반전·재집계에서 유지하고 최종 Duration
  decoder에서 half-even 반올림한다. 일반 Duration field에 REAL을 허용하지 않는다. Fractional Duration을 DateTime에
  더하거나 뺄 때에는 시각의 정확한 정수 microseconds와 합친 뒤 해당 DateTime 결과 경계에서 한 번 half-even 반올림한다.
  PostgreSQL의 native INTERVAL 평균과 반올림 차이는 별도로 관측한다. 기존 평균을 미리 모델 값으로 바꿔 SQL에 재입력하지 않는다.
- 원본 값·기여 입력·중간/최종 결과를 statement 안에서 검증한다. 손상된 temporal 값, month/year interval, infinity,
  모델 범위 밖의 값이 NULL·상쇄·후속 WHERE/HAVING으로 숨겨져서는 안 된다. 실행되지 않은 CASE branch와 FILTER로 제외한
  행은 먼저 평가하지 않는다. PostgreSQL native 원인/SQLSTATE와 SQLite native 원인을 보존한다.
- 같은 행 UPDATE의 원본 값과 target을 함께 검사한다. Related/selected assignment를 허용하지 않으며
  뒤쪽 행 실패·취소·constraint 오류에 전체 statement/transaction rollback과 cache 소유권을 유지한다.
- Helpdesk는 canonical Duration과 명시적 offset DateTime query를 같은 typed parser/OpenAPI 선언으로 연결한다.
  기준 시각은 요청·페이지·client에 명시적으로 유지한다. 입력 오류, 권한 거부와 계산/DB 실패를 구분하고 부분 결과를 게시하지 않는다.
  HTML/API·required nullable 응답·독립 client·브라우저와 전체 native 저장 보존을 함께 확인한다.

## 구현과 검증

고정 Django 6.1의 독립 SQLite/PostgreSQL 관측은 SQL·parameter/native 반환 종류·변환 결과·오류와 저장 보존을 기록한다.
SQLite의 fractional AVG를 temporal 식에 넣는 동작은 아직 source 조사만 했으며 실행 결과로 확정하지 않는다.
Timestamp/Duration 함수와 parser의 실제 upstream module을 source manifest에 포함하고 의도한 차이는 실제 관측 후 명시한다.
PostgreSQL 17의 [시각 함수](https://www.postgresql.org/docs/17/functions-datetime.html)와
[특수 시각 값](https://www.postgresql.org/docs/17/datatype-datetime.html)을 확인했다. INTERVAL의 infinity와
`isfinite` 및 세션 timezone에 따른 native 이동을 source 근거로 삼으며, 이는 실제 pinned DB 실행 증거와 구분한다.
두 앱 생성 소비자에서 typed/dynamic·cross-app·양 DB·세 mode·compile-negative와 수명/실패/범위를 검증한다.
로컬은 포맷·최소 compile·관측된 실패 재현에 사용하고, 완성한 제품·생성 소비자·HTTP 묶음을 push해 자동 전체 CI에서 실행한다.

공통 AST·typed capability와 양 DB의 temporal 조회/UPDATE·집계 경로를 작성했다. SQLite는 canonical text·정수 범위·
평균에서 유래한 fractional microseconds를 구분하며 PostgreSQL은 원본·중간/최종 값과 UTC 이동을 검사한다.
큰 날짜 차이·혼합 NULL/손상 값·중간 범위·CASE/FILTER·statement/transaction rollback·평균 재집계·HAVING·세션 DST의
실제 DB 테스트 코드를 준비했다. 로컬은 최소 compile만 수행했으며 테스트 본문·DB·생성 소비자 실행은 아직 전이다.

두 앱 생성 소비자와 44개 독립 관측 case를 작성하고 기존 세 PostgreSQL 소비자 owner의 CI step·artifact 안에 연결했다.
원본 SQL/parameter·raw 반환·변환 오류 단계·rollback 직전 storage class와 cleanup을 보존한다. 소수 평균 이동·native overflow의
예상 차이는 고정 source에서 도출한 검증 조건이며 실제 양 DB 실행으로 아직 확인하지 않았다. 생성 모듈은 최소 compile했고
잘못된 타입/모델/결과·assignment의 10개 프로그램은 의도한 compiler 거부를 확인했다. 테스트 본문은 실행하지 않았다.

Canonical Duration·explicit-offset DateTime의 공통 query/header codec, typed DateTime output과 Helpdesk 기한 미리보기
HTML/API를 작성했다. 현재 문서 여섯 profile을 export하고 고정 client를 재생성했다. Source 구성과 빌드 확인이며
새 경로의 native HTTP·인가/저장 실패, 독립 client의 큰 날짜 범위·반올림 없는 정수 기대값·required nullable wire와
브라우저의 명시적 기준 시각·윤일·페이지 검증 코드를 연결했다. 원본 로그·현재 source·필수 목록의 감사도 준비했다.
최소 compile과 검사기의 부정 대조까지만 확인했으며 실제 DB/HTTP·client/browser 실행은 원격에서 검증해야 한다.

## 완료 조건

- [ ] 공통 AST의 operand/result kind·NULL·source·정밀도 계약과 양 DB의 조회/UPDATE를 연결한다.
- [ ] Typed/dynamic·생성 facade 소비자·computed Duration 집계와 부정 대조·실패/rollback을 완성한다.
- [ ] 독립 양 DB 관측과 GoDj의 명시적 차이를 실제 결과로 기록한다.
- [ ] Helpdesk 기한 미리보기·typed parameter·HTML/API·client·브라우저를 연결한다.
- [ ] 같은 source의 필수 전체 실행과 원본 증거를 대조하고 현행 문서를 갱신한다.

Date/Time의 달력·시계 이동, Duration scaling/division, named annotation·function/subquery/window,
timezone/locale와 나머지 기능 카탈로그는 계속 전체 제품 목표로 유지한다. 이 작업의 완료로 그 범위를 완료 처리하지 않는다.
