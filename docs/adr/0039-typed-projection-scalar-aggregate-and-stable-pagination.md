# ADR-0039: Typed projection, scalar aggregate와 stable pagination을 하나의 read shape로 확장한다

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-23
- 후속 반영: 2026-09-21, 관계 filter source의 root DTO와 forward scalar/JSON 선택
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
- root projection과 aggregate field는 exact source universe에 속해야 합니다. Forward 대상 선택은 별도의 bound route와 exact terminal field를 보존합니다.
- relation projection과 DTO projection/aggregate는 같은 plan에서 결합하지 않고 structured unsupported로
  fail-closed합니다.
- 관계 filter source에서 root scalar/JSON 경로 DTO를 선택할 수 있습니다. 기존 JOIN의 중복 행·nullable Boolean 의미와
  선택값 기준 DISTINCT·정렬·slice를 유지합니다. Forward 대상의 scalar와 JSON 문서·경로도 nullable DTO로 선택합니다. 관계 MIN/MAX와 related-object hydration의 DTO 결합은 별도 범위입니다.
- 관계 filter의 단일 COUNT(*)는 기존 JOIN row source를 집계합니다. Eager Count는 binding 검증 뒤 관련 객체 선택을 제외합니다.
- 기존 `db.Queryer.Query(context.Context, query.Plan)` port는 바꾸지 않습니다.

Full-model plan은 기존 field order와 descriptor scan 의미를 그대로 사용합니다. DTO projection은 exact selected
field order만 반환하고 model evaluation cache를 읽거나 채우지 않습니다. Aggregate도 별도 row shape이며 model
cache를 오염시키지 않습니다.

### Typed ORM 표면

Model-specific scalar fields는 sealed `ScalarField[M,V]` capability를 구현합니다. DTO projection은
`Projection[M,R]`, fixed-arity `Project1`..`Project4`와 top-level
`SelectInto[M,R](context.Context, QuerySet[M], Projection[M,R])`를 사용합니다.

Scalar aggregate는 typed `CountRows`, `Min`, `Max` expression, fixed-arity aggregate result builder와 top-level
`AggregateInto`를 사용합니다. MIN/MAX는 `OrderedField` capability를 사용하며 빈 입력은 explicit nullable result입니다. Generated
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

GDJ-0066의 관계 filter Count도 검증된 JOIN SELECT를 derived table로 감쌉니다. 모델 행을 전송하지 않으면서
JOIN multiplicity와 Distinct·슬라이스를 유지하려는 선택이며 일반 관계 집계로 지원 범위를 넓히지 않습니다.

PostgreSQL에서 distinct projection의 ordering expression이 SELECT result에 없으면 암묵적으로 result를 넓히지
않고 pre-I/O structured unsupported error를 반환합니다. SQL 문자열 동일성보다 결과, 오류, query count와 cache
의미를 계약합니다.

관계 조건 뒤 root DTO 선택에도 같은 규칙을 적용합니다. 선택하지 않은 source field로 조건을 걸 수 있으며
JOIN alias는 root 선택값만 한정합니다. 모델 또는 related-object scanner로 DTO를 읽지 않습니다.
고정 Django의 [독립 public 관찰](../../conformance/runners/django/related_projection_reference.py)은 direct forward/reverse의
선택값·중복·nullable NOT/OR·DISTINCT·slice를 대조하고, 기존 nested fixture가 유한한 여러 단계의 필터를 검증합니다.
실행 환경과 source 범위는 TEST_EVIDENCE가 소유합니다.

### Forward scalar 결과

`RelatedFieldResult`는 root metadata에 target field를 섞지 않고 exact terminal field와 immutable `RelationPath`를
보존합니다. `Project1..4`는 generated direct selector 및 `ChainForward`로 바인딩한 Integer·String·Boolean·Float·
Decimal·DateTime·Date·Time·Duration·UUID·JSON 값을 선택합니다. 기존 related field 타입은 target 선언과
optional ancestry를 runtime metadata로 소유하므로 선택값 타입은 모두 `*V`입니다. 필수 경로도 같은 API를 쓰며
대상 부재 또는 SQL NULL은 nil입니다. 선택을 위해 원본 field의 nullability나 Decimal precision을 바꾸지 않습니다.

Root nullable projection과 related projection은 같은 typed scanner factory를 사용합니다. Backend가 native
Decimal·Duration·UUID·JSON 등을 변환한 뒤 정확한 scanner가 읽고, 각 행의 pointer는 별도 값을 소유합니다.
Whole JSON의 non-nil JSON null과 SQL NULL은 구분합니다. 이 선택 capability는 관계 ordering/F·MIN/MAX를 허용하지 않습니다.

공통 JOIN 준비는 filter와 selected route의 동일 prefix를 합치고 nullable ancestry와 Boolean 조건을 보존합니다.
Root ID와 target ID처럼 이름·column·kind가 같아도 다른 선택값입니다. DISTINCT의 root ordering 검사는 실제 root
whole-field 선택만 인정하며 target ID로 대신 충족하지 않습니다. LIMIT 0이나 empty IN도 구조·capability 검사를 먼저 합니다.

[Django 6.1 public runner](../../conformance/runners/django/forward_scalar_projection_reference.py)는 11종의 필수·nullable
필드와 네 가지 required/optional 2-hop 경로에서 선택·Boolean filter·DISTINCT·slice를 관찰합니다.
[SQLite raw](../../orm/testdata/forward-scalar-django61-sqlite.json)와
[PostgreSQL raw](../../orm/testdata/forward-scalar-django61-postgres.json)는 정수·Decimal·binary64 bits·microsecond·UUID를
명시적으로 표현하고 JSON의 SQL NULL/대상 부재 목록을 별도로 기록합니다. SQLite의 고유 Decimal 저장 정책을
독립 Django NUMERIC 관찰과 혼동하지 않으며 큰 정밀도는 GoDj의 별도 실제 저장 회귀가 소유합니다.
구현과 실행 결과는 TEST_EVIDENCE에서 구분합니다.

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
- reverse selected value, 관계 ordering/F/aggregate 또는 existing select-related projection과 DTO projection 조합
- Web Core pagination abstraction, MySQL과 추가 backend
