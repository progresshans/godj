# ADR-0043: Safe Template Runtime and Shared Model Form Validation

- 상태: Proposed
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0043](../../work/0043-safe-template-validation-session-auth-and-article-admin.md), WEB-021..027, FRM-001..005, Q-014, M5/M6
- 선행 결정: [ADR-0001](0001-schema-ir-as-canonical-source.md), [ADR-0002](0002-codegen-generics-runtime-metadata.md),
  [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md)
- 대체하는 ADR: 없음

## 맥락

Current Article Web slice는 app-owned DTO를 Go `html/template`로 render합니다. 이 경계는 generated wrapper와 lazy state를
renderer에서 분리하지만 GoDj project의 reusable template language, inheritance, form validation 또는 CSRF slot은 제공하지
않습니다. Admin을 app마다 직접 작성하면 template, model metadata, validation과 HTML field order가 서로 다른 두 번째 schema
원본을 만들게 됩니다.

Django template resolver는 일부 zero-argument callable을 자동 호출합니다. Go에서 같은 behavior를 reflection으로 복제하면 public
method set, network/DB I/O와 mutation을 template text가 암묵적으로 실행할 수 있습니다. 반대로 Go `html/template`의 arbitrary
method/FuncMap 표면을 그대로 export하면 Q-014를 해결하지 못합니다.

Current Schema IR은 field declaration order, kind, nullability, max length와 typed default를 이미 정규화합니다. Generated
`ArticleCreate`/`ArticlePatch`는 type-safe하지만 private field state 때문에 generic runtime form이 reflection 없이 직접 조작할 수
없습니다. 따라서 structural validation과 typed persistence conversion의 소유권을 분리해야 합니다.

## 결정 기준

- Template text가 arbitrary Go method/function/I/O를 실행하지 않는 안전한 기본값
- Context-aware HTML escaping과 explicit, 좁은 safe-value capability
- Startup parse failure와 bounded runtime resource use
- Schema IR을 Form/Admin structural metadata의 유일한 원본으로 사용
- Stable machine error code/order와 renderer-owned human message
- Bound/unbound, cleaned/initial/changed semantics와 invalid-write I/O 0
- Generated typed Create/Patch API와 import graph 보존
- Django 6.1 semantic comparison과 intentional difference의 명시적 분류

## 고려한 선택지

### Go `html/template`와 arbitrary DTO/method를 public API로 노출

빠르지만 DTL 사용자 경험이 없고 callable/FuncMap이 project마다 달라집니다. Template에서 application method를 암묵적으로 실행할
수 있으므로 채택하지 않습니다.

### Django callable resolver를 reflection으로 복제

Reference 결과에는 가깝지만 Go method의 context/error/mutation boundary를 숨깁니다. `alters_data` 같은 Python-specific marker를 Go
method에 재설계해야 하고 새 I/O 권한 표면이 생기므로 채택하지 않습니다.

### Generated Form type와 autosave를 즉시 codegen

Static API는 강하지만 current generated ABI와 publication bundle을 넓히고 field/widget/form lifecycle을 검증하기 전에 ABI를
고정합니다. 첫 slice에는 채택하지 않습니다.

### Closed value template와 IR-derived structural form

Template resolver는 명시적 immutable value만 읽고, form은 normalized metadata로 구조를 만들며 app-owned typed closure가
generated mutation으로 변환합니다. 현재 lower-layer ABI를 바꾸지 않으므로 이 선택지를 prototype합니다.

## Proposed 결정

1. `templates` package는 startup에서 `fs.FS` source를 parse하고 immutable concurrent-safe `Engine`을 게시합니다. Unknown
   grammar/filter/tag, duplicate/unclosed inheritance block, invalid name/path와 cap 초과는 structured error로 fail-closed합니다.
2. Resolver 입력은 String, Boolean, Integer, List, Object, Null과 trusted-code-only SafeHTML의 closed immutable value입니다.
   Raw `any`, reflection, generated model, `template.HTML`, caller FuncMap과 arbitrary method/function invocation은 받지 않습니다.
   Object key의 underscore/private 접근도 거부합니다.
