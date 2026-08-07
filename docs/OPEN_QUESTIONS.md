# 핵심 미결정 사항

- 상태: Active register
- 마지막 검토: 2026-08-08

이 문서의 항목은 초안 예시를 확정 API로 오해하지 않도록 관리합니다. 결정이 나면 개별 ADR로 옮기고 여기에는 결과 링크만 남깁니다.

| ID | 우선순위 | 결정 시점 | 질문 |
|---|---|---|---|
| Q-006 | Resolved for M2 | GDJ-0004 | generated immutable builder + `Change[T]`/`NullableChange[T]` — [ADR-0009](adr/0009-m2-explicit-write-change-state.md) Accepted |
| Q-007 | P1 | M1 | QuerySet result cache와 동시 평가의 정확한 의미는 무엇인가 |
| Q-010 | P1 | M1 | 전역 CLI와 프로젝트 library/generator 버전 불일치를 어떻게 처리하는가 |
| Q-011 | P1 | M1 | request, QuerySet, transaction, hook의 goroutine safety 계약은 무엇인가 |
| Q-012 | Partial | public CLI 전 | migration core 경계는 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md) Accepted, file ABI/lock은 계속 open |
| Q-013 | P1 | M3 전 | cross-app relation의 source/target type, import, reverse path, loader는 어떻게 구성하는가 |
| Q-014 | P2 | M5 전 | DTL parser/runtime 호환 수준과 method exposure 정책은 무엇인가 |
| Q-015 | P2 | M6 전 | Admin에서 보존할 흐름과 새로 설계할 UI/DOM/CSS 경계는 무엇인가 |
| Q-016 | P2 | M7/M8 전 | DRF와 Channels의 정확한 reference version과 호환 범위는 무엇인가 |
| Q-017 | P2 | API freeze 전 | pre-1.0 공개 API와 generated code upgrade 정책은 무엇인가 |
| Q-018 | P2 | 공개 배포 전 | Django trademark와 비공식 프로젝트임을 어떤 이름·고지 정책으로 다루는가 |

## M0에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-001 | 선언 package와 generated target을 import graph에서 분리 — [ADR-0006](adr/0006-codegen-input-package-boundary.md) |
| Q-002 | exact runtime fingerprint와 `uv.lock` hash를 profile에서 검증 — [Compatibility Lab](../conformance/README.md) |
| Q-003 | M0에서 strict JSON protocol v1과 explicit adapter를 채택한 뒤, GDJ-0003에서 contract phase를 결속하는 v2로 명시 승격 — [protocol](../conformance/internal/protocol) |
| Q-004 | 독립 시나리오와 upstream 파생물을 구분하고 Django BSD 고지를 보수적으로 포함 — [Licensing](LICENSING.md) |

## M1에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-005 | `orm` 소유 generic interface + generated zero-state concrete descriptor, generation/compile 시점 freeze — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |
| Q-008 | ordered input을 `ParseDynamic`에서 즉시 typed predicate 또는 error로 변환 — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |
| Q-009 | consumer-owned interface와 external module compile + `go list` dependency gate — [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) |

## M2에서 해결한 질문

| ID | 결과 |
|---|---|
| Q-006 | generated immutable create/patch builder, 별도 nullable change state와 Manager write API — [ADR-0009](adr/0009-m2-explicit-write-change-state.md) |
| Q-012 core | preflighted ProjectState/Operation/Executor와 한 transaction의 SQLite editor/recorder — [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md) |

## Q-001 — Codegen bootstrap — Resolved

초안의 임시 runner import 방식은 schema package가 오래된 generated type 때문에
compile되지 않으면 동작하지 않습니다. 실행 spike에서 rename/delete, stale 사용자
메서드, last-good output 보존을 검증하고 선언/generated package 분리를
채택했습니다. Project runner 형태는 Q-010에서 계속 다룹니다.

## Q-006 — Nullable와 변경 추적

M1 read model은 nullable CharField에 `*string`을 사용해 `nil`, `ptr("")`, 일반 값을
구분합니다. MOD-002~004 oracle과 GDJ-0004 compile spike를 거쳐 generated immutable
create/patch builder, `Change[T]`/`NullableChange[T]`와 Manager write API를
[ADR-0009](adr/0009-m2-explicit-write-change-state.md)에서 채택했습니다. Instance dirty
tracking과 `Save()`는 이 첫 단면의 결정이 아닙니다.

## Q-007 — QuerySet cache

불변 plan과 instance result cache를 분리해야 합니다. chain 후 cache, 동시 `All`, error cache, iterator/Count/Exists 간 공유, goroutine sharing을 Django contract와 race/benchmark로 결정합니다.

## Q-012 — Migration format과 실행 수명주기

MIG-001~004와 GDJ-0004 runtime spike를 거쳐 state/operation/executor/schema editor/
recorder core를 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md)에서
채택했습니다. Migration file encoding,
data callback ABI, dependency graph merge, multi-process lock와 crash recovery는 아직
결정하지 않았으며 public CLI 전에 별도 ADR과 contract가 필요합니다.

## Q-013 — 관계 API

초안의 `RelationField[Post]`에는 target type이 없지만 `PostFields.Author.Name`을 사용합니다. symbolic relation binding, target descriptor, generated loader, reverse relation과 import cycle을 한 설계로 검증해야 합니다.

새 작업이 이 표의 질문에 의존하면 추측으로 확정하지 말고 작업 문서에 명시하고 필요한 ADR/prototype을 먼저 만듭니다.
