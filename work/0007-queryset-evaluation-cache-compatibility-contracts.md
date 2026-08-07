---
id: GDJ-0007
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "af0bc7992cc156f118f75b04f658162ae5dbbb07"
depends_on: ["GDJ-0006"]
contracts: ["QRY-011..QRY-020"]
allowed_paths: ["Makefile", "NOTICE.md", ".github/workflows/ci.yml", "conformance/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# QuerySet Evaluation and Cache Compatibility Contracts

## 사용자에게 보이는 결과

동일한 QuerySet을 반복 평가할 때 결과 snapshot과 DB I/O가 언제 재사용되는지, chain,
`Count`, `Exists`, iterator, 부분 평가, 실패 재시도와 fresh clone이 cache와 어떻게
상호작용하는지를 exact Django 6.1 동작으로 고정합니다.

이 작업은 GoDj cache 제품을 구현하지 않습니다. 현재 값 타입 `QuerySet[M]`에 mutable
state를 성급히 넣기 전에, 후속 GDJ-0008이 만족해야 할 관찰 가능한 계약과 false-green
gate를 만드는 contract-only 작업입니다.

## 고정할 후보 계약

| ID | 잠글 동작 |
|---|---|
| QRY-011 | 같은 QuerySet 전체 평가를 두 번 하면 두 번째는 cache를 재사용 |
| QRY-012 | 성공한 빈 결과도 cache되어 두 번째 query가 0개 |
| QRY-013 | 평가된 QuerySet은 외부 INSERT 뒤에도 snapshot을 유지하고 fresh QuerySet은 새 행을 봄 |
| QRY-014 | 평가된 source와 그 source에서 만든 chained filter가 cache를 공유하지 않음 |
| QRY-015 | 평가 전 `Count`는 별도 query이며 full result cache를 채우지 않고, 평가 후에는 cache를 재사용 |
| QRY-016 | 평가 전 `Exists`는 별도 query이며 full result cache를 채우지 않고, 평가 후에는 cache를 재사용 |
| QRY-017 | iterator는 populated cache를 우회하고 그 cache를 교체하지 않음 |
| QRY-018 | 부분/첫 행 평가는 full result cache를 채우지 않으며 full 평가 뒤에는 cache를 재사용 |
| QRY-019 | 실패한 full evaluation은 cache되지 않고 같은 QuerySet이 복구 뒤 재시도 가능 |
| QRY-020 | evaluated QuerySet의 fresh clone은 다시 query하고 원본 cache와 독립 |

Probe 결과에 따라 한 observation에 여러 독립 오류나 모호한 payload를 넣지 않도록
8~12개 bound 안에서 ID를 조정할 수 있습니다. ID/order/phase를 lock한 뒤에는 변경을
별도 review 없이 섞지 않습니다.

## 목표 흐름

```text
Django 6.1 exact evaluation scenarios
→ step별 result / error / query_count normalization
→ fourth ordered manifest + deterministic oracle
→ explicit GoDj not_implemented suite
→ GDJ-0008 cache ownership/API ADR 입력
```

## 제한 범위

- 기존 Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 exact profile
- 현재 Article field/query subset으로 표현 가능한 terminal evaluation
- 결과와 단계별 query count, 중간 DB 변경에 따른 snapshot/fresh 차이
- protocol v2의 독립 fourth contract set
- pinned Django commit의 문서/test provenance와 라이선스 분류

## 비목표

- GoDj QuerySet cache, Count/Exists/Iterator/First/Fresh 제품 API 구현
- public API 이름이나 cache state ownership 확정
- raw SQL 문자열, Django private `_result_cache`, object identity/alias 계약
- prefetch, streaming chunk size, async API, relation loader
- concurrent same-query singleflight, waiter cancellation 또는 전체 goroutine safety
- hook/signal, migration graph/lock, CLI, PostgreSQL

## 구현 순서

1. Pinned Django commit `fe0a859f537d4238cf49fca39073513206f83122`의 public docs와
   관련 tests/source 경계를 확인합니다. 현재 로컬 checkout HEAD를 profile로 오인하지
   않습니다.
2. Disposable SQLite에서 각 후보를 독립 실행하고 setup DDL/DML과 terminal query
   capture window를 분리합니다.
3. 결과만 같아 우연히 통과하지 않도록 step별 query count와 필요한 중간 INSERT/schema
   repair를 payload에 넣고, 두 process에서 canonical bytes를 비교합니다.
4. fourth manifest/registry/Django runner/oracle/static fixture를 추가합니다.
5. 네 set의 전역 contract ID/scenario uniqueness, 12개 ordered cross-binding과
   order/phase/profile/payload mutation을 검증합니다.
6. 기존 34개 GoDj differential, full/vet/race/CGO=0와 portable/exact Python gate를
   유지하고 상태 문서를 갱신합니다.

## 완료 gate

- [ ] exact disposable probe가 최종 후보별 안정 payload를 두 번 동일하게 생성
- [ ] QRY-011..QRY-020의 ID/order/phase/comparison/provenance가 manifest에 고정
- [ ] Django oracle이 두 independent process에서 byte-identical하고 checksum이 기록됨
- [ ] explicit GoDj `not_implemented` suite가 정확히 계약 수만큼 ordered mismatch
- [ ] 네 set의 전역 uniqueness와 모든 12개 ordered cross-binding이 거부됨
- [ ] snapshot/query-count/error-retry payload mutation이 false green 없이 실패
- [ ] 기존 M1/M2/Save 34개 GoDj differential이 계속 0-diff
- [ ] portable/exact Python, protocol/full/vet/race/CGO=0 gate 통과
- [ ] CURRENT/matrix/evidence/work가 같은 checkout과 상태를 가리킴

## 시작 시 첫 작업

1. Django `QuerySet._fetch_all`, `_clone`, `count`, `exists`, `iterator`, indexing과
   public caching docs에서 외부 동작 후보를 source line/test symbol까지 추적합니다.
2. QRY-011/012/013의 repeated full evaluation, empty cache와 stale snapshot을 먼저
   disposable probe로 재현합니다.
3. QRY-015/016/017의 query window가 setup write를 세지 않는지 검증합니다.
4. QRY-019는 private cache를 읽지 않고 fail → schema repair → same QuerySet retry로
   실패 비캐시를 관찰합니다.

## 알려진 위험

- 현재 GoDj `All(ctx)`는 terminal materialization이지만 Django `all()`은 fresh clone을
  뜻합니다. Python 표면 이름을 그대로 복제하지 않고 후속 ADR에서 Go API를 결정합니다.
- 현재 `QuerySet[M]`은 값 타입/값 receiver입니다. mutex를 값에 직접 넣거나 value copy가
  cache를 암묵적으로 공유하게 만들면 copy-after-use와 race 위험이 있으므로 이 작업에서
  제품 shape를 확정하지 않습니다.
- 결과만 비교하면 현재 uncached GoDj도 우연히 맞을 수 있습니다. 각 scenario의 step별
  query count와 DB mutation을 계약 차원으로 유지합니다.
- Q-011의 goroutine safety는 Django differential로 해결되지 않습니다. Same-query
  concurrency, cache ownership과 waiter context cancellation은 GDJ-0008의 Go-native
  ADR/test 입력으로 따로 기록합니다.
