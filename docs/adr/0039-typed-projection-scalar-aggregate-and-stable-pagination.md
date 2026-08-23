# ADR-0039: Typed projection, scalar aggregate와 stable pagination을 하나의 read shape로 확장한다

- 상태: Accepted
- 날짜: 2026-08-23
- 관련 work/contract: GDJ-0039, QRY-022..033, Q-011, M4
- 대체하는 ADR: 없음

## 맥락

ADR-0012는 immutable QuerySet plan과 mutable evaluation cache를 분리하고 `All`, cold/warm `Count`, `Exists`,
`At`, `First`, `Iterate`의 수명을 고정했습니다. 당시 query AST에 scalar aggregate, offset과 DTO projection이
없어 cold `Count`는 전 행을 전송해 drain하고 `At`은 offset 없이 앞 행을 순회했습니다.

현재 `query.Plan`의 columns는 모델의 허용 source metadata와 실제 SELECT result shape를 동시에 뜻합니다.
Projection이 이 목록을 단순 교체하면 projection에 포함하지 않은 field로 filter/order할 수 없고, aggregate를
모델 row로 가장하면 descriptor scan/cache 계약이 깨집니다. PostgreSQL `DISTINCT`/`ORDER BY`, sliced aggregate와
empty `MAX`도 backend별 우연한 SQL에 맡길 수 없습니다.

Go method는 receiver에 없는 새 type parameter를 선언할 수 없으므로 `QuerySet[M].SelectInto[R]` 형태도 사용할
수 없습니다. Typed DTO/aggregate 결과는 top-level generic function과 generated model-specific bridge가 소유해야
합니다.

## 결정

### Plan의 source와 result를 분리한다

`query.Plan`은 immutable source field universe와 sealed result shape를 별도로 소유합니다.

- source fields는 condition, ordering, relation provenance와 full-model validation authority입니다.
- result shape는 full model, ordered scalar projection 또는 scalar aggregate 중 하나입니다.
- projection과 aggregate field는 exact source universe에 속해야 합니다.
- relation projection과 DTO projection/aggregate는 같은 plan에서 결합하지 않고 structured unsupported로
  fail-closed합니다.
- 기존 `db.Queryer.Query(context.Context, query.Plan)` port는 바꾸지 않습니다.

Full-model plan은 기존 field order와 descriptor scan 의미를 그대로 사용합니다. DTO projection은 exact selected
field order만 반환하고 model evaluation cache를 읽거나 채우지 않습니다. Aggregate도 별도 row shape이며 model
cache를 오염시키지 않습니다.

### Typed ORM 표면

Model-specific scalar fields는 sealed `ScalarField[M,V]` capability를 구현합니다. DTO projection은
`Projection[M,R]`, fixed-arity `Project1`..`Project4`와 top-level
`SelectInto[M,R](context.Context, QuerySet[M], Projection[M,R])`를 사용합니다.

Scalar aggregate는 typed `CountRows`, `Max` expression, fixed-arity aggregate result builder와 top-level
`AggregateInto`를 사용합니다. Empty `MAX`는 zero value가 아니라 explicit nullable result입니다. Generated
project facade는 raw backend나 QuerySet internals를 공개하지 않고 model-specific top-level generic bridge를
생성합니다.

Exact exported names와 supported arity는 GDJ-0039 compile gates에서 고정하며 arbitrary reflection, map/string
projection과 runtime wrapper decoding은 도입하지 않습니다.

### Distinct, offset과 cache 의미

`QuerySet.Distinct()`와 `QuerySet.Offset(int) (QuerySet, error)`는 immutable derived plan과 새 evaluation state를
만듭니다. Negative/overflow offset은 backend I/O 전에 structured query error입니다. Offset-only SQL도 SQLite와
PostgreSQL에서 같은 의미를 냅니다.

- full `All`은 distinct/offset/limit 결과를 자기 cache에 저장합니다.
- warm `Count`는 ADR-0012대로 그 cache length를 재사용합니다.
- cold `Count`는 aggregate plan을 실행하고 full cache를 채우지 않습니다.
- projection/aggregate terminal은 source QuerySet의 model cache를 읽거나 채우지 않습니다.
- `Count`와 aggregate는 filter/distinct/order/offset/limit이 이미 적용된 logical QuerySet을 대상으로 합니다.
  Backend compiler는 필요하면 derived table을 사용하며 aggregate row 자체에 slice를 잘못 적용하지 않습니다.

PostgreSQL에서 distinct projection의 ordering expression이 SELECT result에 없으면 암묵적으로 result를 넓히지
않고 pre-I/O structured unsupported error를 반환합니다. SQL 문자열 동일성보다 결과, 오류, query count와 cache
의미를 계약합니다.

### Article 사용자 흐름

Article app은 query-string parsing과 page-size cap을 소유합니다. Web Core public API는 넓히지 않습니다.

```text
published filter → distinct → stable ID order → offset/limit
→ ID/title/published DTO projection
→ matching count/latest ID aggregate
→ SQLite/PostgreSQL에서 같은 rendered response
```

## 결과

- Source metadata를 projection result 목록으로 오용하지 않고 filter/order와 좁은 SELECT를 함께 지원합니다.
- Cold count가 전 모델 row를 전송하지 않으며 sliced/distinct 의미를 보존합니다.
- SQLite/PostgreSQL compiler가 같은 AST와 error ownership을 사용합니다.
- DTO와 aggregate result는 model descriptor/cache와 다른 명시적 typed lifetime을 가집니다.
- Generator는 새 generic bridge를 project bundle에 원자적으로 게시해야 하므로 partial/manual output 갱신을
  허용하지 않습니다.

## 의도적으로 결정하지 않는 것

- Q의 AND/OR/NOT tree와 F/field-to-field expression
- bulk create/update/delete와 cache invalidation
- `select_for_update`, transaction-bound QuerySet와 backend lock capability
- annotation/grouping/having, dynamic values, subquery, window function
- related-column projection/aggregate 또는 existing select-related projection과 DTO projection 조합
- Web Core pagination abstraction, MySQL과 추가 backend

## 검증

GDJ-0039는 QRY-022..033 Django reference/result 계약, core unit/compile/race tests, SQLite/PostgreSQL compiler와
actual integration, generated bundle drift/whole-candidate compile, Article loopback HTTP parity를 거칩니다. 작은
subtask마다 full matrix를 반복하지 않고 core, backend/generated, final frozen milestone의 세 green checkpoint를
사용합니다.
