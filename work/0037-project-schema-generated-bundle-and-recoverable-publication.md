---
id: GDJ-0037
status: active
updated: 2026-08-21
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "57ddff374f7afa97346532ed143f4a88d73c7428"
depends_on: ["GDJ-0036"]
contracts: ["Q-010", "Q-017"]
allowed_paths:
  - "AGENTS.md"
  - ".gitignore"
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "codegen/**"
  - "cmd/godj/**"
  - "project/**"
  - "internal/cmd/m1generate/**"
  - "internal/compiletest/**"
  - "internal/projectcheck/**"
  - "internal/projectgenerate/**"
  - "internal/projectspec/**"
  - "examples/article/**"
  - "conformance/relationproduct/**"
  - "conformance/relationqueryproduct/**"
  - "conformance/relationobjectproduct/**"
  - "conformance/relationreverseproduct/**"
  - "conformance/relationprefetchproduct/**"
  - "conformance/relationselectproduct/**"
  - "conformance/relationdeleteproduct/**"
  - "conformance/internal/protocol/**"
  - "conformance/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0036-project-schema-generated-bundle-and-recoverable-publication.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0036-pre-release-compatibility-reset.md"
  - "work/0037-project-schema-generated-bundle-and-recoverable-publication.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Project Schema Generated Bundle and Recoverable Publication

## 사용자에게 보이는 결과

```bash
godj generate
godj generate --check
```

하나의 project schema 선언에서 app/project current generated output 전체를 한 번 생성하고 compile한 뒤 게시합니다.
`--check`는 선택한 project tree와 Git 상태를 변경하지 않고 manifest, exact roster, hash, snapshot과
interrupted-publication 상태를 검사합니다. Candidate compile용 격리 workspace는 project tree 밖에 만들고 정리합니다.

```text
ProjectSpec
→ one normalized project snapshot
→ one immutable GeneratedBundle
→ whole-candidate compile
→ recoverable coordinated publication
→ committed manifest
```

## 목표

- ProjectSpec을 app/project generation의 단일 입력으로 만듭니다.
- Current renderer roster 전체를 opaque immutable GeneratedBundle로 생성합니다.
- Canonical manifest와 project snapshot/generator ABI provenance를 고정합니다.
- Target mutation 전 whole-candidate compile과 read-only `generate --check`를 구현합니다.
- 정상 실패에서 prior bundle을 exact 보존하고 crash 뒤 exact old/new로 복구합니다.
- Mixed generation을 snapshot marker compile seal로 fail-closed합니다.
- Article과 strongest relation product를 실제 bundle/CLI 경계로 전환합니다.
- 작은 변경마다 full matrix를 반복하지 않고 phase checkpoint와 final milestone gate를 분리합니다.

## 비목표

- 물리적 single generated file 또는 generic filesystem transaction framework
- publication 중 external compiler의 무중단 성공
- distributed/network filesystem atomicity와 검증 없는 Windows 지원
- raw-model embedding/unwrap/sidecar 최종 선택
- reverse/general ORM, 새 Field/Relation, PostgreSQL/Web/Form/Admin/API 기능
- migration writer/autodetector와 first-alpha 이후 upgrade/semver 정책
- arbitrary plugin generator layout 또는 user-edited generated source merge

## 선행 조건과 기준 상태

- Baseline `57ddff374f7afa97346532ed143f4a88d73c7428`의 CI #101/run
  `32347190714`는 exact 26/26 jobs·326/326 steps, annotations 0, clean-worktree 24/24와 no-rewrite 10/10을
  통과했습니다.
- GDJ-0036은 current-only IR/Definition/State, explicit LoadedDefinitionSet, unified migration backend와 current
  generated ABI를 완료했습니다.
- Current relation runtime/product status는 exact 12/127=`122 passing + 5 deviation`, REL-001..012 12/12입니다.
- MIG-075..086은 계속 diagnostic `oracle_locked`/unregistered입니다.
- 장기 결정 후보는 [ADR-0036](../docs/adr/0036-project-schema-generated-bundle-and-recoverable-publication.md)이
  소유합니다.

## 고정 경계

