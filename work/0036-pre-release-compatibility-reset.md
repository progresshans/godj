---
id: GDJ-0036
status: active
updated: 2026-08-20
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "d824b6916348286abb50dfa16a492332e97cd714"
depends_on: ["GDJ-0035"]
contracts: ["Q-010", "Q-012", "Q-013", "Q-017"]
allowed_paths:
  - "AGENTS.md"
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "cmd/godj/**"
  - "schema/**"
  - "codegen/**"
  - "orm/**"
  - "migrations/**"
  - "db/sqlite/**"
  - "conformance/**"
  - "internal/compiletest/**"
  - "internal/projectcheck/**"
  - "examples/article/**"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/LICENSING.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/adr/0019-versioned-migration-definition-source.md"
  - "docs/adr/0020-migration-definition-loader-product-shape.md"
  - "docs/adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md"
  - "docs/adr/0031-relation-aware-project-facade-and-generated-upgrade-boundary.md"
  - "docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md"
  - "docs/adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md"
  - "docs/adr/0035-pre-release-current-only-format-and-generated-publication.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md"
  - "work/0036-pre-release-compatibility-reset.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Pre-release Compatibility Reset

## 사용자에게 보이는 결과

첫 외부 alpha 이전의 개발 중 형식을 영구 legacy로 취급하지 않습니다. 사용자는 하나의 현재 Schema IR과
Migration Definition을 load하고, 명시적 loaded definition set을 하나의 revision-fenced backend entry로
apply/unapply/restart합니다. Scalar와 ForeignKey 모델은 같은 current format과 generated publication에 속합니다.

## 목표

- Schema IR, definition document/digest, historical state를 각각 하나의 current format으로 통합합니다.
- context.Context hidden handoff 대신 명시적 migrations.LoadedDefinitionSet을 lifecycle 입력으로 사용합니다.
- scalar/relation fenced begin을 하나의 MigrationIntent와 capability surface로 통합합니다.
- 개발 중 generated-byte 보존 정책을 제거하고 app/project candidate set을 현재 ABI로 한 번 재기준화합니다.
- GDJ-0035 D4d~D4f의 SQLite DDL, rollback, restart, row/sequence 보존 의미를 새 경로에서 유지합니다.
- 작은 단계마다 full hosted/evidence cycle을 반복하지 않고 compile checkpoint와 최종 integration gate로 검증합니다.

## 비목표

- Historical state, recorder, revision fence, commit durability, resource bound 또는 canonical digest를 제거하지 않습니다.
- Django observable behavior contract를 GoDj 과거 형식 호환과 혼동하지 않습니다.
- PostgreSQL, Web Core, 새 Field/Relation 종류, general migration writer를 이번 reset에 추가하지 않습니다.
- 기존 Git history와 EVID-097~099/D4g Phase 0 증거를 다시 쓰지 않습니다.
- 중간 호환 shim이나 구 reader를 남겨 두지 않습니다.

## 선행 조건과 기준 상태

- 기준점은 clean d824b6916348286abb50dfa16a492332e97cd714입니다.
- 저장소 안에는 release tag나 지원해야 할 외부 migration/generated ABI 증거가 없습니다. 외부 alpha를 만들기 전까지
  untagged 개발 snapshot은 지원 대상이 아닙니다.
- GDJ-0035 D4d nullable Add, D4e empty-source required Add, D4f bounded Remove/remake 구현은 역사적 회귀 기준입니다.
- D4g Phase 0 observer 결과는 characterization으로 보존하지만 MIG-075..086 status/registry publication은 중단합니다.
- 장기 결정은 [ADR-0035](../docs/adr/0035-pre-release-current-only-format-and-generated-publication.md)가 소유합니다.

## 설계

1. Schema IR은 relation arm의 유무와 관계없이 CurrentFormatVersion = 1 하나를 사용합니다.
2. Definition document는 persisted format_version = 1 하나만 가집니다. Loader ABI, operation codec, Schema IR
   숫자는 wire tuple이 아니라 current implementation concern입니다.
3. definition.Load는 root-owned opaque migrations.LoadedDefinitionSet을 반환합니다. Executor는 raw
   []Migration과 hidden context metadata가 아니라 이 값 하나를 받습니다.
4. ProjectState는 current IR만 담습니다. 관계 존재 여부는 format 숫자가 아니라 실제 field 의미로 계산합니다.
5. Backend는 mandatory capabilities와 BeginMigration(HistoryTransition, MigrationIntent) 하나를 구현합니다.
   SQLite 내부의 FK-on/catalog/remake 준비 분기는 backend ABI가 아니라 sealed intent 실행 세부사항으로 남습니다.
6. Codegen의 물리 파일 분할은 내부 구현입니다. 이번 reset은 과거 byte-preservation 분기를 제거하고 현재 generator와
   checked-in app/project output set을 하나의 ABI로 재기준화합니다. Project-level manifest, 전체 candidate compile과
   coordinated publication/repair는 Q-017 후속 작업으로 남기며 이번 checkpoint의 구현으로 계산하지 않습니다.

