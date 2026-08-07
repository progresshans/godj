# ADR-0011: M2 Save는 typed option과 Manager orchestration으로 구현한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0005, GDJ-0006, MOD-008..MOD-019, Q-006
- 대체하는 ADR: 없음

## 맥락

GDJ-0005는 Django의 mutable model `save()`에서 관찰되는 새 객체 INSERT, fully loaded
객체의 전체 field UPDATE, `update_fields`, force mode, explicit primary key와 transaction
rollback 의미를 12개 계약으로 고정했습니다. 이 의미를 Go에 옮길 때 Python의 private
`_state`나 문자열 field API만 복제해서는 안 됩니다.

현재 GoDj에는 generated model별 hidden auto-key presence, `WriteDescriptor[M]`, immutable
`InsertPlan`/`UpdatePlan`, exact affected-row를 반환하는 `db.Mutator`와 caller-owned
`Atomic` session이 있습니다. 반면 기존 `Manager.Update`는 0행을 모두 오류로 처리하고,
generated `MutationPatch`는 empty patch를 거부하므로 Save의 no-op과 UPDATE→INSERT
fallback을 그대로 표현할 수 없습니다.

## 결정 기준

- 모델이 다른 field mask를 컴파일 시점에 섞을 수 없어야 함
- 기본 Save와 explicit empty `update_fields`를 구분해야 함
- primary key field는 typed API에서 컴파일되지 않고 dynamic API에서는 I/O 전에 거부돼야 함
- auto-key의 숫자 zero와 presence 상태를 섞지 않아야 함
- UPDATE 0행을 mode별 fallback 또는 `NotUpdated`로 구분해야 함
- Save가 내부 transaction을 열거나 rollback 뒤 Go object를 되돌리지 않아야 함
- 기존 Query mutation AST와 backend interface를 불필요하게 확장하지 않아야 함
- 실제 제품 API로 MOD-008..019를 관찰할 수 있어야 함

## 고려한 선택지

### generated instance method를 authoritative API로 사용

`article.Save(ctx, backend, ...)`는 Django와 표면이 가깝습니다. 그러나 generated model에
`Save` field가 있으면 Go에서 field와 method가 충돌하며, backend와 Manager binding도
각 model method에 반복됩니다. 첫 단면에서 이 이름을 모든 사용자 model에 예약하지
않습니다.

### 기존 Create와 Update를 조건별로 호출

기존 API를 재사용할 수 있지만 `CreateInput`을 이미 만들어진 instance로 역변환해야 하고,
explicit-key INSERT를 표현하지 못합니다. 또한 empty patch 오류와 0-row 오류가 Save의
no-op/fallback 의미를 잃게 합니다.

### generic Manager가 별도 Save orchestration을 소유

`WriteDescriptor[M]`에서 model value를 assignment로 만들고 기존 immutable Query plan을
실행합니다. model별 codegen은 explicit key constructor와 generic option type을 추론하는
작은 helper만 생성합니다. 실행 정책은 ORM에, SQL과 constraint 분류는 backend에
남습니다.

## 결정

Authoritative API는 다음 generic Manager method입니다.

```go
func (m Manager[M]) Save(
    ctx context.Context,
    backend db.Mutator,
    value *M,
    options ...SaveOption[M],
) error
```

- `SaveOption[M]`은 interface가 아니라 private immutable state를 가진 concrete generic
  value입니다. 따라서 typed nil이나 외부 구현의 동적 dispatch가 없고 zero value는
  I/O 전에 invalid plan으로 거부합니다.
- Save는 caller의 instance pointer를 받습니다. Auto INSERT 성공 즉시 hidden key presence와
  값을 설정하고, outer transaction이 나중에 rollback돼도 object 값을 되돌리지 않습니다.
- Save는 `db.Mutator`만 받고 자체 transaction을 열지 않습니다. Atomicity는 caller가
  `db.Atomic` callback과 transaction-bound session으로 선택합니다.
- 옵션이 없으면 PK가 없는 instance는 non-PK field 전체 INSERT, PK가 있는 instance는
  writable concrete non-PK field 전체 UPDATE입니다.
- Default explicit-key UPDATE가 0행이면 같은 key를 포함한 INSERT로 fallback합니다.
  Force update의 0행은 `not_updated/force_update_missing_row`이며 INSERT하지 않습니다.
- Explicit empty update mask는 성공 no-op이고 backend I/O가 0입니다.
- UPDATE가 1행보다 많거나 음수인 backend 결과는 fallback하지 않고 structured
  unexpected-rows 오류입니다.

