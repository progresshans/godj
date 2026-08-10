# Architecture Decision Records

ADR은 되돌리기 어렵거나 여러 하위 시스템에 영향을 주는 결정을 기록합니다. 현재 상태나 작업 체크리스트를 담지 않습니다.

## 상태

- `Proposed`: 검토 또는 prototype 필요
- `Accepted`: 현재 방향으로 채택
- `Rejected`: 채택하지 않음
- `Superseded`: 새 ADR로 대체

Accepted ADR을 바꿀 때는 원문을 결과에 맞춰 조용히 다시 쓰지 않습니다. 새 ADR을 만들고 이전 ADR의 상태와 대체 링크를 갱신합니다. 오탈자, 링크, 사실 오류는 decision 의미를 바꾸지 않는 범위에서 수정할 수 있습니다.

## 목록

| ADR | 상태 | 결정 |
|---|---|---|
| [0001](0001-schema-ir-as-canonical-source.md) | Accepted | Schema IR을 모델 의미의 정규화된 단일 원본으로 사용 |
| [0002](0002-codegen-generics-runtime-metadata.md) | Accepted | Codegen, Generics, Runtime Metadata의 역할 분리 |
| [0003](0003-typed-and-dynamic-query-apis.md) | Accepted | typed/dynamic query API가 동일 AST로 수렴 |
| [0004](0004-cli-and-project-binary.md) | Accepted | 전역 CLI와 프로젝트 바이너리 역할 분리 |
| [0005](0005-contract-first-vertical-slices.md) | Accepted | contract-first 수직 단면으로 구현 순서 구성 |
| [0006](0006-codegen-input-package-boundary.md) | Accepted | Codegen 입력과 generated target package의 import graph 분리 |
| [0007](0007-m1-model-runtime-and-dynamic-query-boundaries.md) | Accepted | M1 descriptor, nullable read, dynamic lookup과 dependency 경계 |
| [0008](0008-m1-sqlite-driver-and-execution-boundary.md) | Accepted | M1 pure-Go SQLite driver와 중립 실행 경계 |
| [0009](0009-m2-explicit-write-change-state.md) | Accepted | M2 generated write builder와 explicit change state |
| [0010](0010-m2-migration-state-and-executor-boundary.md) | Accepted | M2 migration state, operation, executor와 recorder 경계 |
| [0011](0011-m2-save-lifecycle-orchestration.md) | Accepted | M2 typed Save option, explicit key와 Manager orchestration 경계 |
| [0012](0012-queryset-evaluation-cache-ownership.md) | Accepted | QuerySet evaluation state ownership, concurrency와 terminal API 경계 |
| [0013](0013-immutable-migration-planner.md) | Accepted | 불변 migration identity graph, applied state와 zero-I/O planner 경계 |
| [0014](0014-migration-plan-execution-atomic-reverse.md) | Accepted | migration별 plan 실행과 same-transaction atomic reverse |
| [0015](0015-recorder-backed-applied-state.md) | Accepted | 별도 recorder read port, applied-state 검증과 explicit history check |
| [0016](0016-historical-project-state-reconstruction.md) | Accepted | Loaded migration definition의 dependency-ordered historical state replay |
| [0017](0017-revision-fenced-migration-lifecycle.md) | Accepted | 각 migration transaction의 recorder revision fence |
| [0018](0018-revision-fenced-migration-lifecycle-product-shape.md) | Accepted | Connection-free revision session, commit durability와 MIG-052 canonical-order deviation |
| [0019](0019-versioned-migration-definition-source.md) | Accepted | Explicit strict data document, version tuple와 atomic definition load 경계 |
| [0020](0020-migration-definition-loader-product-shape.md) | Accepted | Bounded definition loader package, immutable set/report와 existing lifecycle handoff |
| [0021](0021-project-linked-migration-check.md) | Accepted | `godj.toml`, private project runner와 DB-free migration check의 contract/test-only 경계 |
| [0022](0022-project-runtime-and-global-migration-check.md) | Accepted | Public project runtime과 global `godj migrations check` 제품 경계 |
| [0023](0023-symbolic-relation-binding-and-shared-relation-ast.md) | Accepted | Symbolic cross-app relation binding, import-cycle-free project bridge와 shared relation AST |
| [0024](0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md) | Accepted | AutoField ForeignKey IR v3, additive mixed-app companion과 atomic project binding |
| [0025](0025-forward-foreign-key-predicate-and-sqlite-inner-join.md) | Accepted | Required forward FK exact predicate, shared relation path와 SQLite reusable INNER JOIN |
| [0026](0026-forward-foreign-key-object-cache-and-nullability.md) | Proposed | Forward FK object wrapper/cache, nullable access와 relation-aware SQLite isnull trim |

새 ADR은 [TEMPLATE.md](TEMPLATE.md)를 복사하고 4자리 일련번호를 사용합니다.
