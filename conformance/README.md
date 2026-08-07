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
추가했습니다. 이 set은 아직 10 `oracle_locked`이고 GoDj product adapter는 fail-closed합니다.
제품용 Schema/ORM/SQLite 구현은 루트의 `schema`, `codegen`, `query`, `orm`, `db`
package에 있으며 이 디렉터리는 그 동작을 oracle에 연결합니다.

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
| `runners/django` | 명시적인 Django scenario와 type-preserving normalizer |
| `runners/godj` | M1 read, M2 write/migration/Save, QuerySet cache, migration planning과 plan execution 제품 package를 실행하는 여섯 GoDj observation adapter; restart set은 아직 미지원 |
| `oracles/**/*.json` | Django runner가 만든 byte-deterministic expected observation |
| `oracles/**/SHA256SUMS` | checked-in oracle byte checksum |
| `internal/protocol` | strict decoder, validator, canonical value, comparator |
| `fixtures/godj*.json` | 미구현 상태가 pass되지 않는 set별 protocol fixture와 reviewed sparse deviation expectation |
| `codegenbootstrap` | Q-001 package bootstrap 실행 실험 |
| `cmd/godjcheck` | GoDj observation을 생성해 locked Django oracle과 비교 |

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

Recorder-backed restart set은 제품 adapter가 아직 없으므로 `godjcheck`가 exit 2와 actual
output 미생성으로 fail-closed해야 합니다. Static baseline은 별도 비교에서 MIG-027..036
ordered 10 status mismatch를 냅니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  -actual conformance/fixtures/godj-migration-restart-not-implemented.json
```

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
`make godj-conformance`는 여섯 제품 set을 실행하며 총 분류는 63 `passing` + 4
DEV-0001 `deviation`입니다. 상세 증거는
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
기록합니다. 현재 분류는 기존 제품 `63 passing + 4 deviation`에 새 10
`oracle_locked`를 더한 것이며 77 product passing이 아닙니다.

## Provenance

현재 query/write/migration/Save/QuerySet evaluation-cache/migration-planning/execution/
recorder-restart scenario는 Django 코드를
번역하지 않고 GoDj 고유 fixture로 독립적으로
작성했습니다. Static migration fixture도 public `migrate` 경로를 관찰하기 위한 독립
정의입니다. Manifest의
upstream 문서/test reference는 동작 근거와 버전을 추적하기 위한 것입니다. 파생물
분류와 고지 규칙은 `docs/LICENSING.md`와 `NOTICE.md`를 따릅니다.
