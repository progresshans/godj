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
| [0019](0019-versioned-migration-definition-source.md) | Superseded | Strict data document의 dual tuple 경계; ADR-0035가 current-only wire로 대체 |
| [0020](0020-migration-definition-loader-product-shape.md) | Superseded | Existing lifecycle/context handoff; ADR-0035가 explicit loaded set으로 대체 |
| [0021](0021-project-linked-migration-check.md) | Accepted | `godj.toml`, private project runner와 DB-free migration check의 contract/test-only 경계 |
| [0022](0022-project-runtime-and-global-migration-check.md) | Accepted | Public project runtime과 global `godj migrations check` 제품 경계 |
| [0023](0023-symbolic-relation-binding-and-shared-relation-ast.md) | Accepted | Symbolic cross-app relation binding, import-cycle-free project bridge와 shared relation AST |
| [0024](0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md) | Superseded | ForeignKey IR v3/additive bytes; ADR-0035가 current-only IR/publication으로 대체 |
| [0025](0025-forward-foreign-key-predicate-and-sqlite-inner-join.md) | Accepted | Required forward FK exact predicate, shared relation path와 SQLite reusable INNER JOIN |
| [0026](0026-forward-foreign-key-object-cache-and-nullability.md) | Accepted | Forward FK object wrapper/cache, nullable access와 relation-aware SQLite isnull trim |
| [0027](0027-reverse-foreign-key-accessor-and-lookup.md) | Accepted | Reverse FK exact lookup, owner related-set accessor와 SQLite reverse INNER JOIN |
| [0028](0028-reverse-foreign-key-prefetch.md) | Accepted | Reverse FK two-stage batch prefetch와 atomic warm related-set publication |
| [0029](0029-one-hop-forward-select-related.md) | Accepted | One-hop forward required/nullable eager projection과 reverse-path pre-I/O rejection |
| [0030](0030-project-bound-protect-and-set-null-delete.md) | Accepted | Declared project incoming policy 기반 SQLite PROTECT/SET_NULL relation delete |
| [0031](0031-relation-aware-project-facade-and-generated-upgrade-boundary.md) | Superseded | Physical fixture byte-preserving feasibility; ADR-0035가 current bundle publication으로 대체 |
| [0032](0032-production-forward-project-facade-and-additive-first-publication.md) | Superseded | Frozen exact-13 additive publication; ADR-0035가 current bundle publication으로 대체 |
| [0033](0033-forward-foreign-key-assignment-save-and-cache-ownership.md) | Accepted | Django-first REL-002 forward assignment/save, PK-presence, canonical preflight와 per-edge COW cache 경계 |
| [0034](0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md) | Superseded | D4d~D4f evidence는 보존; dual profile/state/backend와 D4g publication은 ADR-0035로 대체 |
| [0035](0035-pre-release-current-only-format-and-generated-publication.md) | Accepted | Current-only IR/definition/state, explicit loaded set, unified migration intent와 current generated ABI reset |
| [0036](0036-project-schema-generated-bundle-and-recoverable-publication.md) | Accepted | ProjectSpec/GeneratedBundle, whole-candidate compile와 recoverable coordinated publication |
| [0037](0037-postgresql-current-contract-backend.md) | Accepted | PostgreSQL 17 current backend profile, returned insert key와 semantic migration capability |
| [0038](0038-minimal-web-core-request-lifetime-and-representation.md) | Accepted | Immutable Web Core, request lifetime와 explicit DTO representation |
| [0039](0039-typed-projection-scalar-aggregate-and-stable-pagination.md) | Accepted | Source/result shape 분리, typed projection/scalar aggregate와 stable pagination |
| [0040](0040-composable-typed-boolean-predicates-and-article-search.md) | Accepted | 하나의 typed Boolean predicate tree와 bounded Article 검색 |
| [0041](0041-typed-scalar-comparisons-and-field-references.md) | Accepted | Typed scalar comparison과 same-model field reference RHS |
| [0042](0042-project-linked-runserver-and-article-development-loop.md) | Accepted | Optional project-linked `runserver`와 generated-aware 개발 루프 |
| [0043](0043-safe-template-and-model-form-validation.md) | Accepted | Closed-value safe template와 IR-derived Model Form validation 경계 |
| [0044](0044-session-auth-csrf-and-bounded-article-admin.md) | Accepted | Process session/auth/CSRF와 bounded Article Admin 경계 |
| [0045](0045-closed-parameterized-routing-and-reverse.md) | Accepted | Static API를 보존하는 closed integer parameter route/reverse 경계 |
| [0046](0046-json-serializer-and-session-authenticated-article-api.md) | Accepted | Reflection-free JSON serializer와 session-authenticated Article API 경계 |
| [0047](0047-explicit-single-runtime-system-state.md) | Accepted | Current migration 기반 single-runtime auth/session/audit와 clean sequential restart 경계 |
| [0048](0048-database-coordinated-system-state-and-shared-csrf-key-ring.md) | Accepted | Database-coordinated cooperative multi-runtime system state와 shared CSRF key ring |
| [0049](0049-first-party-bff-and-bearer-api-authentication.md) | Accepted | First-party session, BFF와 strict injected Bearer API authentication profile; corrected exact hosted acceptance 완료 |
| [0050](0050-canonical-embedded-application-model-facade.md) | Accepted | Embedded raw scalar/user method와 project-owned relation state를 결합하는 hosted-verified current application model facade |
| [0051](0051-project-linked-explicit-migrate.md) | Accepted | Project-owned backend opener와 strict global `migrate` orchestration 경계 |
| [0052](0052-project-linked-deterministic-makemigrations.md) | Accepted | Additive-only schema autodetection, deterministic current definition과 recoverable global `makemigrations` publication 경계 |
| [0053](0053-project-linked-read-only-migration-status.md) | Accepted | Project-linked read-only applied-history snapshot과 deterministic `showmigrations` list 경계 |
| [0054](0054-project-linked-targeted-migration-plan-and-reverse-safety.md) | Accepted | Exact target, non-authoritative plan과 bounded reverse migration safety 경계 |
| [0055](0055-project-linked-deterministic-migration-sql-projection.md) | Accepted | Exact forward migration의 deterministic DB-free SQL projection 경계 |

새 ADR은 [TEMPLATE.md](TEMPLATE.md)를 복사하고 4자리 일련번호를 사용합니다.
