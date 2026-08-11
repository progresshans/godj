# GoDj Compatibility Lab

이 디렉터리는 Django reference profile, contract manifest, normalized observation,
comparator, M0 codegen bootstrap spike와 GoDj observation adapter를 보관합니다.
GDJ-0003은 같은 exact profile에 write/migration 전용 두 번째 contract set을 추가했고,
GDJ-0004는 그 set을 실제 제품 package에 연결했습니다.
GDJ-0005는 mutable Save lifecycle 전용 세 번째 reference set을 추가했습니다.
GDJ-0007은 QuerySet evaluation/cache 전용 네 번째 reference set을 추가했습니다.
GDJ-0008은 네 번째 set을 실제 제품 adapter에 연결해 `passing`으로 전환했습니다.
GDJ-0009은 migration dependency/applied-state planning 전용 다섯 번째 reference set을
추가했고, GDJ-0010은 immutable public Planner adapter를 연결해 `passing`으로
전환했습니다.
GDJ-0011은 multi-migration plan execution 전용 여섯 번째 reference set을 추가했습니다.
GDJ-0012는 여섯 번째 GoDj live adapter와 fail-closed DEV-0001 expectation을 연결해
여섯 exact `passing`과 네 verified `deviation`으로 전환했습니다.
GDJ-0013은 durable recorder read와 fresh restart planning 전용 일곱 번째 reference set을
추가했습니다. GDJ-0014는 이 set을 read-only recorder와 fresh file-backed backend를 쓰는
GoDj live adapter에 연결해 10 `passing`으로 전환했습니다.
GDJ-0015는 loaded migration definition의 historical `ProjectState` reconstruction 전용
여덟 번째 reference set을 추가했습니다. GDJ-0016은 immutable public reconstructor와
read-only recorder-backed GoDj live adapter를 연결해 이 set을 10 `passing`으로
전환했습니다.
GDJ-0017은 fresh/target/failure/restart migration lifecycle 전용 아홉 번째 reference set을
추가했습니다. GDJ-0017 완료 당시 이 10개는 `oracle_locked`였고 제품 adapter는 없었습니다.
별도 `lifecyclefence` package는 revision fence의 test-only feasibility를 검증할 뿐 제품 API나
backend 구현이 아닙니다.

GDJ-0018은 public `Executor.Migrate`와 revision-fenced SQLite backend를 사용하는 아홉 번째
GoDj live adapter를 연결했습니다. Lifecycle 9개는 `passing`, MIG-052만 reviewed DEV-0002
`deviation`이며 GDJ-0018 완료 당시 9 product set의 분류는
`92 passing + 5 deviation`이었습니다.
GDJ-0019는 explicit migration definition source 전용 열 번째 reference set을 contract-only로
추가했습니다. GDJ-0019 완료 당시 MIG-057..064 여덟 개는 `oracle_locked`였고 제품 loader와
열 번째 GoDj adapter가 없었습니다. 따라서 reference는 10 set/105 unique contract/90 ordered
cross-binding으로 늘었지만 당시 제품 분류는 `92 passing + 5 deviation` 그대로였습니다.
GDJ-0020은 public `migrations/definition` bounded loader와 열 번째 actual adapter를 연결해
MIG-057..064를 8 `passing`으로 전환했습니다. GDJ-0020 완료 당시 제품 분류는 정확히 10 adapter/105 contract의
`100 passing + 5 deviation`입니다. Source discovery/CLI, writer/upgrade, executable/custom
operation과 non-SQLite migration backend 지원을 뜻하지 않습니다.
GDJ-0021은 database-free migration project check의 decision/compatibility contract 열 개를
Accepted ADR-0021에 묶인 열한 번째 reference set으로 추가했습니다. GDJ-0021 완료 당시
MIG-065..074는 `oracle_locked`였고 product adapter, global CLI 또는 production project-linked
runner는 없었습니다. Reference corpus는 11 set/115 unique contract/110 ordered cross-binding으로
늘었지만 제품 분류는 10 adapter/105 contract의 `100 passing + 5 deviation`을 유지했습니다.
GDJ-0022는 actual global kernel과 project-linked loader report를 결합하는 열한 번째 GoDj adapter를
연결해 MIG-065..074를 10 `passing`으로 전환했습니다. GDJ-0022 완료 당시 제품 분류는 정확히
11 adapter/115 contract의 `110 passing + 5 deviation`이며 DB-aware drift check나 non-SQLite backend 지원을
뜻하지 않습니다.
GDJ-0023은 ForeignKey relation 동작 12개를 exact Django reference로 고정한 열두 번째 set을
추가했습니다. 이 단계의 REL-001..012는 모두 `oracle_locked`였고 제품 adapter는 없었습니다.
GDJ-0024는 generated v3 relation metadata와 project binder를 관찰하는 열두 번째 product adapter를
연결해 REL-001 metadata를 `passing`으로 전환했습니다. GDJ-0025는 별도 generated relation-query product와
SQLite required one-hop `INNER JOIN`을 연결해 REL-004도 actual `passing`으로 전환했습니다. GDJ-0026은
additive generated relation-object companion/project bridge와 actual SQLite를 통해 REL-003 required lazy cache와
REL-006 nullable access/source-key `isnull`을 `passing`으로 전환했습니다. GDJ-0027은 별도 generated
reverse-relation product와 actual SQLite를 연결해 REL-005 reverse accessor/lookup도 `passing`으로 전환했습니다.
GDJ-0028은 exact ten-file generated reverse-prefetch product와 actual SQLite의 한 번짜리 root `IN` batch를 연결해
REL-012 reverse prefetch도 `passing`으로 전환했습니다. GDJ-0029는 app-local projection scanner 두 개와
project select-related companion을 더한 exact twelve-file product를 actual SQLite에 연결해 REL-009 required
INNER, REL-010 nullable LEFT OUTER와 REL-011 reverse-path pre-I/O rejection을 함께 `passing`으로 전환했습니다.
GDJ-0030 active implementation은 별도 exact thirteen-file relation-delete product와 project-bound collector를
actual SQLite에 연결해 REL-007 `PROTECT`와 REL-008 `SET_NULL`을 `passing`으로 전환했습니다. Exact-head hosted
implementation 검증과 Accepted/completed 문서 전이는 아직 남아 있습니다. REL-002만 ordered payload-free
`not_implemented` actual과 `oracle_locked` manifest 상태를 유지합니다. 현재 reference는
12 set/127 contract/132 ordered cross-binding이고, 제품 분류는 12 adapter/127 contract의
`121 passing + 5 deviation + 1 oracle_locked`입니다. 이는 relation metadata, required predicate/object cache,
nullable local-key access/`isnull`, bounded reverse accessor/lookup, exact reverse prefetch와 one-hop forward eager
selection 및 bounded project-bound `PROTECT`/`SET_NULL` delete 11/12의 제품 증거이며 ForeignKey assignment,
general cascade/eager graph/DDL/migration 전체
지원을 뜻하지 않습니다.
제품용 Schema/ORM/SQLite/migration 구현은 루트의 `schema`, `codegen`, `query`, `orm`,
`db`, `migrations` package에 있으며 이 디렉터리는 그 동작을 oracle에 연결합니다.

