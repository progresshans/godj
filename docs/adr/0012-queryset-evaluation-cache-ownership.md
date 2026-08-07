# ADR-0012: QuerySet 평가 상태의 ownership과 terminal API를 명시한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0007, GDJ-0008, QRY-011..QRY-021, Q-007, Q-011
- 대체하는 ADR: 없음

## 맥락

GDJ-0007은 성공한 empty/non-empty full evaluation cache, stale snapshot, chain과 fresh
copy의 독립성, cold/warm `Count`/`Exists`/index/`First`, cache-bypass iterator와 실패
재시도를 11개 exact Django 계약으로 고정했습니다. 이 ADR을 결정할 당시 GoDj
`QuerySet[M]`은 immutable query plan을 가진 값 타입이고 `All(ctx)`를 호출할 때마다
backend를 실행했습니다.

Go의 값 복사와 goroutine/context 모델은 Django object reference와 다릅니다. 값 타입에
mutex/cache를 직접 넣으면 copy-after-use 위험이 있고, pointer state를 추가하면 direct
copy가 무엇을 공유하는지 공개 동작이 됩니다. `All(ctx)`도 이미 materialization terminal로
사용하므로 Django의 fresh clone 메서드 `all()` 이름을 그대로 쓸 수 없습니다.

## 결정해야 하는 기준

- immutable query plan과 mutable evaluation state를 분리해야 함
- direct value copy, chain과 explicit fresh copy의 state ownership이 예측 가능해야 함
- 성공한 empty/non-empty 결과만 cache하고 실패/cancellation은 재시도 가능해야 함
- 같은 logical QuerySet의 동시 full evaluation이 race와 중복 I/O를 통제해야 함
- waiter context cancellation이 다른 caller의 owner query 수명을 잘못 취소하지 않아야 함
- cached slice/model pointer를 caller가 바꿔 다음 결과를 오염시키는지 명시해야 함
- cold terminal은 full cache를 채우지 않고 warm terminal은 cache를 재사용해야 함
- row close, backend error와 context propagation을 기존 경계에서 보존해야 함
- QRY-011..021을 실제 제품 API로 관찰할 수 있어야 함

## 검토할 선택지

### QuerySet 값을 복사할 때 cache도 값으로 복사

Plan과 cache가 완전히 독립해 보이지만 mutex를 포함한 사용 후 복사와 slice/pointer alias가
생깁니다. 한 logical query의 직접 복사마다 중복 I/O가 발생하고 동시 평가 조정도 어렵습니다.

### 모든 QuerySet 복사와 chain이 하나의 pointer state를 공유

직접 복사는 단순하지만 `Filter`/`OrderBy`/`Limit` 뒤 plan이 다른 chain까지 source cache를
공유할 위험이 있습니다. QRY-014와 fresh-copy 계약을 만족하려면 plan 변환마다 새 state를
반드시 만들어야 합니다.

### Direct copy만 pointer state를 공유하고 plan 변환/fresh copy는 새 state를 사용

Immutable plan 값과 별도 evaluation state pointer를 둡니다. Direct Go assignment는 같은
logical query state를 공유하고, plan을 바꾸는 모든 constructor와 explicit fresh API는
unpopulated state를 받습니다. 동시 evaluation과 waiter cancellation을 state machine으로
관리해야 하며 cached result 반환의 clone 깊이는 별도 결정이 필요합니다.

## 결정

`QuerySet[M]`은 immutable plan과 별도 pointer evaluation state를 가집니다.

```go
type QuerySet[M any] struct {
    backend    db.Queryer
    descriptor ModelDescriptor[M]
    plan       query.Plan
    evaluation *evaluationState[M]
}
```

- `Manager.Using`은 항상 새 evaluation state를 만듭니다.
- 직접 Go value copy는 같은 logical QuerySet으로 정의하고 state pointer를 공유합니다.
- `Filter`, `OrderBy`, 성공한 `Limit`와 `Fresh`는 plan이 실질적으로 같아도 새 state를
  만듭니다. Source cache를 derived handle에 복사하지 않습니다.
- QuerySet 값 자체에는 mutex를 넣지 않습니다.

