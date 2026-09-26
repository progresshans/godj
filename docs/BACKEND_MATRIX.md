# Backend 범위

공통 AST·historical state를 유지하고 backend 차이는 capability, compiler와 schema editor가 소유한다.
아래는 구현 범위이며 현재 변경의 모든 플랫폼 PASS를 의미하지 않는다. 실행 결과는 [Evidence](status/TEST_EVIDENCE.md)에 있다.

| 기능 | SQLite | PostgreSQL |
|---|---|---|
| Driver | modernc.org/sqlite, database/sql | pgx database/sql adapter |
| Query/CRUD | current scalar/FK AST와 typed write | current scalar/FK AST와 typed write |
| Conflict insert | nullable column의 non-NULL 값을 포함한 명시적 unique tuple의 native no-op·0/1행 결과, ordinary/relation/coordinated session | 같은 AST·결과·session 계약, schema-qualified target |
| ManyToMany root manager | 같은 AST의 collection 조회/Distinct, add/remove/clear/set·nullable/nonunique through·retained ID/payload·self symmetry·AtomicRelation·cache 소유권 | 같은 runtime/AST·native conflict·incoming 정책, root transaction ownership |
| Borrowed session / model facade | UsingSession/InSession·기존 fence 참여, ordinary/relation/coordinated session lifetime과 warm/empty/eager query 검사 | 같은 공통 facade/runtime·native session 검사, outer transaction 소유권 |
| Relation query | current forward/reverse, eager/prefetch | current-profile relation 경로 |
| Relation delete | supported FK/OneToOne의 CASCADE·PROTECT·SET_NULL, recursive collector·exact-key 삭제 | 같은 graph/runtime과 native FK·AtomicRelation |
| OneToOne | 명시적 cardinality·FK+UNIQUE·single reverse/prefetch·직접 조건/isnull/Boolean 조합·typed forward/reverse eager tree | 동일 공통 AST/runtime과 native 제약 |
| Migration | revision session, current Create/Delete/Add/Remove와 choices·Decimal precision·Unique·관계 cardinality/reverse namespace/delete policy AlterField의 검증된 형태 | current schema-bound lifecycle와 해당 capability |
| Explicit ManyToMany migration | 기존 through의 Add/Remove/Rename·reverse, DDL 없는 전체 graph/catalog 검증과 행·sequence 보존 | 동일한 상태·history 계약, endpoint/through 잠금·catalog 검증 |
| Automatic ManyToMany migration | 소유 table Create/Add/Remove/Rename·reverse, link PK·sqlite_sequence·rootpage 보존, managed pair index 이름 변경 | 같은 logical operation, link PK·table/sequence OID·last_value/is_called 보존, managed PK/FK/unique/sequence 이름 변경 |
| SQL projection | immutable DB-free renderer | schema-bound immutable DB-free renderer |
| Field/model uniqueness | column·named tuple unique index·Create/Add/Alter와 constraint Add/Remove·reverse·remake 보존·모든 key catalog | column·named tuple UNIQUE·독립 B-tree·Create/Add/Alter와 constraint Add/Remove·reverse·모든 key catalog |
| System state | file-backed cooperative runtime와 explicit operator | schema-bound cooperative runtime와 explicit operator |
| CGO | pure Go 경로 | pure Go 경로 |

CASCADE의 native 기반은 양 DB에서 FK를 `ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`로 생성한다.
정책 변경·역방향은 SQLite의 sealed remake와 PostgreSQL constraint timing ALTER를 사용하며, 실제 timing drift를 거부한다.
SQLite의 retained FK/unique·행·sequence 보존과 required 순환 생성/삭제·deferred COMMIT 실패 후 정리를 영향 범위에서 검증했다.
PROTECT/SET_NULL은 기존 즉시 검사 표현을 유지한다. 공통 ORM과 generated project deleter는 CASCADE의 전체 도달 그래프를 고정하고,
모든 보호 검사 뒤 SET_NULL·중복 없는 exact-key 삭제를 실행한다. Transitive fingerprint는 후손의 변경도 I/O 전에 거부한다.
TicketLabel의 migration·scoped 소비자와 양 DB CASCADE/PROTECT·실패 경로를 연결했다.
[GDJ-0098](../work/0098-cascade-and-ticket-label-links.md)은 source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의 Hosted 전체 통합까지 완료했다.

