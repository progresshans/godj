---
id: GDJ-0008
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "9050b4d7d2a1ed961da5e7bdefde8f4c8653eb33"
depends_on: ["GDJ-0007"]
contracts: ["QRY-011..QRY-021"]
allowed_paths: ["Makefile", "docs/**", "work/**", "orm/**", "query/**", "db/**", "codegen/**", "examples/**", "internal/**", "conformance/runners/godj/**", "conformance/cmd/godjcheck/**", "conformance/contracts/query-cache-manifest.json"]
integration_owner: "one primary agent"
---

# QuerySet Evaluation and Cache Product Slice

## 사용자에게 보이는 결과

같은 typed `QuerySet[M]`의 성공한 전체 평가를 재사용하고, chain과 fresh copy는 독립된
평가 상태를 가지며, `Count`, `Exists`, iterator와 `First`가 cache 상태에 맞는 I/O를
수행하도록 구현합니다. 실패한 평가는 cache하지 않고 같은 QuerySet에서 재시도할 수
있어야 합니다.

완료 시 GoDj runtime adapter가 GDJ-0007의 QRY-011..021을 실제 제품 package로 실행해
Django oracle과 0-diff여야 합니다. Python의 `_result_cache`, QuerySet class graph와
객체 identity는 복제하지 않습니다.

## 시작 전 결정 spike

제품 source를 바꾸기 전에 [ADR-0012](../docs/adr/0012-queryset-evaluation-cache-ownership.md)를
Proposed에서 Accepted로 올리고 다음 경계를 compile/runtime/race 증거로 결정합니다.

1. 값 타입 `QuerySet[M]`의 직접 Go 복사와 같은 logical query가 평가 상태를 공유하는지,
   `Filter`/`OrderBy`/`Limit`와 fresh copy는 어떤 시점에 새 상태를 받는지
2. 성공한 empty/non-empty 결과, 실패, caller cancellation과 재시도의 상태 전이
3. 같은 QuerySet의 동시 전체 평가를 한 번의 backend 실행으로 합칠지와 waiter context
   cancellation이 owner query에 영향을 주지 않는 방식
4. cached slice와 nullable pointer를 caller에게 돌려줄 때 alias/변이 의미
5. `All(ctx)`와 충돌하지 않는 fresh-copy 이름, `Count`/`Exists`/iterator/`At`/`First`의 Go API
6. cold terminal이 full result cache를 채우지 않으면서 기존 immutable Query AST와
   backend row lifecycle을 재사용하는 방식

## 제한 범위

- 현재 generated `Article`과 typed QuerySet/SQLite read subset
- 성공한 empty/non-empty full evaluation cache와 실패 후 재시도
- direct value copy, chained QuerySet와 explicit fresh copy의 평가 상태 ownership
- cold/warm `Count`, `Exists`, index에 대응하는 `At`, ordered `First`
- cache bypass iterator와 기존 cache 보존
- 같은 logical QuerySet의 concurrent full evaluation, waiter cancellation과 race safety
- QRY-011..021 실제 GoDj adapter와 differential

## 비목표

- Python indexing 표면, private `_result_cache` 또는 object identity 복제
- relation/prefetch, deferred field, projection/aggregate, async iterator
- streaming chunk size나 raw SQL 문자열 호환
- model instance를 여러 goroutine에서 mutation하는 안전성
- cache eviction, TTL, cross-process/shared cache
- PostgreSQL 또는 다른 DB backend

## 구현 순서

1. 기존 `QuerySet[M]`, `db.Queryer`/`Rows`, SQLite compiler와 adapter 경계를 표로 만들고
   compile/runtime/race spike 결과를 ADR-0012에 기록합니다.
2. Backend-independent evaluation state와 성공/실패/cancellation 상태 전이를 unit-test
   first로 구현합니다.
3. Chain/fresh copy가 새 평가 상태를 만들고 direct copy 정책이 race 없이 유지되도록
   QuerySet constructor/clone 경계를 연결합니다.
