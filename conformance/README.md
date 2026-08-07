# GoDj Compatibility Lab

이 디렉터리는 Django reference profile, contract manifest, normalized observation,
comparator, M0 codegen bootstrap spike와 M1 GoDj observation adapter를 보관합니다.
제품용 Schema/ORM/SQLite 구현은 루트의 `schema`, `codegen`, `query`, `orm`, `db`
package에 있으며 이 디렉터리는 그 동작을 oracle에 연결합니다.

## 정본과 생성물

| 경로 | 역할 |
|---|---|
| `profiles/*.json` | exact reference runtime과 dependency lock fingerprint |
| `contracts/manifest.json` | 순서가 있는 초기 contract와 provenance, 비교 차원 |
| `runners/django` | 명시적인 Django scenario와 type-preserving normalizer |
| `runners/godj` | M1 제품 package를 실행하는 GoDj observation adapter |
| `oracles/**/oracle.json` | Django runner가 만든 byte-deterministic expected observation |
| `internal/protocol` | strict decoder, validator, canonical value, comparator |
| `fixtures/godj-not-implemented.json` | 미구현 상태가 pass되지 않는 protocol fixture |
| `codegenbootstrap` | Q-001 package bootstrap 실행 실험 |
| `cmd/godjcheck` | GoDj observation을 생성해 locked Django oracle과 비교 |

Machine-readable manifest는 실행 입력의 정본입니다. 사람이 보는 진행 상태는
`docs/status/IMPLEMENTATION_MATRIX.md`가 요약하며 두 파일의 상태를 같은 변경에서
갱신합니다.

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

GoDj M1 SQLite backend는 별도 실행 환경입니다. 현재 module pin은
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

두 observation을 직접 비교할 수 있습니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json \
  -actual path/to/godj-observation.json
```

현재 GoDj 구현을 oracle과 직접 비교하려면 다음을 실행합니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
```

`not_implemented` actual은 정상 mismatch입니다. Comparator test는 result value, list
order, phase, error category/code, contractual message, DB state, metrics를 각각 변형해
false green이 생기지 않는지 검증합니다.

## Provenance

초기 scenario는 Django 코드를 번역하지 않고 독립적으로 작성했습니다. Manifest의
upstream 문서/test reference는 동작 근거와 버전을 추적하기 위한 것입니다. 파생물
분류와 고지 규칙은 `docs/LICENSING.md`와 `NOTICE.md`를 따릅니다.