```go
type PackageSpec struct {
    PackageName string
    ImportPath  string
    Directory   string
}

type AppSpec struct {
    Alias   string
    Package PackageSpec
    Schema  ir.Schema
}

type ProjectSpec struct {
    Project PackageSpec
    Apps    []AppSpec
}

type GeneratedBundle struct { /* opaque immutable snapshot */ }

func GenerateProject(ProjectSpec) (GeneratedBundle, error)
func (GeneratedBundle) SnapshotSHA256() string
func (GeneratedBundle) Files() []GeneratedFile
func (GeneratedBundle) Manifest() []byte

func Check(context.Context, string, codegen.GeneratedBundle) (CheckReport, error)
func NewGoCandidateVerifier(string, codegen.GeneratedBundle) (CandidateVerifier, error)
func Publish(context.Context, string, codegen.GeneratedBundle, CandidateVerifier) error

type ProjectRoot struct { /* opaque retained identity seal */ }
func SealProjectRoot(string, uint64, uint64) (ProjectRoot, error)
func CheckRoot(context.Context, ProjectRoot, codegen.GeneratedBundle) (CheckReport, error)
func NewGoCandidateVerifierRoot(ProjectRoot, codegen.GeneratedBundle) (CandidateVerifier, error)
func PublishRoot(context.Context, ProjectRoot, codegen.GeneratedBundle, CandidateVerifier) error
```

- Public codegen은 semantic ProjectSpec과 immutable bundle만 노출합니다.
- Whole-candidate/check/publish/recovery는 `internal/projectgenerate`가 소유합니다.
- Root는 실행 경계이며 snapshot digest에 넣지 않습니다. Canonical relative layout과 import identity는 넣습니다.
- Package directory는 `.` 또는 dot/dot-dot/empty/backslash segment가 없는 clean slash-relative path입니다.
- `GeneratedBundle.Files()`는 canonical `0644` Go source만 포함하고 manifest는 별도
  `.godj/generated-manifest.json` commit marker입니다.
- Persisted format-1 manifest decoder는 안전한 prior ABI/owned roster를 읽고, desired bundle validator만 exact current
  13-role ABI/4n+8 roster를 요구합니다. 이것이 version drift와 stale-file upgrade를 가능하게 합니다.
- 기존 renderer는 pure helper로 유지할 수 있지만 공식 project generation call path는 bundle 하나로 수렴합니다.
- Existing per-file `WriteFile`은 공식 project publisher에서 제거하고 필요하면 internal helper로만 남깁니다.
- Go candidate verifier는 private bundle-backing root를 untouched project root에 overlay하고 `./...`를
  `go test -c`로 compile할 뿐 test binary, user `init` 또는 `TestMain`을 실행하지 않습니다.
- `Check`는 clean이면 nil, drift면 exact report와 `ErrGeneratedDrift`, journal이 있으면 `DriftInterrupted`와
  `ErrPublicationInterrupted`를 함께 식별할 수 있는 error를 반환합니다.
- Shared internal paths는 `.godj/publication-journal.json`, `.godj/generate.lock`, `.godj/transactions`로 고정합니다.
- Global orchestration은 선택한 project root를 device/inode identity로 seal하고 check, candidate verification과
  publication 전 과정에서 같은 authority를 재사용합니다. Path rebound는 target mutation 전에 거부합니다. String
  기반 함수는 root를 그 호출 시점에 seal하는 internal convenience wrapper입니다.
- Commit marker rename+directory fsync 전 cancellation/ordinary error는 synchronous exact-old rollback입니다.
  Durable commit 뒤 publisher 내부 cleanup/recovery는 cancellation을 outcome으로 반환하지 않고 새 snapshot을
  유지합니다. 복구 결과를 증명할 수 없으면 `ErrPublicationRecoveryRequired`로 fail-closed합니다. Publisher가
  성공한 뒤 outer private workspace/retained root cleanup이 실패하면 CLI는 success stdout 없이
  `project_generation_process_error/project_cleanup_failed`와 exit 3을 반환할 수 있지만 target은 이미 commit됐을 수
  있으므로 `godj generate --check`로 상태를 확인합니다.

## Phase A — Pure whole-project bundle

- [x] ProjectSpec deep clone/normalize/canonical ordering
- [x] duplicate alias/import/path/app/model/namespace fail-before-output
- [x] current app 4/project 8 renderer union
- [x] generator ABI roster, snapshot digest와 canonical manifest
- [x] immutable file/source/manifest accessors
- [x] app/project snapshot marker compile seal

