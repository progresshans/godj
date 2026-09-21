# ADR-0072: Column uniqueness and physical constraint ownership

- 상태: Accepted — 공통 선언·이력·ORM·양 DB와 Helpdesk Form/Admin/API/client 연결 구현. 통합 검증은 GDJ-0095에서 진행 중.
- 날짜: 2026-09-21
- 관련 작업: [GDJ-0095](../../work/0095-model-uniqueness.md)

## 모델 의미

외부 참조의 중복 저장을 막으려면 모델의 고유성 선언을 실제 DB 제약으로 연결해야 한다.
저장 전에 같은 값이 있는지 조회하는 검사는 동시 쓰기의 원자적 무결성 보장이 될 수 없다.
Schema IR의 `Field.Unique`와 `schema.Unique()`가 column 고유성의 정본이다.
12종 scalar와 현재 ForeignKey가 같은 속성을 사용한다. PK는 이미 고유하므로 별도의 Unique flag는 정규화한다.
Unique FK의 many-to-one metadata와 reverse collection API는 그대로다. OneToOneField를 암묵적으로 만들지 않는다.

SQL NULL의 기본 정책은 distinct다. 빈 문자열·zero UUID·JSON null·숫자값의 동등성은 SQL NULL과 구분한다.
각 backend의 실제 저장·equality·collation 정책을 따르며 기존 Float/Decimal/UUID/JSON 값 정책을 바꾸지 않는다.
고정된 [Django 관찰](../../conformance/runners/django/unique_reference.py)은 외부 결과의 기준이다.
[Django unique 계약](https://docs.djangoproject.com/en/6.1/ref/models/fields/#unique)과
[PostgreSQL unique constraint](https://www.postgresql.org/docs/17/ddl-constraints.html#DDL-CONSTRAINTS-UNIQUE-CONSTRAINTS)를 참조한다.

## 이력과 실행 권한

Generated descriptor/schema·project wire·historical Create/Add/Alter·canonical digest·자동 변경 계획이 고유성을 보존한다.
Unique 추가/제거는 전체 before/after field를 가진 단일 facet의 AlterField다. Choices·precision·다른 속성과 섞이면 거부한다.
명시적 false와 생략은 같은 의미다. 정규화된 PK에 별도 Unique를 기록한 historical wire는 거부한다.

`UniqueConstraints` capability는 제약의 생성·변경·제거와 유지되는 target/transitive 모델의 물리 검증을 함께 뜻한다.
지원하지 않는 backend는 transaction 전에 거부하며 순수 SQL projection도 선언을 버리지 않는다.
Unique AlterField에는 물리 SQL이 필요하다. Metadata-only 빈 group으로 성공할 수 없다.

## PostgreSQL

Native UNIQUE constraint와 그에 종속된 단일 column B-tree index를 사용한다.
`godj/postgres/unique/v1` domain과 길이로 구분한 table/column bytes의 SHA256 앞 192-bit로 이름을 정한다.
Constraint와 index가 같은 `godj_uq_` 이름을 사용하고, 기존 이름 소유권 검사가 table·PK·FK·sequence와 충돌을 거부한다.
Server truncation·자동 이름 변경·기존 객체의 묵시적 채택은 사용하지 않는다.

CreateModel과 AddField는 named UNIQUE를 함께 생성한다. Unique AlterField는 ADD/DROP CONSTRAINT이며
drop에는 RESTRICT를 쓴다. AddField의 reverse는 column과 그 소유 constraint/index를 제거한다.
기존 table lock·revision fence·transaction·최종 catalog 검증 뒤 recorder를 게시하는 순서는 유지한다.
기존 중복은 DB가 거부하고 전체 migration이 rollback된다. 값을 자동 삭제·변환하지 않으며 명시적 데이터 수정 후 재시도한다.
PostgreSQL identity의 충돌 시 sequence 할당 등 native transaction 의미를 재작성하지 않는다.

Catalog는 선언한 PK/FK/Unique의 정확한 집합을 검사한다. Unique는 이름·소유 index OID·column·validated·nondeferrable
형태여야 한다. Index는 unique/immediate/valid/ready/live, 단일 key와 include 없음, 기본 NULL distinct,
B-tree·column collation·해당 값 타입의 pg_catalog 기본 operator class·기본 방향·storage option 없음이어야 한다.
알 수 없는 index, standalone unique index, partial/expression/compound/include·deferrable·NULLS NOT DISTINCT와
다른 collation/operator class는 현재 선언으로 채택하지 않는다. PK index에도 같은 검증을 적용한다.
물리 속성은 [pg_index](https://www.postgresql.org/docs/17/catalog-pg-index.html)와
[pg_opclass](https://www.postgresql.org/docs/17/catalog-pg-opclass.html)에서 읽는다.

Django의 문자열 LIKE 보조 index와 내부 객체 이름을 복제하지 않는다. 현재 선언이 소유하는 constraint/index만 생성·검증한다.
일반 index 선언과 query 성능 최적화는 별도의 모델/실행 범위다.

Insert/update의 non-PK SQLSTATE 23505는 `integrity_error/unique_constraint`로 분류한다.
기존 `unique_primary_key`는 유지하고 원인 오류를 보존한다. 표시 문자열에는 native 오류의 중복 값을 넣지 않는다.
Go context 취소는 해당 context의 오류로 반환한다. 서버 오류 문구를 파싱해 field를 추측하지 않는다.

## SQLite

Table/column 선언과 별도의 `CREATE UNIQUE INDEX`를 한 operation에서 순서대로 실행한다.
`godj/sqlite/unique/v1` domain과 길이로 구분한 table/column bytes의 SHA256 앞 192-bit를 사용해
56자 `godj_uq_` 이름을 결정한다. Index schema는 명시적 `main`이며 기본 BINARY ASC와 distinct SQL NULL을 사용한다.
Unique AlterField는 CREATE/DROP INDEX다. Scalar RemoveField는 소유 index를 먼저 제거한 뒤 column을 제거한다.
기존 이름의 객체를 `IF NOT EXISTS`로 묵시적으로 채택하지 않는다. [SQLite unique index](https://sqlite.org/lang_createindex.html)를 따른다.

물리 검증은 [index_list](https://sqlite.org/pragma.html#pragma_index_list)의 이름·unique·origin·partial과
[index_xinfo](https://sqlite.org/pragma.html#pragma_index_xinfo)의 column ordinal/name·방향·collation·key/auxiliary를 검사한다.
정확히 선언된 단일 BINARY ASC key와 auxiliary rowid만 허용한다. 현재 INTEGER PRIMARY KEY AUTOINCREMENT는
별도 index가 없으므로 미선언 index·자동 UNIQUE index·다른 column·compound/expression/partial·NOCASE/DESC를 거부한다.
직접 변경되는 모델뿐 아니라 유지되는 모든 direct/transitive target에도 같은 검사를 적용한다.
기존에 허용하던 untouched target의 미선언 nonunique index도 이제 명시적으로 거부한다.

Initial/final 상태와 중간 operation의 이름을 함께 예약한다. Main/TEMP의 table/view/index가 선언된 index 이름이나 소유권과
충돌하면 revision claim 전에 거부한다. Trigger 이름은 SQLite의 별도 namespace이므로 이름만으로 충돌시키지 않으며,
변경 table이나 control을 참조하는 기존 trigger 위험 검사는 유지한다. 무관한 table/trigger는 보존한다.
Catalog 객체의 소유 table과 INTEGER PRIMARY KEY의 물리 의미는 [sqlite_schema](https://sqlite.org/schematab.html)를 따른다.

FK RemoveField의 sealed remake는 해당 operation의 After 모델에 남는 unique index를 재생성한다.
기존 table의 index 이름은 DROP TABLE 뒤 해제되므로 row copy·table 교체·sequence 복원 뒤 index를 생성한다.
같은 step에서 앞선 operation이 uniqueness를 바꿨더라도 초기 물리 상태로 되돌리지 않는다.
모든 DDL과 최종 검증은 pinned transaction 안에서 실행하며 중간/최종 실패는 원래 operation이 소유한다.
실패 상태에서 recorder 기록과 commit을 거부하고 원래 행·index·sequence·revision/recorder·FK 설정을 복구한다.
제약을 정확히 검증하지 않는 legacy direct editor는 unique 모델/필드를 명시적으로 거부한다.

Insert/update의 구조화된 SQLite extended code 2067은 `integrity_error/unique_constraint`다.
Insert의 1555는 기존 `unique_primary_key`를 유지한다. Native cause는 보존하고 표시 문자열은 중복 값을 노출하지 않는다.
Context 취소를 우선하며 오류 문구만으로 충돌이나 field를 추측하지 않는다.

## 공통 사전 검증

`orm.Manager.ValidateUniqueCreate`/`ValidateUniqueUpdate`는 `context.Context`와 `db.Queryer`를 받고
`(validation.Errors, error)`를 반환한다. Create/Update와 같은 mutation 준비 단계에서 metadata·필수/default·typed 값·patch의
생략 필드 보존과 PK 소유권을 검사한다. 검증을 호출해도 저장하지 않으며 Create/Update에 묵시적 조회를 추가하지 않는다.
기존 생성 descriptor/input을 사용하므로 field 타입별 별도 validator 생성 코드를 만들지 않는다.

Create는 정규화된 기본값을 포함한다. Update는 명시적으로 지정한 필드만 검사하고 current instance의 presence-aware PK를 제외한다.
명시적으로 존재하는 0 PK도 제외하며 PK가 없는 instance나 PK를 바꾼 patch는 거부한다. SQL NULL은 distinct 정책에 따라 조회하지 않고
빈 문자열·false·zero UUID·JSON null은 실제 값으로 검사한다. Typed 입력 정리 이전의 원문이나 오류 메시지에서 값을 추측하지 않는다.

모든 Query AST를 I/O 전에 만들고 모델 선언 순서대로 조회한다. 각 조회는 현재 backend의 exact/storage equality를 사용하며
PK projection·LIMIT 1로 존재 여부만 확인한다. Result cache를 공유하지 않는다. 중복은 값/행 식별자를 담지 않는 field별
`validation.CodeUnique` (`unique`)다. DB 오류·row iteration/close 오류·context 취소는 일반 error로 반환하며 앞선 부분 진단을 게시하지 않는다.
새 field가 있는 미래 버전에서 이미 만들어진 manager의 metadata snapshot을 바꾸지 않는다.

권한 검사와 current object 조회는 호출자가 먼저 수행한다. 이 검사는 advisory이며 두 검사가 모두 통과한 뒤에도 실제 제약이
경쟁 쓰기를 거부할 수 있다. 사전 조회와 최종 insert/update 오류를 같은 의미로 사용자에게 전달하되 DB 제약을 생략하거나
실행 장애·취소·불명확한 transaction 결과를 정상적인 입력 오류로 숨기는 것은 소비자 연결에서 허용하지 않는다.

## Operation별 SQL 묶음

테이블 DDL과 별도 unique index DDL을 하나의 operation에 담을 수 있도록 backend SQL renderer를 `[][]string`으로 변경했다.
공통 root가 각 operation의 필요/metadata 규칙과 전체 자원을 확인한 뒤 순서대로 flat SQL을 게시한다.
양 DB renderer·프로젝트 runner와 소비자는 이 계약을 사용한다. 빈 group만 no-op이며 빈 body는 오류다.
이 pure projection 계약이 실제 DB의 DDL/rollback 소유권을 대신하지 않는다.
구체적인 출력·제한·실패 의미는 [ADR-0055](0055-project-linked-deterministic-migration-sql-projection.md)가 소유한다.

## 입력 소비자와 오류 소유권

`validation.Reject`는 입력 진단과 내부 원인을 분리한다. 표시 문자열과 `unique` 진단에 중복 값이나 기존 행을 넣지 않는다.
`validation.Rejected`는 직접 전달된 rejection만 인식한다. Wrapped/joined error를 따라가 입력 오류로 바꾸지 않으므로
rollback 실패·connection cleanup 실패·commit/transaction outcome unknown을 정상적인 입력 거부로 숨기지 않는다.
진단이 비어 있으면 원인 오류를 그대로 반환한다. 호출자는 저장이 commit되지 않았음을 확인한 뒤 rejection을 게시한다.

SQLite의 coordinated/relation transaction은 rollback과 connection 반환이 모두 성공한 경우 callback 오류를 그대로 반환한다.
Cleanup 실패가 추가되면 양쪽 원인을 보존하고 기존 unknown outcome·quarantine 의미를 유지한다.
Helpdesk는 transaction 뒤 확인한 context 취소도 실행 오류로 유지한다.

`Form.WithErrors`는 bound form에 post-clean 진단을 더한 새 값을 만든다. 거부된 필드만 cleaned data에서 제외하고
initial·changed·나머지 cleaned data를 보존한다. Non-field 오류는 cleaned data를 지우지 않으며 unknown field는 설정 오류다.
Admin의 create/update callback은 이 rejection으로 같은 화면에 안전하게 escape한 제출 원문과 field/non-field 진단을 표시한다.
진단은 선택된 form field나 `validation.NonField`만 허용한다. 실행 오류와 취소는 오류 경로에 남긴다.
API는 `ValidationErrorResponse`로 같은 진단을 기존 HTTP 400 `validation_error` envelope에 담는다.

Helpdesk는 nullable `external_reference`에 `schema.Unique()`를 선언한다. Non-null UUID는 category를 넘어 전역 고유하며
SQL NULL은 여러 행에 허용한다. UUID 별칭은 정규화된 값으로 비교하고 zero UUID는 실제 고유값이다.
Create/PUT/PATCH/Admin은 인증·CSRF·권한과 대상 category/행 확인 뒤 같은 transaction 안에서 사전 검증한다.
자기 행은 제외하며 부분 수정의 생략값과 명시적 NULL을 구분한다. 사전 중복은 `external_reference/unique`다.
사전 조회 이후에도 native constraint가 최종 무결성을 소유한다. Callback 내부의 직접 insert/update `unique_constraint`만
`__all__/unique`로 변환하며 오류 문구로 field를 추측하지 않는다. Transaction이 실패하면 다른 수정도 저장하지 않는다.
OpenAPI와 독립 generated client도 같은 응답을 소비한다.

실제 생성된 migration `0016_alter_ticket_external_reference`는 기존 UUID column에 고유성을 추가한다.
기존 중복 데이터는 migration을 실패시키며 값이나 이력을 자동 삭제하지 않는다. 명시적 수정 후 재시도하고 재접속해 확인한다.

## 검증과 남은 범위

이 수직 연결의 통합 milestone은 [GDJ-0095](../../work/0095-model-uniqueness.md)에서 완료했다.
실행 source와 환경별 결과는 TEST_EVIDENCE가 소유하며 이후 변경의 검증으로 옮겨 쓰지 않는다.

Composite/conditional/expression constraint, OneToOneField, nullable unique의 다른 NULL 정책과 일반 backfill은 추가 목표다.
기존 행이 있는 table에 default-bearing/required scalar를 추가하는 현재 미지원 정책도 유지한다.
이 ADR의 양 DB 구현을 전체 고유성 기능이나 전체 프레임워크 완료로 간주하지 않는다.
Source·환경·실패·실행별 증거는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