## 정본과 생성물

| 경로 | 역할 |
|---|---|
| `profiles/*.json` | exact reference runtime과 dependency lock fingerprint |
| `contracts/manifest.json` | M1 read/metadata contract 11개 |
| `contracts/write-migration-manifest.json` | M2 write/transaction/migration contract 11개 |
| `contracts/save-lifecycle-manifest.json` | Save lifecycle reference contract 12개 |
| `contracts/query-cache-manifest.json` | QuerySet evaluation/cache reference contract 11개 |
| `contracts/migration-planning-manifest.json` | Migration planning reference contract 12개 |
| `contracts/migration-execution-manifest.json` | Migration plan execution reference contract 10개 |
| `contracts/migration-restart-manifest.json` | Recorder-backed restart planning reference contract 10개 |
| `contracts/migration-state-reconstruction-manifest.json` | Historical ProjectState reconstruction reference contract 10개 |
| `contracts/migration-lifecycle-manifest.json` | End-to-end migration lifecycle reference contract 10개 |
| `contracts/migration-definition-source-manifest.json` | Explicit versioned migration definition source reference contract 8개 |
| `contracts/migration-project-check-manifest.json` | Project-linked migration catalog check decision contract 10개 |
| `contracts/relation-manifest.json` | ForeignKey relation reference contract 12개; REL-002를 제외한 11개가 현재 product-required |
| `runners/django` | 명시적인 Django observation/GoDj decision-oracle scenario와 type-preserving normalizer |
| `runners/godj` | M1 read부터 relation metadata까지 제품 package를 실행하는 열두 GoDj observation adapter와 immutable actual-handler registry |
| `relationproduct` | checked-in generated cross-app fixture, generated project bridge와 REL-001 actual observation root |
| `relationqueryproduct` | checked-in generated required relation-query fixture와 REL-004 actual SQLite observation root |
| `relationobjectproduct` | checked-in generated relation-object fixture와 REL-003/006 actual SQLite observation root |
| `relationreverseproduct` | exact nine-file generated reverse-relation fixture와 REL-005 actual SQLite observation root |
| `relationprefetchproduct` | exact ten-file generated reverse-prefetch fixture와 REL-012 actual two-query SQLite observation root |
| `relationselectproduct` | exact twelve-file generated one-hop forward select-related fixture와 REL-009/010/011 actual SQLite observation root |
| `relationdeleteproduct` | exact thirteen-file generated project-bound relation-delete fixture와 REL-007/008 actual SQLite observation root |
| `oracles/**/*.json` | 정확한 provenance에 묶인 byte-deterministic expected reference observation |
| `oracles/**/SHA256SUMS` | checked-in oracle byte checksum |
| `internal/protocol` | strict decoder/validator/canonical value, all-observed comparator와 required-observed product comparator |
| `fixtures/godj*.json` | 미구현 상태가 pass되지 않는 set별 protocol fixture와 reviewed sparse deviation expectation |
| `codegenbootstrap` | Q-001 package bootstrap 실행 실험 |
| `lifecyclefence` | GDJ-0017 revision-fence test-only SQLite feasibility와 current-gap characterization |
| `definitionload` | GDJ-0019 test-only feasibility proof와 GDJ-0020 public loader의 independent black-box equivalence gate |
| `projectcheck` | GDJ-0021 descriptor/discovery/process/protocol test-only feasibility gate; product package가 아님 |
| `cmd/godjcheck` | GoDj observation을 생성해 provenance-locked expected reference와 비교 |

각 machine-readable manifest는 해당 contract set 실행 입력의 정본입니다. Profile ID,
ordered contract ID/position, phase와 payload dimension이 suite를 선택 manifest에 묶으며
다른 set의 oracle을 섞으면 validation이 실패합니다. 사람이 보는 진행 상태는
`docs/status/IMPLEMENTATION_MATRIX.md`가 요약하며 두 파일의 상태를 같은 변경에서
갱신합니다.

현재 wire format은 protocol v2입니다. v2는 contract manifest의 expected phase를
필수화하며 v1 profile, manifest와 observation suite를 조용히 받아들이지 않습니다.

## Exact profile

초기 profile은 다음 조합에만 oracle identity를 주장합니다.

