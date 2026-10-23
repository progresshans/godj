# GDJ-0131: 기간 배율·나눗셈과 라벨별 소요 시간 미리보기

- 상태: 완료 — source `c12b5c0f`의 양 DB·세 mode·HTML/API/client·브라우저·전체 플랫폼 필수 실행과 원본 대조 완료
- 시작: 2026-10-18
- 기준: `0ce12980ffdf9c66b50d6e2e120e522e55ca6cf8` — GDJ-0130의 전체 원격 검증과 필수 실행·원본 대조 완료
- 근거: [헌장](../docs/CHARTER.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md), [선택 기준](../docs/DEVELOPMENT_CRITERIA.md), [시각 계산식](../docs/adr/0090-query-update-and-scalar-expressions.md)

## 필요한 기반과 소비자

Helpdesk 라벨 사용 현황에서 저장된 소요 시간을 배율·분할 수로 조정한 합계를 미리 본다.
기존 비용 미리보기처럼 DB의 같은 collection·Category 조건과 읽기 snapshot을 사용하며 저장된 티켓·관계·audit를 보존한다.
배율과 분할 수는 같은 canonical Decimal 입력·HTML/API/OpenAPI/client·브라우저 경로로 제공한다.

이를 위해 Duration과 정수·Float·Decimal의 곱셈/나눗셈, Duration 사이의 무차원 비율을 공통 AST에 연결한다.
일반적인 수치 동작의 완성은 이 소비자에 나타나는 몇 가지 상수 조합으로 제한하지 않는다.

## 기반 계약

- `Duration × Number`·`Number × Duration`·`Duration ÷ Number`는 Duration,
  `Duration ÷ Duration`은 Float 결과다. Number는 signed int64·finite binary64·exact Decimal이다.
  각 operand의 원래 타입과 Decimal metadata를 유지하고 숫자를 무조건 Float으로 바꾸지 않는다.
- 기간 사이의 비율은 원 operand를 먼저 Float으로 바꾸지 않고 나눗셈 결과에서 binary64로 변환한다.
  원 물리 타입·전체 기간 범위를 먼저 검사하고, NULL이 있으면 NULL을 반환한다. NULL이 없는 0 나눗셈은 native 오류이며
  유한 Float 밖의 결과를 Infinity로 게시하지 않는다. SQLite의 AVG 유래 fractional operand는 그 실제 값을 유지한다.
- Typed 함수와 dynamic 계산은 같은 AST를 사용한다. 명시적 typed NULL·source/cardinality·selected result scope와
  공유 입력의 불변 소유권·깊이/node/SQL/parameter 한도를 유지한다. 제곱 기간이나 역기간 등 다른 단위는 명시적으로 거부한다.
- 실제 기여 operand 전체를 검사하고 native 손상 값·0 divisor·범위 오류를 NULL, 부분 결과, 작은 후속 결과로 감추지 않는다.
  실행하지 않는 CASE/FILTER의 평가, context·원래 native 원인·원자 UPDATE/rollback·cache/cursor 수명을 함께 검증한다.
- 정수·Decimal 배율의 정확한 값과 Float의 실제 binary64 값을 유지한다. 마이크로초 미만 중간값은 정확한 분수로
  보존하고, 최종 Duration 읽기/쓰기에서 half-even 반올림한다. 따라서 `1µs ÷ 2 × 2`와 `1µs ÷ 3 × 3`은 1µs다.
  각 Duration 연산의 원 범위 검사는 반올림보다 먼저 적용한다. 기존 native AVG provenance를 새 분수 출처와 구분한다.
  이 계약의 채택은 양 backend 지원이나 실제 실행 완료가 아니다. 고정 Django의 원본 SQL/type·변환과 대조하고
  확인한 차이를 현행 ADR/DEVIATIONS에 기록한다. 유효한 큰 값이나 정밀도를 줄여 backend 구현에 맞추지 않는다.
- Helpdesk의 조정 시간은 각 기여 ticket에서 계산한 뒤 합산한다. `elapsed_factor`와 `elapsed_divisor`는
  canonical Decimal(6,4), 기본 1이며 divisor는 양수만 허용한다. 최종 합계에서만 microsecond half-even 반올림한다.
  현재 권한·Category·빈 라벨·NULL/zero 구분·날짜/시간과 무관한 기존 필드·전체 저장 상태를 유지한다.

