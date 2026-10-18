# GDJ-0132: Date/Time 산술과 서비스 일정 미리보기

- 상태: 산술·두 생성 앱·독립 비교·Helpdesk HTML/API·독립 client·브라우저 연결, 최소 compile 확인; 실제 원격 검증 전
- 시작: 2026-10-18
- 기준: `73ee17042b42b48701026e15c329405fe210afe1` — GDJ-0131의 수정 source; 별도 전체 CI 진행 중
- 근거: [헌장](../docs/CHARTER.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md), [선택 기준](../docs/DEVELOPMENT_CRITERIA.md), [산술 AST](../docs/adr/0090-query-update-and-scalar-expressions.md)

## 필요한 기반과 소비자

Helpdesk의 기존 `service_on` Date와 `service_at` Time에서 명시적인 UTC 서비스 시작 시각을 계산하고,
입력한 기간만큼 이동한 시작과 저장된 소요 시간을 더한 종료 시각을 미리 본다. 날짜와 시계가 모두 있어야 시작이
존재하며 누락한 날짜/시계/소요 시간을 임의의 기본값으로 채우지 않는다. 기한과 종료 시각의 차이로 일정 여유도 보여준다.
조회는 현재 Category·ViewTicket·읽기 snapshot·DB 페이지와 원본 모델/관계/audit 보존을 유지한다.

시작 시각은 `Date + (Time - midnight)`의 같은 AST로 만든다. 앱이 모든 행을 읽어 Go에서 날짜와 시계를 합치거나
DB별 SQL을 직접 작성하지 않는다. 필요한 Date/Time 산술의 일반 의미를 이 식에 쓰는 상수만으로 제한하지 않는다.

## 계약

- `Date ± Duration`과 `Duration + Date`는 Date를 UTC 자정으로 해석한 DateTime이다.
  기간의 subday·음수·microsecond를 버리지 않는다. Date/DateTime의 네 가지 뺄셈 조합은 Duration이다.
  Date와 DateTime을 섞을 때도 Date는 UTC 자정이며 session timezone이나 서버 시각을 사용하지 않는다.
- `Time ± Duration`과 `Duration + Time`은 하루를 주기로 도는 Time이다. 음수·여러 날도 정확히 처리한다.
  `Time - Time`은 두 시계 값의 부호 있는 차이이며 자정 넘김을 추측하지 않는다. Date와 Time의 직접 덧셈이나
  Time과 DateTime의 뺄셈 등 정의하지 않은 조합은 typed/dynamic 경계에서 거부한다.
- 기간의 정확한 분수와 기존 native AVG 출처를 구분한다. 새 DateTime/Time 결과 경계에서 정확한 합을
  microsecond half-even으로 반올림하며 Time은 그 결과를 하루 안으로 정규화한다. DateTime 결과는 연도 1..9999다.
  모든 실제 operand의 물리 타입·전체 모델 범위는 계산/NULL 전파 전에 검사한다. 24:00·손상 날짜·Infinity나
  범위 밖 중간값을 modulo·반올림·다음 연산·NULL로 숨기지 않는다.
- 공통 AST가 입력/결과 타입·nullable·source/cardinality·selected scope·불변 소유권과 예산을 소유한다.
  Backend는 native 검증과 컴파일을 소유한다. 관계/collection 집계·후속 결과·같은 행 UPDATE·savepoint와
  cache/context/cursor·부분 결과 거부·원 native 원인은 기존 계약을 유지한다.
- 고정 Django의 선언된 signature와 실제 양 DB SQL/type·변환 결과·실패 단계를 별도로 관측한다.
  SQLite의 date/clock 산술과 PostgreSQL의 timezone/반환 타입 차이는 원본을 유지해 확인하고 명시적으로 기록한다.
  소스에서 예상한 차이를 실제 관측 성공이나 광범위한 비교 면제로 사용하지 않는다.

## 완료 조건

- [x] 공통 AST·typed/dynamic·양 backend의 Date/Time 산술과 정확한 분수/원본 범위 검사를 연결한다.
- [x] 구조가 다른 두 앱의 생성 소비자·관계/집계/후속 결과·쓰기/실패/rollback·compile 부정 대조와 독립 Django 비교를 연결한다.
- [x] 서비스 일정 미리보기의 HTML/API·입출력·OpenAPI/독립 client·브라우저와 인가/저장 보존을 연결한다.
- [ ] 같은 source의 필요한 원격 필수 실행·원본 증거를 대조하고 현행 상태를 갱신한다.

위의 연결 완료는 코드 상태이며 테스트 실행 성공이 아니다. GDJ-0131의 진행 중인 CI는 이 후속 작업 사본을 검증하지 않는다. 일반 timezone/locale·named annotation·나머지
function/subquery/window와 전체 카탈로그의 기능 및 기반은 계속 완성 대상이다.

참조: 고정 Django의 [산술 signature와 dispatch](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/expressions.py),
[SQLite 산술 함수](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/backends/sqlite3/_functions.py),
PostgreSQL 17의 [Date/Time 연산](https://www.postgresql.org/docs/17/functions-datetime.html).
