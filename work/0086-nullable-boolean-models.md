---
id: GDJ-0086
status: completed
updated: 2026-09-20
baseline_commit: "6d3d42bd4023b9bc8bbe4588fd5645a949b4df9f"
integration_owner: "root"
---

# Nullable Boolean의 세 상태를 모델과 소비자에 연결

## 결과와 범위

Helpdesk ticket의 검토 결과처럼 아직 결정하지 않음·참·거짓을 구분하는 nullable Boolean을 지원한다.
Schema IR의 nullable 속성을 generated pointer/field API, typed/dynamic query, 실제 SQLite/PG migration·CRUD,
선택한 Form/Admin·JSON/OpenAPI/client까지 연결한다. 생략·명시적 null·false를 서로 바꾸지 않는다.
기존 nonnullable Boolean의 checkbox와 default 동작, 인증·권한·mutation/transaction 소유권은 유지한다.

관련 의미는 [Schema IR](../docs/adr/0001-schema-ir-as-canonical-source.md),
[typed scalar](../docs/adr/0041-typed-scalar-comparisons-and-field-references.md),
[Form](../docs/adr/0043-safe-template-and-model-form-validation.md),
[OpenAPI](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)를 따른다.
역사 migration의 일반 nullability 변경·backfill은 이번 nullable field 추가와 별개다.

## 확인한 경계

- IR과 SQLite schema compiler가 nullable Boolean을 명시적으로 거부한다.
- Generator의 Boolean lowering에는 nullable ORM field가 없고 model Form projection도 nullable을 거부한다.
- Query value와 scanner는 bool/null 값을 이미 구분하지만 generated/typed API·write·relation 경계를 함께 확인해야 한다.
- 고정 Django 6.1의 nullable model Boolean은 NullBooleanField/NullBooleanSelect를 사용한다. Checkbox를 그대로 재사용하면
  미확인과 false를 구분할 수 없다. 직접 field cleaning과 widget 이후 cleaning, JSON parser는 따로 관찰한다.
- 값이 있는 기존 ticket에 nullable/no-default field를 추가하면 NULL이어야 한다. Required/default addition의 기존 empty-table
  guard를 약화하지 않는다. GDJ-0085의 field insertion과 prefix publication을 사용한다.

## 구현과 검증

1. 독립 pinned Django/DRF에서 모델·Form/widget·serializer의 세 상태와 생략/빈 입력을 관찰하고 기준을 고정한다.
2. IR·generator·typed/dynamic ORM와 양 DB·정의 codec·autodetector의 nullable Boolean 경계를 함께 구현한다.
3. 세 상태 Form/Admin 표시·편집과 JSON PUT/PATCH·OpenAPI를 실제 Helpdesk flow 및 별도 생성 client에 연결한다.
4. 명시적 null/false·기존 행·새 연결·failed mutation의 상태, query의 부정/IN/관계, pointer/cache copy를 검증한다.
5. 변경 묶음 뒤 affected tests/generated drift와 필요한 DB/race checkpoint를 실행한다. 실행 상세는 TEST_EVIDENCE에 기록한다.

## 현재 상태와 다음 행동

IR·nullable typed field/scan·generator·양 DB schema/catalog·정의 codec·nullable/no-default 자동 Add를 연결했다.
Helpdesk reviewed field와 0007 migration은 실제 generate/makemigrations 명령으로 생성했다. 기존 행의 NULL 추가·역방향·재접속,
Form의 세 상태 widget과 Admin 초기값·snapshot·재검증, 실제 PUT/PATCH와 별도 OpenAPI client까지 구현했다.
부정 exact와 NULL을 포함한 IN의 다른 의미, generated default/명시적 null/false, query 결과 및 eager Unwrap snapshot 복사를
양 DB에서 검증한다. Related facade의 대상 객체는 기존 계약대로 pointer identity를 보존한다.

제품·생성 소비자 연결과 로컬 영향 검증을 완료했다. 초기 검증에서 드러난 Admin의 null 재검증 경계를 수정했다.
기존 Draft PR에 제품을 통합했고 해당 source의 Hosted ORM 검증도 완료했다. 실행 결과·source·환경과 초기 실패의 구분은
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.