## 현재 schema와 query 폭

Schema는 Auto primary key, signed int64 Integer(nullable/default 포함), Char, Text, Date, DateTime, Time, Duration, Float, Decimal, UUID, JSON, Boolean과 AutoField-target ForeignKey/OneToOne을 지원한다.
일반 Integer는 양 DB에서 BIGINT이며 자동 ID·identity와 구분한다. [정수 의미](adr/0059-signed-integer-field-and-model-growth.md)를 따른다.
Text는 양 DB에서 저장 길이 제약 없는 TEXT이며 nullable/string default를 지원한다. [Form widget·빈 입력 의미](adr/0060-text-field-and-form-widget-semantics.md)는 저장 nullability와 구분한다.
DateTime은 UTC 연도 1..9999·microsecond 정밀도다. SQLite DATETIME의 고정 여섯 자리 UTC text와 PostgreSQL TIMESTAMP WITH TIME ZONE을 사용하며
nullable/time default·comparison/F·projection/Min/Max를 지원한다. [시간 값과 남은 범위](adr/0061-datetime-field-and-canonical-instant-values.md)를 따른다.
String/int64 choices는 DB 제약을 추가하지 않는다. Choices-only AlterField는 양 DB의 revision-fenced lifecycle에서 DDL 없이 상태·recorder를 갱신하며 물리 catalog 검증은 수행한다. [선택값 의미](adr/0063-model-choices-and-metadata-only-migrations.md)를 따른다.
Column uniqueness는 `schema.Unique()`·IR·생성 metadata·historical definition/digest·자동 변경 계획까지 연결했다.
PK는 이미 고유하므로 별도 Unique flag를 정규화하며, Unique FK는 many-to-one/reverse collection을 유지한다.
양 DB는 field·named model uniqueness의 `UniqueConstraints` capability를 제공하며 정확히 선언한 native constraint/index만 허용한다.
Named model constraint의 CreateModel/AddConstraint/RemoveConstraint·역방향·자동 계획과 native 실행을 연결했다.
SQLite는 별도 unique index의 모든 BINARY ASC key와 rowid를 검사하며 FK remake에서 유지되는 index를 재생성한다.
PostgreSQL은 모든 conkey/indkey·방향·collation·operator class를 검사한다. CreateModel의 named 제약은 CREATE TABLE 뒤 별도 ALTER로 추가하여
같은 field의 여러 선언도 native 이름별로 남긴다. 모든 statement는 같은 migration transaction과 operation에 속한다.
ORM은 수정한 member가 속한 복합 제약을 전체 candidate로 사전 검증한다. Category Label의 Form/Admin/API/client도 이 경로를 사용한다.
검증 대상인 모든 모델·direct/transitive target의 미선언 index는 거부한다. Legacy direct editor에는 이 capability를 확장하지 않는다.
기존 중복으로 UNIQUE 추가/역방향 적용이 실패하면 행·catalog·revision/recorder를 보존하고 명시적 수정 후 재시도한다.
Insert/update의 non-PK 충돌은 `integrity_error/unique_constraint`이며 native cause와 context 취소를 유지한다.
공통 ORM의 생성·수정 고유성 사전 검증은 같은 typed mutation과 양 DB exact 조회를 사용하며 저장 제약을 대체하지 않는다.
Form/Admin/API는 확인된 입력 거부를 field/non-field 진단으로 전달한다. Helpdesk 외부 UUID의 실제 migration·양 DB HTTP
소비자와 SQLite 기반 generated client를 연결했다. 실행 오류·취소·rollback 실패는 입력 오류로 바꾸지 않는다.
Column uniqueness 통합은 [GDJ-0095](../work/0095-model-uniqueness.md), named 제약·Label 소비자 통합은 [GDJ-0097](../work/0097-composite-uniqueness-and-labels.md)에서 완료했다. [제약 소유권](adr/0072-column-uniqueness-and-constraint-ownership.md)을 따른다.
OneToOne은 Unique FK와 별도 cardinality를 보존하며 default/named/hidden reverse와 required/nullable 선언을 지원한다.
`AlterFieldRelation` capability는 같은 target/delete policy에서 cardinality·reverse 이름과 관련 Unique 변경을 실행한다.
Unique가 그대로면 metadata-only이며 달라지면 실제 DDL이 필요하다. 기존 중복에 의한 적용 실패는 행·catalog·recorder/revision을 보존한다.
성공한 역방향 적용은 이전 cardinality·reverse 이름·Unique 상태를 복구한다.
Single reverse의 정상 부재·cardinality 오류·cache/prefetch와 양 DB PROTECT/SET_NULL을 연결했다.
단일 reverse는 관계/field isnull과 nullable/Boolean을 포함한 현재 scalar lookup·IN·AND/OR/NOT를 지원한다.
자식의 부재는 physical FK nullability와 별개이며, 조건이 존재를 요구하면 INNER, 나머지는 LEFT OUTER로 부모 행을 보존한다.
일반 Unique FK의 collection reverse는 기존 direct non-null exact 범위를 유지한다. Typed reverse/mixed eager tree를 구현했으며
Facade의 reverse selector·문자열 mixed path도 같은 tree를 사용한다. Forward With/Clear와 명시적 reverse Set은
required/nullable·unsaved·재할당·native 중복 실패의 메모리/저장 경계를 보존한다. Required OneToOne Clear는 메모리에서 가능하나
Save/Unwrap은 I/O 전 required 오류다. Cold reverse clear는 조회 없이 아무 자식도 바꾸지 않는다.
명시적 PK 0의 관계 presence와 absence를 구분한다. PostgreSQL은 새 AutoField 행의 수동 PK INSERT/identity sequence 조정을
현재 unsupported로 거부한다. 이는 key-present target의 관계 할당과 별개이며, 없는 target의 FK 저장은 실제 제약으로 거부한다.
ServiceReport migration·scoped ModelChoice Form/Admin·JSON CRUD/reverse·OpenAPI/client를 연결했다.
Runtime의 관계 삭제는 양 backend의 CoordinatedAtomicRelation과 동일 transaction/fence를 사용한다.
[GDJ-0096](../work/0096-one-to-one-service-reports.md)의 소비자 통합 milestone을 완료했으며 source·플랫폼 범위는 TEST_EVIDENCE가 소유한다.
[일대일 의미](adr/0073-one-to-one-cardinality-and-reverse-objects.md)를 따른다.
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
Form/Admin/API와 Helpdesk 외부 참조·독립 client를 연결했다. UUID PK/FK·generation/callable default는 별도 범위다. Column uniqueness는 위 backend별 범위를 따른다. [UUID 값과 저장](adr/0070-uuid-model-values-and-storage.md)을 따른다.

