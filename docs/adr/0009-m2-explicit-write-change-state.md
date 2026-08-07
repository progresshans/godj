# ADR-0009: M2 write 입력은 변경 의도를 명시적으로 보존한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0003, GDJ-0004, MOD-001..MOD-007, Q-006
- 대체하는 ADR: 없음

## 맥락

M1의 nullable `CharField` read 표현인 `*string`은 읽은 `NULL`, 빈 문자열, 일반 문자열을
구분합니다. 그러나 update 입력에서 `nil` 하나로는 “필드를 변경하지 않음”과 “SQL
NULL로 변경”을 동시에 표현할 수 없습니다.

고정된 Django oracle은 create에서 nullable 값의 omitted와 explicit `None`이 모두
`NULL`이 될 수 있음을 보이지만, MOD-003과 MOD-004는 partial update에서 omitted와
explicit NULL이 서로 다른 동작임을 보입니다. 모델의 zero value나 포인터만으로 이
차이를 추론하면 필드 기본값과 변경 의도가 사라집니다.

## 결정 기준

- MOD-001..MOD-007의 결과와 DB 부작용을 표현할 수 있어야 함
- omitted, NULL, zero/empty value를 reflection 없이 구분해야 함
- nullable이 아닌 필드의 NULL을 가능한 한 compile 시점에 막아야 함
- create와 partial update의 서로 다른 omitted 의미가 API에 드러나야 함
- generated model read 표현과 write patch 표현을 혼동하지 않아야 함

## 고려한 선택지

### 포인터와 Go zero value만 사용

간단하지만 update에서 `nil`이 unchanged인지 NULL인지 알 수 없습니다. 빈 문자열과
false도 omitted로 잘못 해석될 수 있습니다.

### 모델 내부 dirty map

Django instance와 비슷한 `Save()` 경험을 만들 수 있지만 field name 문자열, hidden
mutation, reflection과 복사 시 dirty state 의미가 먼저 고정됩니다. M2 첫 단면의 typed
compile gate를 약화합니다.

### generated create/patch 입력과 generic change state

공통 core가 `unset`, `set(value)`, `set_null` 상태를 보존하고 codegen이 nullable과
required 제약을 필드별 API로 제한합니다. 타입 수는 늘지만 변경 의도가 AST와 backend까지
손실 없이 흐릅니다.

## 결정

M2 product는 generated read model과 별개의 immutable create/patch builder를 만듭니다.

- create의 omitted는 field default 또는 Django 호환 create 규칙을 적용하라는 뜻입니다.
- update의 omitted는 기존 DB 값을 변경하지 않는다는 뜻입니다.
- nullable field는 explicit NULL과 `Set(value)`를 구분합니다.
- empty string과 `false`는 정상적인 명시 값이며 omitted로 해석하지 않습니다.
- generic core는 `Change[T]`와 `NullableChange[T]`를 별도 불변 값으로 보존합니다.
- codegen은 모델·필드별 value receiver `With...` method를 생성합니다. Nullable field만
  `With...Null`을 가지며 non-null field에는 NULL method가 생성되지 않습니다.
- generated input의 `BuildCreate`/`BuildPatch` 반환 타입은 `Mutation[M]`으로 model type을
  generic Manager에 결속합니다. Manager method는 receiver에 없는 새 type parameter를
  선언하지 않습니다.
- 첫 public 단면은 `Manager.Create`, `Manager.Update`, `Manager.Delete`와 generated
  input을 사용합니다. Mutable instance dirty map과 `Save()`는 채택하지 않습니다.
- validation/coercion 오류는 DB I/O 전에 구조화된 error로 반환합니다.
- Schema IR v2는 typed scalar default의 존재를 보존합니다. 현재 `Article.published`의
  application default `false`를 IR에 기록하며, nullable/no-default create omission은
  SQL NULL로 수렴합니다.
- generated model은 auto primary key의 값과 존재 여부를 분리해 보존합니다. Delete는
  성공한 instance의 key 존재 상태를 명시적으로 지우며 adapter가 단순히 `ID == 0`을
  NULL로 추측하지 않습니다.

대표 사용 형태는 다음과 같습니다.

```go
created, err := models.ArticleObjects.Create(
    ctx,
    backend,
    models.NewArticleCreate("Created").
        WithPublished(true).
        WithSummary("Written"),
)

updated, err := models.ArticleObjects.Update(
    ctx,
    backend,
    created,
    models.ArticlePatch{}.WithSummaryNull(),
)
```

Builder는 value receiver로 새 값을 반환합니다. `ArticleCreate{}` zero value 자체는 Go에서
막을 수 없으므로 모두 omitted인 입력으로 해석하고 required/default validation을 I/O 전에
수행합니다.

## 결과

Query/mutation plan과 SQLite binding까지 사용자 의도를 잃지 않고 전달할 수 있습니다.
대신 generated API가 read model만으로 write를 수행하는 형태보다 커지며, create와 update
입력의 역할을 문서와 codegen golden test에서 분명히 해야 합니다.

단순 `Change[T]` 하나에 NULL state를 포함하는 후보는 `Change[string]`인 non-null
`Title`에도 NULL constructor가 대입되어 실제로 compile됐으므로 거부했습니다.
`Change[*T]` 후보는 pointer alias와 generic null type inference 문제가 있어 거부했습니다.

## 의도적으로 결정하지 않은 것

- instance `Save()`와 dirty tracking을 추가할지 여부
- loaded/new/dirty state를 model 내부에 둘지 별도 wrapper에 둘지
- bulk create/update, hook/signal, database-generated default
- concurrent instance mutation과 write object의 goroutine safety

## 검증

- Go 1.26.5 별도 module compile spike에서 positive 후보와 negative compiler fixture 실행
- non-null NULL, wrong scalar, nullable/non-null wrapper 혼합과 존재하지 않는
  `WithTitleNull`이 compile 실패하는지 확인
- `BuildCreate() Mutation[M]`의 cross-model input이 compile 실패하는지 확인
- nullable/non-null/omitted constructor의 external positive·negative compile test
- state round-trip과 immutable mutation plan unit/property test
- MOD-001..MOD-007 differential comparison
- validation failure의 zero-I/O와 SQLite rollback/resource cleanup test

GDJ-0004에서 이 경계를 제품 codegen/generic Manager/SQLite write로 구현했고
[EVID-20260808-003](../status/TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton)의
MOD-001..007 differential과 external compile/race gate로 검증했습니다. Instance
`Save()`의 외부 의미는 GDJ-0005의 MOD-008..019로 고정됐습니다. Fully loaded default
save가 dirty-only가 아니라 field 전체를 쓴다는 결과 때문에 hidden dirty map은 다음
단면의 전제에서 제외합니다. Typed field mask, force mode와 explicit-key constructor를
이 API에 연결하는 public shape는 GDJ-0006/ADR-0011에서 결정합니다.