```text
Django 6.1 / commit fe0a859f537d4238cf49fca39073513206f83122
CPython 3.14.3 managed by uv
SQLite 3.50.4 / locked sqlite_source_id
darwin / arm64
TIME_ZONE=UTC / USE_TZ=true / locale=C / LANGUAGE_CODE=en-us
uv 0.10.12 / uv.lock SHA-256 pinned in the profile
```

CPython 3.14.3은 Django 6.1이 지원하는 Python minor에 속하지만 2026-08-07의 최신
3.14 micro는 아닙니다. 이 profile은 “최신 공식 Python profile”이 아니라 GoDj가
실제로 재생하고 고정한 conformance reference입니다. Runner는 fingerprint나 lock이
하나라도 다르면 oracle을 쓰기 전에 실패합니다.

일반 Linux CI는 이 darwin oracle을 재생성했다고 주장하지 않습니다. Portable
normalizer/scenario test와 checked-in artifact validation만 실행합니다. 권위 있는
regeneration은 exact profile을 가진 환경에서 `make check`로 수행합니다.

GoDj SQLite backend는 Django reference와 별도 실행 환경입니다. 현재 module pin은
`modernc.org/sqlite v1.56.0`이고 내장 SQLite는 3.53.3입니다. Django reference의
SQLite 3.50.4 fingerprint를 Go backend 정보로 덮어쓰지 않으며, 차등 비교는 계약된
외부 동작을 비교합니다.

## 실행

의존성을 잠긴 상태로 설치합니다.

```bash
uv sync --frozen
```

일반 CI 범위를 실행합니다.

```bash
make ci
```

Exact profile에서 oracle 재생까지 확인합니다.

```bash
make check
```

Oracle을 의도적으로 다시 만들 때만 다음을 실행하고 diff를 검토합니다.

```bash
make oracle-regenerate
git diff -- conformance/oracles
```

두 번째 write/migration oracle만 직접 확인할 수도 있습니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/write-migration-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json \
  --check
```

Save lifecycle oracle도 같은 runner에 세 번째 manifest를 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/save-lifecycle-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json \
  --check
```

QuerySet evaluation/cache oracle은 네 번째 manifest와 전용 output을 함께 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/query-cache-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json \
  --check
```

Migration planning oracle은 다섯 번째 manifest와 전용 output을 함께 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-planning-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  --check
```

Migration plan execution oracle은 여섯 번째 manifest와 전용 output을 함께 넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-execution-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  --check
```

Recorder-backed restart planning oracle은 일곱 번째 manifest와 전용 output을 함께 넘겨
확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-restart-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  --check
```

Historical ProjectState reconstruction oracle은 여덟 번째 manifest와 전용 output을 함께
넘겨 확인합니다.

```bash
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  --check
```

Migration lifecycle oracle은 아홉 번째 manifest와 전용 output으로 확인합니다.

```sh
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-lifecycle-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json \
  --check
```

Migration project-check decision oracle은 열한 번째 manifest와 전용 output으로 확인합니다.

```sh
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/migration-project-check-manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json \
  --check
```

두 observation을 직접 비교할 수 있습니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json \
  -actual path/to/godj-observation.json
```

현재 GoDj read 구현을 oracle과 직접 비교하려면 다음을 실행합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
```

Write/migration set도 같은 command에 두 번째 manifest와 oracle을 넘겨 직접 비교합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/write-migration-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json
```

QuerySet evaluation/cache 제품 adapter도 같은 command에 네 번째 manifest와 oracle을
넘겨 직접 비교합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/query-cache-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json
```

Migration planning 제품 adapter도 같은 command에 다섯 번째 manifest와 oracle을 넘겨
직접 비교합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json
```

Static baseline은 별도 비교에서 예상 exit 1과 ordered 12 status mismatch를 계속 내야
합니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  -actual conformance/fixtures/godj-migration-planning-not-implemented.json
```

Manifest에 등록되지 않은 migration-planning scenario는 `godjcheck`가 exit 2로 거부하고
actual output을 쓰지 않습니다.

Migration plan execution 제품 adapter는 locked Django oracle을 먼저 strict self-compare한
뒤 code-owned DEV-0001 selector와 sparse expectation으로 product expectation을 구성합니다.
Deviation fixture가 없거나 selector/status/provenance가 등록 정책과 다르면
`godjcheck`가 exit 2로 거부하고 actual output을 쓰지 않아야 합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -deviation-expected conformance/fixtures/godj-migration-execution-deviation-expected.json
```

Static baseline은 별도 비교에서 예상 exit 1과 MIG-017..026 ordered 10 status mismatch를
계속 냅니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -actual conformance/fixtures/godj-migration-execution-not-implemented.json
```

Recorder-backed restart 제품 adapter는 일곱 번째 manifest와 locked oracle을 직접
비교합니다. Adapter는 file-backed database를 새 backend로 다시 열어 fresh read/check/plan
경계를 실행합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json
```

Static baseline은 별도 비교에서 MIG-027..036 ordered 10 status mismatch를 계속 냅니다.
Manifest에 등록되지 않은 recorder-restart scenario는 현재도 `godjcheck`가 exit 2로
거부하고 actual output을 쓰지 않습니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  -actual conformance/fixtures/godj-migration-restart-not-implemented.json
```

Historical-state static baseline도 MIG-037..046 ordered 10 status mismatch를 냅니다. 실제
`godjcheck` adapter는 public reconstructor와 live database observation으로 10개를 실행하며,
manifest에 등록되지 않은 scenario만 exit 2/no output으로 거부합니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  -actual conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json
```

Revision-fenced migration lifecycle 제품 adapter는 public `Executor.Migrate`와 live SQLite
database로 MIG-047..056을 실행합니다. MIG-052의 canonical sibling order는 locked oracle을
바꾸지 않고 DEV-0002 sparse expectation으로만 대체합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json \
  -deviation-expected conformance/fixtures/godj-migration-lifecycle-deviation-expected.json
```