JSON은 immutable 문서와 exact number token을 사용하며 nil pointer(SQL NULL)와 JSON null을 구분한다.
SQLite TEXT/JSON_VALID CHECK와 PostgreSQL native JSONB, strict read·parameter·historical create/add/reverse를 연결했다.
Exact/IN/F exact·isnull·projection·forward와 non-null reverse exact를 지원한다. 명시적 key/index 경로의 exact/IN/isnull을 typed/dynamic과 같은 AST로 연결했다. PostgreSQL은 root/path·forward contains/contained_by를 native JSONB 연산으로 처리하고 SQLite는 capability 오류로 거부한다.
Root/path·forward has_key/has_keys/has_any_keys도 지원한다. PostgreSQL native JSONB membership과 SQLite object-key 의미,
SQLite의 literal empty/NUL 보정·빈 목록 확장은 ADR-0071/DEV-0017에 명시한다. Root JSON 경로 projection도 typed nullable 결과로 연결했다. 관계 filter source의 root scalar/JSON 경로 DTO는 같은 JOIN과 선택값 기준 DISTINCT를 사용한다. Forward 대상의 JSON 문서 전체와 경로도 nullable DTO로 연결한다. Literal JSON의 gt/gte/lt/lte는 root/path·forward에서 DB별 의미로 처리한다. SQLite path의 array/object RHS는 미지원이며 whole field와 PostgreSQL path는 지원한다. JSON root/path·forward 정렬은 backend 값 의미와 정확한 경로 숫자를 유지한다. Literal 문자열 IContains도 root/path·forward에서 지원한다. SQLite는 저장 TEXT/정확한 subtree와 ASCII 문자 범위, PostgreSQL은 native JSONB TEXT 변환과 UPPER/LIKE를 사용한다. SQL NULL/missing·JSON null의 차이와 정확한 숫자/NUL 검색 정책은 ADR-0071/DEV-0017을 따른다. JSON Min/Max·ordered F·reverse value/path projection·정렬은 현재 미지원이다.
GoDj write의 object key 정규화와 native JSONB numeric equality·지수 전개를 구분하며 PostgreSQL NUL과 readback 크기 초과를 거부한다.
Form/Admin/API·OpenAPI와 Helpdesk JSON 소비자·독립 client를 연결했다. Helpdesk는 native readback 뒤 응답 한도 검사까지 transaction 안에서 처리한다. [JSON 값과 저장](adr/0071-json-values-and-native-storage-boundaries.md)의 범위를 따른다.

