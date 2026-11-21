# 단일 값 관계 표현식의 독립 기준

`observe.py`는 synthetic `input.json`만으로 고정 Django 6.1의 외부 결과를 관찰한다.
Go 구현·출력·기대값을 읽지 않는다. Source는 Django commit
`fe0a859f537d4238cf49fca39073513206f83122`, BSD-3-Clause이며 관련 15개 설치 module을
`source-verification.json`의 upstream URL·byte 수·SHA-256과 대조한다. 전체 wheel source 동일성의 주장은 아니다.

Python 3.14.3·Django 6.1·psycopg 3.3.6과 PostgreSQL 17.10(`server_version_num=170010`)을 요구한다.
의존성은 [고정 uv profile](../../../reference/related-expressions/pyproject.toml)과 lock이 소유한다.
SQLite의 실제 native 버전·OS/architecture·locale/TZ는 출력에 기록한다. SQLite 임시 파일 또는 고유 PostgreSQL schema를
소유하고 각 관찰의 transaction을 rollback하며 전체 저장 전후 동일성과 종료 뒤 정리를 요구한다.

17개 사례는 optional forward/chain·reverse OneToOne/mixed 경로, 두 computed 값 비교·NOT·NULL 포함 OR,
CASE의 값/조건, 그룹/조건부 Count·정렬/slice·DISTINCT·전역 숫자 집계와 Decimal/Duration/Float/Boolean 값을 다룬다.
정수는 JSON 정수, Decimal은 정규 문자열, Duration은 정확한 microsecond 정수로 기록한다.
Backend별 결과를 섞지 않고 같은 backend의 Go 소비자 결과와 대조한다.

CI normal/core·CGO=0/core·race/core-consumers가 다음 두 실행을 각각 소유한다.

```sh
uv sync --project conformance/reference/related-expressions --frozen
uv run --project conformance/reference/related-expressions --frozen python \
  conformance/runners/django/related_expression_reference/observe.py \
  --vendor sqlite --output "$RUNNER_TEMP/related-expression-reference/django61-sqlite.json"
uv run --project conformance/reference/related-expressions --frozen python \
  conformance/runners/django/related_expression_reference/observe.py \
  --vendor postgres --output "$RUNNER_TEMP/related-expression-reference/django61-postgres.json"
```

PostgreSQL URL은 해당 CI owner가 환경변수 `GODJ_TEST_POSTGRES_URL`로 제공한다. 출력에는 URL이나 credential을 저장하지 않는다.
`TestGeneratedRelatedExpressions`는 `GODJ_RELATED_EXPRESSION_REFERENCE_DIR`에서 같은 실행의 두 파일을 읽고
현재 observer/input/manifest hash·17개 순서·오류 없음·storage 보존/정리를 확인한다. 파일 누락·실패·빈 목록은 PASS가 아니다.
Native owner의 잘리지 않은 Go child JSON events도 `godj-related-native.jsonl`로 같은 artifact에 보존한다.
두 root와 양 DB의 runtime/reference 전체 52개 run/pass·skip 0, 별도 부모의 compile-negative 5개를 요구한다.
Portable 실행은 reference test 자체를 구성하지 않고 native invariant 검사를 실행하며 독립 기준 대조로 계산하지 않는다.
환경별 실제 실행 결과는 [TEST_EVIDENCE](../../../../docs/status/TEST_EVIDENCE.md)에 기록한다.