Migration definition source 제품 adapter는 public `migrations/definition.Load`와
`Set.Migrate`를 실행합니다. MIG-057..064는 Django result parity가 아닌 Accepted ADR decision
set이므로 성공 문구는 `locked reference oracle`이며, Django-derived set의 기존
`locked Django oracle` 문구와 구분합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-definition-source-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json
```

Static not-implemented fixture 비교는 MIG-057..064의 ordered status mismatch 8개와 exit 1을
계속 내야 합니다. 제품 adapter는 expected 값을 actual로 복사하지 않으며 source/header/
operation/graph mutation이 non-empty diff 또는 success/error shape rejection을 만드는지 별도
false-green test로 확인합니다.

Migration project-check 제품 adapter는 actual global kernel과 production project-linked entrypoint를
실행해 MIG-065..074를 관찰합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-project-check-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json
```

Static fixture는 계속 MIG-065..074 ordered status mismatch 정확히 10개와 exit 1을 내야 합니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-project-check-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json \
  -actual conformance/fixtures/godj-migration-project-check-not-implemented.json
```

Relation product adapter는 기존 checked-in generated bridge로 REL-001 metadata를 관찰하고, 별도
`relationqueryproduct`의 generated query companion/project bridge와 실제 SQLite rows로 REL-004의 두 forward
predicate case를 관찰합니다. `relationobjectproduct`는 independently generated descriptor seal/object bridge와
actual SQLite source rows로 REL-003 cold/warm object cache와 REL-006 null access/JOIN-0 `isnull`을 관찰합니다.
`relationreverseproduct`는 exact nine generated files의 project-only companion, freshly loaded owner accessor와
typed/dynamic reverse lookup을 actual SQLite에서 관찰합니다. `relationprefetchproduct`는 기존 exact nine-file
union에 project-only prefetch companion 하나만 더한 exact ten-file union을 사용해 owner SELECT 1회와 root
`author_id IN` batch SELECT 1회, JOIN 0, warm access 추가 query 0을 actual SQLite에서 관찰합니다.
`relationselectproduct`는 exact twelve-file union과 actual SQLite joined row scan으로 REL-009/010의
required INNER/nullable LEFT OUTER eager access를 관찰하고, 같은 resolver로 REL-011 reverse path를 pre-I/O
거부합니다. `relationdeleteproduct`는 exact thirteen-file union, 실제 `NO ACTION`/`RESTRICT` FK와 pinned
`BEGIN IMMEDIATE` transaction 안에서 REL-007의 전 incoming edge `PROTECT` count 2와 mutation 0, REL-008의
`UPDATE(2) -> DELETE(1)`/caller key clear를 관찰합니다. REL-002만 original 12-contract order 안에서 payload 없는
`not_implemented`로 남습니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/relation-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json
```

성공 stdout은 정확히
`GoDj product observations match 11 required contracts; 1 remain not implemented`입니다.
Product comparator는 registry가 요구하는 REL-002 이외의 11개 contract를 oracle과 byte-semantic하게 비교하고
locked REL-002가 관찰된 것처럼 나타나면 거부합니다. 기존 11 all-observed adapter의 strict comparator
의미는 바뀌지 않습니다. 비교가 끝나기 전에는 `-actual-output`을 만들지 않으므로 status/payload/
registry mismatch는 exit 1 또는 2, 빈 stdout, output file 없음으로 fail-closed합니다.

`not_implemented` actual은 정상 mismatch입니다. Comparator test는 result value, list
order, phase, error category/code, contractual message, DB state, metrics를 각각 변형해
false green이 생기지 않는지 검증합니다.

Write/migration set은 GDJ-0004의 제한된 제품 수직 단면에서 `passing`입니다. Generated
create/patch API, generic Manager write, SQLite transaction과 migration executor/editor/
recorder를 실행해 MOD-001..007, MIG-001..004가 oracle과 일치합니다. Checked-in
`godj-write-migration-not-implemented.json`은 구현 전 상태가 pass되지 않는다는 것을
지속적으로 확인하는 false-green fixture이며, 현재 GoDj 실행 결과를 뜻하지 않습니다.

Save lifecycle set은 GDJ-0005에서 Django exact reference로 고정했고 GDJ-0006에서 실제
generated model, `Manager.Save`와 SQLite adapter를 연결해 12개 모두 `passing`입니다.
Checked-in `godj-save-lifecycle-not-implemented.json`은 구현 전 상태가 pass되지 않는다는 ordered
12-mismatch false-green fixture로 유지하며 현재 GoDj actual을 뜻하지 않습니다.

QuerySet evaluation/cache set은 GDJ-0007에서 QRY-011..021의 exact Django 결과와
provenance를 `oracle_locked`로 먼저 고정했습니다. GDJ-0008은 generated model, generic
QuerySet과 SQLite 실제 adapter를 연결해 11개 모두 `passing`으로 전환했습니다. 네
manifest의 contract ID/scenario는 전역으로 유일하고 모든 12개 ordered cross-pair가
validation에서 거부됩니다. GDJ-0008 완료 당시 `make godj-conformance`는 M1 11개,
M2 write/migration 11개, Save 12개와 QuerySet cache 11개, 총 45개를 실행했습니다. Manifest에 등록되지 않은 임의
unknown scenario는 계속 exit 2로 fail-closed하며 actual output을 쓰지 않습니다.

