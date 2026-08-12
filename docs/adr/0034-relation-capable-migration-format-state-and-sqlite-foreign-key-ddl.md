# ADR-0034: Relation-capable Migration Format, State, and SQLite ForeignKey DDL

- 상태: Proposed
- 날짜: 2026-08-13
- 관련 work/contract:
  [GDJ-0035](../../work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md),
  MIG-075..MIG-086, Q-010, Q-012, Q-013
- 선행 결정:
  [ADR-0010](0010-m2-migration-state-and-executor-boundary.md),
  [ADR-0014](0014-migration-plan-execution-atomic-reverse.md),
  [ADR-0017](0017-revision-fenced-migration-lifecycle.md),
  [ADR-0019](0019-versioned-migration-definition-source.md),
  [ADR-0020](0020-migration-definition-loader-product-shape.md),
  [ADR-0024](0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)
- 대체하는 ADR: 없음

## 상태와 범위

이 ADR은 **Proposed**입니다. 아래 tuple, digest, state, backend capability와 SQLite DDL/remake shape는
GDJ-0035 Phase A/B에서 검증할 candidate이며 아직 Accepted, Implemented 또는 Verified가 아닙니다.
Activation baseline인 [EVID-083](../status/TEST_EVIDENCE.md#evid-20260812-083--gdj-0034-terminal-exact-head-ci-and-clean-baseline)은
이 문서가 생기기 전 GDJ-0034 terminal head만 증명합니다.

이번 후보는 existing scalar migration ABI를 보존하면서 AutoField-target `ForeignKey` definition과 historical
state를 SQLite lifecycle에 전달하는 단면만 다룹니다. Writer/autodetector, arbitrary schema rewrite,
non-SQLite backend와 broader relation API는 결정하지 않습니다.

## 맥락

Accepted ADR-0019/0020과 현재 제품은 strict legacy definition tuple
`(definition_format=1, loader_abi=1, operation_codec=1, schema_ir=2)`, legacy-only canonical digest v1,
scalar `StateFormatVersion=1`을 구현합니다. Accepted ADR-0024는 relation-capable Schema IR v3와 project binding을
도입했지만 migration tuple v1과 historical state v1은 relation arm을 명시적으로 거부합니다. 따라서 현재 relation
metadata/query/write/delete 제품은 migration definition에서 ForeignKey schema를 생성·추가·제거하거나 restart할 수
없습니다.

SQLite에서는 ForeignKey constraint가 connection-local `PRAGMA foreign_keys`와 table definition에 결속됩니다.
Column/FK removal은 일반적으로 table remake가 필요하고 user rows, `sqlite_sequence`, index/trigger/view와 inbound
constraint를 잘못 다루면 데이터 손실이나 false success가 생깁니다. GoDj의 application-level `PROTECT`/`SET_NULL`
정책을 physical `CASCADE`/`SET NULL`로 번역하면 이미 구현된 mutation semantics도 바뀝니다.

Relation codec, ProjectState, editor capability와 remake를 한 번에 암묵적으로 확장하면 old artifact를 새 의미로
재해석하거나 transaction 시작 뒤에야 dependency/target 오류를 발견할 수 있습니다. Format profile, state
promotion, full preflight, physical policy와 fault outcome을 독립 contract로 먼저 고정해야 합니다.

## 결정 기준

- Legacy tuple, canonical bytes/digest, scalar state와 existing lifecycle의 byte/behavior compatibility
- Relation definition의 lossless profile dispatch와 mixed batch의 deterministic digest
- Historical state가 current generated model이나 runtime registry에 의존하지 않는 단일 원본
- Target/AutoField/declared table·column/dependency 오류가 DB I/O 전에 전부 검출되는 pure atomic preflight
- SQLite exact connection의 FK enforcement와 application/physical delete-policy 분리
- Populated table, reverse/remake, restart와 precommit/commit fault의 데이터·recorder 일관성
- Unsupported schema object를 조용히 잃지 않는 fail-closed capability
- Existing migration transaction/revision fence와 structured cause 보존
- Reference artifact replay가 아닌 actual product observation으로 검증 가능

## 고려한 선택지

### Legacy tuple `(1,1,1,2)`를 relation까지 확장

파일 수와 dispatch는 단순하지만 같은 compatibility tuple이 scalar-only와 relation-bearing meaning을 동시에
가집니다. Old consumer가 relation을 drop하거나 old digest domain이 다른 semantics를 같은 profile로 표현할 수 있어
채택 후보에서 제외합니다.

### Project-wide bundle을 v2로 일괄 변환

한 profile과 digest를 만들기 쉽지만 이미 배포된 old definition을 rewrite하고 migration review/history를
대량 변경합니다. Mixed legacy/new project의 점진적 진화를 막으므로 제외합니다.

### SQLite physical action을 application policy와 동일하게 사용

`SET_NULL`을 `ON DELETE SET NULL`로 둘 수 있지만 GoDj의 explicit application mutation, cache publication과
protected payload 의미를 DB side effect로 우회합니다. `PROTECT`와 `SET_NULL` 모두 physical `NO ACTION`으로
constraint integrity만 맡기는 후보가 기존 제품 의미를 더 잘 보존합니다.

### 모든 schema object를 SQL text로 재작성하는 범용 table remake

다양한 기존 DB를 다룰 수 있지만 trigger/view/index quoting과 hidden/generated column, inbound FK까지 parser/writer
범위를 확대합니다. 이번 packet에서는 exact recognized table shape만 보존하고 그 밖은 capability error로 거부하는
bounded remake를 후보로 둡니다.

### Versioned relation profile, explicit state promotion과 additive relation editor

Old ABI를 보존하면서 relation 문서만 새 profile로 분리하고, mixed set digest가 각 document profile을 포함합니다.
State version과 optional backend capability를 명시하면 old consumer/editor가 relation을 silent-drop하지 않습니다.
이번 Proposed 방향입니다.

## Proposed candidate

### Definition profile와 per-document dispatch

두 exact profile만 허용하는 후보입니다.

```text
legacy:   (definition_format, loader_abi, operation_codec, schema_ir) = (1, 1, 1, 2)
relation: (definition_format, loader_abi, operation_codec, schema_ir) = (1, 2, 2, 3)
```

- `definition_format=1`의 strict JSON envelope/source limits/duplicate-key/framing 의미는 보존합니다.
- Loader ABI v2는 한 batch 안의 per-document exact profile dispatch와 mixed profile set을 소유합니다.
- Codec v2는 existing `create_model`/`add_field`의 scalar arms를 보존하고 IR v3 `foreign_key` field arm을
  losslessly encode/decode합니다.
- Tuple coordinate를 임의로 섞은 `(1,2,1,3)`, `(1,1,2,3)` 같은 hybrid는 negotiation 없이
  coordinate-specific incompatibility로 전체 batch publication 전에 거부합니다.
- Same `(app,name)` identity duplicate와 dependency graph validation은 profile과 무관하게 existing Planner의
  deterministic 의미로 한 번 수행합니다.
- Old document를 relation profile로 rewrite하지 않으며 producer version은 compatibility coordinate가 아닙니다.

### Digest domains와 mixed set

- Legacy-only set은 existing `godj:migration-definition-set:v1`, exact tuple object와 canonical bytes/digest를
  byte-for-byte 유지합니다.
- Relation document가 하나라도 있는 relation-only/mixed set은 새 v2 domain 후보를 사용합니다.
- V2 canonical definition item은 그 item을 decode한 exact compatibility profile을 포함합니다. 따라서 semantic
  operation bytes가 우연히 같아도 legacy/relation profile은 digest에서 구별됩니다.
- Definition order는 existing app/name UTF-8 byte order, dependency order는 canonical identity order,
  operation/field order는 semantic order를 보존합니다. Input source/order/whitespace/producer는 계속 제외합니다.
- Digest는 semantic fingerprint이지 signature, revision fence 또는 expected schema/history trust가 아닙니다.

Phase A 전에는 v2 domain literal, canonical example bytes 또는 digest hash를 확정값으로 기록하지 않습니다.

### Historical state version과 promotion

- Existing `StateFormatVersion=1`은 IR v2 scalar-only state로 계속 사용합니다.
- Candidate `RelationStateFormatVersion=2`는 IR v3 relation-bearing model/field state를 소유합니다.
- Promotion v1→v2는 모든 scalar semantics/order를 losslessly deep-copy합니다. Demotion v2→v1은 relation field가
  하나라도 있으면 structured state error로 거부하고 scalar-only v2에서만 lossless하게 허용합니다.
- New definition profile을 legacy state로 직접 replay하거나 generated current model에서 historical relation을
  보충하지 않습니다.
- Clone, operation before/after state, plan preflight와 reconstructor snapshot은 nested relation state를 alias하지
  않습니다.

### Pure zero-I/O relation preflight

Non-empty lifecycle plan의 첫 `BEGIN` 전에 전체 definition/state/plan에 대해 다음을 검증하는 후보입니다.

- source/target app·model과 declared physical table/column identity
- target model의 exact one AutoField primary key와 ForeignKey scalar compatibility
- relation cardinality, reverse namespace, `SET_NULL`의 nullable requirement
- target creator migration과 source creator/add migration 사이의 explicit dependency ancestry
- remove/unapply가 만드는 before/after state와 declared inbound relation absence
- backend relation capability shape

Target creator가 같은 migration 앞 operation이거나 dependency ancestry에 있어야 하며 filename/input order나 current
runtime binding으로 보완하지 않습니다. 이 pure preflight 실패는 pinned connection/session,
transaction/editor/recorder/revision I/O 0입니다. `sqlite_schema`, `foreign_key_list`, index/trigger/view와 inbound
physical FK를 읽어야 하는 remake eligibility는 여기에 포함하지 않습니다.

### Additive backend boundary

Existing scalar backend API를 breaking-change로 넓히지 않고 optional relation editor capability를 additive하게
검출하는 후보입니다. Core lifecycle은 relation operation이 있을 때만 capability를 요구합니다. Capability와 pure
state/graph transition 검증은 pinned connection/session을 열거나 transaction `BEGIN` 전에 완료해야 합니다.

Candidate operation surface는 relation-bearing CreateModel, nullable ForeignKey AddField와 their reverse/remove only입니다.
Populated table의 required FK AddField는 backfill/default가 없으므로 pre-DDL capability/state error입니다. Empty table
required AddField를 지원할지는 Phase B observation으로 결정하며 이 Proposed 문서는 지원을 약속하지 않습니다.

### SQLite connection and physical ForeignKey policy

- Relation intent로 고정한 exact physical connection에서 transaction `BEGIN` 전에 `PRAGMA foreign_keys=1`을
  확인합니다. 다른 pool connection의 결과나 DSN option만으로 success를 주장하지 않습니다.
- `PROTECT`와 `SET_NULL` relation 모두 physical DDL은 target PK를 참조하는
  `FOREIGN KEY (...) REFERENCES ... (...) ON DELETE NO ACTION` 후보입니다.
- Application delete product는 기존 explicit PROTECT/SET_NULL transaction을 계속 소유하고 physical FK는 orphan
  방지만 담당합니다.
- PRAGMA off, target/table mismatch 또는 unsupported editor는 DDL/recorder write 전에 structured error입니다.

### Bounded SQLite table remake

FK removal/unapply는 native column/FK alteration에 의존하지 않고 recognized table shape에만 bounded remake를
사용하는 후보입니다.

Exact pinned connection에서 `sqlite_schema`, `foreign_key_list`, index/trigger/view와 inbound FK를 읽는 physical
preflight는 DB I/O입니다. `BEGIN IMMEDIATE`가 writer boundary를 고정한 뒤 첫 DDL/row-copy/recorder write 전에
완료하며, pure zero-I/O preflight와 동일한 주장으로 합치지 않습니다. 실패 시 mutation은 0이고 transaction은
rollback해야 합니다.

```text
preflight sqlite schema + relation graph
→ create deterministic temporary table from target after-state
→ copy explicit retained columns in stable row order
→ replace table in the same migration transaction
→ restore and verify sqlite_sequence when applicable
→ verify foreign_key_check and recorder revision/write
→ commit once
```

- User rows, PK values, column values and AutoIncrement sequence를 보존합니다.
- Temporary names are deterministic/collision-checked and never escape a failed transaction.
- Inbound FK, user index, trigger, view, generated/hidden column, unsupported table option 또는 undeclared schema object가
  있으면 SQL text를 추측해 재생성하지 않고 pre-DDL capability error로 거부합니다.
- Remake와 recorder/revision update는 same transaction을 사용합니다. Foreign-key check failure도 commit 전
  rollback합니다.

### Failure, restart and commit outcome

- DDL, row copy, FK verification, recorder write와 revision fence fault는 original cause/rollback cause를 보존하고
  failed migration state를 publish하지 않습니다.
- Precommit failure는 failed migration transaction 전체를 rollback하며 앞선 migration별 durable commit은
  ADR-0014대로 남습니다.
- File-backed close/reopen 뒤 recorder/applied state와 loaded mixed definition set으로 동일 latest/target plan을
  재구성합니다.
- Commit outcome은 success, definite failure, unknown outcome 세 가지를 구분합니다. Unknown outcome은 durable
  result를 추측하거나 automatic retry하지 않고 existing unknown-outcome error boundary를 보존합니다.

## Planned contract matrix

| ID | Candidate decision/reference observation |
|---|---|
| MIG-075 | Existing legacy tuple, state v1, digest-v1 and lifecycle ABI preservation |
| MIG-076 | Exact relation profile plus coordinate mismatch/hybrid rejection |
| MIG-077 | Relation/mixed set canonical per-document profile and digest-v2 behavior |
| MIG-078 | State v1/v2 promote/demote, rejection and deep-copy behavior |
| MIG-079 | Target AutoField/table/reverse/creator-ancestry full preflight before I/O |
| MIG-080 | Relation CreateModel apply/unapply/reapply on SQLite |
| MIG-081 | Nullable FK AddField on populated table; required populated rejection |
| MIG-082 | Reverse/remake row and `sqlite_sequence` preservation |
| MIG-083 | Pinned exact-connection FK-on and physical `NO ACTION` |
| MIG-084 | File-backed restart/reconstruction after relation lifecycle |
| MIG-085 | Precommit DDL/recorder/revision fault rollback/cause behavior |
| MIG-086 | Commit success/definite failure/unknown outcome and no retry |

MIG-075..086은 계획 ID이며 activation에서는 manifest/oracle/static fixture, aggregate/status, bytes/hash 또는
test totals를 추가하지 않습니다.

## 결과와 비용

후보가 검증·Accepted되면 old migration artifact를 바꾸지 않고 relation-capable 문서를 점진적으로 추가할 수 있고,
historical relation meaning과 SQLite physical constraint가 explicit version/capability 경계에 놓입니다. Full preflight와
bounded remake는 silent data loss를 줄이지만 지원 가능한 existing SQLite schema shape를 좁힙니다. Optional editor,
state promotion과 mixed digest는 public compatibility surface와 test matrix를 늘립니다.

현재 Proposed 상태의 결과는 decision space와 검증 순서가 명시된 것뿐입니다. Code, artifact, product status,
Q status와 backend support는 바뀌지 않습니다.

## 의도적으로 결정하지 않은 것

- Writer/autodetector, migration generation, CLI upgrade/downgrade/repair/cache
- Data/custom/RunSQL operation, callback ABI, arbitrary default/backfill
- Self/cyclic/inbound FK, non-AutoField/`to_field`/composite PK, OneToOne/ManyToMany
- General index/trigger/view/generated-column preservation or database adoption
- Relation query/ORM/codegen/facade surface and Q-017 general upgrade policy
- Q-019 retained unknown-outcome connection cap/reconciliation
- PostgreSQL/MySQL/Windows와 non-SQLite DDL transaction/remake policy
- Q-010/Q-012/Q-013 closure or Draft PR merge

## 검증과 승인 조건

1. Phase A에서 pinned Django/SQLite MIG-075..086 independent artifacts/provenance를 만들고 existing artifact bytes를
   보존합니다.
2. Phase B no-product feasibility에서 tuple/mixed digest/state/preflight/SQLite remake와 fault candidate를 검증합니다.
3. Phase C에서 measured evidence에 맞춰 exact API/error/domain/state names를 freeze하고 독립 P0..P3 audit를 받습니다.
4. 그 decision head의 고유 exact-head hosted CI가 성공한 뒤에만 `상태: Accepted`로 바꿀 수 있습니다.
5. Implementation은 local normal/race/CGO-disabled/vet, exact SQLite/file restart/fault/no-rewrite/compile gates와 별도
   implementation/completion/terminal hosted heads를 통과해야 합니다.

Activation head는 자체 unique hosted CI가 `not run/pending`입니다. EVID-083/run `31613170021`을 activation,
decision 또는 implementation proof로 재사용하지 않습니다.
