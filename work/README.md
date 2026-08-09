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
| [GDJ-0019](0019-migration-definition-source-compatibility-contracts.md) | active | Migration definition source/versioned-loader 호환 계약 |

현재 활성 항목과 다음 ready 항목은
[docs/status/CURRENT.md](../docs/status/CURRENT.md)와 일치해야 합니다. 현재 active 항목은
[GDJ-0019](0019-migration-definition-source-compatibility-contracts.md) 하나이고 ready 항목은
없습니다. Baseline은 hosted evidence를 기록한 exact
`3269d662a8b403b5d73096c04abf9fa630b22974`입니다.

GDJ-0019는 explicit caller-provided strict data document, compatibility tuple `(1,1,1,2)`,
fully normalized IR v2, `CreateModel`/non-PK `char`·`boolean` `AddField` codec, atomic/deterministic load와 existing
`Executor.Migrate` reference handoff를 MIG-057..064로 잠그는 contract-only 작업입니다. 완료
목표는 기존 9 product set의 `92 passing + 5 deviation`을 보존하면서 8 `oracle_locked`를 더한
10 reference set/105 contract와 90 ordered cross-binding rejection입니다. Product loader와 GoDj
runner는 별도 GDJ-0020 전까지 만들지 않습니다.

## 운영 규칙

- 작업 시작 전에 baseline branch/commit과 dirty files를 기록합니다.
- 목표, 비목표, contract ID, 수정 허용 경로, 완료 조건이 없으면 구현을 시작하지 않습니다.
- 공개 API 또는 장기 결정이 바뀌면 ADR을 먼저 또는 같은 변경에서 갱신합니다.
- 체크리스트만 완료로 바꾸지 말고 실제 변경 파일과 evidence ID를 기록합니다.
- 중단 시 실패한 정확한 명령과 다음에 실행할 명령을 적습니다.
- completed 항목은 결과와 남은 제한을 보존합니다. 나중 상태로 덮어쓰지 않습니다.

새 항목은 [TEMPLATE.md](TEMPLATE.md)를 사용합니다.