Full evaluation state는 `unready → in-flight → ready`만 가집니다. 성공한 empty/non-empty
결과를 rows iteration과 `Close`까지 완료한 뒤 publish합니다. Backend/query/scan/iteration/
close/context 오류는 cache하지 않고 `unready`로 돌아가 다음 호출이 재시도할 수 있습니다.

같은 state의 동시 `All`은 한 owner만 backend I/O를 실행합니다. 첫 caller의 context가 그
flight의 I/O 수명을 소유합니다. Waiter는 자신의 context와 flight completion을 기다리며,
waiter cancellation은 owner를 취소하지 않습니다. Owner가 cancellation/deadline으로
실패했는데 waiter context가 유효하면 waiter가 새 owner가 되어 재시도할 수 있습니다.
일반 owner 오류는 현재 waiters에게 같은 오류를 전달하고 다음 독립 호출부터 재시도합니다.
Owner 완료와 waiter cancellation이 동시에 ready인 경합의 우선순위는 보장하지 않되,
다른 caller의 context를 취소하지 않는 불변 조건은 지킵니다.

Canonical cache는 caller에게 직접 노출하지 않습니다. `ModelDescriptor[M]`에
`CloneModel(M) M`을 추가하고 nullable pointer까지 model별 generated deep clone을
제공합니다. Scan 결과를 canonical cache에 넣기 전과 `All`/warm `At`/warm `First`로
반환할 때 clone합니다. 기존 `WriteDescriptor.CloneWriteModel`은 호환되는 write 경계를
유지하되 generated 구현이 같은 deep-clone logic에 위임합니다. Django object identity를
복제하는 대신 caller alias와 concurrent mutation으로부터 canonical cache를 격리합니다.

공개 QuerySet API는 zero-I/O state-copy API `Fresh`와 여섯 terminal로 고정합니다.

```go
func (qs QuerySet[M]) Fresh() QuerySet[M]
func (qs QuerySet[M]) All(context.Context) ([]M, error)
func (qs QuerySet[M]) Count(context.Context) (int64, error)
func (qs QuerySet[M]) Exists(context.Context) (bool, error)
func (qs QuerySet[M]) At(context.Context, int) (M, bool, error)
func (qs QuerySet[M]) First(context.Context) (M, bool, error)
func (qs QuerySet[M]) Iterate(context.Context, func(M) error) error
```

- `Fresh`는 plan/backend/descriptor를 보존하고 새 state만 만드는 zero-I/O API입니다.
- Cold `Count`/`Exists`/`At`/`First`는 full result cache를 채우지 않습니다. Warm
  `Count`/`Exists`/`At`/`First`는 cache를 사용합니다.
- `At`은 QRY-018의 index terminal, `First`는 QRY-021의 public first terminal을 별도로
  관찰합니다. 두 API를 하나로 가장하지 않습니다.
- `At`과 `First`의 안정된 첫 행 의미는 명시적 ordering이 있는 plan에 한정하고, 음수
  index와 unordered call은 I/O 전에 structured error로 거부합니다.
- `Iterate`는 callback API로 rows 수명을 내부에서 소유하며 cache를 항상 우회하고 기존
  cache를 읽거나 교체하지 않습니다.
- 모든 terminal은 nil/already-canceled context를 cache 확인 전에 거부합니다.

현재 `db.Queryer`/`query.Plan`에는 scalar aggregate/offset projection이 없습니다. 첫 제품
단면의 cold `Count`는 rows를 보관하지 않고 drain하며, `Exists`/`First`는 effective limit
1, `At`은 effective limit `index+1`로 실행합니다. 기존 plan limit가 더 작으면 이를
늘리지 않습니다. 이 구현은 SQL 문자열을 계약하지 않고 정확한 외부 의미를 우선하지만,
cold `Count`의 O(N) row 전송과 `At`의 offset 없는 순회는 알려진 성능 제한입니다.
Aggregate/scalar/offset AST와 backend 최적화는 별도 수직 단면으로 남깁니다.

Cold terminal과 동시에 실행 중인 `All`은 같은 flight로 합치지 않습니다. Singleflight는
같은 state의 full `All`에만 적용합니다.