Typed mask는 ORM이 구현을 봉인한 `WritableField[M]`을 사용합니다. Private marker
method의 signature가 `M`을 직접 포함하므로 `WritableField[Article]`과 다른 model의
field는 구조적으로 같지 않습니다. Auto primary-key field wrapper는 이 interface를
구현하지 않아 typed mask에서 컴파일되지 않습니다.

```go
orm.UpdateFields(models.ArticleFields.Title)
models.ArticleUpdateFields() // explicit empty mask
```

호환 계약의 invalid primary-key name을 실제 제품 경로에서 검증하기 위해 secondary
dynamic option도 같은 Save plan builder로 수렴시킵니다.

```go
orm.UpdateFieldNames[models.Article]("id")
```

Dynamic name은 descriptor metadata에서 다시 resolve하고 unknown/primary-key field를
backend I/O 전에 structured field error로 반환합니다. Typed field도 내부 reference를
그대로 신뢰하지 않고 같은 metadata에 다시 결속합니다.

Codegen은 model별로 다음 최소 binding만 생성합니다.

- `New<Model>With<PrimaryKeyGoName>(key)` — zero 값도 explicit presence로 기록
- `<Model>UpdateFields(...)` — explicit empty mask와 generic type inference 보조
- `<Model>UpdateFieldNames(...)` — dynamic compatibility path
- `<Model>ForceInsert()` / `<Model>ForceUpdate()` — 인자 없는 generic option 보조

Generated instance `Save` method는 이번 단면에서 만들지 않습니다. Public Query AST에는
Save mode를 넣지 않으며, ORM 내부 immutable execution plan이 optional UPDATE/INSERT,
zero-row action, generated-key assignment과 no-op을 보존합니다.

SQLite는 `modernc.org/sqlite.Error.Code()`의 primary-key constraint extended code를
stable `integrity_error/unique_primary_key`로 분류하고 원 cause를 보존합니다. Conformance
adapter가 driver 문자열을 임의로 해석해 제품 오류를 가장하지 않습니다.

## 결과

- generated model별 field type과 generics가 cross-model mask를 compile-time에 차단합니다.
- 문자열 없는 정상 경로와 runtime dynamic 호환 경로가 같은 metadata/plan 의미를 씁니다.
- 기존 `query.InsertPlan`, `query.UpdatePlan`, `db.Mutator` signature는 유지됩니다.
- explicit key presence는 public 숫자 값 추론 대신 generated constructor로만 설정합니다.
- default Save는 dirty tracking을 도입하지 않고 fully loaded field 전체를 씁니다.
- pointer mutation 때문에 같은 instance의 concurrent Save 안전성은 보장하지 않습니다.

## 의도적으로 결정하지 않은 것

- generated instance method 또는 project-level bound backend API
- deferred field와 automatic deferred mask
- signal/hook, relation/cascade, inheritance
- custom/composite primary key, bulk save, upsert 일반화
- concurrent Save serialization 또는 optimistic locking
- PostgreSQL/MySQL/Oracle constraint code mapping

## 검증

- 별도 Go 1.26.5 compile spike에서 model marker가 없는 generic field interface의
  cross-model false acceptance와, `M` marker 추가 뒤 정상 inference/오용 compile 실패를
  비교합니다.
- generated helper의 non-empty/empty mask, force option과 explicit zero/nonzero key를
  external consumer compile test로 검증합니다.
- primary-key dynamic mask, nil context/backend/value와 force validation이 Mutator 0회인지
  fake backend로 검증합니다.
- default all-field, partial field, force, UPDATE 0행 fallback과 exact INSERT/UPDATE sequence를
  immutable plan/unit test로 검증합니다.
- SQLite primary-key conflict, affected-row, rollback/object state와 resource/race test를
  실행합니다.
- GoDj adapter가 MOD-008..019를 실제 product package로 실행해 Django oracle과 0-diff인지
  확인하고 기존 M1/M2 22개를 유지합니다.
- generator golden/hash/version, last-good preservation, full vet/race/CGO=0와 exact oracle
  gate를 통과합니다.

결정 전 spike는 저장소 밖 `/tmp/godj-save-api-spike.pSZUC4`에서 수행했습니다.
`go test ./candidate ./models . -count=1`, 같은 범위 `-race`, `go vet`과
`-shuffle=on -count=20`이 통과했습니다. Cross-model field, primary-key typed field,
cross-model option과 generated instance method/field 충돌 fixture는 각각 의도한 compiler
오류를 냈습니다. 이 `/tmp` 경로는 재현 보조 자료이며 제품 검증 증거는 GDJ-0006의
checked-in compile/runtime/differential gate로 다시 기록합니다.
