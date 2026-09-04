# ADR-0034: Relation-capable Migration Format, State, and SQLite ForeignKey DDL

- 상태: Superseded by [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md)
- 날짜: 2026-08-13
- Phase C decision freeze: 2026-08-19
- Decision accepted: 2026-08-19
- Latest bounded implementation evidence: 2026-08-20
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

이 ADR의 decision 상태는 bounded relation-capable migration definition/state와 SQLite ForeignKey lifecycle
설계에 한해 **Accepted**입니다. Phase C test-only decision proof EVID-089/090과 그 경계를 기록한 Proposed
decision-freeze documentation head `5bdf013c8f0c1bba25c1c21c1c633cfe07be74ed`의 별도 local/hosted proof
EVID-091을 근거로 아래 bounded product design을 채택합니다. Accepted는 설계 상태이며 제품 구현, product
contract `passing`, 전체 relation backend support 또는 전체 경로의 **Verified**를 뜻하지 않습니다.

- Exact test-only decision-proof head
  `7d36502f104daa62b39744b5705478acc19a7ead`, tree
  `d9e8a6b7bec59828ba0bd2b1864cbba3d9f9396d`는 exact 8개
  `conformance/migrationrelation/*_test.go`만 바꿨습니다.
- [EVID-089](../status/TEST_EVIDENCE.md#evid-20260819-089--gdj-0035-phase-c-test-only-decision-proof-local-validation)은
  그 final bytes의 local decision proof를 기록합니다.
- [EVID-090](../status/TEST_EVIDENCE.md#evid-20260819-090--gdj-0035-phase-c-test-only-decision-proof-exact-head-hosted-ci) /
  [run 32174259324](https://github.com/progresshans/godj/actions/runs/32174259324)는 그 exact head의 고유
  26/26 jobs·342/342 steps와 independent audit P0/P1/P2/P3=`0/0/0/0`을 기록합니다.
- [EVID-091](../status/TEST_EVIDENCE.md#evid-20260819-091--gdj-0035-proposed-decision-freeze-documentation-head-local-validation-and-exact-head-hosted-ci) /
  [run 32183309328](https://github.com/progresshans/godj/actions/runs/32183309328)는 exact Proposed
  decision-freeze documentation head `5bdf013c8f0c1bba25c1c21c1c633cfe07be74ed`의 local final-byte gates와
  고유 26/26 jobs·342/342 steps, independent audit P0/P1/P2/P3=`0/0/0/0`을 기록합니다.
- [EVID-092](../status/TEST_EVIDENCE.md#evid-20260819-092--gdj-0035-adr-0034-acceptance-documentation-head-exact-head-hosted-ci) /
  [run 32187094845](https://github.com/progresshans/godj/actions/runs/32187094845)는 별도 Accepted transition head
  `7cdc6d613f605583c017c92a92040a90c1b56ed6`의 고유 26/26 jobs·342/342 steps와 independent audit
  P0/P1/P2/P3=`0/0/0/0`을 기록합니다. EVID-091을 acceptance proof로 재사용하지 않았습니다.
- [EVID-093](../status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)은
  Phase D1 definition/handoff, D2 private historical state/readiness, D3a direct optional SQLite Create/Delete port를
  서로 다른 product/correction head와 hosted run `32195313382`, `32205324145`, `32218003207`에서
  각각 검증한 결과를 기록합니다. 이 증거는 core `Load`→`Set.Migrate` relation 실행을
  지원한다고 확대해석하지 않습니다.
- [EVID-094](../status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification)은
  Phase D3b product `74c2b7241aca3448f999d84e625fc9233434d977`와 inventory correction
  `167ef0335fcdbcafadecaacf301e6a33671d2ee3`를 별도 local/hosted proof로 검증합니다. Normal loaded
  relation-bearing CreateModel apply/unapply/reapply와 actual-plan preflight만 증명하며 D4 file restart,
  Add/Remove/remake나 MIG status 전환을 증명하지 않습니다.
- [EVID-095](../status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification)은
  exact one-test-file head `424ec4d80684c07e8d961d858909e394ac8de9a9`에서 existing product path의
  bounded file-backed restart scenario를 검증합니다. Product source/API/workflow, capability와 MIG status는
  바뀌지 않았고 raw database-file byte equality, `sqlite_sequence`, general restart와 actual adapter를
  증명하지 않습니다.
- EVID-096 exact-six documentation head `62df9b2ca3bb397ec826d07b2840408544231845`는 unique
  [run 32260744096](https://github.com/progresshans/godj/actions/runs/32260744096)의 exact
  26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했습니다.
- [EVID-097](../status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification)은
  D4d product `3950d98f10544ed18821c1af7960eb1696384eb4`, inventory lock
  `28b141e023d5e851e25e6560fc21a463982bf1be`와 deterministic resource-scan fix
  `dd8336296afec1c05f739817c7ab77bdb63a2535`를 기록합니다. First hosted run `32267789056`의 P1을
  보존하고 distinct [run 32271361724](https://github.com/progresshans/godj/actions/runs/32271361724)의 exact
  26/26·342/342와 audit P0..P3=0으로 fixed head를 검증합니다.
- EVID-097 documentation head `c59669c6fd436b243e96eaf72256535454b705ed`는 unique
  [run 32278555810](https://github.com/progresshans/godj/actions/runs/32278555810)의 exact
  26/26·342/342와 audit P0..P3=0에서 별도로 닫혔습니다.
- [EVID-098](../status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification)은
  D4e product `7c07805918dd680bfd5f85440d71aa14825972b6`와 inventory lock
  `1d86f6e921ec57403980423b83efc17a248a3864`를 기록합니다. Unique
  [run 32282269755](https://github.com/progresshans/godj/actions/runs/32282269755)의 exact 26/26·342/342와
  audit P0..P3=0으로 final head를 검증합니다.
- EVID-098 documentation head `85f92704ded6b9d6bd7da32b3fcff12fe747f74b`는 unique CI #94
  [run 32288383027](https://github.com/progresshans/godj/actions/runs/32288383027)의 exact 26/26·342/342와
  audit P0..P3=0에서 별도로 닫혔습니다.
- [EVID-099](../status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification)은
  D4f product `4982e27437b575cf202b55e7ce8c01fd56a94c9c`와 inventory lock
  `9d5b894643f3394974c91a1127534b219840e0a1`을 기록합니다. Unique CI #95
  [run 32294983953](https://github.com/progresshans/godj/actions/runs/32294983953)의 exact 26/26·342/342와
  audit P0..P3=0으로 final head를 검증합니다.

이번 결정은 existing scalar migration ABI를 보존하면서 AutoField-target `ForeignKey` definition과 historical
state를 기존 revision-fenced SQLite lifecycle로 전달하는 단면만 다룹니다. Writer/autodetector, arbitrary schema
rewrite, non-SQLite backend와 broader relation API는 결정하지 않습니다.

## 맥락

Accepted ADR-0019/0020과 Phase D1 이후의 현재 제품은 strict legacy definition tuple
`(definition_format=1, loader_abi=1, operation_codec=1, schema_ir=2)`, legacy-only canonical digest v1,
scalar `StateFormatVersion=1`을 보존하면서 relation tuple `(1,2,2,3)`, digest v2와 private loaded
authority를 구현합니다. Phase D2는 `RelationStateFormatVersion=2`와 private reconstructor/readiness를
구현했고 Phase D3a는 optional backend API와 SQLite direct Create/Delete port를 구현했습니다. Phase D3b는
core `Executor.Migrate`가 exact applied history로 actual plan을 만들고 whole-plan dry validation 뒤 relation
capability를 선택하도록 연결했습니다. Normal loaded relation-bearing CreateModel은 SQLite에서
apply/unapply/reapply할 수 있습니다. D4는 file close/reopen마다 fresh backend와 mixed loaded set을 만들고
captured schema/rows/history/token/FK snapshot으로 bounded restart를 검증했습니다. D4d/D4e는 아래 Accepted
shape 안에서 target-bearing nullable Add와 empty-source required Add를 구현했고 D4f는 bounded reverse/remove
table remake를 구현했습니다. Arbitrary/general remake, general restart와 actual MIG adapter는 아직 미지원입니다.

SQLite ForeignKey constraint는 connection-local `PRAGMA foreign_keys`와 table definition에 결속됩니다.
Column/FK removal은 table remake가 필요할 수 있고 user rows, `sqlite_sequence`, index/trigger/view와 inbound
constraint를 잘못 다루면 데이터 손실이나 false success가 생깁니다. GoDj의 application-level
`PROTECT`/`SET_NULL` 정책을 physical `CASCADE`/`SET NULL`로 번역하면 이미 구현된 mutation 의미도 바뀝니다.

Relation codec, historical state, editor capability와 remake를 암묵적으로 한 번에 넓히면 old artifact를 새
의미로 재해석하거나 transaction 시작 뒤에야 dependency/target 오류를 발견할 수 있습니다. Format profile,
whole-step state transition, three-stage preflight, exact existing-fence backend port와 fault outcome을 함께
명시해야 합니다.

## 결정 기준

- Legacy tuple, canonical bytes/digest, scalar state와 existing lifecycle의 byte/behavior compatibility
- 한 public loader에서 relation definition을 lossless하게 dispatch하고 mixed batch를 한 graph로 계획
- Historical state가 current generated model이나 runtime registry에 의존하지 않는 단일 원본
- Wire relation declaration이 target key field를 위조하지 못하고 historical exact AutoField를 사용
- Static, history/plan, SQLite physical preflight의 I/O와 mutation 경계를 구분
- Existing revision-fenced session/transaction/history owner를 relation 경로에서도 재사용
- SQLite exact connection의 FK enforcement와 application/physical delete-policy 분리
- Populated nullable/empty required AddField, reverse/remake, restart와 fault outcome의 명시적 경계
- Unsupported schema object를 조용히 잃지 않는 fail-closed capability
- Reference artifact replay가 아닌 actual product observation으로 후속 구현을 검증 가능

## 고려한 선택지

### Legacy tuple `(1,1,1,2)`를 relation까지 확장

같은 compatibility tuple이 scalar-only와 relation-bearing meaning을 동시에 갖게 됩니다. Old consumer가
relation을 drop하거나 old digest domain이 다른 semantics를 같은 profile로 표현할 수 있어 제외합니다.

### Project-wide bundle을 v2로 일괄 변환

이미 배포된 old definition을 rewrite하고 migration review/history를 대량 변경하며 mixed legacy/new project의
점진적 진화를 막으므로 제외합니다.

### SQLite physical action을 application policy와 동일하게 사용

`SET_NULL`을 `ON DELETE SET NULL`로 두면 GoDj의 explicit application mutation과 cache publication을 DB side
effect로 우회합니다. `PROTECT`와 `SET_NULL` 모두 physical `NO ACTION`으로 constraint integrity만 맡깁니다.

### 모든 schema object를 SQL text로 재작성하는 범용 table remake

Trigger/view/index quoting, hidden/generated column과 inbound FK까지 parser/writer 범위를 확대합니다. 이번
packet에서는 exact recognized shape만 보존하고 그 밖은 capability error로 거부합니다.

### Versioned profile, whole-step state transition과 additive existing-fence relation port

Old ABI를 보존하면서 relation document만 새 profile로 분리하고, 한 loader/Planner와 기존 fenced lifecycle에
합류시킵니다. Phase C proof와 Proposed decision-freeze head의 별도 hosted proof를 거쳐 채택한 방향입니다.

## Accepted decision

### Definition profile, one loader and one planner

정의 파일이 선언할 수 있는 exact profile은 다음 두 개입니다.

```text
legacy:   (definition_format, loader_abi, operation_codec, schema_ir) = (1, 1, 1, 2)
relation: (definition_format, loader_abi, operation_codec, schema_ir) = (1, 2, 2, 3)
```

- Existing `migrations/definition`의 `DefinitionFormatVersion=1`, `LoaderABIVersion=1`,
  `OperationCodecVersion=1`, `SchemaIRVersion=2` constant/constructor는 그대로 보존합니다. 같은 package가
  relation profile의 additive exact public constants `RelationLoaderABIVersion = 2`,
  `RelationOperationCodecVersion = 2`, `RelationSchemaIRVersion = 3`을 소유합니다. 세 relation constant의
  declared type은 existing sibling과 동일한 exact `int64`입니다.
- Public entrypoint는 existing `migrations/definition.Load(...Source)` 하나입니다. 별도 relation loader나
  caller-selected decoder를 추가하지 않습니다.
- `definition_format=1` strict JSON envelope, source/resource limits, duplicate-key와 framing 의미를 보존합니다.
- Loader ABI v2가 각 document의 exact tuple을 읽고 그 tuple에 맞는 decoder로 한 번 dispatch합니다. Hybrid
  `(1,2,1,3)`, `(1,1,2,3)`와 unknown coordinate는 전체 publication 전에 coordinate-owned incompatibility로
  실패합니다.
- Codec v2는 existing `create_model`/`add_field` scalar arms를 보존하고 IR v3 `foreign_key` arm을 losslessly
  encode/decode합니다. Legacy document를 relation profile로 rewrite하지 않습니다.
- 모든 document가 decode된 뒤 duplicate identity와 dependency graph는 actual existing `migrations.Planner`
  하나가 combined set 전체에 대해 검증합니다. Profile별 graph나 두 번째 planner를 만들지 않습니다.

### Loader-owned module-private handoff

현재 `definition.Set.Migrate`가 fresh `[]migrations.Migration`만 existing `Executor.Migrate`에 전달하면 loader가
검증한 per-document profile/provenance/digest와 definition pairing을 relation state/lifecycle까지 보존할 수
없습니다. Public API를 넓히지 않고 다음 internal bridge로 이 경계를 닫습니다.

- Implemented `migrations/internal/definitionhandoff.Handoff`는 Go `internal` import boundary 안의 module-private
  immutable carrier입니다. Internal package의 exported identifier는 repository module 내부 연결 이름일 뿐
  consumer API가 아닙니다.
- `definition.Load`는 모든 tuple/decode와 combined actual Planner 검증이 성공한 뒤 exact per-migration profile,
  cloned `SourceID`/`Producer`, canonical definition seal, set v1/v2 digest와 sorted full-graph seal을 생성합니다.
  Carrier는 raw document bytes와 caller-owned slice/map/pointer alias를 보존하지 않습니다.
- `definition.Set`은 unexported handoff field를 소유합니다. Relation-only/mixed set만 nonzero carrier를 가지며
  legacy-only/empty set은 zero를 유지합니다. Existing public `Load`, `Set`, `Definitions`, `Digest`, `Sources`,
  `Set.Migrate`, `Executor.Migrate` signature와 public entrypoint 수는 바뀌지 않습니다.
- Relation-only/mixed `Set.Migrate`는 호출마다 carrier를 fresh clone하고, context가 nonnil일 때만
  `definitionhandoff.WithContext`의 typed unexported key로 붙인 뒤 existing `Executor.Migrate`를 정확히 한 번
  호출합니다. Nil context는 attach 과정에서 panic하지 않고 기존 Executor context error precedence로 그대로
  전달합니다.
- `Executor.Migrate`는 기존 nil/cancellation/deadline/value와 request validation precedence 뒤,
  backend/session open/history/DB I/O 전에 caller-visible definition clone과 carrier의 exact
  profile/provenance/set digest/canonical definition/full-graph seal을 동기 검증합니다. Carrier/context value는
  호출 밖으로 retain하지 않습니다.
- 이 검증 경계만 private loaded-state reconstructor와 prepared lifecycle handoff를 mint할 수 있습니다.
  `Set.Definitions()`가 반환한 copy나 raw caller definitions는 같은 authority를 만들 수 없습니다.

Phase C test-only head는 candidate-local seals로 behavior를 검증했을 뿐이고 later internal bridge를
구현·검증하지 않았습니다. Phase D1이 이 경계를 제품에 구현했고 D3b는 검증된 carrier/readiness를 actual
history/plan 경계와 새 public API 없이 연결했습니다.

### Digest domains

- Empty set과 legacy-only set은 existing `godj:migration-definition-set:v1`, exact tuple object와 canonical
  bytes/digest를 byte-for-byte 유지합니다.
- Relation document가 하나라도 있는 relation-only/mixed set은
  `godj:migration-definition-set:v2` domain을 사용합니다.
- V2 canonical definition item은 그 document의 exact compatibility profile과 normalized semantic definition을
  포함합니다. Definition은 app/name byte order, dependency는 canonical identity order, operation/field는
  semantic order를 보존합니다.
- Source order, whitespace, `SourceID`와 producer는 semantic digest에서 제외합니다. 진단 provenance는
  digest와 별도 snapshot/seal로 보존합니다.
- Digest는 semantic fingerprint이며 signature, revision fence, expected schema/history trust가 아닙니다.

Proof의 golden bytes, digest hash와 private canonical catalog 이름은 회귀 검출 자료이지 product API나
장기 결정의 일부가 아닙니다.

### Wire relation and whole-step historical state

- Existing `migrations.StateFormatVersion=1` constant/constructors는 IR v2 scalar-only state로 유지합니다.
  같은 `migrations` package의 relation-bearing step 내부 state는 additive exact public constant
  `RelationStateFormatVersion = 2`와 IR v3 normalized model graph를 사용합니다.
  `RelationStateFormatVersion`은 existing `StateFormatVersion`와 같은 untyped integer constant입니다.
  Promotion/demotion helpers는 unexported입니다.
- Relation wire arm은 symbolic target app/model, cardinality, reverse와 `on_delete`만 소유합니다.
  `target_field`를 wire에 추가하지 않습니다.
- Preflight는 해당 operation 시점에 보이는 historical target model에서 exact one non-nullable AutoField primary
  key를 찾아 backend intent의 target key snapshot을 파생합니다. Missing/multiple/non-AutoField/nullable target
  key는 I/O 전에 실패합니다. Current generated model이나 runtime binding으로 보충하지 않습니다.
- Scalar v1→relation v2 promotion은 whole migration step 진입 경계에서 lossless deep-copy로 한 번 수행합니다.
  Relation arm이 마지막으로 제거된 step의 완료 경계에서만 scalar-only v2→v1 demotion을 허용합니다. Operation
  중간마다 공개 state format을 왕복하거나 caller가 promotion/demotion을 직접 선택하지 않습니다.
- Demotion 시 relation arm이 하나라도 남아 있으면 structured state error로 실패합니다. Before/after state,
  reconstructor snapshot과 backend intent는 nested relation state를 alias하지 않습니다.
- Relation 여부와 public operation kind는 sealed `Before`→`After` exact model delta에서 파생합니다. Caller가
  별도 `RelationArm`, profile boolean 또는 operation-kind selector로 같은 step을 재분류할 수 없습니다.

Phase D2는 validated `definitionhandoff.Handoff` 경계에서만 private loaded-state reconstructor/readiness를
mint하고 whole-step promotion/demotion과 target derivation을 구현했습니다. Public `NewStateReconstructor`에 raw
relation definitions를 주는 기존 `CategoryState`/`CodeInvalidState` 경계는 보존됩니다. Phase C test helper의
state types, constructors, seals, hashes, catalogs와 error-detail strings는 여전히 noncanonical입니다.

### Three-stage preflight

Relation step은 다음 세 단계를 순서대로 통과해야 합니다.

1. **Static zero-I/O preflight**: request/context, caller-owned source/resource bounds, carrier/profile/provenance/digest/
   definition/full-graph seal, graph chronology, creator ancestry와 loaded-state readiness를 backend/session call 전에
   검증하고 deep-copy합니다. 이 단계는 actual applied history로만 알 수 있는 plan을 추측하거나
   relation capability를 먼저 조회하지 않습니다. 실패 시 backend capability, session open, history read,
   transaction과 DB I/O는 모두 0입니다.
2. **Exact-one fenced history 및 actual-plan preflight**: static readiness 성공 뒤 existing revision-fenced
   session을 한 번 열고 applied-history snapshot을 정확히 한 번 읽습니다. 그 snapshot으로 actual
   `NewAppliedState` → `NewPlanner` → `CheckHistory` → `Plan`을 수행하고, 생성된 actual plan 전체를
   dry-validate해 every step의 historical state, direction, operation chronology, target derivation과 scalar/relation
   구성을 mutation 전에 고정합니다. Forward/reverse는 같은 cloned definition set을 재사용하고
   parent-first apply/child-first unapply를 actual Planner가 소유합니다. Relation capability는 이 exact
   history/actual-plan 성공 후 relation-bearing actual plan에 대해서만 조회하며 every
   `BeginFencedMigration`/`BeginRelationFencedMigration`과
   mutation 전에 필요 capability를 모두 검증합니다. Scalar-only와 no-op actual plan은 relation capability/
   relation begin call이 정확히 0입니다. Relation step이 하나라도 unsupported면 scalar prefix를 begin/commit하지
   않고 전체 request를 fail-closed합니다.
3. **SQLite physical preflight**: relation transaction의 exact pinned connection에서 `BEGIN IMMEDIATE` 뒤 첫
   claim/mutation 전에 `sqlite_schema`, `foreign_key_list`, row cardinality, index/trigger/view, generated/hidden
   column, table option, inbound FK와 temporary-name collision을 검사합니다. 실패 시 DDL/row copy/recorder/
   successor-revision mutation은 0이고 같은 transaction을 rollback합니다.

Static preflight와 physical preflight를 모두 “zero I/O”라고 합치지 않습니다. Scalar-only/no-op plan도
actual plan을 알기 위한 existing fenced history stage는 사용할 수 있지만 relation capability/port call은 0입니다.

### Additive exact existing-fence backend API

Existing scalar `migrations/backend.RevisionFencedBackend`, `RevisionFencedSession`과
`RevisionFencedTransaction`을 breaking change로 넓히지 않습니다. 다음 exported relation port/capability/intent/
operation/target/kind type은 모두 additive하게 `migrations/backend` package의 planned
`migrations/backend/relation.go`가 소유하고 그 package의 existing fenced interface를 embed/reuse합니다. Relation
operation이 있을 때만 이 optional extension을 요구합니다.

```go
type RelationMigrationCapabilities struct {
    CreateModelForeignKeys            bool
    AddNullableForeignKey             bool
    AddRequiredForeignKeyToEmptyTable bool
    RemoveForeignKeyByTableRemake     bool
}

type RelationRevisionFencedBackend interface {
    RevisionFencedBackend
    RelationMigrationCapabilities() RelationMigrationCapabilities
}

type RelationRevisionFencedSession interface {
    RevisionFencedSession
    BeginRelationFencedMigration(
        context.Context,
        HistoryTransition,
        RelationMigrationIntent,
    ) (RevisionFencedTransaction, error)
}

type RelationMigrationOperationKind uint8

const (
    RelationMigrationCreateModel RelationMigrationOperationKind = iota + 1
    RelationMigrationDeleteModel
    RelationMigrationAddField
    RelationMigrationRemoveField
)

type RelationMigrationIntent struct {
    Operations []RelationMigrationOperation
}

type RelationMigrationOperation struct {
    OperationIndex int
    Kind           RelationMigrationOperationKind
    Before         ir.Model
    After          ir.Model
    Targets        []RelationMigrationTarget
}

type RelationMigrationTarget struct {
    SourceField ir.Field
    TargetModel ir.Model
    TargetKey   ir.Field
}
```

- `RelationMigrationOperationKind`의 CreateModel/DeleteModel/AddField/RemoveField 값은 exact `1..4`입니다.
- `RelationMigrationIntent`는 normalized operation sequence만 소유합니다. App/name/direction은 기존
  `HistoryTransition`만 소유하고 relation intent/operation/target은 그 identity를 중복하지 않습니다.
- Relation target이 하나라도 있는 step의 `Operations`는 scalar와 relation operation을 모두 포함한
  complete execution-order sequence입니다. Forward는 source의 zero-based `OperationIndex` `0..n-1`
  순서이고 unapply는 original index를 보존한 채 `n-1..0`으로 역순합니다. Scalar-only step은 existing
  scalar lifecycle로 남고 relation intent/port로 들어가지 않으며, relation intent는 최소 하나의
  relation target을 가져야 합니다. Operation 누락, duplicate index, reorder 또는 같은 model identity의
  이전 `After`와 다음 `Before` 사이 discontinuity는 exact-one fenced history 뒤 actual plan 전체
  dry validation에서, relation capability와 every `BeginFencedMigration`/`BeginRelationFencedMigration` 및
  mutation 전에 거부합니다.
- CreateModel은 `Before`=exact zero `ir.Model`, `After`=full normalized historical model입니다. DeleteModel은
  그 역이고, AddField/RemoveField는 같은 model identity의 full normalized `Before`/`After`로 Add가 exact
  one field를 append하거나 Remove가 exact one field를 제거한 delta를 나타내며 나머지 field의 값과 order를
  보존합니다.
- `Targets`는 operation이 실제로 소유한 relation field의 exact count/order입니다. Create/DeleteModel은
  해당 full model의 relation field order, relation Add/RemoveField는 exact changed field 하나, scalar delta는 zero
  target입니다. 각 target은 exact `SourceField` snapshot, operation-time historical normalized `TargetModel`,
  그 model field의 exact member이자 unique non-nullable AutoField primary key인 `TargetKey`를 소유합니다.
  Operation/model/field/target snapshot과 nested relation은 모두 deep clone이며 caller alias를 보존하지 않습니다.
- `BeginRelationFencedMigration`은 별도 relation transaction을 만들지 않고 existing
  `RevisionFencedTransaction`을 반환합니다. Relation/scalar DDL, recorder, successor revision,
  `CommitFenced`와 `Rollback`은 같은 transaction identity를 사용합니다.
- Relation backend가 없거나 네 capability 중 actual plan에 필요한 항목이 false이면 legacy begin으로
  fallback하지 않습니다. Capability selection은 exact-one fenced history/actual-plan dry validation 후,
  every begin/mutation 전에 structured capability error로 실패합니다.
- 위 exported interface/type/method/capability field 이름과 역할만 Accepted public decision surface입니다.
  Test-only adapters, opaque seals, private catalogs, helper constructors와 proof hashes는 noncanonical입니다.

### SQLite order and physical policy

Relation step의 exact order는 다음과 같습니다.

```text
static request/resource/carrier/profile/digest/graph/chronology/readiness preflight
→ exact-one existing fenced history + actual Planner + whole-plan dry validation
→ relation capability selection when and only when the actual plan needs it
→ exact physical connection에서 PRAGMA foreign_keys=1 확인
→ BEGIN IMMEDIATE
→ physical schema/cardinality preflight
→ expected epoch/revision/history fence claim
→ relation/scalar DDL 또는 bounded remake
→ foreign_key_check
→ exact-one recorder transition + successor revision
→ CommitFenced exactly once
```

- `PRAGMA foreign_keys=1`은 transaction 전에 transaction이 사용할 exact physical connection에서 확인합니다.
  Pool의 다른 connection 결과나 DSN option만으로 success를 주장하지 않습니다.
- `PROTECT`와 `SET_NULL` 모두 target AutoField PK를 참조하는 physical DDL은
  `FOREIGN KEY (...) REFERENCES ... (...) ON DELETE NO ACTION`입니다. Application-level delete product가
  mutation policy를 계속 소유하고 physical FK는 orphan 방지만 담당합니다.
- First write인 revision/history claim 뒤 DDL, row copy, FK check, recorder와 successor revision은 하나의
  existing fenced transaction에서 수행합니다. Recorder나 relation DDL을 위한 두 번째 transaction/session/
  connection을 열지 않습니다.

### Supported operation shape

- Relation-bearing `CreateModel`은 `CreateModelForeignKeys` capability에서 지원합니다.
- Nullable ForeignKey `AddField`는 empty/populated table 모두 `AddNullableForeignKey` capability에서 지원합니다.
- Required ForeignKey `AddField`는 table이 empty이고
  `AddRequiredForeignKeyToEmptyTable` capability가 있을 때만 지원합니다.
- Populated table의 required ForeignKey AddField는 backfill/default 정책이 없으므로 physical mutation 전에
  거부합니다.
- ForeignKey remove/reverse/unapply는 `RemoveForeignKeyByTableRemake` capability와 bounded remake eligibility를
  모두 통과해야 합니다. Parent-first apply, child-first unapply, 마지막 relation 제거 후 demotion을 유지합니다.

Phase D3a 당시 SQLite capability는 exact `{true,false,false,false}`였습니다. D4d 이후 tuple은
`{CreateModelForeignKeys:true, AddNullableForeignKey:true,
AddRequiredForeignKeyToEmptyTable:false, RemoveForeignKeyByTableRemake:false}`였고 D4e 이후 current tuple은
`{CreateModelForeignKeys:true, AddNullableForeignKey:true,
AddRequiredForeignKeyToEmptyTable:true, RemoveForeignKeyByTableRemake:false}`였으며 D4f 이후 current tuple은
`{CreateModelForeignKeys:true, AddNullableForeignKey:true,
AddRequiredForeignKeyToEmptyTable:true, RemoveForeignKeyByTableRemake:true}`입니다. Complete relation intent
내의 zero-target scalar Add/Remove는 같은 transaction에서 실행할 수 있지만 그 것을 relation Add/Remove
capability로 세지 않습니다.

D4d/D4e relation Add 구현은 Accepted public `Targets` 의미를 바꾸지 않고 다음 bounded shape로 닫습니다.

- Forward exact append `AddField`이고 added field는 no-default, non-primary-key `ForeignKey`여야 합니다. Nullable은
  기존 D4d 범위이며 required는 `Nullable=false`와 `PROTECT`만 허용합니다.
- Public Add operation의 `Targets`는 exact changed field 하나입니다. Core dry pass와 execution rematerialization은
  pre-existing source relations가 모두 그 field와 exact same symbolic `(app, model)` target을 선언하는지,
  sealed changed `TargetModel`이 relation-free이고 unique non-null AutoField PK를 갖는지 재검증합니다.
- SQLite는 그 one private immutable target snapshot을 existing source relation field order로만 확장합니다.
  Physical catalog/current runtime registry에서 missing historical authority를 추론하지 않습니다.
- 한 migration step에서 source model당 nullable/required relation Add를 합쳐 하나만 허용합니다. Loaded core의 sealed
  authority/resource closure는 capability lookup과 Begin 전이고, missing capability는 capability selection에서
  pre-Begin 실패합니다. SQLite의 independent static seal은 remaining invalid/direct shapes를 새 pinned relation
  connection 획득과 SQL `BEGIN` 전에 거부합니다. Physical target-outgoing cycle과 pre-existing catalog drift는
  `BEGIN IMMEDIATE` 뒤 physical preflight에서 검사하지만 revision claim과 mutation 전이며, failure는 transaction을
  rollback합니다.
- SQLite native SQL의 exact exemplar는
  `ALTER TABLE "main"."source" ADD COLUMN "editor_id" INTEGER NULL REFERENCES "target" ("id") ON DELETE NO ACTION`입니다.
  Required exemplar는
  `ALTER TABLE "main"."source" ADD COLUMN "reviewer_id" INTEGER NOT NULL REFERENCES "target" ("id") ON DELETE NO ACTION`입니다.
  Post-ALTER table SQL은 GoDj emitted table-level constraints와 SQLite native
  inline clause가 섞인 exact one-pass canonical grammar로 검증하며 case/whitespace/action/order/trailing drift를
  허용하지 않습니다.
- D4d nullable Add는 empty/populated source에서 existing rows와 sequence를 보존하고 new column은 NULL입니다. Final canonical,
  `foreign_key_check`와 recorder fault는 same transaction rollback, sticky failure/no automatic retry와 reopened
  structured snapshot 불변을 유지합니다.
- Required Add는 existing source가 empty일 때만 성공합니다. Exact pinned connection에서
  `PRAGMA foreign_keys=1`과 `BEGIN IMMEDIATE` 뒤, revision/history claim 전에 `SELECT EXISTS`로 emptiness를
  확인합니다. Same-intent earlier `CreateModel` source는 statically empty라 query하지 않습니다. Populated source는
  `sqlite_relation_migration` capability error, `NoOperation`, rollback 1, claim/DDL/recorder 0으로 닫힙니다.
- Required native column은 `pragma_table_xinfo.notnull=1`이고 valid target insert만 성공하며 NULL/orphan insert는
  실패합니다. Target rows/sequence는 source emptiness와 무관하게 보존됩니다.

D4f Remove authority는 public `Targets` 의미를 바꾸지 않고 다음 bounded shape로 닫습니다.

- Backward/unapply가 original exact appended no-default, non-PK ForeignKey를 제거하는 경우만 허용합니다.
  Nullable field는 `PROTECT` 또는 `SET_NULL`, required field는 `PROTECT`여야 합니다. Frozen D4f direct
  E2E fixture는 nullable `PROTECT`와 required `PROTECT`만 검증했으며 dedicated nullable `SET_NULL` D4f
  E2E proof를 주장하지 않습니다.
- Public target은 changed field exact 하나입니다. Core dry pass와 execution rematerialization은 relation-bearing
  boundary의 모든 source relation이 exact same symbolic target을 선언하고 changed sealed target이 relation-free
  exact one non-null AutoField PK를 갖는지 검증합니다. Source model당 relation Add/Remove mutation은 step에서
  합쳐 하나입니다. Missing `RemoveForeignKeyByTableRemake`는 every Begin 전 실패합니다.
- SQLite는 sealed operation들 중 exact relation SourceField order 또는 valid one-field Add/Remove prefix와 일치하는
  unique candidate만 사용합니다. Runtime catalog/current registry에서 authority를 추론하지 않습니다.
- Populated required source의 Remove는 지원하지만, 그 뒤 forward required reapply는 existing nonempty-source
  rule로 fail-closed합니다. Populated required Add 자체는 계속 미지원입니다.

### Bounded SQLite table remake

FK removal/unapply는 native column/FK alteration을 가정하지 않고 exact recognized table shape만 remake합니다.

```text
after-state에서 deterministic temporary table 생성
→ retained column을 명시적 stable order로 copy
→ same transaction에서 table 교체
→ sqlite_sequence 복원·검증
→ foreign_key_check
→ recorder/successor revision
→ commit once
```

- User rows, PK values, retained column values와 exact AutoIncrement `sqlite_sequence` spelling/value를 보존합니다.
- Temporary name은 `__godj_relation_`와 versioned unsigned-64 big-endian length-framed transition tuple SHA-256의
  first 16-byte lowercase hex로 deterministic하게 만들고 namespace 전체에서 case-insensitive collision을
  검사합니다. Failed transaction 밖으로 남지 않습니다.
- Exact pinned connection에서 `BEGIN IMMEDIATE` 뒤 revision claim 전에 remake source를 참조하는
  inbound FK, remake source의 non-PK user index, touched/control table을 소유하거나 참조하는
  trigger/view, relevant table의 generated/hidden column이나 unsupported option, namespace/temp/control
  collision, invalid/case-variant/noninteger/negative sequence를 structured capability error로 거부합니다.
  Unrelated harmless schema object는 허용하며 SQL text를 추측해 재생성하지 않습니다.
- Claim 뒤 source count, deterministic temp create, retained columns의 PK-order copy, `RowsAffected`와 stored count,
  source drop/temp rename, sequence clear/restore/verify, final canonical schema/FKs와 `foreign_key_check`, recorder/
  successor revision을 같은 existing fenced transaction에서 실행한 뒤 exact one commit합니다.

### Failure, commit outcome and restart

- DDL, row copy, FK verification, recorder write와 revision fence fault는 original cause와 rollback/cleanup cause의
  기존 우선순위를 보존하고 failed migration state를 publish하지 않습니다.
- Precommit failure는 현재 migration transaction 전체를 rollback하며 앞서 성공한 migration별 durable commit은
  ADR-0014대로 남습니다.
- `CommitCommitted`만 post-step state와 successor token을 publish합니다. `CommitRolledBack`은 pre-step
  state/token을 보존하고, `CommitUnknown`은 마지막 confirmed pre-step state와 기존
  `commit_outcome_unknown` 경계를 반환합니다.
- Commit error가 durable success의 cleanup error인 경우에도 기존 committed/cleanup 의미를 유지합니다.
- Definite failure와 unknown outcome 모두 relation lifecycle 내부 automatic retry는 0입니다.
- Restart는 file close/reopen 뒤 actual recorder history, revision epoch/token, loaded mixed definition set,
  actual DAG와 product `StateReconstructor`로 같은 latest/target plan을 재구성해야 합니다.

Phase C의 candidate-local restart, fake recorder/revision, private catalog와 helper hash는 actual product-path
restart를 증명하지 않습니다. Phase D2 private state/reconstructor readiness와 D3a direct SQLite optional port는
구현됐고 D3b가 actual recorder history→Planner→core relation lifecycle를 연결했습니다. D4 exact one-test-file
head `424ec4d...`는 file close/reopen 뒤 fresh loaded set/backend가 exact epoch/revision/fingerprint/full-history,
actual DAG와 private reconstructed state를 사용해 full/branch/full transition을 재구성하는 bounded scenario를
검증했습니다. 이는 captured structured snapshot의 관찰이며 raw file bytes나 general restart 계약이 아닙니다.

### Product entry boundary

- Core relation support는 D3b에서 normal loaded-set 경로인 `definition.Load` → `definition.Set.Migrate` →
  `Executor.Migrate`에 연결됐고 이 경로에서만 relation profile/state/lifecycle를 시작합니다. 별도 relation
  executor/public entrypoint를 만들지 않았습니다.
- Relation-only/mixed `Set.Migrate`의 fresh context carrier와 Executor의 pre-I/O seal 검증만 loaded authority를
  전달합니다. Carrier가 없거나 definition/profile/provenance/digest/full-graph pairing이 다르면 private
  reconstructor/prepared lifecycle을 mint하지 않습니다.
- Carrier 없는 raw `Executor.Migrate`, `Set.Definitions()` copy, direct legacy `Executor.Apply`,
  `Executor.Unapply` 또는 `ExecutePlan`에 relation-bearing input을 주면 legacy `BeginMigration`으로 fallback하지
  않고 pre-Begin `CategoryCapability`/`CodeUnsupported`, feature `relation_migration`으로 실패합니다.
- Raw legacy `Executor.Migrate`와 public `NewStateReconstructor`의 scalar behavior는 그대로 유지합니다. Public
  reconstructor에 raw relation input을 주는 기존 경계는 `CategoryState`/`CodeInvalidState`를 유지합니다.
- File reopen만 한 candidate-local/history-only proof는 actual recorder epoch/revision fingerprint, full applied
  history, migration DAG와 actual `StateReconstructor` replay를 검증한 것이 아닙니다.
- D1/D2의 loaded authority/readiness와 D3a의 direct optional SQLite session 검증은 각각 Implemented/Verified입니다.
  D3b는 normal loaded relation-bearing CreateModel을 exact-one history/actual-plan preflight 뒤 direct port로
  연결했고 D4는 이 기존 경로의 bounded file-backed captured-snapshot restart만 Verified했습니다. D4d/D4e/D4f는
  같은 sealed loaded 경로에서 bounded nullable ForeignKey Add, empty-source required Add와 exact backward/remove
  table remake를 구현·검증했습니다. Carrier 없는 raw relation entry는 계속 pre-Begin
  `CategoryCapability`/`CodeUnsupported`, feature `relation_migration`에서 정지합니다. False
  `RemoveForeignKeyByTableRemake`를 가진 다른 backend도 actual backward plan의 every Begin 전에 같은 core
  capability boundary에서 정지합니다.

### Error ownership

- Raw profile/codec/IR failure는 existing definition `CategorySource`를 사용합니다. Tuple coordinate mismatch는
  existing `CodeDefinitionFormatIncompatible`, `CodeLoaderABIIncompatible`,
  `CodeOperationCodecIncompatible`, `CodeSchemaIRIncompatible` 중 해당 coordinate code를 사용하며 별도
  “hybrid” code를 만들지 않습니다. Invalid relation arm/IR은 existing `CodeInvalidOperation`/
  `CodeInvalidIR` ownership을 유지합니다.
- Historical target/reverse/creator/state failure는 migrations `CategoryState`/`CodeInvalidState`와 exact
  migration/operation context를 사용합니다.
- Missing relation port/capability, FK PRAGMA off, populated-table required AddField와 closed-remake hazard는
  `backend.CapabilityError`를 통해 migrations `CategoryCapability`/`CodeUnsupported`로 정규화합니다. Feature는
  core relation boundary의 `relation_migration` 또는 SQLite-owned boundary의 `sqlite_relation_migration`입니다.
- Missing/mismatched loader handoff나 carrier 없는 raw relation entry도 pre-Begin
  `CategoryCapability`/`CodeUnsupported`, feature `relation_migration`을 사용합니다. Public reconstructor의 raw
  relation rejection은 별도 existing `CategoryState`/`CodeInvalidState` 경계를 유지합니다.
- `BeginFencedMigration`/`BeginRelationFencedMigration`, global connection `PRAGMA`, catalog/physical-preflight와 fence-claim
  failure는 migration step-level failure이며 `OperationIndex=NoOperation`을 사용합니다. Category/code는 그
  경계의 existing typed class를 보존하며 operation을 임의로 지목하지 않습니다.
- `SchemaEditor`/row-copy와 final `foreign_key_check`처럼 operation 실행이 소유한 failure만
  migrations `CategoryExecution`/`CodeOperationFailed`와 exact migration/operation context를 사용합니다.
- D4f post-claim CREATE/copy/DROP/RENAME/sequence clear/restore/final-FK failure는 coded `SQLITE_BUSY`여도
  backward news의 original `AddField` operation을 소유하고 `errors.Is` cause를 보존합니다. Revision-fence/
  capability 분류로 재해석하지 않습니다. Mutation failure는 rollback once, sticky no-retry와 no-temp-leak를
  보존하고 recorder failure만 `CategoryRecorder`/`CodeRecordFailed`, `NoOperation`을 사용합니다.
- Recorder, stale/contended/integrity, commit/cleanup/session-close outcome은 existing codes와
  `CommitOutcome`을 재사용하고 public `Cause`/`RollbackCause` priority를 보존합니다.
- Category/code/feature/context/cause ownership과 deterministic selection은 계약입니다. Human message,
  `Detail`, test-only candidate reason strings, private error structs와 helper names는 noncontractual입니다.

## Contract matrix

| ID | Accepted decision/reference observation | 현재 분류 |
|---|---|---|
| MIG-075 | Existing legacy tuple/state v1/digest v1/lifecycle ABI preservation | `oracle_locked` / Accepted legacy reference |
| MIG-076 | Exact relation tuple, per-document dispatch와 hybrid rejection | `oracle_locked` / Accepted-decision reference |
| MIG-077 | Relation-only/mixed set profile-bearing canonical digest v2 | `oracle_locked` / Accepted-decision reference |
| MIG-078 | Whole-step scalar v1↔relation v2 promotion/demotion and deep-copy | `oracle_locked` / Accepted-decision reference |
| MIG-079 | Target AutoField derivation and three-stage plan preflight | `oracle_locked` / Accepted-decision reference |
| MIG-080 | Relation CreateModel apply/unapply/reapply | `oracle_locked` / Django observed |
| MIG-081 | Populated nullable success, empty required support, populated required rejection | `oracle_locked` / observed + Accepted-decision separation |
| MIG-082 | FK remove/remake row and `sqlite_sequence` preservation | `oracle_locked` / Django observed |
| MIG-083 | Exact connection FK-on, physical `NO ACTION` and existing fence reuse | `oracle_locked` / observed + Accepted-decision separation |
| MIG-084 | File-backed restart | `oracle_locked` / Django observed; bounded product-path scenario Verified, actual adapter blocked |
| MIG-085 | Precommit rollback/cause behavior and one fenced transaction | `oracle_locked` / observed + Accepted-decision separation |
| MIG-086 | Commit success/definite failure/unknown outcome and no retry | `oracle_locked` / Accepted-decision reference |

MIG-075..086은 reference-only이며 product handler가 없습니다. Product aggregate는 계속 exact 12 adapters/
127 contracts=`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12입니다.

## 결과와 비용

이 Accepted boundary는 old migration artifact를 바꾸지 않고 relation-capable document를 점진적으로 추가할 수 있고,
historical relation meaning과 SQLite physical constraint가 explicit
version/capability/fence 경계에 놓입니다. Three-stage preflight와 bounded remake는 silent data loss를 줄이지만
지원 가능한 existing SQLite schema shape를 좁힙니다. Optional editor, state promotion과 mixed digest는 public
compatibility surface와 test matrix를 늘립니다.

현재 checkout은 D1 definition/handoff, D2 private relation state/readiness, D3a direct SQLite Create/Delete
optional port, D3b normal loaded core integration과 D4d bounded nullable/D4e empty-source required ForeignKey Add,
D4f bounded Remove-by-remake를 구현했고 각 bounded product slice를 local/hosted에서 검증했습니다. D4 exact test-only head는 existing product path의 bounded
captured-snapshot restart scenario를 추가 검증했습니다. 그러나 product contract/Q status는 바뀌지 않았고
MIG-075..086은 모두 reference-only `oracle_locked`입니다. Populated required Add/reapply, arbitrary/general
remake, general restart와 actual adapter는 미완료입니다.

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

1. Phase A reference-only artifacts와 exact-head hosted CI는 EVID-085/086에서 완료했습니다.
2. Phase B no-product feasibility와 exact-head hosted CI는 EVID-087/088에서 완료했습니다.
3. Phase C test-only decision proof와 exact-head hosted CI는 EVID-089/090에서 완료했습니다.
4. Proposed decision-freeze documentation head `5bdf013...`의 local final-byte gates와 고유 exact-head hosted
   CI/independent audit는 EVID-091에서 완료했습니다.
5. 그 성공을 별도 documentation head에 기록하고 이 ADR의 bounded decision 상태를 `Accepted`로 전환했습니다.
6. Acceptance documentation head `7cdc6d6...` 자체의 고유 exact-head hosted CI와 independent audit는
   EVID-092/run `32187094845`에서 완료했고 EVID-091을 재사용하지 않았습니다.
7. Phase D1 carrier/context/seal, D2 private state/readiness와 D3a direct optional SQLite Create/Delete port는
   EVID-093의 local/hosted proof를 통과했습니다. 각 bounded sub-slice만 Implemented/Verified입니다.
8. D3b는 이 ADR의 exact-one history → actual Planner → whole-plan dry validation → conditional relation
   capability 순서를 새 public API 없이 구현했고 EVID-094의 local/hosted proof를 통과했습니다. Normal loaded
   relation-bearing CreateModel apply/unapply/reapply만 bounded support로 주장합니다.
9. D4 exact one-test-file head는 EVID-095의 local/hosted evidence에서 bounded file-backed
   full/branch/full captured-snapshot restart만 Verified했습니다. Add/Remove/remake, raw file-byte equality,
   `sqlite_sequence`, general restart와 actual adapter는 각자의 exact evidence 전에는 주장하지 않습니다.
10. EVID-096 exact-six documentation head `62df9b2...`는 별도 run `32260744096`의 고유 exact
    26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했고, 그 run을 later D4d product proof로 재사용하지
    않았습니다.
11. D4d product `3950d98...`와 inventory lock `28b141e...`의 first hosted run `32267789056`은 macOS Intel
    race job의 wall-clock assertion P1 실패를 보존합니다. Deterministic visit-count fix `dd83362...`의 distinct
    run `32271361724`가 exact 26/26 jobs·342/342 steps와 audit P0..P3=0을 통과했으므로 bounded nullable
    ForeignKey Add만 Implemented/Verified로 주장합니다. 이 D4d 경계에서는 required Add/Remove-remake, general
    restart, actual adapter와 completion/terminal을 주장하지 않습니다.
12. EVID-097 documentation head `c59669c...`는 distinct run `32278555810`의 exact 26/26·342/342와 audit
    P0..P3=0으로 닫혔습니다. 그 run을 D4e product proof로 재사용하지 않았습니다.
13. D4e product `7c07805...`와 inventory lock `1d86f6e...`는 distinct run `32282269755`의 exact
    26/26·342/342와 audit P0..P3=0을 통과했습니다. Empty-source required Add만 Implemented/Verified로
    주장하며 populated source, Remove/remake, general restart, actual adapter와 completion/terminal은 주장하지
    않습니다.
14. EVID-098 documentation head `85f9270...`는 distinct CI #94/run `32288383027`의 exact 26/26·342/342와
    audit P0..P3=0으로 닫혔습니다. 그 run을 D4f product proof로 재사용하지 않았습니다.
15. D4f product `4982e27...`와 inventory lock `9d5b894...`는 distinct CI #95/run `32294983953`의 exact
    26/26·342/342와 audit P0..P3=0을 통과했습니다. Exact bounded backward/remove table remake만
    Implemented/Verified로 주장하며 arbitrary/general remake, populated required Add/reapply, general restart,
    actual adapter, status/deviation decision과 completion/terminal은 주장하지 않습니다.
16. D4g의 첫 action은 oracle-blind observer-only characterization입니다. MIG-075..086을 모두
    `oracle_locked`로 유지한 채 actual observation을 수집하고, 현재 omitted allowed path와 DEV/deviation
    필요 여부를 explicit decision gate에서 먼저 다룹니다. 이 ADR은 deviation을 묵시적으로 승인하지 않습니다.

EVID-090/run `32174259324`는 exact test-only proof head만, EVID-091/run `32183309328`은 exact Proposed
decision-freeze docs head `5bdf013...`만, EVID-092/run `32187094845`는 acceptance docs head `7cdc6d6...`만
증명합니다. EVID-093의 세 hosted run도 각 D1/D2/D3a correction head만 증명합니다. EVID-094/run
`32231149900`은 D3b correction head `167ef03...`만 증명합니다. EVID-095/run `32248885053`은 D4 test-only
head `424ec4d...`만 증명하고 EVID-096/run `32260744096`은 exact-six docs head `62df9b2...`만 증명합니다.
EVID-097의 run `32267789056`은 P1 failure를, distinct run `32271361724`는 deterministic fix head
`dd83362...`만 증명합니다. Runs `32278555810`/`32282269755`는 각각 exact docs head `c59669c...`/D4e final
head `1d86f6e...`만 증명합니다. CI #94/run `32288383027`은 exact EVID-098 docs head `85f9270...`만,
CI #95/run `32294983953`은 exact D4f final head `9d5b894...`만 증명하며 이 문서 append나 later D4g
observer/status/deviation decision, completion/terminal proof로 재귀 사용하지 않습니다.
Draft PR #1은 open/draft/unmerged이며 사용자 요청 전 merge하지 않습니다.