## 완료 조건

- [x] 공통 AST·typed/dynamic·정밀도 계약과 양 backend의 조회/쓰기/집계·후속 결과를 함께 연결한다.
- [x] 구조가 다른 두 앱의 생성 소비자와 compile 부정 대조·native 실패/rollback, 독립 Django 비교를 연결한다.
- [x] 라벨별 소요 시간 조정의 HTML/API·입출력·OpenAPI/client·브라우저와 인가/저장 보존을 연결한다.
- [x] 같은 source의 필수 실행과 원본 증거를 대조하고 현행 상태를 갱신한다.

Source `c12b5c0f`의 같은 AST·typed API·양 backend·생성 소비자·Helpdesk와 독립 Django 비교를 세 mode에서 검증했다.
같은 source의 전체 플랫폼·브라우저 통합과 필수 원본 대조를 완료했으며 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.
Date/Time 이동은 GDJ-0132에서 같은 source로 검증했다. 일반 timezone/locale·named annotation·나머지 function/subquery/window와 전체 카탈로그는 계속 완성 대상이다.

## 정밀도 구현과 근거

[Python 3.14 timedelta 연산](https://docs.python.org/3.14/library/datetime.html#timedelta-objects)은 숫자 곱셈/나눗셈의
결과를 microsecond로 half-even 반올림한다. 고정 Django의 SQLite `_sqlite_format_dtdelta`는 곱셈/나눗셈에서
중간 숫자를 반환하며 최종 Duration 변환과 분리된다. PostgreSQL 17.10의
[interval 연산 및 NUMERIC epoch 추출](https://github.com/postgres/postgres/blob/REL_17_10/src/backend/utils/adt/timestamp.c)은
서로 다른 물리 연산이다. 소스에서 확인한 구조를 새 기능의 실제 native 관측 성공으로 표현하지 않는다.

기간 비율은 원 숫자의 정확한 유리수 비율을 최종 binary64로 변환한다. PostgreSQL도 NUMERIC 정수 쌍과
정확한 이진 지수 탐색·half-even significand를 사용하며 임의로 고정한 소수 자릿수로 근사하지 않는다.
큰 기간을 int64나 Float operand로 줄이지 않는 경계를 포함해 양 DB에서 검증했다.

기간을 반환하는 연산은 선택된 결과·CASE·비교·정렬·그룹·재집계까지 분수를 보존한다.
공통 `internal/durationcalc`는 원본을 변경하지 않는 계산 값, 정확한 숫자 배율, 전체 모델 범위와 최종 half-even을 소유한다.
연분수의 계수와 종료 위치를 부호·짝홀에 맞게 인코딩한 query key는 byte 순서와 정확한 수치 순서를 일치시킨다.
같은 기약분수는 같은 key이므로 SQLite의 DISTINCT·GROUP BY·MIN/MAX가 문자열이나 BLOB의 임의 순서를 따르지 않는다.
일반 모델 열은 이 내부 query key를 저장 값으로 받지 않는다.

SQLite의 산술·후속 결과·비교/HAVING·정렬·집계·UPDATE와 별도 ORM scanner를 작성했다. 원 native AVG만 포함한
기존 식은 기존 backend 의미를 유지하며, scaling에서 기여한 실제 AVG 값을 정확한 분수로 넘긴다.
신규 분수 집계는 기여 값의 범위를 확인하고 최종 SUM/AVG에서 모델 범위를 검사한다. 누적 상태를 개별 Duration으로
반올림하지 않는다. 이 경로의 실제 native 테스트·부정 대조·생성 소비자·process 검증은 source·환경별로
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록했으며 현재 source의 전체 통합을 완료했다.

PostgreSQL의 NUMERIC pair·canonical key를 산술·CASE·비교/HAVING·selected result·빈 입력·
MIN/MAX/COUNT·SUM/AVG와 최종 UPDATE에 연결했다. Float 배율은 `float8send`의 실제 IEEE 비트에서 분수를 만들며,
분수 비율은 정확한 이진 지수 탐색과 half-even significand를 거쳐 마지막에 Float으로 변환한다.
DateTime 이동은 정확한 epoch와 분수의 합을 반올림하고, INTERVAL 저장은 day/subday로 나누어 전체 모델 범위를 유지한다.
SUM/AVG의 native ARRAY_AGG는 원 source의 query level에서 실행하고 다음 projection이 분수를 합산한다.
상수 식에서도 원 source의 행 수를 잃지 않으며 CASE의 각 분기와 비교 양쪽은 물리 타입을 명시적으로 맞춘다.
DB helper 설치나 session 설정 변경은 추가하지 않았다.

양 DB의 공통 native 검사, PG binary64/반올림 경계와 구조가 다른 두 생성 앱의 typed/dynamic·선택 결과·그룹·
collection·same-row UPDATE·savepoint·cache/context/cursor 검사를 작성했다. 새 타입 오용 여섯 건은 실제 compile 거부를
확인했다. 생성 소비자의 compile 뒤 원격 실행에서 발견한 비교 fixture의 세션 생성자 오용을 수정했다. 생성기 출력 ABI를 바꿀 필요는 없었으며 같은
Schema IR의 선언에서 생성한 field/facade를 새 generic API로 소비한다.

고정 Django 관측에 배율·나눗셈·분수 합계·Float/Decimal 경계·DateTime 이동·0 나눗셈·기간 비율 11개를 추가했다.
기존 44개와 함께 원 SQL·parameter·native replay·변환 결과·실패 단계·전체 저장 보존을 비교하도록 작성했다.
분수의 중간 표현과 반올림 계약 차이는 양 구현의 값과 native 타입/오류를 각각 검사한다. 첫 CI에서 독립 SQLite 55개
관측 뒤 완료 검사의 이전 count가 실패했다. 원본 대조·변조 거부와 완료 검사 수정, 새 계산 패키지의 source 결합을 보완했다.
후속 양 DB 관측을 대조하고 확인한 차이를 [DEV-0024](../docs/DEVIATIONS.md#dev-0024--기간-배율의-정확한-중간값과-최종-반올림)에 기록했다.
수정 source `c12b5c0f`의 실제 제품·환경별 실행과 전체 통합을 완료했으며 상세 source와 범위는 TEST_EVIDENCE를 따른다.

Helpdesk의 `adjusted_elapsed_total`을 기존 여섯 지표와 `CombineAggregates`로 합성하고 모든 집계에 같은 Category
FILTER를 적용했다. 같은 query 선언을 HTML/API에서 사용하며 공통 양수 Decimal codec과 OpenAPI 문법을 추가했다.
페이지 링크는 비용/시간 입력을 모두 보존한다. 독립 client는 보존한 응답에서 분수를 합산해 최종 half-even을 계산하며,
브라우저와 양 DB 검사에 중간 반올림 차이·음수/±0·NULL·외부 Category·0 divisor 거부·범위 초과와 저장 보존을 작성했다.
Source `d1370569`의 Chromium·SQLite client normal/CGO0·PostgreSQL Helpdesk race를 확인했으나 같은 CI에서
기반과 독립 비교의 실패가 있었다. 수정 전후 집중 재현을 확인했으며 부분 성공을 수정 source의 전체 성공으로 전이하지 않는다.

[Draft PR #2](https://github.com/progresshans/godj/pull/2)의 [CI 39091754185](https://github.com/progresshans/godj/actions/runs/39091754185) attempt 1에서
현재 기반과 라벨별 소요 시간 조정의 실제 양 DB·HTML/API·OpenAPI/client·브라우저 및 전체 플랫폼을 검증했다.
이 작업의 완료는 헌장과 전체 카탈로그의 나머지 목표를 완료한 것이 아니다.

구현 근거는 PostgreSQL의 [NUMERIC div/mod/gcd](https://www.postgresql.org/docs/17/functions-math.html),
[bytea 인코딩](https://www.postgresql.org/docs/17/functions-binarystring.html),
[recursive CTE](https://www.postgresql.org/docs/17/queries-with.html), 고정 17.10의
[int8send](https://github.com/postgres/postgres/blob/REL_17_10/src/backend/utils/adt/int8.c)와
[network byte order](https://github.com/postgres/postgres/blob/REL_17_10/src/include/libpq/pqformat.h)다.
Django의 [mixed Duration dispatch](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/expressions.py)와
[SQLite 실행 함수](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/backends/sqlite3/_functions.py)는
관측 구성의 소스 근거이며 실제 성공 증거를 대신하지 않는다.
