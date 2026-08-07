# ADR-0009: M2 write 입력은 변경 의도를 명시적으로 보존한다

- 상태: Proposed
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0003, MOD-001..MOD-007, Q-006
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

## 제안 결정

M2 product spike는 generated read model과 별개의 create/patch 입력을 만듭니다.

- create의 omitted는 field default 또는 Django 호환 create 규칙을 적용하라는 뜻입니다.
- update의 omitted는 기존 DB 값을 변경하지 않는다는 뜻입니다.
- nullable field는 explicit NULL과 `Set(value)`를 구분합니다.
- empty string과 `false`는 정상적인 명시 값이며 omitted로 해석하지 않습니다.
- generic core는 세 상태를 불변 값으로 보존하고, codegen은 non-null field에 NULL
  constructor가 노출되지 않는지 external negative compile test로 검증합니다.
- validation/coercion 오류는 DB I/O 전에 구조화된 error로 반환합니다.

정확한 exported type과 constructor 이름은 compile spike 전까지 확정하지 않습니다.

## 결과

Query/mutation plan과 SQLite binding까지 사용자 의도를 잃지 않고 전달할 수 있습니다.
대신 generated API가 read model만으로 write를 수행하는 형태보다 커지며, create와 update
입력의 역할을 문서와 codegen golden test에서 분명히 해야 합니다.

## 의도적으로 결정하지 않은 것

- instance `Save()`와 Manager/Repository write API 중 최종 사용자 형태
- loaded/new/dirty state를 model 내부에 둘지 별도 wrapper에 둘지
- bulk create/update, hook/signal, database-generated default
- concurrent instance mutation과 write object의 goroutine safety

## 검증

- nullable/non-null/omitted constructor의 external positive·negative compile test
- state round-trip과 immutable mutation plan unit/property test
- MOD-001..MOD-007 differential comparison
- validation failure의 zero-I/O와 SQLite rollback/resource cleanup test