이 기능의 현재 검증 완료 여부는 [CURRENT](status/CURRENT.md)와 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)가 소유한다.


Columnless ManyToMany 선언과 자동 storage projection·generated metadata는 지원한다. 명시적 through의 선택한 두 FK는
nullable이거나 pair unique가 없어도 기존 제약을 유지한다. `ExplicitManyToMany` capability는 Add/Remove/Rename·reverse와
retained binding의 전체 historical graph/catalog 검증을 소유하며 데이터·DDL을 재작성하지 않는다. Definition/digest와 자동 계획은
선언을 보존하고, 최초 생성은 모델과 선택한 FK를 만든 뒤 AddManyToMany를 배치한다.
`AutomaticManyToMany` capability는 같은 logical operation 안에서 소유 table의 Create/Add/Remove/Rename·reverse를 수행한다.
Raw CreateModel의 columnless 선언·자동 계획과 SQL projection도 지원한다. 실제 관리 밖의 참조를 조용히 retarget하지 않으며,
transient table과 파생 제약 이름까지 초기/최종 검증한다. 소유 table끼리의 의존성은 먼저 생성·역순 제거하고, owner의 저장 FK가
자신의 intermediary를 다시 참조하는 생성 순환은 모델을 만든 뒤 AddField로 작성한다. 일반 collection manager/query는 아직 미지원이다.
모든 Django Field, relation-as-PK, arbitrary `to_field`나 범용 constraint/index migration을 지원하지 않는다.
Scalar comparison·Boolean composition·same-model field reference와 projection/aggregate는 구현한 AST 범위 안에서만 허용한다.
Scalar COUNT/MIN/MAX와 현재 관계 filter 위의 단일 COUNT(*)를 지원한다. 관계 COUNT는 원래 JOIN·Distinct·정렬·
슬라이스의 결과 행 수를 센다. Eager Count는 selected projection을 먼저 제외한다. 일반 관계 집계는 미지원이다.
유한한 여러 단계의 forward FK는 required/nullable source의 scalar comparison·icontains·isnull·IN과 AND/OR/NOT을 같은 AST로 처리한다.
Nullable JOIN은 필터가 대상 존재를 요구하면 INNER, 나머지는 LEFT OUTER이며 부정 조건은 joined 대상 column의 NULL을 보정한다.
선언상 nullable과 optional JOIN 뒤 nullable을 같은 operand 판단에 반영한다. Source-key isnull은 마지막 target JOIN만 생략하며 상위 경로와 optional NULL을 보존한다.
Typed/dynamic 대상은 Integer·Float·Decimal·UUID·Char/Text·Date·DateTime·Time·Duration의 nullable/non-null과 Boolean이다.
Forward 대상의 11종 scalar와 JSON 문서/경로를 root field와 함께 typed DTO로 선택하며, 관련 값은 항상 nullable pointer로 반환한다. 같은 forward scalar에 ASC/DESC를 제공하고 정렬에만 등장하는 optional 경로도 행을 보존한다.
원본 field metadata는 유지하고 target 부재·SQL NULL을 표현한다. Reverse value 선택·관계 ordering/F/MIN/MAX는 미지원이다.
여러 selected direct·nested forward relation과 다른 forward/reverse filter JOIN의 All/First·중복·Distinct·슬라이스를 지원한다.
선택한 경로의 모든 prefix를 한 SQL로 읽고 하위 관계 접근에 cache를 넘긴다. 입력 tree는 깊이 64·중복 포함 1024 node로 제한하며
Count는 구조·binding 검사 뒤 projection을 제외한다. 실행 환경별 근거는 [테스트 증거](status/TEST_EVIDENCE.md)가 소유한다.
Forward/reverse/ManyToMany의 mixed scalar 조건·AND/OR/NOT·isnull·IN을 같은 keyed AST로 처리한다.
한 Filter와 연속 Filter의 collection scope를 구분하고 중복·Distinct·Count와 부정 EXISTS를 유지한다.
ManyToMany direct/nested/filtered prefetch는 양방향·nullable/nonunique through·self와 여러 selection을 같은 runtime에서 처리한다.
기본 조회는 기존 through query와 target eager projection으로 999 owner key씩 읽은 전체 결과를 함께 반환한다.
Custom target filter를 위한 `ResultPrefetch`의 owner projection·join 재사용·batch 소속 검사를 양 DB compiler에 연결했다.
Nested typed/path API는 기본 collection query와 공통 materialization으로 연결했다.
Custom target Filter·OrderBy·Distinct와 하위 prefetch 설정을 generated API에 연결했다.
Held query의 조건·설정과 manager 변경 뒤 기본 조회를 구분한다. 설정된 target query의 eager·추가 prefetch에 하위 설정을 전달한다.
Owner별 slice를 Query AST·양 DB window compiler와 runtime/generated named snapshot에 연결했다.
Snapshot·Limit/Offset·Read는 일반 manager와 별도 결과를 유지하며 nested/eager graph와 session lifetime을 보존한다.
Custom ManyToMany는 grouping/membership에 같은 전체 owner 집합을 사용한다. 큰 integer IN은 SQLite JSON array parameter와
PostgreSQL bigint array parameter로 조회해 owner 집합을 나누거나 정수 정밀도를 낮추지 않는다.
설정된 prefetch query의 streaming은 아직 미지원이다.
단일 FK/역방향 OneToOne prefetch와 하위 컬렉션을 같은 graph로 연결하며 root eager와 직접 조합해 이미 읽은 부모를 재사용한다.
Reverse FK collection과 ManyToMany의 target eager·하위 prefetch를 같은 graph에 연결하고 파생 query의 설정을 유지한다.
Custom single prefetch query, 관계를 넘는 F와 collection value projection/ordering·일반 관계 집계는 미지원이다.
OneToOne reverse/mixed materialization과 facade selector·문자열 경로는 같은 JOIN/행 검증 경로에서 지원한다.
Incoming 정책을 가진 target의 outgoing FK는 보존하며 PROTECT·SET_NULL·삭제는 기존 AtomicRelation과 native FK 제약을 따른다.
[관계 lookup 의미](adr/0040-composable-typed-boolean-predicates-and-article-search.md#직접-forward-대상의-scalar-lookup)를 따른다.
유한한 self/cyclic forward 조회는 위 범위에 포함한다. 일반 순환 관계의 migration·mutation이나 object identity 공유까지 지원한다는 뜻은 아니다.
지원하지 않는 표현은 silent fallback이나 client-side full scan으로 바꾸지 않는다.

## SQLite 경계

Backend가 생성하는 모든 physical connection은 외래키 검사를 ON으로 설정하고 readback 1을 확인한 뒤 pool에 게시한다.
파일 reopen과 pool 증가/교체에도 적용한다. Driver-level OFF 옵션은 이 무결성 조건을 낮추지 않는다. Migration remake의
제한된 FK suspension은 별도의 admission/terminal 복원·폐기 경로가 계속 소유한다.

경로 최대 길이는 공통 AST의 64 hop이다. SQLite는 [물리 JOIN 제한](https://www.sqlite.org/limits.html#max_join)에 따라
root를 포함해 64 table까지만 허용한다. Outer SELECT와 각 collection EXISTS의 JOIN 수를 compiler가 각각 계산하여 초과를 빈 조회의 생략 전에 거부한다.

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
