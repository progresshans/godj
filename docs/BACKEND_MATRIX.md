# Backend 범위

공통 AST·historical state를 유지하고 backend 차이는 capability, compiler와 schema editor가 소유한다.
아래는 구현 범위이며 현재 변경의 모든 플랫폼 PASS를 의미하지 않는다. 실행 결과는 [Evidence](status/TEST_EVIDENCE.md)에 있다.

| 기능 | SQLite | PostgreSQL |
|---|---|---|
| Driver | modernc.org/sqlite, database/sql | pgx database/sql adapter |
| Query/CRUD | current scalar/FK AST와 typed write | current scalar/FK AST와 typed write |
| Relation query | current forward/reverse, eager/prefetch | current-profile relation 경로 |
| Relation delete | supported physical FK의 PROTECT/SET_NULL | declared current backend capability 기준 |
| Migration | revision session, current Create/Delete/Add/Remove와 choices-only·Decimal precision-only AlterField의 검증된 형태 | current schema-bound lifecycle와 해당 capability |
| SQL projection | immutable DB-free renderer | schema-bound immutable DB-free renderer |
| System state | file-backed cooperative runtime와 explicit operator | schema-bound cooperative runtime와 explicit operator |
| CGO | pure Go 경로 | pure Go 경로 |

## 현재 schema와 query 폭

Schema는 Auto primary key, signed int64 Integer(nullable/default 포함), Char, Text, Date, DateTime, Time, Duration, Float, Decimal, UUID, JSON, Boolean과 AutoField-target ForeignKey를 지원한다.
일반 Integer는 양 DB에서 BIGINT이며 자동 ID·identity와 구분한다. [정수 의미](adr/0059-signed-integer-field-and-model-growth.md)를 따른다.
Text는 양 DB에서 저장 길이 제약 없는 TEXT이며 nullable/string default를 지원한다. [Form widget·빈 입력 의미](adr/0060-text-field-and-form-widget-semantics.md)는 저장 nullability와 구분한다.
DateTime은 UTC 연도 1..9999·microsecond 정밀도다. SQLite DATETIME의 고정 여섯 자리 UTC text와 PostgreSQL TIMESTAMP WITH TIME ZONE을 사용하며
nullable/time default·comparison/F·projection/Min/Max를 지원한다. [시간 값과 남은 범위](adr/0061-datetime-field-and-canonical-instant-values.md)를 따른다.
String/int64 choices는 DB 제약을 추가하지 않는다. Choices-only AlterField는 양 DB의 revision-fenced lifecycle에서 DDL 없이 상태·recorder를 갱신하며 물리 catalog 검증은 수행한다. [선택값 의미](adr/0063-model-choices-and-metadata-only-migrations.md)를 따른다.
Date는 timezone/clock 없는 Gregorian 연도 1..9999의 `calendar.Date`다. 양 DB에서 DATE와 canonical `YYYY-MM-DD`를 사용한다.
Nullable/default·comparison/IN/F·projection/Min/Max·forward relation을 지원하며 [날짜 경계](adr/0065-calendar-date-field-and-input-boundaries.md)를 따른다.
Time은 자정을 포함하는 날짜·시간대 없는 clock과 별도 NULL이다. 양 DB TIME과 canonical clock parameter를 사용하며
literal/default·typed/dynamic query·IN/F·projection/Min/Max·forward relation을 연결했다. PostgreSQL timetz와 구분하고
SQL text scanner의 precision/24:00 경계는 [ADR-0066](adr/0066-clock-time-field-and-precision-boundaries.md)를 따른다.
Decimal은 immutable coefficient/exponent와 명시적 max_digits 1..1000·decimal_places 0..max_digits를 사용한다.
SQLite BLOB numeric order key와 PostgreSQL NUMERIC(p,s)는 초과 scale을 반올림하지 않는 공통 write 검증을 거친다.
Typed/dynamic comparison·IN·F·projection·Min/Max와 generated root/eager scan, historical create/add/remove를 연결했다.
Form/Admin의 원문 precision 검증과 Helpdesk 예상 비용의 fixed-scale JSON/OpenAPI·독립 client까지 연결했다.
Precision-only AlterField는 기존 값을 변경 전후 범위로 검사하고, SQLite에서는 metadata만, PostgreSQL에서는 NUMERIC typmod를 변경한다.
Reverse의 범위 초과는 값을 반올림하지 않고 실패한다. SQLite의 Django NUMERIC 물리 형식 채택·unbounded NUMERIC·일반 type/default/nullability 변경은 미지원이다.
[정확한 값·물리 저장 경계](adr/0069-exact-decimal-values-and-storage.md)를 따른다.
UUID는 모든 128-bit pattern의 복사 가능한 값과 별도 NULL을 사용한다. SQLite CHAR(32)의 canonical lowercase hex TEXT와 PostgreSQL native UUID를 연결했다.
Typed/dynamic comparison·IN/F·projection·Min/Max·forward 및 현재 reverse exact scalar, historical create/nullable add와 reverse를 지원한다.
PostgreSQL 17의 UUID Min/Max는 canonical text의 C collation 집계 뒤 native UUID로 반환하며, NULL 정렬 위치는 DB별 기존 의미를 유지한다.
Form/Admin/API와 Helpdesk 외부 참조·독립 client를 연결했다. UUID PK/FK·uniqueness·generation/callable default는 별도 범위다. [UUID 값과 저장](adr/0070-uuid-model-values-and-storage.md)을 따른다.

