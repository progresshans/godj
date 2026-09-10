# 설계 결정

현재 계층·계약은 [ARCHITECTURE](../ARCHITECTURE.md), 동시성은 [CONCURRENCY](../CONCURRENCY.md)가 설명한다.
ADR은 대안과 이유를 찾아볼 때 해당 항목만 읽는다. 오래된 API 예시·파일 수·CI 단계는 현재 구현의 호환 요구가 아니다.
Accepted는 설계 채택이며 코드 구현과 platform 검증은 [Matrix](../status/IMPLEMENTATION_MATRIX.md)/[Evidence](../status/TEST_EVIDENCE.md)에서 구분한다.

작은 수정마다 새 ADR을 만들지 않는다. 여러 하위 시스템의 의미·안전성·공개 경계를 바꾸는 결정만 간결하게 기록한다.
기존 결정을 바꾸면 현행 명세와 이 목록의 대체 관계를 함께 갱신한다. CI run/bytes/Job roster는 Evidence에만 둔다.

## 현재 남긴 이유

| ADR | 설계 상태 | 선택한 방향 |
|---|---|---|
| [0001](0001-schema-ir-as-canonical-source.md) | Accepted | Schema IR을 모델 의미의 정규화된 단일 원본으로 사용 |
| [0002](0002-codegen-generics-runtime-metadata.md) | Accepted | Codegen, Generics, Runtime Metadata의 역할 분리 |
| [0003](0003-typed-and-dynamic-query-apis.md) | Accepted | Typed query API와 Dynamic lookup API를 함께 제공 |
| [0004](0004-cli-and-project-binary.md) | Accepted | 전역 `godj` CLI와 프로젝트 바이너리의 역할을 분리 |
| [0005](0005-contract-first-vertical-slices.md) | Accepted | Contract-first 수직 단면으로 구현한다 |
| [0006](0006-codegen-input-package-boundary.md) | Accepted | Codegen 입력은 generated target package와 분리한다 |
| [0007](0007-m1-model-runtime-and-dynamic-query-boundaries.md) | Accepted | M1 모델 runtime과 dynamic query 경계를 고정한다 |
| [0008](0008-m1-sqlite-driver-and-execution-boundary.md) | Accepted | M1 SQLite backend는 modernc database/sql driver를 사용한다 |
| [0009](0009-m2-explicit-write-change-state.md) | Accepted | M2 write 입력은 변경 의도를 명시적으로 보존한다 |
| [0010](0010-m2-migration-state-and-executor-boundary.md) | Accepted | M2 migration은 state, operation, executor, recorder 경계를 먼저 검증한다 |
| [0011](0011-m2-save-lifecycle-orchestration.md) | Accepted | M2 Save는 typed option과 Manager orchestration으로 구현한다 |
| [0012](0012-queryset-evaluation-cache-ownership.md) | Accepted | QuerySet 평가 상태의 ownership과 terminal API를 명시한다 |
| [0013](0013-immutable-migration-planner.md) | Accepted | Migration planning은 불변 identity graph와 분리된 applied state를 사용한다 |
| [0014](0014-migration-plan-execution-atomic-reverse.md) | Accepted | Migration plan 실행은 migration별 commit과 원자적 reverse를 사용한다 |
| [0015](0015-recorder-backed-applied-state.md) | Accepted | Recorder-backed applied state는 별도 read port와 explicit history check를 사용한다 |
| [0016](0016-historical-project-state-reconstruction.md) | Accepted | Historical ProjectState는 loaded migration definition을 dependency order로 replay해 재구성한다 |
| [0017](0017-revision-fenced-migration-lifecycle.md) | Accepted | Migration lifecycle은 각 migration transaction에서 recorder revision을 검증한다 |
| [0018](0018-revision-fenced-migration-lifecycle-product-shape.md) | Accepted | Revision-fenced lifecycle은 Executor와 backend-owned session으로 조립한다 |
| [0021](0021-project-linked-migration-check.md) | Accepted | Project-Linked Migration Check Contract |
| [0022](0022-project-runtime-and-global-migration-check.md) | Accepted | Public Project Runtime and Global Migration Check |
| [0023](0023-symbolic-relation-binding-and-shared-relation-ast.md) | Accepted | Symbolic Relation Binding and Shared Relation AST |
| [0025](0025-forward-foreign-key-predicate-and-sqlite-inner-join.md) | Accepted | Forward ForeignKey Predicate and SQLite INNER JOIN |
| [0026](0026-forward-foreign-key-object-cache-and-nullability.md) | Accepted | Forward ForeignKey Object Cache and Nullability |
| [0027](0027-reverse-foreign-key-accessor-and-lookup.md) | Accepted | Reverse ForeignKey Accessor and Lookup |
| [0028](0028-reverse-foreign-key-prefetch.md) | Accepted | Reverse ForeignKey Prefetch |
| [0029](0029-one-hop-forward-select-related.md) | Accepted | One-hop Forward `select_related` |
| [0030](0030-project-bound-protect-and-set-null-delete.md) | Accepted | Project-bound `PROTECT` and `SET_NULL` Delete |
| [0033](0033-forward-foreign-key-assignment-save-and-cache-ownership.md) | Accepted | Forward ForeignKey Assignment, Save, and Cache Ownership |
| [0035](0035-pre-release-current-only-format-and-generated-publication.md) | Accepted | Pre-release Current-only Format and Generated ABI Reset |
| [0036](0036-project-schema-generated-bundle-and-recoverable-publication.md) | Accepted | Project Schema Generated Bundle and Recoverable Publication |
| [0037](0037-postgresql-current-contract-backend.md) | Accepted | PostgreSQL Current-contract Backend |
| [0038](0038-minimal-web-core-request-lifetime-and-representation.md) | Accepted | Minimal Web Core Request Lifetime and Representation |
| [0039](0039-typed-projection-scalar-aggregate-and-stable-pagination.md) | Accepted | Typed projection, scalar aggregate와 stable pagination을 하나의 read shape로 확장한다 |
| [0040](0040-composable-typed-boolean-predicates-and-article-search.md) | Accepted | 하나의 typed Boolean predicate tree로 Article 검색을 확장한다 |
| [0041](0041-typed-scalar-comparisons-and-field-references.md) | Accepted | typed scalar comparison과 same-model field reference를 하나의 condition RHS로 표현한다 |
| [0042](0042-project-linked-runserver-and-article-development-loop.md) | Accepted | Project-linked Runserver and Generated-aware Development Loop |
| [0043](0043-safe-template-and-model-form-validation.md) | Accepted | Safe Template Runtime and Shared Model Form Validation |
| [0044](0044-session-auth-csrf-and-bounded-article-admin.md) | Accepted | Server-side Session/Auth/CSRF and Bounded Article Admin |
| [0045](0045-closed-parameterized-routing-and-reverse.md) | Accepted | Closed Parameterized Routing and Reverse |
| [0046](0046-json-serializer-and-session-authenticated-article-api.md) | Accepted | JSON Serializer and Session-authenticated Article API |
| [0047](0047-explicit-single-runtime-system-state.md) | Accepted | Explicit Single-Runtime System State |
| [0048](0048-database-coordinated-system-state-and-shared-csrf-key-ring.md) | Accepted | Database-Coordinated System State and Shared CSRF Key Ring |
| [0049](0049-first-party-bff-and-bearer-api-authentication.md) | Accepted | First-Party, BFF and Bearer API Authentication Profiles |
| [0050](0050-canonical-embedded-application-model-facade.md) | Accepted | Canonical Embedded Application Model Facade |
| [0051](0051-project-linked-explicit-migrate.md) | Accepted | Project-linked Explicit Migrate |
| [0052](0052-project-linked-deterministic-makemigrations.md) | Accepted | Project-linked Deterministic Makemigrations |
| [0053](0053-project-linked-read-only-migration-status.md) | Accepted | Project-linked Read-only Migration Status |
| [0054](0054-project-linked-targeted-migration-plan-and-reverse-safety.md) | Accepted | Project-linked Targeted Migration Plan and Reverse Safety |
| [0055](0055-project-linked-deterministic-migration-sql-projection.md) | Accepted | Project-linked Deterministic Migration SQL Projection |
| [0056](0056-explicit-operator-provisioning-and-open-existing.md) | Accepted | Explicit Operator Provisioning and Open-existing System State |
| [0057](0057-sqlite-retained-connection-terminal-quarantine.md) | Proposed | SQLite Retained-connection Terminal Quarantine |

## 대체된 결정

ADR-0019/0020/0024/0031/0032/0034의 옛 format·handoff·generated publication 규칙은
[ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md)로 대체됐다.
고유한 strict decoding·immutability·revision/durability·FK preflight·candidate preservation 이유는 현행 아키텍처에 남겼다.
원문은 [고정 Git 이력](https://github.com/progresshans/godj/tree/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr)에 보존한다.