## 결과

- Immutable plan의 copy-on-write 경계를 유지하면서 cache mutex 복사를 피합니다.
- Direct copy는 같은 logical snapshot을 공유하고 chain/fresh는 독립 snapshot을 얻습니다.
- 성공한 빈 결과도 cache되며 오류와 cancellation은 영구 고정되지 않습니다.
- Cached model deep clone은 warm evaluation마다 O(N) 복제 비용이 있지만 alias/race 격리를
  명시적으로 보장합니다.
- `Count`/`Exists`/`At`/`First`/`Iterate`는 기존 `db.Rows` 경계를 재사용하므로 이번 단면에
  raw SQL이나 backend 전용 public interface를 추가하지 않습니다.

## 의도적으로 결정하지 않는 것

- Python indexing 표면 또는 Django private `_result_cache`
- model object identity, relation/prefetch와 deferred field semantics
- async iterator, streaming chunk size, cache eviction/TTL
- request/transaction/hook 전체의 goroutine safety
- transaction callback 밖으로 나온 session-bound QuerySet의 수명
- PostgreSQL과 다른 backend의 query optimization

## 검증

저장소 밖 `/tmp/godj-adr0012-spike`의 별도 Go 1.26 module에서 direct-copy 32-goroutine
singleflight, derived/fresh 독립 state, empty success cache, 일반 failure 재시도, owner
cancellation 뒤 live waiter 재시도, waiter-only cancellation 격리, nullable `*string`과
동시 caller mutation deep clone, 위 terminal method-expression compile을 검증했습니다.

```text
go test -count=1 ./...                       PASS, 10 tests
go vet ./...                                 PASS
go test -race -count=1 ./...                 PASS
go test -count=50 -shuffle=on ./...          PASS
go test -race -count=10 -shuffle=on ./...    PASS
```

Spike의 `queryset.go` SHA-256은
`9324a19af02faeea12193d40e153edbb5943d03d0d9e361fdb0354d8406e7675`, test는
`6827b58e947eedb5a93ca97c2d3ee55efd02d8643f8ee95af2e407993f3ac2b9`입니다. 이 경로는
결정 증거일 뿐 제품 source가 아니었습니다. 이 spike 시점에는 GDJ-0008이 같은 불변
조건을 checked-in unit/compile/race/SQLite/differential test로 다시 검증해야 했으며,
ADR Accepted만으로 QRY-011..021 제품 지원이나 `passing`을 뜻하지 않았습니다.

### Checked-in 제품 검증

GDJ-0008은 위 결정을 저장소 제품 source에 구현했습니다. `orm.QuerySet[M]`의 direct-copy/
chain/fresh ownership, 성공/실패/cancellation 상태 전이, 같은 state `All` singleflight,
owner 취소 뒤 live waiter 재시도와 waiter-only cancellation 격리, cold/warm terminal,
callback iterator와 rows cleanup을 unit/race/SQLite test로 검증했습니다. External consumer
compile-positive/negative gate는 terminal signature와 cross-model callback/result type
오용을 확인합니다. `godj-codegen-m2-v3`가 nullable pointer를 deep clone하는
`CloneModel`을 만들고 기존 `CloneWriteModel`이 이에 위임하는 것도 golden, deterministic
generation과 drift gate로 고정했습니다.

실제 GoDj adapter는 QRY-011..021 모두 Django oracle과 의미적으로 0-diff입니다. 두 독립
Go actual은 각각 56,283 bytes, SHA-256
`c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`로 서로
byte-identical합니다. Django oracle은 56,426 bytes, SHA-256
`d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`이므로 서로 다른 두
runtime artifact 자체가 byte-identical하다는 뜻은 아닙니다. Protocol comparator가 계약된
result/error/DB state/metrics를 0-diff로 판정했습니다. Static query fixture의 ordered 11
mismatch와 arbitrary unknown scenario fail-closed도 계속 유지합니다. 전체 명령과 checkout
증거는
[EVID-20260808-007](../status/TEST_EVIDENCE.md#evid-20260808-007--gdj-0008-queryset-evaluation-and-cache-product-slice)에
기록합니다.