## 구현 checkpoint

### A. Current IR과 Definition Set

- [x] Schema IR v2/v3 분기와 relation-only generator dispatch 제거
- [x] single format definition parser/canonical digest 구현
- [x] explicit LoadedDefinitionSet 구현과 context handoff 제거
- [x] 구 profile/digest fixture와 MIG-075/077 legacy topology 계약 retire/reframe

### B. State와 Backend Lifecycle

- [x] ProjectState promotion/demotion 제거와 current IR reconstruction 통합
- [x] 하나의 MigrationIntent, capability, begin entry로 core/test double/SQLite 전환
- [x] raw direct relation execution은 semantic scan으로 계속 fail-closed
- [x] D4d~D4f 및 commit/rollback/restart 회귀 gate 통과

### C. Generated Publication과 Conformance

- [x] relation model이 scalar descriptor/write/query 기반을 직접 생성하도록 current main generator 통합
- [x] compatibility-only private write model과 old-byte locks 제거
- [x] golden, checked-in generated consumer, inventory를 안정화 뒤 한 번 재기준화
- [x] retained Django observation과 current product adapter/status를 새 계약으로 정렬

### D. 통합 검증과 인수인계

- [x] affected package normal/CGO0/vet와 focused SQLite lifecycle
- [x] full normal/race/CGO0/386, clean consumer compile, generate-check
- [ ] 최종 frozen tree의 full hosted matrix 한 번
- [ ] CURRENT/MATRIX/TEST_EVIDENCE와 남은 제한을 최종 결과에 맞춰 한 번 갱신

## 검증 주기

- 매 변경: gofmt, compile, affected package test, generated drift
- checkpoint: 해당 vertical slice normal/race/CGO0와 실제 SQLite apply/backward/reopen canary
- final integration: full make ci, external consumer compile, deterministic regeneration, hosted matrix
- 문서-only follow-up: link/frontmatter/status consistency만 검증하며 full product matrix를 재귀적으로 요구하지 않습니다.

## 위험과 rollback

- Import cycle: root migrations가 migrations/definition을 import하지 않도록 loaded set ownership을 둡니다.
- Data loss: D4 physical preflight/claim/DDL/recorder 순서를 바꾸지 않고 actual DB 회귀를 먼저 잠급니다.
- False green: experimental observer와 old shim을 product passing으로 계산하지 않습니다.
- Reset rollback은 legacy shim 복원이 아니라 기준점 d824b69 branch와 Git history를 비교 기준으로 유지하는 것입니다.

## 진행 기록

- [x] 2026-08-20: 사용자 승인과 current checkout read-only audit로 reset 방향 확정
- [x] 2026-08-20: GDJ-0035 D4g publication 중단, GDJ-0036/ADR-0035 경계 작성
- [x] Checkpoint A 구현
- [x] Checkpoint B 구현
- [x] Checkpoint C 구현
- [x] 최종 local 통합 검증
- [ ] EVID-100 문서 인수인계와 exact-head hosted 검증

## 다음 정확한 작업

최신 implementation bytes의 full normal/race/CGO-disabled/vet/`make ci`와 Linux/386 all-package compile은
통과했습니다. 독립 최종 감사를 닫고 local implementation commit을 만든 뒤 EVID-100과 상태 mirror를 비재귀적으로
기록합니다. Hosted matrix와 completion은 그 exact head의 별도 증거로 닫습니다.

## 결과와 인수인계

Checkpoint A/B/C 구현은 완료됐고 current relation-product inventory는 fresh process 두 번 모두
`842/842/0`, 86,679 bytes, SHA-256 `706ded972a7beb198cb44aa67feb6c1560e72b0389042df734fd54f24da6759d`로
일치했습니다. C는 current generated ABI와 checked-in output 재기준화까지이며, Q-017의 project-level manifest와
coordinated publication은 구현하지 않았습니다. Current MIG-075..086 manifest/oracle은 각각
7,858/120,502 bytes, SHA-256 `ec90feaf988e5c014a9cc08d00f6744993af146f2e5d5c4cd86d1ed6e18f25a9` /
`5beadac7a80d0903d552e0bf9d5fae85b139ce0754d9163184d907fcf0da5968`로 재기준화됐습니다. Go diagnostic
actual은 639,682 bytes/`374a31be2a5a2f9d64a726f5fc29f9dadf4ffcde30b68b7e42adcb5ca4504ed2`이지만 oracle
semantic comparison이 아니며 12 contract는 계속 locked/unregistered입니다. 현재 기준점과 GDJ-0035 증거는
불변입니다. `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off UV_OFFLINE=1 make ci`와
`GOOS=linux GOARCH=386 CGO_ENABLED=0 ... go test -run '^$' -exec=/usr/bin/true ./...`가 최신 implementation
bytes에서 통과했습니다. 아직 hosted exact-head 증거는 없으므로 migration relation contract status나 broader
framework support를 올리지 않습니다.
