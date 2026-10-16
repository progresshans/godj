# GDJ-0128: 시각·기간 계산식과 기한 변경 미리보기

- 상태: 원격 fixture 오류 수정 후 접속 진단 보완 완료, 최신 source 전체 CI 준비
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

공통 AST·typed capability·양 DB 조회/UPDATE·집계와 두 앱 생성 소비자를 연결했다. 큰 날짜 차이·NULL/손상 값·
중간 범위·CASE/FILTER·statement/transaction rollback·평균 재집계·HAVING·세션 DST를 native 환경에서 검사한다.

고정 Django 6.1의 독립 SQLite/PostgreSQL 관측은 SQL·parameter/native 반환·변환 오류 단계와 저장 보존을 기록한다.
Timestamp/Duration의 upstream module 27개를 source manifest에 결합했다. 실제 평균 이동·범위 오류의 차이는
[DEV-0022](../docs/DEVIATIONS.md#dev-0022--temporal-계산의-정확한-범위와-평균-이동)에 분류했다. Native 구현의
정수→REAL 승격이나 변환 실패를 GoDj 성공으로 바꾸지 않고 별도 기대값과 저장 상태를 확인한다.

Canonical Duration·explicit-offset DateTime query/header codec, typed DateTime output과 Helpdesk 기한 미리보기
HTML/API·독립 client·브라우저를 연결했다. 여섯 OpenAPI profile의 실제 export/generated drift도 검증한다.
Client는 전체 모델 날짜 범위의 정수 기대값과 required nullable wire를, 브라우저는 명시적 기준 시각·윤일·페이지·실패를 확인한다.

첫 원격 실행의 검증 코드가 PostgreSQL의 canonical Duration 반환과 고정 reference의 오류/Int4 타입을 잘못 가정했다.
실패를 국소 재현해 수정하고 source `9657a87346a1e6d1b903a7e692d8ee36ac7e1cce`를 push했다.
[자동 전체 CI 38841006796](https://github.com/progresshans/godj/actions/runs/38841006796)의 같은 source에서
양 DB·세 mode temporal 생성 소비자·Helpdesk·독립 client와 normal 브라우저, 독립 관측의 원본 증거를 대조했다.
실행 누락·skip·관측/저장 변조를 거부하는 감사기도 확인했다. 전체 플랫폼 실행·최종 집계와 전체 증거 감사는 진행 중이다.
이전 source의 실패·국소 재현·환경별 raw/hash는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

재현 과정에서 PostgreSQL physical-session 설정 거부가 일반적인 접속 실패로 바뀌는 진단 손실을 확인했다.
비밀을 포함하지 않는 닫힌 설정 진단만 공개 오류에 투영하도록 로컬에서 보완하고, 실제 거부/정상 접속과 정리를 재현했다.
기존 admission 조건은 유지하며 native role-default 경로의 진단·driver carrier 제거 검사도 보강했다.
이 후속 변경은 위 source의 CI에 포함되지 않는다. 진단 root/child 13개를 원격 필수 목록에 연결했으며,
완성된 변경 묶음의 push와 새 source의 자동 전체 CI가 남아 있다.

## 완료 조건

- [x] 공통 AST의 operand/result kind·NULL·source·정밀도 계약과 양 DB의 조회/UPDATE를 연결한다.
- [x] Typed/dynamic·생성 facade 소비자·computed Duration 집계와 부정 대조·실패/rollback을 완성한다.
- [x] 독립 양 DB 관측과 GoDj의 명시적 차이를 실제 결과로 기록한다.
- [x] Helpdesk 기한 미리보기·typed parameter·HTML/API·client·브라우저를 연결한다.
- [ ] 같은 source의 필수 전체 실행과 원본 증거를 대조하고 현행 문서를 갱신한다.

Date/Time의 달력·시계 이동, Duration scaling/division, named annotation·function/subquery/window,
timezone/locale와 나머지 기능 카탈로그는 계속 전체 제품 목표로 유지한다. 이 작업의 완료로 그 범위를 완료 처리하지 않는다.