4. `Count`, `Exists`, iterator, `At`과 ordered `First` terminal을 cache cold/warm 의미에 맞게
   추가하고 context/error/row close를 검증합니다.
5. External consumer compile gate와 fake/SQLite integration, cancellation/concurrency/
   alias mutation gate를 추가합니다.
6. GoDj adapter를 네 번째 manifest에 연결하고 11개 모두 통과할 때만 status를
   `passing`으로 올립니다.
7. Full/vet/race/CGO=0/differential/mutation gate와 상태 문서를 갱신합니다.

## 완료 gate

- [ ] ADR-0012가 compile/runtime/race spike 증거와 함께 Accepted
- [ ] direct copy/chain/fresh state ownership unit·race test 통과
- [ ] empty/non-empty success cache, failure/cancellation retry test 통과
- [ ] concurrent same-query evaluation과 waiter cancellation test 통과
- [ ] Count/Exists/iterator/At/First cold·warm 및 resource cleanup test 통과
- [ ] external consumer positive/negative compile와 generated drift gate 통과
- [ ] QRY-011..021이 모두 `passing` 또는 승인된 `deviation`
- [ ] 기존 M1/M2/Save 34개 differential이 계속 0-diff
- [ ] full/vet/race/CGO=0/exact oracle/checksum/mutation gate 통과
- [ ] CURRENT/matrix/evidence/work/ADR가 같은 checkout을 가리킴

## 시작 시 첫 작업

1. `orm.QuerySet[M]`의 값 receiver와 chain constructor가 plan/backend/descriptor를 어떻게
   복사하는지, `All(ctx)`가 rows를 소유·닫는 경계를 실제 코드로 추적합니다.
2. `/tmp` 또는 focused test에서 pointer evaluation state를 공유하는 direct copy와
   chain/fresh reset 후보를 race/cancellation과 함께 비교합니다.
3. Cached result 반환 시 slice와 nullable pointer alias가 다음 호출의 결과를 오염시키는
   mutation을 먼저 재현합니다.
4. QRY-011/012/019의 success-empty/failure-retry state machine을 fake backend로 가장
   먼저 고정하고 ADR-0012를 Accepted로 올린 뒤 public terminal을 구현합니다.

## 알려진 위험

- 값 타입에 mutex를 직접 넣으면 copy-after-use 문제가 생깁니다. 공유 state pointer와
  immutable plan의 ownership을 분리해야 합니다.
- Caller context가 취소된 waiter 때문에 다른 goroutine의 owner query까지 취소되면
  독립 요청 간 의미가 섞입니다. 반대로 owner 실패를 영구 cache하면 retry 계약을
  깨뜨립니다.
- Cached `[]M`을 그대로 반환하면 caller slice/model pointer mutation이 cache를 바꿀 수
  있습니다. 복제 깊이와 성능을 ADR에서 명시해야 합니다.
- `Count`/`Exists`를 full materialization으로 구현하면 query count만 맞더라도 과도한
  메모리 사용을 숨길 수 있습니다. 첫 slice의 성능/AST 경계를 의도적으로 결정합니다.
- Django `all()`은 fresh clone이지만 GoDj의 `All(ctx)`는 이미 terminal입니다. 표면 이름을
  억지로 복제하지 않습니다.

## 인수인계

- Baseline은 GDJ-0007 machine artifact commit
  `9050b4d7d2a1ed961da5e7bdefde8f4c8653eb33`입니다.
- 네 번째 manifest는 QRY-011..021 `oracle_locked`, Django oracle은 `observed`, static
  fixture는 11개 `not_implemented`입니다. 아직 GoDj 제품 pass가 아닙니다.
- Oracle은 56,426 bytes, SHA-256
  `d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`입니다.
- 첫 변경은 ADR-0012와 focused test여야 하며 manifest status는 actual adapter가 0-diff가
  되기 전까지 올리지 않습니다.
