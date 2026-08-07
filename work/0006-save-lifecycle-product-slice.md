---
id: GDJ-0006
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "138581da38bfbb6ba89ea5ca82752dfd3d76df02"
depends_on: ["GDJ-0005"]
contracts: ["MOD-008..MOD-019"]
allowed_paths: ["go.mod", "go.sum", "Makefile", "schema/**", "codegen/**", "orm/**", "query/**", "db/**", "internal/**", "conformance/**", "examples/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Save Lifecycle Product Slice

## 사용자에게 보이는 결과

한 generated `Article` instance를 새 객체 또는 loaded 객체로 저장하고, typed
`update_fields`, force insert/update, explicit primary key와 transaction rollback을
Go 방식의 `context.Context`/`error` API로 실행할 수 있게 합니다.

완료 시 GoDj runtime adapter가 GDJ-0005의 MOD-008..019를 실제 제품 package로 실행해
Django oracle과 0-diff여야 합니다. Python의 private `_state`나 내부 class graph는
복제하지 않습니다.

## 시작 전 결정 spike

다음 API 경계를 별도 compile/runtime fixture에서 비교한 뒤 ADR-0011로 결정합니다.

1. `Manager[M].Save(ctx, backend, *M, ...SaveOption[M])`와 generated instance method
   wrapper 중 context/backend 의존성과 사용성이 더 명확한 형태
2. 문자열 field name 대신 generated typed field가 들어가는 `UpdateFields[M]` mask와
   explicit empty mask 표현
3. hidden auto-key presence를 깨지 않으면서 사용자가 explicit PK instance를 만드는
   generated constructor/builder
4. default save의 UPDATE-all과 0-row INSERT fallback을 기존 immutable mutation plan에
   확장할지 별도 save plan으로 표현할지
5. force flag 조합과 `NotUpdated`를 기존 `query.Error` category/code에 매핑하는 방식

Spike는 nullable pointer alias, cross-model field mask, PK update field와 nil
context/backend가 compile 또는 zero-I/O validation에서 안전한지도 검증합니다.

## 제한 범위

- Fully loaded `Article` 한 model과 현재 Auto/Char/Boolean/nullable Char subset
- 새 instance save와 auto PK assignment
- 기본 loaded save의 writable field 전체 UPDATE
- typed named/empty `update_fields`
- force insert/update와 mutually exclusive validation
- explicit auto PK의 UPDATE 및 UPDATE→INSERT fallback
- 기존 SQLite `Atomic` 안에서 rollback 후 object/DB state 차이
- MOD-008..019 GoDj adapter

## 비목표

- Django private `_state` 복제 또는 reflection dirty map
- deferred field loading과 automatic deferred-field mask
- signal/hook, inheritance, relation/cascade
- custom/composite PK, bulk operation 또는 upsert 일반화
- PostgreSQL과 migration file/graph/lock

## 구현 순서

1. Compile/runtime spike와 ADR-0011로 public shape, typed mask, explicit key 경계를 잠급니다.
2. Backend-independent immutable save plan과 structured error taxonomy를 test-first로
   구현합니다.
3. Codegen이 model별 explicit-key constructor와 Save binding을 결정적으로 생성합니다.
4. Generic Manager가 default/partial/force/fallback semantics를 zero-I/O validation과 함께
   구현합니다.
5. SQLite write 경로가 exact affected-row, explicit-key insert와 cancellation/cleanup을
   보존하도록 확장합니다.
6. GoDj adapter를 세 번째 manifest에 연결하고 12개 모두 통과할 때만 status를
   `passing`으로 올립니다.
7. Compile/golden/full/race/CGO=0/differential gate와 상태 문서를 갱신합니다.

## 완료 gate

- [x] ADR-0011이 compile/runtime spike 증거와 함께 Accepted
- [x] cross-model/PK/nil/empty-mask positive·negative compile 및 zero-I/O test 통과
- [x] default/partial/force/explicit-key save plan unit/property test 통과
- [x] SQLite integration의 affected-row/fallback/error/rollback/resource test 통과
- [x] generated output determinism, golden, last-good와 external consumer compile 통과
- [x] MOD-008..019가 모두 `passing` 또는 승인된 `deviation`
- [x] 기존 M1/M2 22개 differential이 계속 0-diff
- [x] full/vet/race/CGO=0/exact oracle/checksum gate 통과
- [x] CURRENT/matrix/evidence/work/ADR가 같은 checkout을 가리킴

## 시작 시 첫 작업

1. 기존 `WriteDescriptor`, `Mutation`, `Manager.Update`와 SQLite `Mutator`가 재사용 가능한
   경계와 fallback에 부족한 반환값을 표로 만듭니다.
2. `/tmp` 독립 Go module에서 typed `SaveOption[M]`/field mask 두 후보와 explicit PK
   constructor를 compile합니다.
3. MOD-011/012/014/016의 zero-I/O와 MOD-017/018의 UPDATE/INSERT sequence를 fake backend로
   먼저 재현합니다.
4. 선택 결과를 ADR-0011에 기록한 뒤에만 제품 source를 확장합니다.

## 알려진 위험

- 현재 model은 auto-key 값과 존재 상태를 숨은 flag로 분리합니다. Public `ID` 값만
  바꿔 explicit key라고 추측하면 zero value와 presence가 다시 섞입니다.
- Default save는 dirty tracking이 아니라 fully loaded field 전체를 저장합니다. 숨은
  dirty map을 먼저 도입하면 MOD-009와 어긋납니다.
- UPDATE 0행 fallback과 force/update_fields 0행 오류는 같은 backend 결과에서 다른
  결정을 하므로 mode가 plan/executor까지 손실 없이 전달돼야 합니다.
- Save가 pointer를 mutate하므로 같은 instance를 여러 goroutine에서 공유하는 안전성을
  이번 단면이 보장한다고 표현하지 않습니다.

## 구현 결과

- [ADR-0011](../docs/adr/0011-m2-save-lifecycle-orchestration.md)에 따라 authoritative
  API를 `Manager[M].Save(ctx, db.Mutator, *M, ...SaveOption[M])`로 확정했습니다.
- `SaveOption[M]`은 private immutable state를 가진 concrete generic value이며,
  `WritableField[M]`의 sealed model marker가 cross-model mask를 compile-time에
  차단합니다. Auto primary key field는 typed mask를 구현하지 않습니다.
- 기본 Save는 fully loaded writable field 전체를 UPDATE하고, named/empty mask, force
  validation, explicit-key UPDATE와 0-row INSERT fallback을 별도 policy로 보존합니다.
- Generated code는 explicit zero를 포함한 key presence를 보존하는
  `New<Model>With<Key>`, typed/dynamic mask와 force helper를 생성합니다. Model field와
  충돌할 수 있는 instance `Save` method는 생성하지 않습니다.
- SQLite는 exact affected-row를 유지하고 modernc의 structured extended result code로
  primary-key 충돌을 `integrity_error/unique_primary_key`에 매핑하며 driver cause를
  보존합니다.
- GoDj Save adapter는 MOD-008..019를 실제 ORM/codegen/SQLite 경로로 실행합니다.
  세 manifest의 11 + 11 + 12, 총 34개 계약이 모두 Django oracle과 0-diff입니다.

## 수정 파일

- Generic core/error: `orm/save.go`, `orm/field.go`, `query/error.go`, `db/db.go`
- SQLite: `db/sqlite/write.go`와 integration/internal regression tests
- Codegen/consumer gates: `codegen/**`, `examples/article/models/zz_godj_generated.go`,
  `internal/compiletest/**`
- Conformance: `conformance/runners/godj/**`, `conformance/cmd/godjcheck/**`,
  `conformance/contracts/save-lifecycle-manifest.json`, `Makefile`
- 결정: `docs/adr/0011-m2-save-lifecycle-orchestration.md`

## 검증과 독립 감사

- 제품·contract commit:
  `af0bc7992cc156f118f75b04f658162ae5dbbb07`
- 최종 증거:
  [EVID-20260808-005](../docs/status/TEST_EVIDENCE.md#evid-20260808-005--gdj-0006-save-lifecycle-product-slice)
- `make check`, uncached full Go/vet/race/CGO=0, generation drift, Python
  portable/exact 36-test와 세 differential set이 통과했습니다.
- 두 독립 GoDj Save actual은 각각 11,743 bytes이고 SHA-256
  `bc129818165d1ea147afa39a083964bcad710f744b341d4f083fdac2581dd225`로
  byte-identical했습니다.
- 독립 제품 감사에서 P0–P3 결함이 없었습니다. Mutation audit가 contract별 metrics
  하드코딩과 SQLite error-string 분류 false green을 재현했고, 임의 contract recorder
  sequence와 opaque wrapped extended-code 회귀 테스트로 두 구멍을 닫았습니다.

## 남은 제한과 인수인계

- 한 generated `Article`, Auto/Char/Boolean/nullable Char와 SQLite one-row Save만
  검증됐습니다. Deferred field, hook/signal, inheritance, relation/cascade, custom PK,
  bulk/upsert와 다른 DB backend는 지원을 주장하지 않습니다.
- Save는 caller-owned transaction session을 사용하며 같은 mutable instance의 goroutine
  안전성이나 optimistic locking을 보장하지 않습니다.
- Static `godj-save-lifecycle-not-implemented.json`은 현재 실행 결과가 아니라 구현 전
  상태가 false pass하지 않는지 확인하는 12-mismatch fixture로 유지합니다.
- 다음 작업은 [GDJ-0007](0007-queryset-evaluation-cache-compatibility-contracts.md)에서
  오래 열린 Q-007의 evaluation/cache/terminal 의미를 제품 API 변경 전에 exact
  Django 계약으로 고정합니다.
