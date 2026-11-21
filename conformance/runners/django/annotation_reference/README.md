# 독립 annotation 비교

`observe.py`는 고정 Django commit `fe0a859f537d4238cf49fca39073513206f83122`의 외부 동작 77개를
관측한다. 기존 관측 source의 SHA-256은 `671c01676e34630d7d1f0ece791974e142bc198faad2a96670ee6b75fbade297`이며
GoDj 구현·출력·expected fixture를 읽지 않는다. Django의 관련 열 개 module과 BSD-3-Clause 출처는
`source-verification.json`에 있다. 이 파일의 과거 플랫폼 정보와 실제 실행의 runtime은 구분한다.

`run.py`는 같은 observer를 변경 없이 불러오고 PostgreSQL용 임시 DB를 소유·삭제한다.
SQLite 파일·테이블 정리와 PostgreSQL DB 삭제가 확인된 뒤에만 결과를 기록한다. 기존 출력 파일은 덮어쓰지 않는다.

```sh
uv run --project conformance/reference/related-expressions --frozen python \
  conformance/runners/django/annotation_reference/run.py \
  --vendor sqlite --output /tmp/annotation-sqlite.json
```

PostgreSQL은 `GODJ_TEST_POSTGRES_URL`과 고정 PostgreSQL 17.10 환경이 필요하다. 각 vendor는 새 process에서 실행한다.
Go 생성 소비자는 전체 관측 이름과 source/runtime/정리 상태를 대조한 후 결과·오류와 명시한 Go 차이를 검증한다.
Go 계약이 다른 이름 세 사례·혼합 수치 타입과 SQLite int64 overflow는 Django의 실제 결과도 보존된 고정 관측과
일치해야 한다. 결과·오류·입력 변경과 누락·중복을 거부하며, 나머지 모든 사례는 새 관측과 실제 Go 결과를 비교한다.
관측 목록의 존재나 설계상 차이 선언만으로 제품 실행 성공을 대신하지 않는다.

Go adapter는 같은 DB 연산과 결과를 공개 Go API로 표현하며 Python 호출 모양을 복제하지 않는다.
Collection 조건은 project relation lookup으로 bind한다. DISTINCT projection은 ADR-0090에 따라 정렬 표현식을
직접 선택한다. Django가 암묵적으로 더하는 ID 정렬 열도 SQL 선택에 명시하고 공개 DTO에는 요청한 두 값만 반환한다.
집계 결과를 다시 집계하거나 empty aggregate의 기본값을 선택할 때는 명시적 result stage를 사용한다.
이 연결은 Go에서 기대 값을 계산하거나 조회 행을 다시 정렬·필터링하는 방식으로 결과 차이를 숨기지 않는다.
