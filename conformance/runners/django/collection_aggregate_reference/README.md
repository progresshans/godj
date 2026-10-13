# 컬렉션 집계의 독립 기준

`observe.py`는 synthetic `input.json`과 설치된 upstream module만 읽는다. GoDj 구현·기대값·실행 결과는
입력이 아니다. Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`의 BSD-3-Clause source
20개 module을 `source-verification.json`의 upstream URL·byte 수·SHA-256과 대조한다.
전체 wheel source 동일성을 주장하지 않는다.

Python 3.14.3·Django 6.1·psycopg 3.3.6·PostgreSQL 17.10(`server_version_num=170010`)을 요구한다.
기존 [고정 uv profile](../../../reference/related-expressions/pyproject.toml)과 lock을 함께 사용한다.
SQLite native version·OS/architecture·locale/TZ는 매 실행의 출력에 기록한다. 관찰기는 고유 SQLite 파일 또는
PostgreSQL schema를 만들며, 사례별 transaction rollback과 전체 저장 전후 동일성·종료 정리를 요구한다.

30개 사례는 reverse FK·M2M·nullable ancestor·reverse OneToOne·여러 collection 경로에서
COUNT/MIN/MAX/SUM/AVG·DISTINCT·FILTER, 원본 부정 조건과 집계 부정 조건, 연속 Filter의 JOIN 재사용,
여러 JOIN의 행 곱, 그룹 source의 DISTINCT, HAVING·정렬/slice·빈 전역 집계·빈 페이지·재집계를 관찰한다.
중복·NULL through FK·Category 밖 연결도 포함한다. Decimal은 정규 문자열, Duration은 microsecond 정수다.
Duration AVG의 SQL 비교와 반환 시 반올림처럼 backend가 다른 결과는 각 backend의 관찰과 따로 대조한다.

CI normal/core·CGO=0/core·race/core-consumers가 다음 실행을 소유한다.

```sh
uv sync --project conformance/reference/related-expressions --frozen
for vendor in sqlite postgres; do
  uv run --project conformance/reference/related-expressions --frozen python \
    conformance/runners/django/collection_aggregate_reference/observe.py \
    --vendor "$vendor" --output "$RUNNER_TEMP/collection-aggregate-reference/django61-$vendor.json"
done
```

PostgreSQL URL은 owner가 `GODJ_TEST_POSTGRES_URL`로 제공하며 출력에 credential을 기록하지 않는다.
`TestGeneratedCollectionAggregates`는 `GODJ_COLLECTION_AGGREGATE_REFERENCE_DIR`의 현재 실행 파일에서
observer/input/manifest hash·runtime pin·30개 순서·오류 없음·storage 보존/정리를 검증한다.
Go 소비자는 catalog/records 두 앱에 모델을 생성해 같은 관찰과 별도의 Go-native invariant를 실행한다.
잘리지 않은 native child JSON events를 `godj-collection-native.jsonl`로 같은 artifact에 남긴다.
두 root·양 DB의 reference/runtime 전체 82개 run/pass·skip 0, 부모의 compile-negative 5개가 필수다.
Portable owner는 reference test를 구성하지 않고 그 환경의 invariant만 실행한다.

실행 결과와 source-bound 검증 범위는 [TEST_EVIDENCE](../../../../docs/status/TEST_EVIDENCE.md)에 기록한다.
