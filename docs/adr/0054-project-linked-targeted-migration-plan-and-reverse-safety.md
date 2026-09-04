# ADR-0054: Project-linked Targeted Migration Plan and Reverse Safety

- 상태: Accepted
- 날짜: 2026-08-30
- 관련 work/contract: GDJ-0052, MIG-119..MIG-128, Q-010, Q-012, Q-019
- 대체하는 ADR: 없음

## 맥락

Completed GDJ-0049는 project-linked latest-only `migrate`, GDJ-0050은 additive `makemigrations`, GDJ-0051은 read-only
`showmigrations`를 제공합니다. Core Planner와 loaded Executor에는 named/zero target, reverse dependency closure, fenced
step transaction과 commit-outcome 분류가 이미 있지만 public CLI는 이 destructive 경계를 노출하지 않습니다. 사용자는 현재
상태를 볼 수 있어도 특정 migration으로 이동하거나 실행 전에 exact plan을 확인할 수 없습니다.

## 결정 기준

- 기존 latest-only 성공 JSON과 transaction/cleanup 의미 보존
- Django에서 익숙한 exact target/zero/reverse 의미
- Preview와 execute 사이 drift를 안전하게 처리하는 fresh-snapshot authority
- Existing library `ZeroTarget` behavior를 깨지 않는 public unknown-app rejection
- Backend-neutral shared core와 SQLite/PostgreSQL 동일 계약
- Current-only strict private wire, bounded output와 secret-free failure

## 고려한 선택지

### A. Target execute만 먼저 공개

코드 변경은 작지만 첫 public destructive reverse를 사용자가 실행 전에 확인할 표준 경로가 없습니다. 이미 완성된
`showmigrations`는 상태만 보여 주고 실제 dependency closure는 보여 주지 않습니다.

### B. Preview plan을 저장하고 같은 plan을 실행

Preview 이후 다른 runtime이 history를 바꾸면 stale plan은 올바른 실행 authority가 아닙니다. Plan token/CAS를 새 public
surface로 만들면 distributed authorization과 retention 정책까지 이번 packet에 끌어옵니다.

### C. Target execute와 non-authoritative plan을 함께 공개

두 mode는 definition/history/graph/dry/capability preflight를 공유하되 execute가 항상 새 session에서 다시 계획합니다. Preview는
사람과 도구를 위한 관측 결과이고 write authorization이 아닙니다.

## 제안 결정

선택지 C를 채택합니다.

1. Exact public CLI grammar는 latest/named/app-zero 각 execute/plan과 optional trailing `--project PATH` 조합의 여덟
   형태입니다. Existing library `TargetedLifecycleRequest(first, rest...)`의 caller-ordered multi-target/mixed-plan 계약은
   Plan/Migrate에서 그대로 보존합니다.
2. Exact lowercase `zero`만 app-zero이며 app-only, option permutation과 multiple target은 거부합니다. 문법상 유효한
   prefix-looking token은 별도 prefix resolution을 하지 않고 exact identity로 조회하며, exact match가 없으면
   `target_not_found`입니다.
3. `Executor.Plan`은 `Executor.Migrate`와 동일한 loaded snapshot validation, revision-fenced history read, history check,
   historical state reconstruction, whole-plan dry validation과 backend capability preflight를 공유합니다.
4. Plan mode는 `BeginMigration`을 호출하지 않고 detached `[]PlanStep`만 반환합니다. Session close failure면 plan을 폐기합니다.
5. Execute는 preview output/token을 입력받지 않고 항상 fresh history snapshot에서 plan을 다시 계산합니다.
6. `KnownAppZeroTarget`은 graph에 app node가 없으면 `target_not_found`를 반환합니다. Existing `ZeroTarget`의 valid unknown-app
   empty plan은 accepted library contract로 유지합니다.
7. Named applied target은 target과 direct cross-app branch를 유지하고 same-app descendants 및 그 descendants에 의존하는
   applied cross-app dependents를 dependency-safe reverse order로 처리합니다. App-zero는 해당 app roots와 applied
   dependents를 되돌립니다.
8. Private migrate wire는 current-only v2와 `__godj_project_migrate_runner_v2`로 교체합니다. v1 reader/argument를 남기지 않습니다.
9. Execute response의 existing summary shape와 public JSON은 보존하고, plan response는 bounded exact `{plan:[...]}` JSON입니다.
10. Reverse step마다 기존 fenced transaction/recorder transition을 사용합니다. Middle failure는 committed prefix만 남기며
    commit outcome unknown은 자동 retry하지 않습니다.
11. Request는 16 MiB, response/public plan은 minimal JSON 최악 expansion을 포함한 101 MiB, plan은 2,048 rows,
    identity는 string당 1 MiB와 aggregate 16 MiB로 제한합니다. Protocol은 duplicate/mixed
    mode-result/order/identity와 unpaired UTF-16 surrogate escape를 fail-closed하며 replacement rune으로 identity를
    정규화하지 않습니다.
12. `--plan`은 SQL rendering, transaction-local physical schema/cardinality preflight나 실행 중 lock 유지가 아닙니다.
    같은 순간의 semantic dry/capability plan일 뿐 실제 실행 성공을 보장하지 않습니다.
13. Django app-zero reference의 비교 불가능(incomparable)한 reverse sibling은 exact `B1, A3, A2, A1` 순서이지만,
    existing DEV-0002 GoDj canonical plan은 `A3, A2, B1, A1`입니다. 두 plan은 같은 membership과 dependency-safe
    partial order를 만족합니다. Reference bytes는 Django 순서를 고정하고 product comparison은 MIG-122에만 bounded sparse
    deviation을 적용해 기존 GoDj deterministic order를 보존합니다.

## 결과

- 사용자는 apply와 reverse 전에 exact dependency plan을 볼 수 있습니다.
- Core preparation이 plan/execute 사이에서 하나로 통합되어 validation drift를 줄입니다.
- Public unknown app zero는 오타를 성공 no-op로 숨기지 않으면서 기존 library behavior는 보존합니다.
- Preview-to-execute race는 결과 차이로 관찰될 수 있지만 stale plan 실행으로 이어지지 않습니다.
- Backend schema와 `RevisionFencedBackend`/transaction interface는 바뀌지 않습니다.

## 의도적으로 결정하지 않은 것

- SQL text와 human-readable operation description
- App-only latest, prefix/ambiguous target, public CLI multiple target와 multiple database
- fake/repair/adoption/prune, squash/replacement/merge
- destructive writer/autodetector 확대와 interactive confirmation
- plan token/CAS, distributed lock 유지와 non-cooperative writer
- Q-019 retained-resource policy, general upgrade/semver