JSON은 immutable 문서와 exact number token을 사용하며 nil pointer(SQL NULL)와 JSON null을 구분한다.
SQLite TEXT/JSON_VALID CHECK와 PostgreSQL native JSONB, strict read·parameter·historical create/add/reverse를 연결했다.
Exact/IN/F exact·isnull·projection·forward와 non-null reverse exact를 지원한다. 명시적 key/index 경로의 exact/IN/isnull을 typed/dynamic과 같은 AST로 연결했다. PostgreSQL은 root/path·forward contains/contained_by를 native JSONB 연산으로 처리하고 SQLite는 capability 오류로 거부한다.
Root/path·forward has_key/has_keys/has_any_keys도 지원한다. PostgreSQL native JSONB membership과 SQLite object-key 의미,
SQLite의 literal empty/NUL 보정·빈 목록 확장은 ADR-0071/DEV-0017에 명시한다. Root JSON 경로 projection도 typed nullable 결과로 연결했다. 관계 filter source의 root scalar/JSON 경로 DTO는 같은 JOIN과 선택값 기준 DISTINCT를 사용한다. Forward 대상의 JSON 경로도 nullable DTO로 연결한다. JSON range/order/Min/Max·reverse path projection은 현재 미지원이다.
GoDj write의 object key 정규화와 native JSONB numeric equality·지수 전개를 구분하며 PostgreSQL NUL과 readback 크기 초과를 거부한다.
Form/Admin/API·OpenAPI와 Helpdesk JSON 소비자·독립 client를 연결했다. Helpdesk는 native readback 뒤 응답 한도 검사까지 transaction 안에서 처리한다. [JSON 값과 저장](adr/0071-json-values-and-native-storage-boundaries.md)의 범위를 따른다.

이 기능의 현재 검증 완료 여부는 [CURRENT](status/CURRENT.md)와 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)가 소유한다.