3. First language subset은 variable/dotted lookup, `if`, `for`/`empty`, `extends`/`block`, `include`, closed
   `default`/`length`/`lower`, URL reverse와 CSRF capability입니다. Autoescape가 기본이며 SafeHTML만 escape를 생략합니다.
4. Parse tree depth/node count, include/inheritance depth, loop item count, context depth와 rendered bytes에 explicit cap을 적용합니다.
   Context cancellation은 render를 중단하고 partial output을 publish하지 않습니다.
5. `validation`은 stable `Field`, `Code`, ordered `Params`와 immutable Errors를 소유합니다. Exact localized prose/HTML은 contract가
   아니며 renderer가 code를 표시 문구로 변환합니다.
6. `forms`는 immutable Spec과 Data를 bind해 Bound/Valid/Errors/Cleaned/Initial/Changed를 제공합니다. Unbound form은 validation
   error를 만들지 않고 bound-empty는 required rule을 실행합니다. Field와 cross-field validator는 DB I/O를 소유하지 않습니다.
7. `forms/model`은 normalized `ir.Model`의 declaration order, Char/Boolean/nullability/default/max length를 structural field spec으로
   투영합니다. Auto primary key는 editable field가 아니고 unsupported kind/override는 startup error입니다.
8. Persistence는 Form core가 소유하지 않습니다. Article adapter가 typed cleaned accessor를 generated `NewArticleCreate`와
   `ArticlePatch`에 명시적으로 연결하고 Manager Create/Update를 호출합니다. Reflection, dynamic field assignment와 generic autosave는
   추가하지 않습니다.
9. WEB-027 Django callable observation은 GoDj no-call 결과와 다를 수 있습니다. Phase A actual에서 차이가 확인되면 oracle을
   완화하지 않고 좁은 DEV-0003 candidate와 explicit `deviation`으로 검토합니다.

## 결과

- Template와 Form/Admin은 runtime metadata를 재해석한 별도 schema가 아니라 normalized IR을 사용합니다.
- Template text의 권한이 순수 value resolution/rendering으로 제한되고 application I/O는 handler의 explicit context/error 경계에 남습니다.
- Generated ABI 변경 없이 첫 ModelForm user flow를 만들 수 있지만 app마다 typed persistence closure가 필요합니다.
- Django callable auto-call과 exact HTML/widget output은 호환되지 않을 수 있으며 decision/deviation을 명시해야 합니다.
- Full DTL/custom extension과 generated Form API는 후속 decision입니다.

## 의도적으로 결정하지 않은 것

- Custom tag/filter registry, arbitrary Go functions, async render와 streaming
- Template cache invalidation/watch/reload, filesystem override precedence와 i18n
- Multipart/file upload, widgets, FormSet, localization와 generic ModelForm autosave
- Generated Form types, Serializer public API와 API validation error format
- Full Django error messages, DOM/HTML whitespace와 widget class parity

## 검증

- [ ] WEB-021..027 and FRM-001..005 pinned Django reference, not-implemented fixtures and oracle no-rewrite
- [ ] Parser/resolver fuzz, unknown/private/callable negative tests, all resource caps and context cancellation
- [ ] Autoescape/SafeHTML, include/inheritance cycle and partial-output atomicity tests
- [ ] IR projection clone/order/default/null/max-length and unsupported-field startup tests
- [ ] Bound/unbound/cleaned/changed/error determinism, invalid mutation I/O 0 and external compile usability
- [ ] Race/CGO0/vet, SQLite/PostgreSQL Article form actual and final frozen hosted matrix

## 현재 구현 상태

Activation 시점에는 Proposed입니다. Product code, passing contract 또는 accepted deviation은 없습니다. Phase A compile/reference
checkpoint와 Phase B product gates 뒤에만 위 exact public names와 Accepted status를 확정합니다.
