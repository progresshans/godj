# ADR-0012: QuerySet 평가 상태의 ownership과 terminal API를 명시한다

- 상태: Proposed
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0007, GDJ-0008, QRY-011..QRY-021, Q-007, Q-011
- 대체하는 ADR: 없음

## 맥락

GDJ-0007은 성공한 empty/non-empty full evaluation cache, stale snapshot, chain과 fresh
copy의 독립성, cold/warm `Count`/`Exists`/index/`First`, cache-bypass iterator와 실패
재시도를 11개 exact Django 계약으로 고정했습니다. 현재 GoDj `QuerySet[M]`은 immutable
query plan을 가진 값 타입이고 `All(ctx)`를 호출할 때마다 backend를 실행합니다.

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

## 제안하는 방향

세 번째 선택지를 우선 prototype합니다. 최종 Accepted 전까지 다음 세부 사항은 확정된
공개 API가 아닙니다.

- `QuerySet[M]` 내부에 plan과 분리된 pointer evaluation state를 둡니다.
- direct value copy는 같은 logical evaluation state를 공유하는 후보로 검증합니다.
- `Filter`, `OrderBy`, `Limit`와 explicit `Fresh` 후보는 새 evaluation state를 만듭니다.
- full evaluation은 성공한 empty/non-empty 결과만 cache하고 오류나 owner cancellation은
  waiters를 깨운 뒤 다음 호출이 재시도할 수 있게 합니다.
- 같은 state의 동시 full evaluation은 한 owner가 실행하고 나머지는 자신의 context로
  기다리는 후보를 검증합니다. Waiter cancellation은 owner를 취소하지 않습니다.
- `Count`, `Exists`, iterator, index에 대응하는 `At`과 ordered `First`는 cold 상태에서 full cache를 채우지 않고,
  warm 상태에서는 contract에 맞게 cache를 읽거나 우회합니다.
- Canonical cached values를 외부에 직접 노출하지 않도록 descriptor-level `CloneModel`
  후보를 nullable pointer mutation/race로 검증합니다.
- Public fresh-copy 이름은 terminal `All(ctx)`와 충돌하지 않는 Go 이름을 선택합니다.

현재 `db.Queryer`/`query.Plan`에는 scalar aggregate projection이 없습니다. 첫 제품 단면은
cold `Count`가 result를 보관하지 않고 rows를 drain하는 정확성 우선 후보를 비교하되,
O(N) 전송 비용을 최종 최적화로 과장하지 않습니다. Aggregate/scalar AST 확장은 별도
수직 단면으로 남길 수 있습니다.

## Accepted 전 필수 증거

- 현재 QuerySet 값 receiver/constructor의 copy graph와 backend/rows ownership 표
- direct copy/chain/fresh 후보의 external compile test
- concurrent same-state evaluation, owner failure/cancellation과 waiter cancellation race test
- success-empty와 failure-retry state machine fake backend test
- cached slice와 nullable pointer mutation을 구분하는 alias test와 복제 비용 판단
- Count/Exists/iterator/First cold/warm integration과 row close/cancellation test
- QRY-018 index와 QRY-021 `First`를 서로 다른 제품 terminal로 관찰하는 gate
- QRY-011..021 actual adapter 0-diff 가능성을 보여주는 vertical prototype

## 의도적으로 결정하지 않는 것

- Python indexing 표면 또는 Django private `_result_cache`
- model object identity, relation/prefetch와 deferred field semantics
- async iterator, streaming chunk size, cache eviction/TTL
- request/transaction/hook 전체의 goroutine safety
- transaction callback 밖으로 나온 session-bound QuerySet의 수명
- PostgreSQL과 다른 backend의 query optimization

## 현재 상태

이 ADR은 GDJ-0008 시작점의 Proposed 문서입니다. 위 증거와 독립 감사를 거쳐 선택지,
public signature, cache clone 깊이와 concurrency state transition을 구체화한 뒤에만
Accepted로 올립니다. 현재 제품이 QRY-011..021을 지원한다고 주장하지 않습니다.
