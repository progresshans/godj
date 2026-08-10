# Work items

이 디렉터리는 여러 턴과 여러 사람이 재개할 수 있는 실행 단위의 정본입니다. 설계 문서를 복사하지 않고 관련 문서와 ADR을 링크합니다.

## 상태

```text
proposed → ready → active → completed
                    └→ blocked
                    └→ superseded
```

- `proposed`: 순서·범위 검토 전
- `ready`: 선행 조건과 완료 기준이 정해짐
- `active`: 현재 checkout에서 진행 중
- `blocked`: blocker와 해제 조건이 명시됨
- `completed`: 실제 결과와 검증 증거까지 기록됨
- `superseded`: 다른 work item으로 대체됨

한 시점에 통합 작업은 하나만 `active`로 둡니다. 독립 subtask를 병렬 실행할 때는 수정 예정 경로와 통합 소유자를 분명히 기록합니다.

## 목록

| ID | 상태 | 작업 |
|---|---|---|
| [GDJ-0000](0000-documentation-foundation.md) | completed | 문서·결정·인수인계 기반 수립 |
| [GDJ-0001](0001-compatibility-lab.md) | completed | Django 6.1 Compatibility Lab |
| [GDJ-0002](0002-model-to-query-walking-skeleton.md) | completed | 첫 Model-to-Query 수직 단면 |
| [GDJ-0003](0003-write-migration-compatibility-contracts.md) | completed | Write/Migration 호환 계약 확장 |
| [GDJ-0004](0004-write-migration-walking-skeleton.md) | completed | Write/Migration 첫 제품 수직 단면 |
| [GDJ-0005](0005-save-lifecycle-compatibility-contracts.md) | completed | Save lifecycle 호환 계약 확장 |
| [GDJ-0006](0006-save-lifecycle-product-slice.md) | completed | Save lifecycle 제품 수직 단면 |
| [GDJ-0007](0007-queryset-evaluation-cache-compatibility-contracts.md) | completed | QuerySet evaluation/cache 호환 계약 |
| [GDJ-0008](0008-queryset-evaluation-cache-product-slice.md) | completed | QuerySet evaluation/cache 제품 수직 단면 |
| [GDJ-0009](0009-migration-planning-compatibility-contracts.md) | completed | Migration planning 호환 계약 확장 |
| [GDJ-0010](0010-immutable-migration-planner-product-slice.md) | completed | Immutable migration graph/applied-state planner 제품 단면 |
| [GDJ-0011](0011-migration-plan-execution-compatibility-contracts.md) | completed | Multi-migration plan execution 호환 계약 |
| [GDJ-0012](0012-migration-plan-execution-orchestrator.md) | completed | Migration plan 실행 orchestrator와 atomic-reverse 결정 |
| [GDJ-0013](0013-recorder-backed-restart-planning-compatibility-contracts.md) | completed | Recorder-backed restart planning 호환 계약 |
| [GDJ-0014](0014-recorder-backed-restart-planning-product-slice.md) | completed | Recorder-backed restart planning 제품 단면 |
| [GDJ-0015](0015-historical-project-state-reconstruction-compatibility-contracts.md) | completed | Historical ProjectState reconstruction 호환 계약 |
| [GDJ-0016](0016-historical-project-state-reconstruction-product-slice.md) | completed | Historical ProjectState reconstruction 제품 단면 |
| [GDJ-0017](0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md) | completed | Migration lifecycle 호환 계약과 revision-fence spike |
| [GDJ-0018](0018-revision-fenced-migration-lifecycle-product-slice.md) | completed | Revision-fenced migration lifecycle 제품 단면 |
| [GDJ-0019](0019-migration-definition-source-compatibility-contracts.md) | completed | Migration definition source/versioned-loader 호환 계약 |
| [GDJ-0020](0020-migration-definition-loader-product-slice.md) | completed | Migration definition loader 제품 단면 |
| [GDJ-0021](0021-migration-project-check-compatibility-contracts.md) | completed | Project-linked migration check 호환/결정 계약 |
| [GDJ-0022](0022-migration-project-check-product-slice.md) | completed | Project-linked migration check 제품 단면 |
| [GDJ-0023](0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md) | completed | ForeignKey 관계 호환 계약과 cross-app binding feasibility |
| [GDJ-0024](0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md) | completed | AutoField ForeignKey IR v3, atomic binding과 REL-001 제품 metadata |
| [GDJ-0025](0025-forward-foreign-key-predicate-product-slice.md) | completed | REL-004 forward ForeignKey predicate와 SQLite reusable INNER JOIN |
| [GDJ-0026](0026-forward-foreign-key-object-cache-and-nullability-product-slice.md) | active | REL-003/006 forward object cache, nullable access와 SQLite isnull trim |

