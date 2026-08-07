# ADR-0007: M1 모델 runtime과 dynamic query 경계를 고정한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0002, Q-005, Q-006, Q-008, Q-009,
  QRY-001..QRY-010, SCH-001
- 대체하는 ADR: 없음

## 맥락

M1은 생성된 `Article`을 generic QuerySet으로 읽으면서 model type을 보존하고, Admin과
HTTP 같은 동적 입력도 typed API와 같은 Query AST로 보내야 합니다. 이를 위해
descriptor 초기화, nullable 조회 표현, dynamic lookup 오류 시점과 package
interface 소유권을 실제 compile/runtime 계약으로 좁혀야 합니다.

QRY-007은 SQL `NULL`, 빈 문자열, 일반 문자열을 구분하고 QRY-008/QRY-010은 잘못된
동적 lookup이 evaluation이 아닌 construction 단계에서 실패할 것을 요구합니다.
QRY-009는 query 구성 중 DB I/O가 0이어야 합니다. Partial update와 relation은 M1
범위가 아닙니다.

## 결정 기준

- 다른 model의 descriptor/predicate가 compile 단계에서 섞이지 않을 것
- package init panic과 mutable global registry를 만들지 않을 것
- runtime metadata가 Schema IR에서 생성되고 외부에서 변경되지 않을 것
- dynamic 오류가 DB 실행 전에 구조화된 error로 반환될 것
- typed/dynamic predicate가 같은 AST constructor를 사용할 것
- external consumer module과 dependency direction을 자동 검증할 수 있을 것
- M1 범위를 넘어 update/JSON/Form 의미를 성급히 고정하지 않을 것

## 고려한 선택지

### Generated zero-state concrete descriptor + consumer-owned interface

생성된 상태 없는 concrete type이 `orm.ModelDescriptor[M]`를 구현합니다. `Scan`의 반환
타입에 `M`이 들어가므로 다른 model descriptor interface를 잘못 만족하지 않습니다.
Metadata는 매 호출 독립적인 값을 반환합니다. 별도 freeze lifecycle이나 registry가
필요하지 않습니다.

### Runtime descriptor spec/freeze

검증 전후 type을 분리할 수 있지만 M1에는 relation binding이 없고, project bootstrap에
추가 오류 상태와 초기화 순서를 도입합니다. Cross-app relation 단면에서 다시 평가할 수
있습니다.

### Nullable wrapper 또는 `database/sql` nullable type

`Nullable[T]`는 API/JSON/Form 의미를 새로 만들고 `sql.Null[T]`는 생성 model을 SQL
표현에 결합합니다. 현재 조회 계약에는 `*string`의 세 상태로 충분합니다.

### Error-bearing fluent QuerySet

동적 오류를 QuerySet 내부에 저장하면 fluent syntax는 유지되지만 오류 관찰 시점이
evaluation으로 밀려 locked contract와 맞지 않습니다.

## 결정

- `orm`이 `ModelDescriptor[M]` interface를 소유합니다. M1의 interface는 독립 복사인
  `Metadata() ir.Model`과 `Scan(db.Row) (M, error)`를 제공합니다.
- Codegen은 model마다 exported zero-state concrete descriptor와 compile-time interface
  assertion을 생성합니다. Descriptor는 생성·compile 시점부터 frozen이며 mutator나
  runtime registration을 제공하지 않습니다.
- Nullable `CharField`의 M1 조회 type은 `*string`입니다. `nil`은 SQL `NULL`, non-nil
  `""`는 빈 문자열, 그 외 non-nil은 일반 값입니다. Codec 내부 scan destination은
  `sql.NullString`을 사용할 수 있지만 생성 model field에 노출하지 않습니다.
- Dynamic 입력은 순서가 있는 `[]orm.LookupInput`으로 받고
  `orm.ParseDynamic[M]`이 즉시 `[]Predicate[M]` 또는 구조화된 error를 반환합니다.
  성공한 predicate는 typed `Filter` chain에 그대로 합류합니다.
- M1 오류 code는 `unknown_field`, `unsupported_lookup`, `disallowed_lookup`,
  `invalid_value`이며 `errors.Is/As`로 분류할 수 있습니다.
- `exact`, CharField의 `icontains`, 모든 field의 `isnull`을 지원합니다. Django처럼
  `null=False` field의 `isnull=true`도 유효한 query이며 빈 결과를 만듭니다.
- Query plan은 copy-on-write 불변 값입니다. M1 QuerySet에는 result cache를 넣지 않으며
  `All` 호출마다 평가합니다. 이것은 Q-007의 최종 cache 계약이 아닙니다.
- 실제 nested external module compile fixture와 `go list -json` direct import 검사를
  dependency/타입 안전 gate로 사용합니다.

## 결과

생성 descriptor는 package init error나 race 가능한 freeze 상태 없이 읽을 수 있고,
metadata copy를 변형해도 다음 조회가 달라지지 않습니다. `Predicate[Article]`과
`Predicate[Other]`는 compile 단계에서 구분됩니다. Dynamic lookup은 construction
단계에서 실패하고 executor를 호출하지 않으며 typed/dynamic 성공 경로가 같은 AST를
만듭니다.

`*string`은 단순하지만 model을 JSON/Form에 노출할 때의 표현과 partial update에서
“생략”을 표현하지 못합니다. 이 비용은 M1 범위를 정확히 제한하는 대신 수용합니다.

## 의도적으로 결정하지 않은 것

- create/update patch에서 omitted, explicit NULL, zero value를 구분하는 type
- JSON, Form, Serializer의 nullable 표현
- QuerySet result/error cache와 여러 terminal operation 간 공유
- QuerySet, transaction, hook의 전체 goroutine safety 계약
- relation binding을 위한 registry와 descriptor 확장
- pre-1.0 public API upgrade 정책

## 검증

- generated descriptor compile assertion과 잘못된 model 대입 negative compile
- nullable `NULL`/`""`/일반 값 및 row 간 pointer 비공유 SQLite test
- typed/dynamic AST equality, 원본 plan 불변, construction I/O 0
- unknown/unsupported/disallowed/invalid dynamic error test
- external consumer positive compile와 잘못된 predicate/value negative compile
- descriptor metadata copy와 concurrent read race test
- M0 QRY-001..QRY-010, SCH-001 differential comparison