Checkpoint:

```bash
go test -count=1 ./codegen \
  -run 'Test(ProjectSpec|ProjectBundle|ProjectManifest|ProjectSnapshot|GenerateProject)'
go test -race -count=1 ./codegen \
  -run 'Test(ProjectBundle|ProjectSnapshot|GenerateProject)'
```

완료 조건:

- input permutation byte-identical, caller mutation 격리
- Schema/layout/generator ABI 변화가 snapshot을 각각 변경
- full current output union과 external consumer compile
- app 간 direct import 0

## Phase B — Whole-candidate compile and read-only check

- [x] new/replacement/stale-delete를 포함한 complete overlay
- [x] app/project/external consumer compile-only verifier
- [x] user init/TestMain non-execution
- [x] exact manifest/inventory/hash/snapshot drift report
- [x] interrupted journal read-only diagnosis
- [x] broken generated target을 import하지 않는 bootstrap path

Checkpoint:

```bash
go test -count=1 ./internal/projectgenerate ./internal/compiletest \
  -run 'Test(WholeCandidate|ProjectBundle|GeneratedCheck|ExternalConsumer)'
```

완료 조건:

- missing/extra/modified/stale 하나만 있어도 bundle drift
- verifier/check failure에서 target write 0
- check 뒤 선택한 project tree/Git 상태 불변; 격리 candidate workspace는 외부에서 정리
- external consumer positive/negative compile

## Phase C — Recoverable coordinated publication

- [x] single project-generation writer lock
- [x] prior manifest/owned-file digest CAS
- [x] same-filesystem stage+fsync와 whole-candidate compile
- [x] durable journal과 deterministic rename
- [x] manifest-last commit marker와 directory fsync
- [x] precommit rollback/postcommit cleanup recovery
- [x] exact-owner stale removal과 user-edit/symlink/path rejection

Checkpoint:

```bash
go test -count=1 ./internal/projectgenerate \
  -run 'Test(Publish|Rollback|Recovery|Journal|Conflict|Interrupted|Concurrent)'
go test -race -count=1 ./internal/projectgenerate \
  -run 'Test(Publish|Recovery|Concurrent)'
```

완료 조건:

- filesystem step fault마다 exact old 또는 exact new만 accepted
- child-process kill 뒤 다음 generate가 결정적으로 복구
- cancellation/ordinary failure 뒤 partial accepted state 0
- concurrent publisher serialization
- stale ownership/hash 불일치에서 destructive action 0

## Phase D — CLI, product adoption and handoff

- [x] global CLI `generate`/`generate --check`와 private project runner 연결
- [x] declaration-side Config가 generated target을 import하지 않음
- [x] m1generate single-file 경계 제거와 bundle runner 전환
- [x] Article과 canonical relation product bundle adoption
- [x] project manifest 중심 `make generate-check`
- [x] protocol/inventory/current docs 한 번 재기준화
- [x] current relation behavior/status 불변 검증

Checkpoint:

```bash
go test -count=1 \
  ./cmd/godj ./project ./internal/projectcheck \
  ./internal/projectgenerate/linked ./internal/projectgenerate/protocol ./internal/projectspec \
  ./examples/article ./conformance/relationdeleteproduct ./conformance/internal/protocol
make generate-check
```

완료 조건:

- exact 네 generation argv와 exit/error taxonomy, migration check argv 불변
- 별도 generation protocol, invalid/nil declaration closed failure와 missing/broken generated bootstrap
- Article app 4/project 8=`12`, relationdelete app 8/project 8=`16` source와 canonical manifest
- selected project device/inode seal, `--check` target/Git 무변경과 candidate non-execution
- publisher postcommit cancellation과 outer cleanup ambiguity가 서로 다른 outcome으로 고정
- `make generate-check` 두 project clean, current relation behavior/status 불변

## 병렬 소유권

공용 타입과 manifest schema를 integration owner가 먼저 동결한 뒤 최대 세 lane을 병렬 실행합니다.

| Lane | 독점 경로 | 책임 |
|---|---|---|
| Integration owner | `codegen/project_api.go`, `internal/projectgenerate/types.go`, `project/**`, `cmd/godj/**`, docs/work/status | public/shared API, CLI, 최종 통합 |
| A | `codegen/project_bundle*`, `codegen/project_manifest*`, renderer 내부 | Phase A |
| B | `internal/projectgenerate/candidate*`, `check*`, `internal/compiletest/project_bundle*` | Phase B |
| C | `internal/projectgenerate/publish*`, `journal*`, `lock*`, crash/fault tests | Phase C |

