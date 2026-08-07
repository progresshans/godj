# 핵심 미결정 사항

- 상태: Active register
- 마지막 검토: 2026-08-08

이 문서의 항목은 초안 예시를 확정 API로 오해하지 않도록 관리합니다. 결정이 나면 개별 ADR로 옮기고 여기에는 결과 링크만 남깁니다.

| ID | 우선순위 | 결정 시점 | 질문 |
|---|---|---|---|
| Q-006 | Resolved | GDJ-0006 | ADR-0011의 Manager Save, concrete typed option/field mask와 generated explicit-key helper로 MOD-008..019 Verified |
| Q-007 | Resolved | GDJ-0008 | ADR-0012 ownership/API와 QRY-011..021 제품 adapter가 Verified; 총 45개 contract passing |
| Q-010 | P1 | M1 | 전역 CLI와 프로젝트 library/generator 버전 불일치를 어떻게 처리하는가 |
| Q-011 | Partial | GDJ-0008/M5+ | QuerySet evaluation subset은 ADR-0012와 race/cancellation test로 해결; request/transaction/hook 범위는 후속 단계에서 결정 |
| Q-012 | Partial | GDJ-0013/public CLI 전 | ADR-0010 executor, ADR-0013 planner와 ADR-0014 ExecutePlan/atomic reverse까지 검증; recorder read/restart planning, file ABI/data callback/lock은 계속 open |
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
| Q-006 | generated create/patch와 nullable change state는 ADR-0009, mutable instance Save/typed mask/explicit key orchestration은 [ADR-0011](adr/0011-m2-save-lifecycle-orchestration.md); MOD-001..019 verified |
| Q-012 core | preflighted ProjectState/Operation/Executor와 한 transaction의 SQLite editor/recorder — [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md) |

## Q-001 — Codegen bootstrap — Resolved

초안의 임시 runner import 방식은 schema package가 오래된 generated type 때문에
compile되지 않으면 동작하지 않습니다. 실행 spike에서 rename/delete, stale 사용자
메서드, last-good output 보존을 검증하고 선언/generated package 분리를
채택했습니다. Project runner 형태는 Q-010에서 계속 다룹니다.

## Q-006 — Nullable와 변경 추적

M1 read model은 nullable CharField에 `*string`을 사용해 `nil`, `ptr("")`, 일반 값을
구분합니다. MOD-002~004 oracle과 GDJ-0004 구현을 거쳐 generated immutable
create/patch builder, `Change[T]`/`NullableChange[T]`와 Manager write API를
[ADR-0009](adr/0009-m2-explicit-write-change-state.md)에서 채택·검증했습니다. Django가
기본 `save()`에서 실제 dirty tracking을 하는지 추측하지 않고, instance save/new/loaded,
`update_fields`, force flag, explicit PK와 rollback 의미를
[GDJ-0005](../work/0005-save-lifecycle-compatibility-contracts.md)의 MOD-008..019로
고정했습니다. Fully loaded default save는 field 전체를 쓰며 dirty-only가 아닙니다.
Go에서는 [ADR-0011](adr/0011-m2-save-lifecycle-orchestration.md)에 따라 concrete
`SaveOption[M]`, sealed `WritableField[M]`, generated explicit-key constructor와
`Manager[M].Save`를 사용합니다. Generated instance method는 model field `Save`와의
충돌 때문에 만들지 않으며, MOD-008..019가 이 경계로 모두 통과했습니다.

## Q-007 — QuerySet cache

불변 plan과 instance result cache를 분리해야 합니다.
[GDJ-0007](../work/0007-queryset-evaluation-cache-compatibility-contracts.md)에서 chain,
반복/빈/실패 평가, iterator/Count/Exists/fresh clone의 Django 외부 동작을 먼저
QRY-011..021의 exact 계약으로 고정했습니다. 당시 oracle은 `oracle_locked`이고 제품은
intentionally uncached였습니다. Value-copy QuerySet의 cache ownership, cached result alias,
동시 `All`, waiter cancellation과 Go terminal 표면은 그 결과를 입력으로
[ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에서 Accepted했습니다.
[GDJ-0008](../work/0008-queryset-evaluation-cache-product-slice.md)은 이를 제품 unit/compile/
race/differential test로 구현해 QRY-011..021 모두를 `passing`으로 검증했습니다. 따라서
Q-007은 해결됐습니다. Q-011은 QuerySet evaluation subset만 해결됐고 request,
transaction-bound session과 hook의 goroutine ownership은 후속 단계에 남습니다.

## Q-012 — Migration format과 실행 수명주기

MIG-001~004와 GDJ-0004 제품 구현을 거쳐 state/operation/executor/schema editor/
recorder core를 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md)에서
채택·검증했습니다.
[GDJ-0009](../work/0009-migration-planning-compatibility-contracts.md)는 제품 graph를 먼저
만들지 않고 MIG-005..016으로 dependency/applied-state 기반 forward/backward plan,
multi-target 중복 제거와 잘못된 graph/history 오류를 contract-only로 고정했습니다.

[ADR-0013](adr/0013-immutable-migration-planner.md)은 `ProjectState`와 applied history를
분리하고 immutable identity graph의 zero-I/O Planner를 사용하기로 결정했습니다.
[GDJ-0010](../work/0010-immutable-migration-planner-product-slice.md)은 이 경계와 actual
adapter를 구현해 MIG-005..016을 모두 `passing`으로 검증했습니다. MIG-012는 caller target
order와 dependency precedence만 잠그며, incomparable sibling의 Django private DFS
tie-break는 호환 계약이 아닌 Go deterministic 정책입니다.

[GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md)은
multi-migration execution과 partial commit/failure stop 의미를 MIG-017..026의 여섯 번째
exact set으로 잠갔습니다. Django backward의 `schema_then_record` 때문에 정상 backward
세 계약의 transaction model과 recorder failure 한 계약의 DB state/phase가 GoDj 기존
same-transaction reverse와 다릅니다.

완료된 [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md)는 full zero-I/O
preflight, migration별 existing Apply/Unapply commit과 last durable state를 가진 최소
`ExecutePlan`을 구현했습니다. Same-transaction reverse는
[ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)와
[DEV-0001](DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
Accepted/Verified 결정이며 제품 상태는 `63 passing + 4 deviation`입니다.

다음 [GDJ-0013](../work/0013-recorder-backed-restart-planning-compatibility-contracts.md)은
새 process/executor가 durable recorder에서 applied identity를 읽고 남은 plan을 계산하는
의미를 contract-first로 고정합니다. 이 작업은 recorder read 제품 API나 public file/CLI를
동시에 확정하지 않습니다.

Migration file encoding, recorder read/list, data callback ABI, graph merge/squash/optimizer,
multi-process lock와 crash recovery는 여전히 결정하지 않았으며 public CLI 전에 별도
ADR과 contract가 필요합니다. Planner와 execution subset 완료도 Q-012 전체 해결을 뜻하지
않습니다.

## Q-013 — 관계 API

초안의 `RelationField[Post]`에는 target type이 없지만 `PostFields.Author.Name`을 사용합니다. symbolic relation binding, target descriptor, generated loader, reverse relation과 import cycle을 한 설계로 검증해야 합니다.

새 작업이 이 표의 질문에 의존하면 추측으로 확정하지 말고 작업 문서에 명시하고 필요한 ADR/prototype을 먼저 만듭니다.