모든 Django Field, OneToOne/ManyToMany, arbitrary `to_field`나 범용 constraint/index migration을 지원하지 않는다.
Scalar comparison·Boolean composition·same-model field reference와 projection/aggregate는 구현한 AST 범위 안에서만 허용한다.
Scalar COUNT/MIN/MAX와 현재 관계 filter 위의 단일 COUNT(*)를 지원한다. 관계 COUNT는 원래 JOIN·Distinct·정렬·
슬라이스의 결과 행 수를 센다. Eager Count는 selected projection을 먼저 제외한다. 일반 관계 집계는 미지원이다.
유한한 여러 단계의 forward FK는 required/nullable source의 scalar comparison·icontains·isnull·IN과 AND/OR/NOT을 같은 AST로 처리한다.
Nullable JOIN은 필터가 대상 존재를 요구하면 INNER, 나머지는 LEFT OUTER이며 부정 조건은 joined 대상 column의 NULL을 보정한다.
선언상 nullable과 optional JOIN 뒤 nullable을 같은 operand 판단에 반영한다. Source-key isnull은 마지막 target JOIN만 생략하며 상위 경로와 optional NULL을 보존한다.
Typed/dynamic 대상은 Integer·Float·Decimal·UUID·Char/Text·Date·DateTime·Time·Duration의 nullable/non-null과 Boolean이다.
여러 selected direct·nested forward relation과 다른 forward/reverse filter JOIN의 All/First·중복·Distinct·슬라이스를 지원한다.
선택한 경로의 모든 prefix를 한 SQL로 읽고 하위 관계 접근에 cache를 넘긴다. 입력 tree는 깊이 64·중복 포함 1024 node로 제한하며
Count는 구조·binding 검사 뒤 projection을 제외한다. 실행 환경별 근거는 [테스트 증거](status/TEST_EVIDENCE.md)가 소유한다.
Reverse non-exact/OR/NOT, 관계를 넘는 F·다단계 reverse traversal·reverse eager materialization은 미지원이다.
[관계 lookup 의미](adr/0040-composable-typed-boolean-predicates-and-article-search.md#직접-forward-대상의-scalar-lookup)를 따른다.
유한한 self/cyclic forward 조회는 위 범위에 포함한다. 일반 순환 관계의 migration·mutation이나 object identity 공유까지 지원한다는 뜻은 아니다.
지원하지 않는 표현은 silent fallback이나 client-side full scan으로 바꾸지 않는다.

## SQLite 경계

Backend가 생성하는 모든 physical connection은 외래키 검사를 ON으로 설정하고 readback 1을 확인한 뒤 pool에 게시한다.
파일 reopen과 pool 증가/교체에도 적용한다. Driver-level OFF 옵션은 이 무결성 조건을 낮추지 않는다. Migration remake의
제한된 FK suspension은 별도의 admission/terminal 복원·폐기 경로가 계속 소유한다.

경로 최대 길이는 공통 AST의 64 hop이다. SQLite는 [물리 JOIN 제한](https://www.sqlite.org/limits.html#max_join)에 따라
root를 포함해 64 table까지만 허용한다. 실제 JOIN 수를 compiler가 계산하여 초과를 빈 조회의 생략 전에 거부한다.

FK enforcement는 connection별 설정이다. GoDj가 사용하는 raw relation/migration path는 필요한 FK 상태와 physical schema를
확인한다. FK-off 또는 out-of-band writer를 포함한 모든 process가 자동 보호된다고 주장하지 않는다.

Migration remake는 허용된 schema에서 row/null/default/PK/sequence 의미를 보존해야 한다. Preflight가 확인하지 않은
index/trigger/물리 shape는 mutation 전에 거부한다. Closed historical self/cyclic graph와 검증한 inbound FK는 지원하며,
필요한 remake/self Delete의 FK suspension·복원·quarantine은 [ADR-0064](adr/0064-historical-relation-graphs-and-sqlite-remakes.md)를 따른다. Migration revision metadata는 cooperating writer 사이의
freshness를 검증하며 임의 schema drift나 cutover 이전 non-cooperating ABA를 복원하지 않는다.

Raw transaction의 rollback/discard를 확인하지 못하면 Backend는 connection 하나를 보관하고 새 I/O를 terminal quarantine으로
닫는다. 이미 admitted된 작업, Close 순서와 outcome unknown은 [CONCURRENCY](CONCURRENCY.md)를 따른다.
In-memory DB의 Close 이후 복구 가능성을 durable file과 동일하게 주장하지 않는다.

## PostgreSQL 경계

현재 conformance의 서비스 이미지는 workflow에 고정한 PostgreSQL 17.10 profile을 사용한다. DB URL과 schema는 caller가
명시적으로 제공한다. Tests는 scope별 schema를 사용하고 credential-bearing 설정을 로그나 oracle에 넣지 않는다.

PostgreSQL source-bound actual은 해당 source·observer·환경에서 실제 실행한 증거다. SQLite local PASS나 옛 checked-in actual을
현재 PostgreSQL 실행으로 대체하지 않는다. 테스트 실행과 증거 생성·소비 경계는 [TESTING](TESTING.md)를 따른다.

## 다른 환경

MySQL/MariaDB/Oracle와 GIS는 장기 범위이며 현재 backend 지원이 아니다. Windows용 CLI·파일 publication·signal/PTY 동작도
Linux/macOS 결과만으로 검증됐다고 표현하지 않는다. OS/architecture/mode별 지원 주장은 실제 Hosted 결과에만 연결한다.
새 backend나 환경을 추가할 때는 전부를 한 번에 구현하지 않고 같은 external flow와 실패 의미를 수직으로 검증한다.
