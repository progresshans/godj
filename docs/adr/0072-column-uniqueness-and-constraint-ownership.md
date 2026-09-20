# ADR-0072: Column uniqueness and physical constraint ownership

- 상태: Accepted — 공통 선언·이력과 PostgreSQL 구현. SQLite·입력 소비자 연결은 GDJ-0095에서 진행 중.
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
Unique AlterField에는 물리 SQL이 필요하다. Metadata-only 빈 slot으로 성공할 수 없다.

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

## 남은 구현

SQLite의 명시적인 unique index 소유권과 Create/Add/remake에 필요한 ordered SQL 묶음을 연결한다.
현재 SQLite는 이 capability를 제공하지 않으므로 고유성 migration과 SQL projection을 거부한다.
Form/Admin/API의 사전 검증과 DB 충돌 오류, Helpdesk의 외부 참조, 실제 generated client까지 이어서 검증한다.
사전 검증이 통과하더라도 DB 제약을 최종 무결성 경계로 유지한다.

Composite/conditional/expression constraint, OneToOneField, nullable unique의 다른 NULL 정책과 일반 backfill은 추가 목표다.
기존 행이 있는 table에 default-bearing/required scalar를 추가하는 현재 미지원 정책도 유지한다.
이 ADR의 PostgreSQL 구현을 전체 고유성 기능이나 전체 프레임워크 완료로 간주하지 않는다.
Source·환경·실패·실행별 증거는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
