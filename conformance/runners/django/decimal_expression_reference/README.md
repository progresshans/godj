# Decimal 계산식의 독립 기준

관찰기는 synthetic `input.json`과 고정 upstream module만 읽는다. GoDj 소스·기대값·실행 결과는 입력이 아니다.
Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`의 BSD-3-Clause 20개 module을
`source-verification.json`의 byte 수·SHA-256과 대조한다. 전체 wheel 동일성을 주장하지 않는다.
Python 3.14.3·psycopg 3.3.6·PostgreSQL 17.10은 기존 [고정 환경](../../../reference/related-expressions/)을 사용한다.

30개 관측은 Decimal 산술·NULL·CASE·관계·계산값 조건/정렬·SUM/AVG/MIN/MAX·DISTINCT/FILTER·HAVING·
empty/all-NULL·파생 집계·UPDATE 원본 행 의미·반올림/overflow·zero division·native scale과 상수의 원문 표기를 보존한다.
각 관측은 고유 DB namespace의 transaction에서 실행되고 rollback 후 모든 테이블의 전체 native 컬럼을 대조한다.
종료 시 DB 정리도 검증한다. 관찰 오류는 예상한 종류와 SQLSTATE에만 한정하고 그 외 오류는 실행 실패다.

Decimal의 문자열 정규화는 소수점 뒤 0만 제거한다. Python Decimal context의 `normalize()`로 큰 값을
반올림하지 않는다. PostgreSQL의 NUMERIC display scale과 SQLite NUMERIC affinity를 그대로 관측한다.
고정 Django/psycopg의 PostgreSQL `Decimal('100') / Decimal('3')`는 SQL `100 / 3`으로 관측된다.
`100.0 / 3.0` 표기도 별도로 관측하며 GoDj는 두 표기 모두 명시적 Decimal kind의 NUMERIC 연산으로 처리한다.
GoDj SQLite의 canonical BLOB·정확한 연산, PostgreSQL Decimal 상수의 NUMERIC cast와 양 DB의 target precision 거부는 명시적 deviation이며,
이 관측값과 동일하다고 주장하지 않는다. 실제 Go 비교와 native 실패/저장 보존은 생성 소비자가 별도로 실행한다.
원본 JSON·SQL·관찰기/입력/source digest와 native Go 이벤트는 기존 세 PostgreSQL consumer owner artifact에 보존한다.
실행 실패의 원 이벤트도 보존하되 잘린 출력은 성공으로 인정하지 않는다.