두 독립 Go query-cache actual은 각각 56,283 bytes이며 SHA-256
`c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`로 서로
byte-identical합니다. Django oracle은 56,426 bytes, SHA-256
`d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`이므로 양쪽 artifact가
byte-identical한 것은 아닙니다. Result/error/DB state/metrics의 계약 의미를 comparator가
0-diff로 판정한 것입니다. Checked-in `godj-query-cache-not-implemented.json`의 ordered
11 mismatch는 현재 제품 actual이 아니라 구현 전 false-green 회귀 증거로 그대로
유지합니다. Go-native singleflight/cancellation/deep-clone/terminal gate와 전체 명령은
[EVID-20260808-007](../docs/status/TEST_EVIDENCE.md#evid-20260808-007--gdj-0008-queryset-evaluation-and-cache-product-slice)에
기록합니다.

Migration planning set은 GDJ-0009에서 MIG-005..016의 exact Django 결과와 provenance를
`oracle_locked`로 고정했습니다. 다섯 manifest의 ID/scenario는 전역으로 유일하고 모든
20개 ordered cross-pair가 validation에서 거부됩니다. Oracle은 39,139 bytes, SHA-256
`7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474`, manifest는
10,623 bytes, SHA-256
`7e8f0d19c8f227721e7cfe4254a4f39d1313e801f1ea0a759e14c46a3dbbe876`, static fixture는
1,869 bytes, SHA-256
`a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13`입니다.
GDJ-0009 완료 당시 `make godj-conformance`는 제품 adapter가 있는 45개만 실행했습니다.
Fifth-set determinism, mutation, static mismatch와 fail-closed 증거는
[EVID-20260808-008](../docs/status/TEST_EVIDENCE.md#evid-20260808-008--gdj-0009-migration-planning-compatibility-contracts)에
기록합니다.

GDJ-0010은 `migrations.NewPlanner`, `NewAppliedState`, `Planner.Plan`과
`PlanningError`를 사용하는 실제 fifth adapter를 추가했습니다. Manifest는 10,551 bytes,
SHA-256 `f51d737bd68eafae32f7942669b467e3457372873ec536a13491ded60ef27ca6`이고
MIG-005..016이 모두 `passing`입니다. GDJ-0010 완료 당시 `make godj-conformance`는 다섯 제품 set의
11 + 11 + 12 + 11 + 12, 총 57개를 실행합니다.

두 독립 Go migration-planning actual은 각각 39,094 bytes, SHA-256
`eb5bf3b6f41855684582f67b3be675da42975b8fc1ed9c7085f6d35a078eac32`로 서로
byte-identical하며 Django oracle과 protocol 의미상 12개 0-diff입니다. Logical DB state와
zero metrics는 실제 DB probe가 아니라 pure structural planner capture에서 산출합니다.
Static ordered 12 mismatch, plan 하위값/dependency/error-code mutation과 adapter source
hardcode 금지 증거는
[EVID-20260808-009](../docs/status/TEST_EVIDENCE.md#evid-20260808-009--gdj-0010-immutable-migration-planner-product-slice)에
기록합니다.

Migration plan execution set은 GDJ-0011에서 MIG-017..026의 exact Django 결과와
provenance를 `oracle_locked`로 고정했습니다. 여섯 manifest의 ID/scenario는 전역으로
유일하고 모든 30개 ordered cross-pair가 validation에서 거부됩니다. Manifest는 8,720
bytes, SHA-256
`f414cd7a495f6e6765df06ca1427485ecc16a8d19c344f190f5f1421dc2a517d`, oracle은 47,119
bytes, SHA-256
`641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`, static fixture는
1,685 bytes, SHA-256
`6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`입니다.

두 독립 random-hashseed process와 checked-in oracle은 byte-identical합니다. External
metrics는 connection summary와 compact ordered steps만 포함하고, raw render/operation/
recorder/transaction trace는 runner 내부 assertion입니다. Historical before/after는
MIG-019에만 있고 MIG-023/024는 `fault_point=before_record_write`를 명시합니다. MIG-024는
최종 schema A1, records A1/A2와 `commit` phase를 고정합니다.

GDJ-0011 완료 당시 static fixture는 ordered 10 mismatch이고 product `godjcheck`는
exit 2/no output으로 fail-closed했습니다. 당시 30 cross-binding, semantic mutation,
exact regeneration과 full gate는
[EVID-20260808-010](../docs/status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts)에
기록합니다.

GDJ-0012의 approved manifest는 9,120 bytes, SHA-256
`1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9`입니다. Django oracle과
static fixture bytes는 그대로 유지했고, sparse deviation expectation은 4,685 bytes,
SHA-256 `568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547`입니다.
두 독립 live Go actual은 각각 47,446 bytes, SHA-256
`f191d116cc38194e2019df358c31f752101fdacb005d9cc442b701d8d4afde4b`로 byte-identical합니다.
GDJ-0012 완료 당시 `make godj-conformance`는 여섯 제품 set을 실행했고 총 분류는 63
`passing` + 4 DEV-0001 `deviation`이었습니다. 상세 증거는
[EVID-20260808-011](../docs/status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록합니다.

Recorder-backed restart set은 GDJ-0013에서 MIG-027..036의 exact Django result와
provenance를 `oracle_locked`로 고정했습니다. 일곱 manifest의 ID/scenario는 전역으로
유일하고 모든 42개 ordered cross-pair가 validation에서 거부됩니다. Manifest는 10,225
bytes, SHA-256
`93e25d02208a765001760f76715ff6e9642451c5823efc62cc40b1d249dbd42b`, oracle은 33,888
bytes, SHA-256
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`, static fixture는
1,715 bytes, SHA-256
`31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`입니다.

두 독립 random-hashseed process와 checked-in oracle은 byte-identical합니다. Static
fixture는 ordered 10 mismatch이고 product `godjcheck`는 exit 2/no output입니다. Recorder
presence/identity, alias, plan order/direction, unknown/known history, restart tail과
zero-mutation mutation 증거는
[EVID-20260808-012](../docs/status/TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)에
기록합니다. GDJ-0013 완료 당시 분류는 기존 제품 `63 passing + 4 deviation`에 새 10
`oracle_locked`를 더한 것이며 77 product passing이 아니었습니다.

GDJ-0014는 manifest status만 10 `passing`으로 전환해 10,165 bytes, SHA-256
`79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290`로 만들었습니다.
Locked oracle과 static fixture는 각각 33,888 bytes/
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`, 1,715 bytes/
`31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`로 유지됩니다. 두
독립 live Go actual은 각각 33,795 bytes, SHA-256
`f9e4d3dc7078426f06a08374a36a670a36e1fa2ae08562fd08f80e91db1b31cb`로
byte-identical하며 locked oracle과 semantic 0-diff입니다. GDJ-0014 완료 당시
`make godj-conformance`의 일곱 제품 set 분류는 `73 passing + 4 deviation`이고, static ordered 10 mismatch와 42
cross-binding은 계속 false-green gate입니다. 상세 증거는
[EVID-20260808-013](../docs/status/TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)에
기록합니다.

Historical ProjectState reconstruction set은 GDJ-0015에서 MIG-037..046의 exact Django
result와 provenance를 `oracle_locked`로 고정했습니다. 여덟 manifest의 ID/scenario는
전역으로 유일하고 모든 56개 ordered cross-pair가 validation에서 거부됩니다. Manifest는
9,257 bytes, SHA-256
`04b7e92a5bbf9ff50f0247be7708dfb18a5534e40bac86a518a6b744fc0ef728`, oracle은 89,997
bytes, SHA-256
`bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`, static fixture는
1,715 bytes, SHA-256
`9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`입니다.

두 독립 random-hashseed process와 checked-in oracle은 byte-identical합니다. Logical
state는 loaded definition에서 replay하고 deliberately divergent live database는 전후
불변입니다. State의 app/model/field와 table/column/kind/primary-key/null/max-length/default,
request target/position, applied/graph와 DB/metrics mutation을 각각 검증합니다. Static
fixture는 ordered 10 mismatch이고 제품 `godjcheck`는 당시 exit 2/no output이었습니다. 기존
일곱 product set은 `73 passing + 4 deviation`이었으므로 GDJ-0015 완료 시 분류는
`73 passing + 4 deviation + 10 oracle_locked`, reference 총계는 87개였습니다. 상세 증거는
[EVID-20260808-014](../docs/status/TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)에
기록합니다.

완료된 [GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)은
Accepted [ADR-0016](../docs/adr/0016-historical-project-state-reconstruction.md)의 immutable
reconstructor와 explicit empty/latest/before/after/applied request, real SQLite recorder를
읽는 여덟 번째 adapter를 구현했습니다. Passing manifest는 9,197 bytes, SHA-256
`85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c`입니다. Locked
oracle/static bytes는 유지됐고, 두 Go actual은 각각 89,867 bytes, SHA-256
`a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`로
byte-identical하며 oracle과 protocol 의미상 10개 0-diff입니다. GDJ-0016 완료 당시 8 product
set의 분류는 `83 passing + 4 deviation`; 87 unique contract와 56 cross-binding gate를
유지합니다. 상세
증거는
[EVID-20260808-015](../docs/status/TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
기록합니다.

Migration lifecycle set은 GDJ-0017에서 MIG-047..056의 exact Django result와 provenance를
아홉 번째 manifest에 고정했습니다. Fresh/latest, applied prefix, fully-applied no-op,
named forward/reverse, app zero target, unknown legacy identity, explicit inconsistent-history
preflight, middle failure와 file-backed fresh restart를 다룹니다. Manifest는 13,680 bytes,
SHA-256
`23a9e919edff932ae781f0768aeaf7f184fe392ec53598fa18524cf50d979a8e`, oracle은 98,436
bytes, SHA-256
`7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, static fixture는
1,681 bytes, SHA-256
`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`입니다. 두 독립
random-hashseed process와 checked-in oracle은 byte-identical합니다.

아홉 set의 ID/scenario 97개는 전역으로 유일하고 72개 ordered cross-binding이 모두
거부됩니다. Static comparison은 MIG-047..056 ordered status mismatch 10개와 exit 1이고,
제품 `godjcheck`는 등록되지 않은 lifecycle scenario를 exit 2/no actual output으로
fail-closed합니다. 따라서 GDJ-0017 완료 당시 분류는 기존 `83 passing + 4 deviation`과 새
`10 oracle_locked`이며 reference 97개 전체를 제품 지원으로 표현하지 않습니다.

`lifecyclefence` spike는 현재 unfenced 조합의 first-write 전/step 사이 stale gap을 재현하고,
persistent epoch와 monotonic revision을 주 fence로, recorder identity fingerprint를 보조
integrity gate로 검증했습니다. 각 step은 pinned SQLite connection의 `BEGIN IMMEDIATE` 안에서
expected token을 확인하고 successor token, schema와 recorder를 함께 commit합니다. Conflict는
current/tail mutation 없이 last-durable `ProjectState`를 반환하고 자동 retry하지 않습니다.
Two connections/processes, bootstrap 경쟁, DDL/recorder 뒤 fault, BUSY/LOCKED 분류와 unsupported
capability fail-closed를 검증했지만 이는 제품 implementation이 아닙니다. 상세 증거는
[EVID-20260808-016](../docs/status/TEST_EVIDENCE.md#evid-20260808-016--gdj-0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike)에
기록합니다.

완료된 [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](../docs/adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 public
`Executor.Migrate`가 already-loaded definition을 exact-one opaque revision session으로 읽고,
SQLite `BEGIN IMMEDIATE` transaction에서 epoch/revision/fingerprint fence, schema, recorder와
successor token을 함께 commit하도록 제품화했습니다. Unsupported backend로 fallback하지 않고,
existing recorder의 자동 adoption도 하지 않습니다.

현재 lifecycle manifest는 13,735 bytes, SHA-256
`5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`입니다. Locked Django
oracle과 static fixture는 각각 98,436 bytes/
`7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, 1,681 bytes/
`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`로 유지됩니다.
DEV-0002 sparse expectation은 6,769 bytes, SHA-256
`58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`이며 MIG-052의
`result.plan[0]`, `result.plan[1]`, `result.plan[2]`, `metrics.steps[0]`, `metrics.steps[1]`,
`metrics.steps[2]`만 바꿉니다. 기존 DEV-0001 네 계약은 그대로입니다.

두 독립 Go actual은 각각 98,304 bytes, SHA-256
`a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical하고
reviewed product expectation과 10-contract match입니다. 이때 확정된 9 product adapter의
역사적 분류는 `92 passing + 5 deviation`이며, 97 unique contract와 72 ordered
cross-binding gate를 유지합니다.

완료된 [GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)과 Accepted
[ADR-0019](../docs/adr/0019-versioned-migration-definition-source.md)는 caller-provided bytes,
strict data-only JSON v1, tuple `(1,1,1,2)`, closed `CreateModel`/non-PK `char`·`boolean`
`AddField`, atomic loader-owned snapshot, canonical digest와 stage-major failure precedence를
MIG-057..064로 고정했습니다. MIG-064는 public Django graph/executor의 reference-only success
observation이며 digest는 handoff observation metadata이지 executor argument가 아닙니다.

GDJ-0019 completion manifest는 5,195 bytes/SHA-256
`8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`, oracle은 29,851
bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture는
1,574 bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`입니다. 갱신된
`SHA256SUMS`는 959 bytes/
`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다. Exact Python은
164개 모두 통과했고 portable run은 149 passed/15 skipped입니다. 열 set의 105 ID/scenario와
90 ordered cross-binding도 검증했습니다.

완료된 [GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)과 Accepted
[ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 새 leaf package
`migrations/definition`에 파일 I/O 없는 `Load(...Source) (Set, LoadReport, error)`를
구현했습니다. Zero `Set`은 canonical empty set이고, source/document와 반환 accessor는
loader-owned deep copy입니다. Raw document는 set에 보존하지 않습니다. `Set.Migrate`는 fresh
definition copy와 immutable request value만 기존 `Executor.Migrate`에 정확히 한 번 넘깁니다.

Loader의 exact numeric cap은 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB,
JSON depth 64, document JSON values 65,536, batch JSON values 262,144, migration별 dependencies
2,047, operations 2,048, `CreateModel` fields 2,048입니다. Strict scanner는 invalid UTF-8/BOM,
trailing value, any-depth duplicate member, surrogate와 integer lexeme를 closed JSON 의미로
검증하고, canonical RFC 6901 failure order를 bounded lazy path comparator로 선택합니다.
Source-owned failure만 9개 `migration_definition_source_error` code로 분류하며 resource breach는
별도 code 없이 `reason=resource_limit_exceeded`와 stable limit/maximum/actual로 보고합니다.
Graph failure는 raw `*migrations.PlanningError`, lifecycle failure는 기존 raw error identity와
`errors.As` 의미를 보존합니다.

GDJ-0020 status-only manifest는 5,147 bytes/SHA-256
`688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488`입니다. Oracle 29,851
bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture 1,574
bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`와 `SHA256SUMS`
959 bytes/`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`는 변경하지 않았습니다.
MIG-057..064는 decision-reference actual 8 `passing`이며 현재 10 adapter/105 contract의 제품
분류는 `100 passing + 5 deviation`; 90 ordered cross-binding gate도 유지합니다. 두 독립 product
actual은 각각 29,631 bytes/SHA-256
`a3f40f9bbee06d4edc4af0a00f40a76da259207995ac20d030101aa2ec3aec87`로 서로 byte-identical하고
locked reference oracle과 protocol difference 0입니다. Cross-runtime raw JSON byte 동일성은
계약이 아닙니다.

제품 commit `6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1의
[run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)에서 Ubuntu 24.04
portable job과 macOS 15 arm64 exact job이 모두 통과했습니다. Ubuntu job은
`CGO_ENABLED=0 GOARCH=386 go test -count=1 ./migrations/definition`을 실제 Linux/386 runtime에서
실행했습니다. File/directory/module/remote discovery, public migration CLI, writer/upgrade/cache,
custom/executable/data/raw-SQL operation과 PostgreSQL/MySQL 등 non-SQLite lifecycle backend는
여전히 미지원입니다.

GDJ-0021 reference artifact의 MIG-065..074는 당시 exact 10 `oracle_locked`였습니다. Manifest는
4,580 bytes/SHA-256
`0cd8d77b03820af75c8bda8434620f40acd1a3cb6319cf4fb732db4b38d44218`, oracle은 19,971
bytes/`49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, static fixture는
1,729 bytes/`86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`입니다. 기존
`SHA256SUMS` 10줄을 byte-identical prefix로 보존하고 11번째 oracle line만 append한 파일은
1,061 bytes/SHA-256
`74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`입니다. 이 artifact는
Django 결과 parity가 아니라 Accepted ADR-0021의 독립 GoDj decision oracle입니다.

GDJ-0022 status-only manifest는 4,520 bytes/SHA-256
`0bbf254e80fea17b52070d0589da5ddcd401ff67440062a89b4fcd3e8309c048`입니다. Oracle, static fixture와
`SHA256SUMS` bytes는 위 GDJ-0021 값에서 바꾸지 않았습니다. Actual product adapter는 injected
in-process backend를 통해 global kernel을 실행하고 runner stage에서 같은 production linked
entrypoint를 호출해 두 actual report를 결합합니다. MIG-065..074는 10 `passing`, 현재 제품 분류는
11 adapter/115 contract의 `110 passing + 5 deviation`이며 static fixture는 계속 ordered 10 mismatch를
만듭니다.

GDJ-0023 relation reference manifest는 10,842 bytes/SHA-256
`08124b420e6313e4c2c1a5be32a3bdd29d831f02f1479bc3591af6f8f7da1522`였고 REL-001..012 모두
`oracle_locked`였습니다. GDJ-0024 status-only manifest는 10,836 bytes/SHA-256
`1a844ae1f0da7226b0dd936ee5b3eb884144e4caaf829ec2f6c822ab361b4254`입니다. GDJ-0025는 REL-004만
`passing`으로 바꾼 10,830 bytes/SHA-256
`944be1b941b9217ed27c2f6d5a33662cdfafc23f0c7698cad5ebb80849b633f0` status-only manifest입니다. GDJ-0026은
REL-003/006만 추가로 `passing`으로 바꾼 10,818 bytes/SHA-256
`e548332401932059a87920f90fb7a1300aa02e3c5775335e3b6eda90cc84293a` status-only manifest입니다.
GDJ-0027은 REL-005만 추가로 `passing`으로 바꾼 10,812 bytes/SHA-256
`640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136` status-only manifest입니다.
GDJ-0028은 REL-012만 추가로 `passing`으로 바꾼 10,806 bytes/SHA-256
`70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477` status-only manifest입니다.
GDJ-0029는 REL-009/010/011만 추가로 `passing`으로 바꾼 10,788 bytes/SHA-256
`64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141` status-only manifest입니다.
GDJ-0030 active implementation은 REL-007/008만 추가로 `passing`으로 바꾼 10,776 bytes/SHA-256
`3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46` status-only manifest입니다.
Oracle 33,792
bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, 12-line
`SHA256SUMS` 1,148 bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`는
바꾸지 않았습니다. 기존 REL-001/004 fixture bytes도 보존합니다. 별도 checked-in REL-004 fixture는 v2 target/v3
source schema에서 main/metadata/query companion과 두 project bridge를 실제 재생성하고 manual FK-enabled
SQLite fixture의 real QuerySet 결과를 관찰합니다. REL-003/006 fixture는 additive object companions/project
object bridge를 재생성하고 actual SQLite loader/cache/source-key Plan을 관찰합니다. REL-005 fixture는 authors
main/metadata/object, blog main/metadata/query/object와 project binding/reverse companion의 exact nine files를
재생성하고 accessor `[10,11]`과 lookup `[1]`을 관찰합니다. REL-012 fixture는 이 exact nine-file union을
바꾸지 않고 project prefetch companion 하나를 더해 exact ten files를 재생성하며 `[1:[10,11],2:[],3:[12]]`,
두 SELECT, root `author_id IN`, key count 3, JOIN 0과 warm access extra query 0을 관찰합니다.
REL-009/010/011 fixture는 기존 object product 아홉 파일에 app-local projection companion 두 개와 project
select-related companion 하나를 더한 exact twelve files를 재생성합니다. Required plain/eager 4-vs-1 SELECT와
INNER 1, nullable LEFT OUTER 1, warm access extra query 0, reverse `posts` path의 query/mutation 0을 관찰합니다.
REL-007/008 fixture는 같은 prerequisite union에 project relation-delete companion 하나를 더한 exact thirteen files를
재생성하고, 실제 FK schema에서 `PROTECT` distinct source count 2와 mutation 0, `SET_NULL` UPDATE 2행 뒤 target
DELETE 1행 및 하나의 transaction을 관찰합니다. 모든 actual은 oracle/static expected artifact를 import하지 않습니다.

Required workflow topology는 full/exact 2 + independent project-check proof 4 + relation-binding proof 4 +
SQLite 4 + actual project-check product 4 + Python compatibility 4의 existing exact 22를 보존하고,
relation-product Linux/macOS x64/arm64 4개를 더한 exact 26 executions입니다. 각 relation-product leg는
normal/race/CGO-disabled/vet, generated fixture/compile proof, artifact no-rewrite와 clean worktree를
검증합니다. Exact top-level package inventory는 687 run/687 pass/0 skip이고, encoded inventory는 69,597
bytes/SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`입니다.
Python compatibility
matrix는 Ubuntu 24.04에서 CPython 3.12.13, 3.13.15, 3.14.3, 3.14.7과 Django 6.1/asgiref
3.12.1/sqlparse 0.5.5와 uv 0.12.3을 isolated하게 고정하고 portable 193 tests/17 intentional skips 및
127 scenario payload 498,051 bytes/SHA-256
`2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`을 검증합니다. 이는 checked-in
exact oracle identity를 넓히지 않으며 기존 CPython 3.14.3 profile/oracle/uv.lock job이 계속 유일한
oracle regeneration 경계입니다.

## Provenance

현재 query/write/migration/Save/QuerySet evaluation-cache/migration-planning/execution/
recorder-restart/historical-state/lifecycle/definition-source scenario는 Django 코드를
번역하지 않고 GoDj 고유 fixture로 독립적으로
작성했습니다. Static migration fixture도 public `migrate` 경로를 관찰하기 위한 독립
정의입니다. Manifest의
upstream 문서/test reference는 동작 근거와 버전을 추적하기 위한 것입니다. MIG-057..064는
모두 Accepted ADR-0019 decision provenance를 가지며, Django behavior를 실제 관찰한
MIG-057/MIG-064만 pinned Django provenance를 별도로 가집니다. 파생물
분류와 고지 규칙은 `docs/LICENSING.md`와 `NOTICE.md`를 따릅니다.
MIG-065..074는 Accepted ADR-0021 decision provenance만 가지며 Django source/test를
참조하지 않습니다. Django-named exact profile과 oracle directory 재사용은 corpus 관리
경계일 뿐 Django-derived 분류가 아닙니다. REL-001..012는 Django 6.1 commit에 고정된 documentation/test
provenance를 가지지만 scenario와 GoDj product fixture는 독립 작성했으며 Django source를 번역하지
않았습니다.