- Lane agent는 shared API와 다른 lane 파일을 수정하지 않습니다.
- Phase별 full CI/hosted run을 금지하고 위 affected checkpoint만 실행합니다.
- Phase D와 generated fixtures/protocol lock/docs는 integration owner가 A/B/C 통합 뒤 한 번 수행합니다.

## 검증 주기

- 매 변경: gofmt, compile, affected tests, generated drift
- Phase checkpoint: 위 slice normal/race/CGO0와 필요한 filesystem canary
- Final integration: full `make ci`, Linux/386 compile, repo-external source-clean copy `generate --check` 한 번
- Final hosted: frozen implementation/documentation head의 matrix 한 번
- 문서-only evidence append: link/frontmatter/status consistency만; evidence 자체를 위한 recursive full matrix는 실행하지 않음

## 전체 완료 기준

- [x] 공식 generation input은 ProjectSpec 하나
- [x] 공식 publication input은 immutable GeneratedBundle 하나
- [x] 모든 app/project output과 manifest가 동일 snapshot/ABI 소유
- [x] whole-candidate compile이 첫 target mutation보다 앞섬
- [x] `--check` read-only와 exact drift diagnosis
- [x] ordinary failure에서 prior committed bundle exact 보존
- [x] crash 뒤 exact old/new 복구, hybrid accepted/compile-success 0
- [x] stale removal은 manifest-owned exact-old 파일로 제한
- [x] generated target bootstrap 순환 없음
- [x] current relation product behavior/status 불변
- [x] final local full/386/source-clean-copy gate
- [ ] final exact-head hosted matrix와 independent P0..P3=0 audit
- [x] CURRENT/MATRIX/TEST_EVIDENCE/ADR/work 정합성

## 2026-08-21 affected local checkpoint

- 전체 명령·환경·packet 경계는
  [EVID-104](../docs/status/TEST_EVIDENCE.md#evid-20260821-104--gdj-0037-project-bundle-and-recoverable-publication-affected-local-verification)에 고정했습니다.
- Phase A/B producer/check/candidate와 Article/relationdelete adoption은 normal/race/CGO-disabled/vet에서 통과했습니다.
  Article은 exact 12 files/snapshot `2f39e045e436ae70856736b78d203d494124cf5cc6e6f5ab57dcb4a9c2b07fbe`,
  relationdelete는 exact 16 files/snapshot
  `4b618261fcdec4fb126e8b20714700343543613390d1187439a315455ef5f775`입니다.
- Phase C `./internal/projectgenerate` 전체 normal/race/CGO-disabled/vet와 SIGKILL recovery가 통과했고 independent
  source audit은 P0/P1/P2/P3=`0/0/0/0`이었습니다. 이는 프로세스 중단과 fsync/rename 정적 순서를 검증하며 실제
  전원 손실을 직접 모사했다는 주장은 아닙니다.
- Phase D `./internal/projectcheck ./cmd/godj ./project ./examples/article ./conformance/relationdeleteproduct
  ./conformance/internal/protocol` 및 generation protocol/linked/projectspec은 normal/race/CGO-disabled/vet에서
  통과했고 independent source audit은 P0/P1/P2/P3=`0/0/0/0`이었습니다.
- `GOTOOLCHAIN=local GOWORK=off GOPROXY=off GOSUMDB=off make generate-check`, `make format-check`, `gofmt`와
  `git diff --check`가 통과했습니다. Final full offline `make ci`, Linux/386 all-package compile과
  repository-external source-clean-copy `generate --check`/compile-only gate도 통과했습니다. Exact committed-head
  hosted matrix가 아직 없으므로 work는 active/local Implemented candidate입니다.

## 다음 정확한 작업

Final local integration과 Phase E 문서·상태 갱신은 끝났습니다. 다음은 frozen implementation/documentation tree의
commit/push와 exact-head hosted verification이며 별도 사용자 승인이 있을 때만 수행합니다. Q-017 전체는 raw-model UX,
capability/namespace, reverse/general upgrade와 generator/library semver 때문에 계속 P1/open입니다.
