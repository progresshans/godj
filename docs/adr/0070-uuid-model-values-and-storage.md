# ADR-0070: UUID 모델 값과 저장·입력 경계

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0093](../../work/0093-uuid-models.md)

설계 채택, 구현과 환경별 검증은 구분한다. 현재 진행과 실행 근거는 CURRENT·TEST_EVIDENCE가 소유한다.

## 값과 정규화 원본

UUID는 복사 가능한 `[16]byte` Go 값으로 전체 128-bit pattern을 보존한다. 특정 UUID version/variant로 제한하지 않으며
zero UUID도 존재하는 값이다. Nullable 모델의 `*uuid.UUID`에서 nil만 NULL이다. 모델·query·cache는 caller-owned byte slice를 보관하지 않는다.
일반 생성 정책·random source·callable default는 별도 책임이며 값 생성자에 I/O나 전역 상태를 숨기지 않는다.

`uuid.Parse`는 ASCII 32-hex와 8-4-4-4-12 표기를 허용하고 대소문자를 정규화한다. `String`은 lowercase hyphenated,
`Hex`는 lowercase 32-hex를 반환한다. UUID 모델 값과 query/mutation은 문자열·정수·float와 다른 타입이다.
Typed/dynamic query에 입력한 다른 타입을 암묵 변환하지 않는다. 잘못된 길이·표기는 오류이며 일반 오류에 panic을 쓰지 않는다.

Schema IR의 UUID kind와 default arm이 단일 정규화 원본이다. Default/wire는 canonical 36-byte 문자열을 사용하며
canonical 아닌 표기·다른 scalar arm·다른 kind의 UUID payload를 거부한다. Clone·hash·historical digest·자원 한도에도 같은 값을 포함한다.
생성 모델은 nullable/default·typed predicate·write·root/eager scanner·동적 metadata를 이 IR에서 만든다.
현재 UUIDField는 일반 scalar다. UUID PK/FK·uniqueness·choices를 함께 지원한 것으로 넓히지 않는다.

## DB 저장과 쿼리

SQLite는 Django UUIDField와 같은 CHAR(32), canonical lowercase hex TEXT를 사용한다. 고정 길이의 ASCII 순서는 unsigned UUID 순서와 같다.
ORM scanner는 NULL, typed UUID 또는 정확한 SQLite canonical hex만 받는다. 외부 uppercase/hyphenated 문자열·잘못된 길이·BLOB·
다른 storage class를 조회하면서 조용히 정규화하지 않는다. 다른 표기의 equality·ordering 불일치를 숨기지 않고 scan 오류로 처리한다.
임의 외부 writer의 모든 동작이나 COUNT처럼 값을 decode하지 않는 조회까지 검사하는 계약은 아니다.

PostgreSQL은 native UUID column과 pgx UUID binary parameter를 사용한다. database/sql의 canonical native UUID text는 backend adapter가
검사한 뒤 typed UUID로 변환한다. 그 변환을 generic ORM이나 다른 문자열 column에 적용하지 않는다.
Root·projection·eager·transaction에서 같은 adapter를 사용하며 실패한 scanner는 이전 값을 게시하지 않는다.

양 DB의 equality/range/IN/F·정렬과 root MIN/MAX는 같은 UUID 값을 사용한다. PostgreSQL 17에는 native MIN/MAX(uuid)가 없으므로
compiler가 `MIN/MAX(column::text COLLATE "C")::uuid`를 만든다. 고정 canonical text의 C 순서가 native unsigned UUID 순서와 같고,
마지막 cast는 결과의 UUID DB type을 유지한다. Backend의 고정 `search_path=pg_catalog` connection 계약을 따른다.
Common Query AST는 DB cast/collation을 알지 않으며 backend가 물리 집계를 소유한다. DISTINCT·slice의 원래 source 경계와
all-NULL/empty aggregate의 NULL은 유지한다. NULL 정렬 위치는 각 DB의 기존 동작을 따른다.

Historical create/nullable add와 그 reverse는 UUID IR·catalog type·default identity를 보존한다. 기존 행의 nullable 추가는 NULL이며
literal default는 generated write에서 적용한다. DB default/backfill·일반 type/nullability 변경·forward RemoveField 자동 계획은 별도 범위다.
기존 revision fence·잠금·FK/catalog preflight·transaction·history publication 경계를 낮추지 않는다.

## 사용자 입력 경계

고정 Django/DRF의 public UUID 입력 관찰은 [독립 runner](../../conformance/runners/django/uuid_reference.py)와
[raw](../../internal/uuidtest/testdata/django61.json)에 보존한다. Form의 whitespace 처리, serializer의 integer/float token 구분,
braces/URN 등의 별칭은 strict model parser와 다른 입력 계층이다. Python 객체 내부 구조나 `.int`의 bool 표현을 Go 객체로 복제하지 않는다.

Form/Admin·serializer·OpenAPI·Helpdesk 외부 참조 연결은 이 입력 계층의 후속 구현이다. 네 reference representation을 관찰한 것만으로
GoDj의 모든 출력 옵션을 지원한다고 표시하지 않는다. 각 입력의 수용/거부·정규 출력과 JSON의 정확한 128-bit integer 처리는 별도로 검증한다.