현재 활성 항목과 다음 ready 항목은
[docs/status/CURRENT.md](../docs/status/CURRENT.md)와 일치해야 합니다. 현재 ready 항목은 없고 최근
완료 항목은 [GDJ-0025](0025-forward-foreign-key-predicate-product-slice.md), active 항목은
[GDJ-0026](0026-forward-foreign-key-object-cache-and-nullability-product-slice.md)입니다.
GDJ-0024 final evidence/status head `5bf143575e9b703117a328c1fc5b7eb5823fbfd6`은 run
`31351169780`의 exact 26/26 jobs·326/326 recorded steps를 통과해 EVID-038에 기록됐습니다. GDJ-0025는
이 clean tested baseline에서 REL-004-only query/join 수직 단면과 Proposed ADR-0025를 활성화했습니다.
Activation commit `cf8cb589575836cb1393079ce04ff06fc549800a`는
[run 31354040515](https://github.com/progresshans/godj/actions/runs/31354040515)의 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. Frozen implementation은 local `make ci`, relation inventory
492/492/0·49,902 bytes·SHA-256 `05064a7f...82eb`, twelve adapters, bounded Linux/386 compile과 four
independent audits P0/P1/P2/P3=0을 통과해
[EVID-20260810-039](../docs/status/TEST_EVIDENCE.md#evid-20260810-039--gdj-0025-rel-004-forward-predicate-pre-hosted-local-validation)에
기록했습니다. Implementation commit `98db55a30ff71a2f2f70722cb569a046208a5403`은
[run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)의 exact 26/26 jobs·326/326
recorded steps를 통과했고
[EVID-040](../docs/status/TEST_EVIDENCE.md#evid-20260810-040--gdj-0025-github-hosted-exact-26-job-implementation-head-ci)에
기록했습니다. Product는 REL-001/004 actual 2/12, exact
`112 passing + 5 deviation + 10 oracle_locked`입니다. Work는 completed, ADR-0025는 required one-hop
exact/SQLite INNER JOIN에 한해 Accepted이고 Q-013은 `Partial`입니다. Completion-documentation commit
`7b5cebda7410ae8c096a8c30bd60daad1295bbf2`도
[run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)의 exact 26/26 jobs·326/326
recorded steps를 통과해
[EVID-041](../docs/status/TEST_EVIDENCE.md#evid-20260810-041--gdj-0025-github-hosted-completion-documentation-head-exact-26-job-ci)에
기록했습니다. 그 EVID-041/final-status commit `bffc52844de87a2791959ea1e8f99c60dd13d1aa`도 별도
[run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949)의 exact 26/26 jobs·326/326
steps를 통과해 [EVID-042](../docs/status/TEST_EVIDENCE.md#evid-20260810-042--gdj-0025-final-exact-head-ci-and-gdj-0026-activation-baseline)에
기록했습니다. GDJ-0026은 이 tested baseline에서 REL-003/006 object/cache/nullability slice와 Proposed
ADR-0026을 활성화했습니다. 이 activation documentation diff 자체의 exact-head CI는 pending입니다.
GDJ-0024는 baseline `50578ddc...`의 EVID-034 exact 22/22를 GDJ-0023 final evidence로 닫고,
mixed v2 target/v3 relation source companion, atomic `orm.BindProject`와 REL-001 metadata만 구현할 exact
boundary를 활성화했습니다. Activation commit `758cd093...`은 run `31344980929`의 exact 22/22·273/273
steps를 통과했습니다. Implementation commit `05e6e218...`은 IR v3/companion/bridge/binder/migration
fail-closed와 REL-001 metadata actual, 11 ordered not-implemented, exact-26 workflow를 구현했고 run
`31348285559`의 exact 26/26 jobs·326/326 recorded steps를 통과했습니다. Work는 completed, ADR-0024는
bounded metadata architecture에 한해 Accepted이고 Q-013은 `Partial`입니다. GDJ-0022는
Accepted ADR-0021/MIG-065..074를 independent product global CLI/public project runner/flat discovery로
구현해 10 contract를 `passing`으로 전환했고, final evidence head까지 exact 18-job hosted acceptance를
완료했습니다. GDJ-0023은 Q-013을 추측으로 제품화하지 않고 pinned Django 6.1 ForeignKey 외부 동작
REL-001..012와 test-only cross-app binding/import-cycle/shared-AST feasibility를 고정했습니다.
GDJ-0020은
`codex/revision-fenced-migration-lifecycle@6172d843a4bb234592cafc176a8d1191933b141c`의 제품 구현과
Draft PR #1 exact-head CI를 근거로 완료됐고,
[ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 Accepted입니다.

완료된 GDJ-0019는 explicit caller-provided strict data document, compatibility tuple `(1,1,1,2)`,
fully normalized IR v2, `CreateModel`/non-PK `char`·`boolean` `AddField` codec, atomic/deterministic load와 existing
`Executor.Migrate` reference handoff를 MIG-057..064로 잠그는 contract-only 작업입니다. 완료
결과는 기존 9 product set의 `92 passing + 5 deviation`을 보존하면서 8 `oracle_locked`를 더한
10 reference set/105 contract와 90 ordered cross-binding rejection입니다. Completed GDJ-0020은
열 번째 product adapter를 구현해 105 product contract의 `100 passing + 5 deviation`을
local/exact-head hosted gate에서 검증했습니다. Filesystem/module discovery, CLI, writer/upgrade/cache는
여전히 제품 미구현 범위입니다. Completed GDJ-0021은 그중 가장 작은 DB-free
`godj migrations check` 경험의 project selection/build/runner/flat discovery를 MIG-065..074의
contract-only decision reference로 검증했습니다. 결과는 11 reference set/115 contract/110 ordered
cross-binding과 새 10 `oracle_locked`이며, 제품은 계속 10 adapter/105 contract의
`100 passing + 5 deviation`입니다. [ADR-0021](../docs/adr/0021-project-linked-migration-check.md)은
local/10-job hosted evidence를 근거로 Accepted이지만 전역 CLI나 project package가 구현됐다는 뜻이
아닙니다. Q-010/Q-012는 `Partial`을 유지합니다. Status 7 + general 9의 exact 16-file
completion-documentation commit `34ae58fc2490deb8f884a0b5591520b11bae8669`도 별도 exact 10-job
hosted CI를 통과했습니다. EVID-026 append/status 교정 commit
`f7fbbd50465a610ed9492227909eece524455f15`도 run `31322959993`에서 exact 10 jobs를 통과했습니다.
GDJ-0022는 exact 11 adapter/115 contract의 `110 passing + 5 deviation`과 Accepted
[ADR-0022](../docs/adr/0022-project-runtime-and-global-migration-check.md)를 구현했습니다. Initial head
`06858dd6aafeb20449bc4fbfa9aeac78c7a794ce`의 run `31329231255`는 네 Python leg 모두 테스트 전 uv
exact-string assertion에서 실패해 취소했고, metadata suffix를 허용한 fix head
`3dfeff2a881a3313883729943519896798d92afc`의 run `31329294154`는 existing 10 + product 4 + Python
compatibility 4인 exact 18/18을 성공했습니다. EVID-028/status commit
`68b408add3b050d0938ccebc6c83200499f57b2a`의 run `31330601427`은 16 success/2 macOS product normal
failure였고, helper/harness와 production wait reconciliation을 고친 final stabilization head
`385382efffd1872ae7fb427192bab27b95dc57e2`의 run `31332208055`는 exact 18/18을 다시 성공했습니다.
EVID-029/status commit `1f161f311daa775e6a386ec0df568ff85d681f15`도 별도 run `31333420261`의
exact 18/18을 통과했고 EVID-030에 기록했습니다. GDJ-0023 activation commit
`d5d00d9e803c637a78961ed6f7dac0b415ce7901`도 제공된 verified run `31335315454`의 기존 exact 18/18을
통과했습니다. Phase A REL-001..012 reference와 Phase B test-only relationbinding은 local 구현/검증,
두 independent final audit P0/P1/P2/P3 finding 0과 implementation head `b56ccf5`의
[run 31338151743](https://github.com/progresshans/godj/actions/runs/31338151743) exact 22/22까지
통과했습니다. Work는 completed, ADR-0023은 Accepted, Q-013은 `Partial`이며 relation product adapter는
0입니다. Completion-documentation head `31784ae1`도 별도
[run 31339409336](https://github.com/progresshans/godj/actions/runs/31339409336)의 exact 22/22와
[EVID-20260810-033](../docs/status/TEST_EVIDENCE.md#evid-20260810-033--gdj-0023-github-hosted-completion-documentation-head-exact-22-job-ci)으로
검증했습니다. 이어 final evidence/status head `50578ddc4756452b2a9a0d2afd75711a35b76d8a`의
[run 31340170361](https://github.com/progresshans/godj/actions/runs/31340170361)도 exact 22/22와 273/273
steps를 성공해
[EVID-20260810-034](../docs/status/TEST_EVIDENCE.md#evid-20260810-034--gdj-0023-final-evidence-documentation-exact-head-ci-and-gdj-0024-activation-baseline)에
기록했습니다. 이 tested clean baseline 뒤 GDJ-0024 activation 문서 diff 자체의 exact-head hosted CI는
activation commit `758cd093...` / run `31344980929`의 exact 22/22로 해소됐습니다. GDJ-0024 local
implementation/audit와 그 증거는
[EVID-20260810-035](../docs/status/TEST_EVIDENCE.md#evid-20260810-035--gdj-0024-rel-001-metadata-product-pre-hosted-local-validation)에
기록했습니다. Implementation exact-head exact-26 hosted acceptance는
[EVID-20260810-036](../docs/status/TEST_EVIDENCE.md#evid-20260810-036--gdj-0024-github-hosted-exact-26-job-implementation-head-ci)에
기록했습니다. GDJ-0024 완료 당시 product는 12 adapter/127 contract=
`111 passing + 5 deviation + 11 oracle_locked`, REL-001
actual 1/12이며 broader relation query/write/delete/DDL/migration과 non-SQLite backend support는 아닙니다.

## 운영 규칙

- 작업 시작 전에 baseline branch/commit과 dirty files를 기록합니다.
- 목표, 비목표, contract ID, 수정 허용 경로, 완료 조건이 없으면 구현을 시작하지 않습니다.
- 공개 API 또는 장기 결정이 바뀌면 ADR을 먼저 또는 같은 변경에서 갱신합니다.
- 체크리스트만 완료로 바꾸지 말고 실제 변경 파일과 evidence ID를 기록합니다.
- 중단 시 실패한 정확한 명령과 다음에 실행할 명령을 적습니다.
- completed 항목은 결과와 남은 제한을 보존합니다. 나중 상태로 덮어쓰지 않습니다.

새 항목은 [TEMPLATE.md](TEMPLATE.md)를 사용합니다.
