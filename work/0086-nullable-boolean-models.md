---
id: GDJ-0086
status: active
updated: 2026-09-20
baseline_commit: "4320eba32a0dcb3a1e21b6244c87e32a89dad5b6"
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

별도 `feature/nullable-boolean-models`에서 현재 거부 지점과 고정 Django/DRF source를 확인하고 독립 관찰을 준비했다.
Model field cleaning, direct Form과 widget을 거친 Form, JSON serializer의 생략/null/default/partial, 실제 기존 table의 nullable
Boolean 추가·저장·재접속·조회·역방향을 raw로 보존한다. ORM의 명시적 bool 입력과 Python의 넓은 coercion을 혼동하지 않는다.
특히 `exclude(flag__in=[False, None])`와 `exclude(flag=False)`의 NULL 행 포함 여부가 다르므로 공통 AST의 기존 list 의미를 확인한다.
IR·nullable typed field/scan·generator lowering·양 DB schema/catalog·정의 codec·nullable/no-default 자동 Add의 기본 연결을 작성했다.
Affected package compile-only를 확인했다. 기존 nullable Boolean 거부 test는 default/identity 보존과 유효한 SQL·잘못된 default
거부 검사로 전환 중이다. Go runtime PASS는 아직 없고 Form/Admin·실제 생성 소비자·JSON/OpenAPI/client 연결을 이어가야 한다.
GDJ-0085의 Hosted 검증은 통합 담당이 기존 source에서 마무리하며, 이 작업의 결과로 합산하지 않는다.
