# 테스트·검증 증거

- 마지막 갱신: 2026-08-08
- 현재 GoDj 코드·호환 계약 테스트 증거: EVID-20260808-009

이 파일은 실제로 실행한 검증만 기록합니다. 계획된 명령이나 다른 checkout의 결과를 현재 통과처럼 기록하지 않습니다.

## Evidence 형식

```markdown
### EVID-YYYYMMDD-NNN — 제목

- Date/time:
- Work/contract IDs:
- Checkout/commit:
- Environment/backend:
- Command:
- Exit status:
- Result summary:
- Failures/skips:
- Artifacts:
- Notes:
```

## EVID-20260807-001 — Documentation foundation validation

- Date/time: 2026-08-07T16:29:02+09:00
- Work/contract IDs: GDJ-0000
- Checkout/commit: `main` at `fe7bf44`, 문서 32개가 untracked인 상태
- Environment/backend: macOS, `markdown-it-py 4.0.0`; DB 적용 없음
- Exit status: 0
- Result summary: 33개 Markdown 파일 parse, local link, H1/heading level, trailing whitespace, Git whitespace 검사 통과
- Failures/skips: 실패 없음. Go source/test, differential runner, CI가 없어 코드 테스트는 실행하지 않음.
- Artifacts: 기존 `README.md`와 신규 `AGENTS.md`, `docs/**/*.md`, `prompts/*.md`, `work/*.md`; 신규 Markdown 32개는 untracked

실행한 명령:

```bash
markdown-it $(rg --files -g '*.md' | sort) >/dev/null
```

```bash
python3 - <<'PY'
from pathlib import Path
import re

root = Path('.').resolve()
errors = []
for path in sorted(root.rglob('*.md')):
    raw = path.read_text()
    text = re.sub(r'```.*?```', '', raw, flags=re.S)
    text = re.sub(r'`[^`]*`', '', text)
    for match in re.finditer(r'(?<!!)\[[^\]]+\]\(([^)]+)\)', text):
        target = match.group(1).strip().split('#', 1)[0]
        if target and not target.startswith(('http://', 'https://', 'mailto:')):
            if not (path.parent / target).resolve().exists():
                errors.append(f'{path.relative_to(root)}: missing {target}')
    in_code = False
    previous = 0
    h1 = 0
    for number, line in enumerate(raw.splitlines(), 1):
        if line.startswith('```'):
            in_code = not in_code
            continue
        if in_code:
            continue
        heading = re.match(r'^(#{1,6})\s+', line)
        if not heading:
            continue
        level = len(heading.group(1))
        h1 += level == 1
        if previous and level > previous + 1:
            errors.append(f'{path.relative_to(root)}:{number}: H{previous}->H{level}')
        previous = level
    if h1 != 1:
        errors.append(f'{path.relative_to(root)}: H1 count {h1}')
if errors:
    print('\n'.join(errors))
    raise SystemExit(1)
print(f'local-links/headings: OK ({len(list(root.rglob("*.md")))} files)')
PY
```

```bash
if rg -n '[[:blank:]]+$' -g '*.md' .; then exit 1; fi

failed=0
while IFS= read -r file; do
  output=$(git diff --no-index --check -- /dev/null "$file" 2>&1) || code=$?
  code=${code:-0}
  if [ -n "$output" ] || [ "$code" -gt 1 ]; then
    echo "$output"
    failed=1
  fi
  unset code
done < <(rg --files -g '*.md' | sort)
[ "$failed" -eq 0 ]

git status --short --untracked-files=all
```

## EVID-20260807-002 — GDJ-0001 Compatibility Lab

- Date/time: 2026-08-07T18:10:58+09:00
- Work/contract IDs: GDJ-0001, META-001, META-002, QRY-001..QRY-010,
  SCH-001, GEN-010
- Checkout/commit: `main` at
  `927788d28f964a9597ff0962138bc56e78de7b14`; 실행 시 미커밋 변경은 이 evidence와
  CURRENT/work 상태 문서뿐이며 구현 source는 commit과 동일
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5, uv 0.10.12,
  CPython 3.14.3, Django 6.1, SQLite 3.50.4, source ID
  `2025-07-30 19:33:53 4d8adfb30e03f9cf27f800a2c1ba3c48fb4ca1b08b0f5ed59a4d5ecbf45e20a3`,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: 모든 합격 gate 0; 의도적 `not_implemented` 비교는 예상대로 1
- Result summary: Go top-level test 21개 pass, race pass, vet pass; Python exact
  test 10개 pass; 11개 contract/profile/oracle validation pass; oracle byte check와
  SHA-256 pass; Markdown 38개 parse/link/heading/whitespace와 workflow YAML parse pass
- Failures/skips: 실패 없음. Portable Python run에서 exact darwin profile test 2개를
  명시적으로 skip하고 exact run에서 두 테스트 모두 pass. GitHub-hosted workflow는
  아직 push되지 않아 원격 실행하지 않음.
- Artifacts: `conformance/contracts/manifest.json`, exact profile, oracle
  `0fc307d8be596c993bd1424c365de8c17ae9ace626d603e2e62272011845b7b0`,
  protocol/runner tests, codegen bootstrap fixture, `.github/workflows/ci.yml`
- Notes: 일반 Linux CI는 portable validation만 수행하고 darwin/arm64 oracle을
  재현했다고 주장하지 않음. CI YAML은 syntax parse와 로컬 동일 명령만 검증했으며
  GitHub Actions service execution은 미수집.

실행한 핵심 명령:

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
make check
```

`make check`는 다음을 포함했습니다.

```bash
go test ./...
LC_ALL=C TZ=UTC uv run --frozen python -m unittest discover \
  -s conformance/runners/django/tests -v
go run ./conformance/cmd/contractcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -suite conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
go run ./conformance/cmd/contractcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -suite conformance/fixtures/godj-not-implemented.json
GODJ_EXACT_PROFILE=1 LC_ALL=C TZ=UTC uv run --frozen python \
  -m unittest discover -s conformance/runners/django/tests -v
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json \
  --check
```

False-green 확인:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json \
  -actual conformance/fixtures/godj-not-implemented.json
```

예상 결과는 exit 1과 `not_implemented` mismatch 11개였고 실제 결과가
일치했습니다. Comparator unit test는 result value/order, phase, error category/code,
contractual message, DB state, metrics mutation도 각각 실패로 판정했습니다.

## EVID-20260808-001 — GDJ-0002 Model-to-Query Walking Skeleton

- Date/time: 2026-08-08T01:12:35+09:00
- Work/contract IDs: GDJ-0002, SCH-M1-001, GEN-M1-001, QRY-001..QRY-010,
  SCH-001, QRY-M1-001, DB-SQLITE-001
- Checkout/commit: `main` at
  `bb9225df91f12f2faaa3d50da5b9555819fe0256`; 실행 시 미커밋 변경은 완료 상태와
  다음 work handoff 문서 `docs/ROADMAP.md`, `docs/status/CURRENT.md`,
  `work/0002-model-to-query-walking-skeleton.md`, `work/README.md`, `work/0003-*`뿐이며
  product/conformance source는 commit과 동일
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  `modernc.org/sqlite v1.56.0`, `modernc.org/libc v1.74.4`, Go backend SQLite 3.53.3;
  uv 0.10.12, CPython 3.14.3, Django 6.1 reference SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: final `make check`와 focused SQLite fingerprint test 0
- Result summary: generated drift/target compile, 전체 Go test/vet/race,
  `CGO_ENABLED=0`, external positive/negative compile fixtures, Python portable/exact
  11-test suite, protocol/artifact validation, GoDj-vs-Django 11-contract differential,
  oracle byte check 모두 통과
- Failures/skips: portable Python run에서 exact profile test 2개가 의도적으로 skip되고
  exact run에서는 모두 pass. Final 이전 첫 `make check`는 manifest가 `passing`인데
  Django runner가 `oracle_locked`만 허용해 실패했으며, locked 이후 lifecycle
  (`red`/`passing`/`deviation`)을 허용하고 regression test를 추가한 commit에서 재실행해
  통과. GitHub-hosted CI는 push하지 않아 실행하지 않음.
- Artifacts: generated `examples/article/models/zz_godj_generated.go`, GoDj observation
  adapter/command, manifest 11개 `passing`, schema hash
  `745a63388b268f0ff1331e516473a73655563b3a7ca77f5c1005b0aeb16677b2`, oracle
  `0fc307d8be596c993bd1424c365de8c17ae9ace626d603e2e62272011845b7b0`
- Notes: Django reference profile SQLite 3.50.4와 Go backend SQLite 3.53.3을 별도
  fingerprint로 기록함. SQL 문자열 동일성이 아니라 manifest의 결과/오류/DB state/
  metrics를 비교함.

실행한 최종 gate:

```bash
make check
```

`make check`는 다음 M1 gate를 포함했습니다.

```bash
go run ./internal/cmd/m1generate -check
go test ./...
go vet ./...
CGO_ENABLED=0 go test ./db/sqlite ./conformance/runners/godj -count=1
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
go test -race ./...
GODJ_EXACT_PROFILE=1 LC_ALL=C TZ=UTC uv run --frozen python \
  -m unittest discover -s conformance/runners/django/tests -v
LC_ALL=C TZ=UTC uv run --frozen python -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/manifest.json \
  --output conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json --check
```

Go backend runtime fingerprint 확인:

```bash
go list -m -f '{{.Path}} {{.Version}}' modernc.org/sqlite modernc.org/libc
go test ./db/sqlite -run TestSQLiteBackendInterruptsRunningStatement -count=1 -v
```

출력은 `modernc.org/sqlite v1.56.0`, `modernc.org/libc v1.74.4`,
`backend=modernc.org/sqlite sqlite=3.53.3`이었습니다.

## EVID-20260808-002 — GDJ-0003 Write-Migration Compatibility Contracts

- Date/time: 2026-08-08T02:09:38+09:00
- Work/contract IDs: GDJ-0003, META-001, META-002, MOD-001..MOD-007,
  MIG-001..MIG-004, Q-006, Q-012
- Checkout/commit: `main` at
  `3e7c87839265e1b07b6d69f59f52e596623b1eb5`; 실행 시 미커밋 변경은 완료 상태와
  다음 work handoff 문서뿐이며 product/conformance source와 machine artifact는 commit과
  동일
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5, uv 0.10.12,
  CPython 3.14.3, Django 6.1 reference SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`;
  M1 Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3
- Exit status: `make check`, 전체 `CGO_ENABLED=0` Go test와 checksum은 0;
  M2 static actual 비교와 두 cross-set 비교는 예상한 mismatch로 각각 1
- Result summary: protocol v2 expected phase/profile/ordered set/payload binding, M1
  GoDj differential 11개 `passing`, M2 manifest 계약 11개 `oracle_locked`와 Django
  oracle suite 11개 `observed`, 전체
  Go test/vet/race, Python portable 27개와 exact 27개, deterministic oracle byte check,
  checksum, 외부 DB 보존, manifest-output 결속과 mutation false-green gate 통과
- Failures/skips: 예상하지 않은 실패 없음. Portable Python run은 exact-profile 전용
  3개를 명시적으로 skip하고 exact run에서 모두 통과. 각 scenario 실행 뒤 table,
  transaction, rollback과 recorder baseline도 검증합니다. M2 GoDj 제품 adapter가 없어
  static actual의 11개 `not_implemented` mismatch가 예상됨. GitHub-hosted workflow는
  push하지 않아 실행하지 않음.
- Artifacts: M1 v2 oracle
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 v2 oracle
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`, protocol v2
  manifests/profile/fixtures, ADR-0009/0010
- Notes: EVID-20260807-002와 EVID-20260808-001의 v1 hash는 당시 checkout의 역사적
  증거로 유지합니다. v2는 expected phase를 wire contract에 결속하도록 envelope를
  명시적으로 올렸고 M1 외부 동작 11개는 새 artifact에서도 계속 일치합니다.

실행한 최종 gate:

```bash
make check
CGO_ENABLED=0 go test ./... -count=1
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
```

`make check`는 generation drift, 전체 Go test/vet/race와 format, portable/exact Python
suite, 두 contract set validation, M1 GoDj differential과 두 oracle regeneration byte
check를 포함했습니다.

M2의 명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/write-migration-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json \
  -actual conformance/fixtures/godj-write-migration-not-implemented.json
```

예상대로 exit 1과 ordered MOD 7개, MIG 4개의 `not_implemented` status mismatch가
발생했습니다. 실제 checked-in artifact의 양방향 cross-set 결속도 확인했습니다.

```bash
go run ./conformance/cmd/contractcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -suite conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json

go run ./conformance/cmd/contractcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/write-migration-manifest.json \
  -suite conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
```

두 명령 모두 ordered contract ID/position 차이로 exit 1이었습니다. Expected phase
결속은 별도의 protocol mutation test에서 검증했습니다.

## EVID-20260808-003 — GDJ-0004 Write and Migration Walking Skeleton

- Date/time: 2026-08-08T03:00:47+09:00
- Work/contract IDs: GDJ-0004, MOD-001..MOD-007, MIG-001..MIG-004,
  SCH-M1-001, GEN-M1-001, Q-006, Q-012
- Checkout/commit: clean `main` at
  `de099f31738c1df0dcc4c6ffd609d0fb4f0d4683`; 제품 구현 `e337a95`, M2 adapter와
  manifest passing 전환 `84d50f3`, 최종 경계 hardening `de099f3`
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  `modernc.org/sqlite v1.56.0`, `modernc.org/libc v1.74.4`, Go SQLite 3.53.3;
  uv 0.10.12, CPython 3.14.3, Django 6.1 reference SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: `make check`, 전체 `CGO_ENABLED=0` Go test와 oracle checksum 모두 0;
  static not-implemented 비교는 예상대로 1과 11 mismatch
- Result summary: Schema IR v2/default, generated immutable create/patch, generic one-row
  write, SQLite Atomic, ProjectState/typed migration Executor와 SQLite editor/recorder를
  구현했습니다. 전체 Go test/vet/race, generation drift, external compile, Python
  portable/exact 27-test suite, oracle byte check가 통과했고 M1 11개와 M2 11개가 각각
  Django oracle과 0-diff입니다.
- Failures/skips: 예상하지 않은 실패 없음. Portable Python은 exact-profile 전용 3개를
  skip하고 exact run에서는 27개 모두 pass. GitHub-hosted workflow는 push하지 않아
  실행하지 않음.
- Artifacts: generated schema hash
  `b10fcd2ffbc2369355c165abef4725178c04bb9a6055f77f31214188aad37621`, M1 oracle
  `e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869`, M2 oracle
  `35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac`, 두 manifest
  각각 11 `passing`
- Notes: independent review에서 nullable pointer alias, SQLite identifier case-fold,
  AddField default backfill과 CHECK/generated-column DROP dependency false-green을
  재현했습니다. 최종 commit은 deep clone/ASCII canonicalization과 table-rebuild
  capability error, 회귀 테스트로 네 경계를 닫았습니다. SQL 문자열 동일성이 아니라
  결과/DB state/error/transaction recovery를 비교합니다.

실행한 최종 gate:

```bash
make check
CGO_ENABLED=0 go test ./... -count=1
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
```

`make check`는 다음을 포함했습니다.

```bash
go run ./internal/cmd/m1generate -check
go test ./...
go vet ./...
go test -race ./...
CGO_ENABLED=0 go test ./db/sqlite ./conformance/runners/godj -count=1
PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv run --frozen python \
  -m unittest discover -s conformance/runners/django/tests -v
GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC \
  uv run --frozen python -m unittest discover -s conformance/runners/django/tests -v
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/write-migration-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json
```

두 `godjcheck` 명령은 각각 `11 contracts` 일치를 출력했습니다. Checked-in
`godj-write-migration-not-implemented.json` 비교는 ordered MOD 7개와 MIG 4개가
정확히 11개의 `not_implemented` status mismatch를 내는지 별도로 확인했습니다.

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/write-migration-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json \
  -actual conformance/fixtures/godj-write-migration-not-implemented.json
```

이 명령은 의도한 exit 1과 `observationcmp: 11 difference(s)`를 반환했습니다.

## EVID-20260808-004 — GDJ-0005 Save Lifecycle Compatibility Contracts

- Date/time: 2026-08-08T03:25:49+09:00
- Work/contract IDs: GDJ-0005, META-001, META-002, MOD-008..MOD-019, Q-006
- Checkout/commit: conformance source와 machine artifact가 clean `main` commit
  `138581da38bfbb6ba89ea5ca82752dfd3d76df02`; evidence 작성 시 미커밋 변경은 완료
  상태와 다음 work handoff 문서뿐
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  `modernc.org/sqlite v1.56.0`, Go SQLite 3.53.3; uv 0.10.12,
  CPython 3.14.3, Django 6.1 reference SQLite 3.50.4/source ID
  `2025-07-30 19:33:53 4d8adfb30e03f9cf27f800a2c1ba3c48fb4ca1b08b0f5ed59a4d5ecbf45e20a3`,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: final `make check`, full `CGO_ENABLED=0` Go test, focused protocol race,
  two-process oracle comparison과 checksum 모두 0; static actual 비교는 예상대로 1과
  12 mismatch
- Result summary: fully loaded default save, named/empty `update_fields`, force error,
  explicit PK UPDATE/INSERT fallback과 rollback object/DB state를 12개 독립 계약으로
  고정했습니다. Python portable 36-test에서 exact 4개를 skip하고 exact run은 36개
  모두 pass했습니다. 세 set의 전역 ID/scenario uniqueness, 6개 cross-binding과
  Save payload mutation 9개가 false green 없이 실패했습니다. 기존 GoDj M1/M2
  differential은 각각 11개 0-diff를 유지했습니다.
- Failures/skips: 예상하지 않은 실패 없음. Save 제품 adapter는 이 작업 범위 밖이므로
  static fixture가 MOD-008..019의 12개 `not_implemented` status mismatch를 내는 것이
  기대 결과입니다. GitHub-hosted workflow는 push하지 않아 실행하지 않았습니다.
- Artifacts: `save-lifecycle-manifest.json` 12개 `oracle_locked`, exact oracle SHA-256
  `05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`, Django runner,
  static fixture, 3-set artifact/checksum/mutation gate
- Notes: initial `/tmp` probe와 최종 runner 모두 independent process determinism을
  확인했지만 정본 hash는 checked-in runner oracle의 위 값입니다. `_state.adding` 등
  Django private state와 raw SQL 문자열은 payload에서 제외했습니다. Pinned Django
  commit의 모든 manifest 문서 heading과 test class/method reference가 존재함을
  재확인했습니다.

실행한 최종 gate:

```bash
make check
CGO_ENABLED=0 go test ./... -count=1
go test -race ./conformance/internal/protocol -count=1
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
```

`make check`는 generation drift, 전체 Go test/vet/race, focused `CGO_ENABLED=0`, Python
portable/exact suite, 세 manifest/oracle/fixture validation, 기존 M1/M2 GoDj
differential과 세 oracle byte check를 포함했습니다.

Save oracle의 별도 process determinism:

```bash
probe_dir=$(mktemp -d /tmp/godj-save-oracle.XXXXXX)
LC_ALL=C TZ=UTC PYTHONHASHSEED=random uv run --frozen python \
  -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/save-lifecycle-manifest.json \
  --output "$probe_dir/one.json"
LC_ALL=C TZ=UTC PYTHONHASHSEED=random uv run --frozen python \
  -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/save-lifecycle-manifest.json \
  --output "$probe_dir/two.json"
cmp "$probe_dir/one.json" "$probe_dir/two.json"
cmp "$probe_dir/one.json" \
  conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json
```

두 임시 파일과 checked-in oracle 모두 같은 SHA-256
`05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`를 냈습니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/save-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json \
  -actual conformance/fixtures/godj-save-lifecycle-not-implemented.json
```

이 명령은 의도한 exit 1, ordered MOD-008..019 status mismatch 12개와
`observationcmp: 12 difference(s)`를 반환했습니다.

## EVID-20260808-005 — GDJ-0006 Save Lifecycle Product Slice

- Date/time: 2026-08-08T04:02:11+09:00
- Work/contract IDs: GDJ-0006, MOD-008..MOD-019, GEN-M1-001, Q-006
- Checkout/commit: 제품·adapter·manifest 상태가 clean `main` commit
  `af0bc7992cc156f118f75b04f658162ae5dbbb07`; evidence 작성 시 미커밋 변경은
  GDJ-0006 완료와 GDJ-0007 handoff 문서뿐
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  `modernc.org/sqlite v1.56.0`, Go SQLite 3.53.3; uv 0.10.12,
  CPython 3.14.3, Django 6.1 reference SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: `make check`, uncached full Go test/race/CGO=0, format/whitespace,
  generation, three oracle checksum과 two-process actual comparison 모두 0; static
  not-implemented comparison은 예상대로 1과 12 mismatch
- Result summary: concrete generic `SaveOption[M]`, sealed model-specific
  `WritableField[M]`, `Manager[M].Save`, generated explicit-key/option helper와
  structured SQLite primary-key 오류를 구현했습니다. Default/partial/empty/force,
  explicit-key UPDATE→INSERT와 outer transaction rollback semantics가 MOD-008..019에서
  Django oracle과 0-diff입니다. 기존 M1/M2 22개를 포함한 총 34개 contract가
  `passing`입니다.
- Failures/skips: 예상하지 않은 실패 없음. Portable Python 36-test는 exact-profile
  전용 4개를 skip했고 exact run은 36개 모두 pass했습니다. GitHub-hosted workflow는
  push하지 않아 실행하지 않았습니다.
- Artifacts: product/conformance commit `af0bc79`, generated code version
  `godj-codegen-m2-v2`, Save oracle SHA-256
  `05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb`,
  GoDj Save actual SHA-256
  `bc129818165d1ea147afa39a083964bcad710f744b341d4f083fdac2581dd225`
- Notes: actual 두 파일은 independent process에서 각각 11,743 bytes로
  byte-identical했습니다. 독립 제품 감사에서 P0–P3 결함이 없었습니다. Read-only
  mutation audit가 adapter metrics 하드코딩과 SQLite error-string 분류를 전체 test의
  false green으로 재현했고, 임의 contract recorder sequence와 opaque wrapped
  structured error 회귀를 추가한 뒤 두 mutation이 실패함을 다시 확인했습니다.

실행한 최종 gate:

```bash
make check
go test ./... -count=1
go test -race ./... -count=1
CGO_ENABLED=0 go test ./... -count=1
gofmt -d codegen conformance db examples internal orm query
git diff --check
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
```

`make check`는 generation drift, 전체 Go test/vet/race, focused `CGO_ENABLED=0`, Python
portable/exact 36-test, 세 manifest/oracle/fixture validation, 세 oracle byte check와
M1/M2/Save differential을 실행했습니다. 별도 uncached full 명령은 같은 checkout에서
각각 통과했습니다.

GoDj Save actual의 independent process determinism:

```bash
task_save_run_one=$(mktemp -d /tmp/godj-save-final-one.XXXXXX)
task_save_run_two=$(mktemp -d /tmp/godj-save-final-two.XXXXXX)
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/save-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json \
  -actual-output "$task_save_run_one/actual.json"
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/save-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json \
  -actual-output "$task_save_run_two/actual.json"
cmp "$task_save_run_one/actual.json" "$task_save_run_two/actual.json"
shasum -a 256 "$task_save_run_one/actual.json" "$task_save_run_two/actual.json"
```

두 실행은 모두 `12 contracts` 일치를 출력했고 같은 hash를 냈습니다.

명시적 구현 전 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/save-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json \
  -actual conformance/fixtures/godj-save-lifecycle-not-implemented.json
```

이 명령은 의도한 exit 1과 ordered 12개의 status mismatch를 냈습니다. Static fixture는
현재 제품 actual이 아니라 false-green 회귀 입력입니다.

## EVID-20260808-006 — GDJ-0007 QuerySet Evaluation and Cache Compatibility Contracts

- Date/time: 2026-08-08T04:35:22+09:00
- Work/contract IDs: GDJ-0007, META-001, META-002, QRY-011..QRY-021, Q-007,
  Q-011
- Checkout/commit: conformance source와 machine artifact가 clean `main` commit
  `9050b4d7d2a1ed961da5e7bdefde8f4c8653eb33`; evidence 작성 시 미커밋 변경은
  GDJ-0007 완료와 GDJ-0008 handoff 문서뿐
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  `modernc.org/sqlite v1.56.0`, Go SQLite 3.53.3; uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit
  `fe0a859f537d4238cf49fca39073513206f83122`, reference SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: final `make check`, uncached full Go test/race/CGO=0, focused protocol/
  command test, checksum과 two-process oracle comparison 모두 0; static actual 비교는
  예상대로 1과 11 mismatch; unsupported GoDj query-cache adapter는 예상한 exit 2
- Result summary: repeated/empty/stale full evaluation, chain, Count/Exists, iterator,
  cold index/First, failure retry와 fresh copy를 QRY-011..021의 네 번째 ordered set으로
  고정했습니다. Python portable 44-test에서 exact 5개를 skip했고 exact run은 44개
  모두 pass했습니다. 네 set의 전역 ID/scenario uniqueness, 12개 ordered cross-binding,
  query-count/result/error와 live-fixture/capture propagation mutation이 false green 없이
  실패했습니다. 기존 GoDj M1/M2/Save 34개 differential은 0-diff를 유지했습니다.
- Failures/skips: 예상하지 않은 실패 없음. QuerySet cache 제품 adapter는 이 작업 범위
  밖이므로 static fixture의 ordered 11개 `not_implemented` mismatch와 `godjcheck`의
  fail-closed unsupported가 기대 결과입니다. GitHub-hosted workflow는 push하지 않아
  실행하지 않았습니다.
- Artifacts: `query-cache-manifest.json` 11개 `oracle_locked`, exact oracle 56,426 bytes
  SHA-256 `d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`,
  manifest SHA-256 `3d7b20e2e5f75905847eb0042633dbe6ec1dd11dcbd225b3ed57d677cf4af730`,
  static fixture SHA-256 `5cdec6cbd5440527529b08774673136c079895ab834fe2821a1626000d611d87`
- Notes: SQL 문자열, Django private `_result_cache`, Python object identity는 계약하지
  않았습니다. QRY-019의 missing-table 오류는 기존 제품 taxonomy와 맞춘
  `backend_error/missing_table`만 비교합니다. 독립 contract/provenance와 mutation 감사는
  최종 수정 뒤 P0–P3 finding이 없음을 확인했습니다.

실행한 최종 gate:

```bash
make check
go test ./... -count=1
go test -race ./... -count=1
CGO_ENABLED=0 go test ./... -count=1
go test ./conformance/internal/protocol ./conformance/cmd/godjcheck -count=1
git diff --check
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
```

`make check`는 generation drift, 전체 Go test/vet/race, focused `CGO_ENABLED=0`, Python
portable/exact 44-test, 네 manifest/oracle/fixture validation, 네 oracle byte check와 기존
M1/M2/Save 34-contract differential을 실행했습니다.

QuerySet cache oracle의 independent process determinism:

```bash
task_query_run=$(mktemp -d /tmp/godj-query-cache-final.XXXXXX)
LC_ALL=C TZ=UTC PYTHONHASHSEED=random uv run --frozen python \
  -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/query-cache-manifest.json \
  --output "$task_query_run/one.json"
LC_ALL=C TZ=UTC PYTHONHASHSEED=random uv run --frozen python \
  -m conformance.runners.django \
  --profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  --manifest conformance/contracts/query-cache-manifest.json \
  --output "$task_query_run/two.json"
cmp "$task_query_run/one.json" "$task_query_run/two.json"
cmp "$task_query_run/one.json" \
  conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json
```

두 임시 파일과 checked-in oracle은 모두 56,426 bytes이며 같은 SHA-256을 냈습니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/query-cache-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json \
  -actual conformance/fixtures/godj-query-cache-not-implemented.json
```

이 명령은 의도한 exit 1, ordered QRY-011..021 status mismatch 11개와
`observationcmp: 11 difference(s)`를 반환했습니다. Query-cache manifest를 현재 제품
`godjcheck`에 넘기면 unknown scenario를 명시해 exit 2로 fail-closed하며 output을 쓰지
않습니다.

## EVID-20260808-007 — GDJ-0008 QuerySet Evaluation and Cache Product Slice

- Date/time: 2026-08-08T05:07:45+09:00
- Work/contract IDs: GDJ-0008, META-002, GEN-M1-001, DB-SQLITE-001,
  QRY-011..QRY-021, Q-007, Q-011
- Checkout/commit: 제품 source와 query-cache manifest가 clean `main` commit
  `6f1aab78a6e365e62f5a3b59b040b90b981b4978`; evidence 작성 시 미커밋 변경은
  GDJ-0008 완료와 GDJ-0009 handoff 문서뿐
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  `modernc.org/sqlite v1.56.0`, Go SQLite 3.53.3; uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit
  `fe0a859f537d4238cf49fca39073513206f83122`, reference SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: `make check`, uncached full Go test/race/CGO=0, vet, generation drift,
  100회 ORM state test, 20회 focused race, checksum과 두 GoDj actual comparison 모두 0;
  static actual 비교는 예상한 exit 1과 ordered 11 mismatch
- Result summary: direct value copy가 공유하는 evaluation state, chain/`Fresh` 독립 state,
  성공한 empty/non-empty full cache, 실패/cancellation 재시도, concurrent `All`
  singleflight와 waiter context 격리, generated deep clone을 구현했습니다. Cold/warm
  `Count`, `Exists`, `At`, `First`와 cache-bypass `Iterate`를 public API로 연결하고
  QRY-011..021 실제 GoDj adapter가 Django oracle과 normalized semantic 0-diff를 냈습니다.
  기존 34개를 포함한 네 set 총 45개 manifest가 `passing`입니다.
- Failures/skips: 예상하지 않은 실패 없음. QuerySet static fixture의 11개
  `not_implemented` status mismatch는 구현 전 false-green baseline으로 계속 기대합니다.
  GitHub-hosted workflow는 push하지 않아 실행하지 않았습니다.
- Artifacts: query manifest 8,987 bytes SHA-256
  `35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2`;
  Django oracle 56,426 bytes SHA-256
  `d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`;
  static fixture SHA-256
  `5cdec6cbd5440527529b08774673136c079895ab834fe2821a1626000d611d87`;
  generated Article 8,517 bytes SHA-256
  `bfa6395ddf4a6597a06e0f74ef0d875cc1416ff07c8d06f6803aa6ca0a22eaf4`,
  schema hash `b10fcd2ffbc2369355c165abef4725178c04bb9a6055f77f31214188aad37621`,
  generator `godj-codegen-m2-v3`
- Notes: 두 독립 GoDj actual은 각각 56,283 bytes로 서로 byte-identical하고 SHA-256
  `c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`입니다.
  Django oracle과는 byte-identical하지 않으며 strict protocol comparator의 typed
  result/DB state/metrics/error 의미가 0-diff입니다. Cold `Count`의
  O(N) row 전송과 `At`의 offset 없는 O(index) 순회는 ADR-0012에 기록한 제한입니다.

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
go run ./internal/cmd/m1generate -check
go test -count=100 -shuffle=on ./orm
go test -race -count=20 -shuffle=on ./orm
git diff --check
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
```

`make check`는 generated drift, 전체 Go test/vet/race, focused `CGO_ENABLED=0`, Python
portable/exact 44-test, 네 manifest/oracle/fixture validation, 네 oracle byte check와
M1/M2/Save/QuerySet 45-contract differential을 실행했습니다.

두 독립 GoDj actual과 semantic differential:

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/query-cache-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json \
  -actual-output /tmp/godj-query-cache-actual-final.CeOGVB/one.json
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/query-cache-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json \
  -actual-output /tmp/godj-query-cache-actual-final.CeOGVB/two.json
cmp /tmp/godj-query-cache-actual-final.CeOGVB/one.json \
  /tmp/godj-query-cache-actual-final.CeOGVB/two.json
```

두 `godjcheck` 실행은 각각 `11 contracts` match를 출력했고 두 actual은 같은 bytes/hash를
냈습니다. 이는 actual끼리의 결정성과 Django oracle에 대한 semantic 0-diff를 각각
검증하며 oracle과 actual의 byte identity를 주장하지 않습니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/query-cache-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json \
  -actual conformance/fixtures/godj-query-cache-not-implemented.json
```

이 명령은 의도한 exit 1과 QRY-011..021 ordered status mismatch 11개를 반환했습니다.
Unknown scenario를 known handler로 fail-open시키는 mutation은
`TestRunRejectsUnknownScenarioWithoutWritingActualOutput`에서 exit 0 대 expected exit 2로
실패했습니다. 별도 `/tmp` mutation 감사는 context 재검사, caller clone, Count close,
SQLite missing-table 분류, generated nullable clone을 각각 제거했을 때 focused gate가
모두 변이를 탐지했으며 최종 제품/false-green 감사에서 P0–P3 finding은 없었습니다.

## EVID-20260808-008 — GDJ-0009 Migration Planning Compatibility Contracts

- Date/time: 2026-08-08T05:49:20+09:00
- Work/contract IDs: GDJ-0009, META-001, META-002, MIG-005..MIG-016, Q-012
- Checkout/commit: machine artifact가 clean `main` commit
  `9fc3df42f17b61b0a0202f21d3d99190c0db2d28`; evidence/ADR/GDJ-0010 handoff 문서는
  후속 미커밋 변경
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5; uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit
  `fe0a859f537d4238cf49fca39073513206f83122`, reference SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: `make check`, uncached full Go test/race/CGO=0, vet, focused Python/Go,
  five checksums와 two-process exact regeneration 모두 0. Static fixture 비교는 의도한
  exit 1, GoDj unsupported product 실행은 의도한 exit 2.
- Result summary: MIG-005..016의 linear/applied-pruned/prior/zero/cross-app/multi-target
  plan과 target/history/graph/cycle error를 다섯 번째 exact reference set에 고정했습니다.
  Django oracle 12개는 `observed`, manifest는 12개 `oracle_locked`, static GoDj fixture는
  ordered 12개 `not_implemented`입니다. 기존 네 GoDj adapter의 45개 contract는 모두
  0-diff를 유지했습니다.
- Failures/skips: 예상하지 않은 실패 없음. Portable Python은 59 tests 중 exact-only 6개를
  의도적으로 skip했고 exact run은 59개 모두 pass했습니다. 제품 planner/adapter는 이번
  contract-only 범위 밖이므로 static 12 mismatch와 unsupported scenario가 정상입니다.
  GitHub-hosted workflow는 push하지 않아 실행하지 않았습니다.
- Artifacts: manifest 10,623 bytes SHA-256
  `7e8f0d19c8f227721e7cfe4254a4f39d1313e801f1ea0a759e14c46a3dbbe876`;
  Django oracle 39,139 bytes SHA-256
  `7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474`;
  static fixture 1,869 bytes SHA-256
  `a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13`
- Notes: 다섯 set의 ID/scenario는 전역으로 유일하고 20개 ordered cross-binding이 모두
  거부됩니다. MIG-014는 public history consistency preflight 의미입니다. MIG-012는
  dependency precedence, caller target order와 shared dependency deduplication만 잠그며
  incomparable sibling의 Django private DFS tie-break와 cycle message/path는 계약하지
  않습니다.

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
LC_ALL=C TZ=UTC uv run --frozen python -m unittest \
  conformance.runners.django.tests.test_migration_planning_scenarios -v
(
  cd conformance/oracles/django-6.1-sqlite-darwin-arm64
  shasum -a 256 -c SHA256SUMS
)
git diff --check
```

`make check`는 portable Python 59 tests/6 skips, exact 59 tests, 다섯
manifest/oracle/static validation, 다섯 oracle byte check, full Go/vet/race, SQLite/GoDj
adapter focused CGO=0와 기존 M1/M2/Save/QuerySet 제품 45-contract differential을
실행했습니다. Full CGO=0은 위 별도 명령으로 확인했습니다.

두 독립 exact process와 checked-in oracle:

```text
/tmp/godj-migration-planning-audit-final.hqaVJ9/one.json
/tmp/godj-migration-planning-audit-final.hqaVJ9/two.json
conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json
```

세 파일은 모두 39,139 bytes와 같은 SHA-256으로 byte-identical했습니다. Exact runner는
random hash seed와 graph insertion normal/reverse/rotate에서도 fixed ordered-target 결과를
재현했습니다.

명시적 미구현 baseline은 `observationcmp`에서 예상 exit 1과 MIG-005..016 순서의 status
mismatch 12개를 냈습니다. 실제 build한 `godjcheck`에 fifth manifest를 넘긴 실행은 exit 2,
stdout 0 bytes, actual 파일 미생성과 unsupported scenario를 확인했습니다.

False-green gate는 plan order/direction, requested target, applied prefix, shared dependency
duplicate, retained branch DB state, missing-target request facts, missing-dependency graph facts와
`state_unchanged`를 각각 바꿨을 때 비교를 실패시켰습니다. Python runner는 live planner
결과 전파, unexpected DDL/write/non-SELECT, recorder/schema cleanup과 inconsistent-history
preflight 사용을 별도로 검증했습니다. 최종 독립 계약/게이트 감사에서 P0–P3 finding은
없었습니다.

## EVID-20260808-009 — GDJ-0010 Immutable Migration Planner Product Slice

- Date/time: 2026-08-08T06:33:43+09:00
- Work/contract IDs: GDJ-0010, META-002, MIG-005..MIG-016, Q-012
- Checkout/commit: 제품과 machine gate가 clean `main` commit
  `31d264ad7c85a23b511a7549d698c1c3b0577e92`; 검증 시점에 상태/evidence/GDJ-0011
  handoff 문서는 후속 미커밋 변경이었습니다.
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5; Go Planner는
  backend-neutral pure computation. Differential reference는 uv 0.10.12, CPython 3.14.3,
  Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, SQLite 3.50.4,
  `LC_ALL=C`, `TZ=UTC`
- Exit status: `make check`, uncached full Go test/vet, full CGO-disabled test,
  planner 100회 shuffle, focused race와 two-process actual이 모두 0. Static fixture 비교는
  의도한 exit 1.
- Result summary: immutable migration identity graph, 별도 `AppliedState`, caller-ordered
  named/zero target와 structured `PlanningError`를 public API로 구현했습니다. 다섯 번째
  GoDj adapter가 실제 `NewPlanner`/`NewAppliedState`/`Planner.Plan`을 실행해 MIG-005..016
  12개가 Django oracle과 semantic 0-diff이며 다섯 제품 set의 총 57개 contract가
  `passing`입니다.
- Failures/skips: 예상하지 않은 실패 없음. Portable Python은 59 tests 중 exact-only 6개를
  의도적으로 skip했고 exact run은 59개 모두 pass했습니다. Static fixture는 의도한
  MIG-005..016 ordered 12 status mismatch를 냈습니다. GitHub-hosted workflow는 push하지
  않아 실행하지 않았습니다.
- Artifacts: manifest 10,551 bytes SHA-256
  `f51d737bd68eafae32f7942669b467e3457372873ec536a13491ded60ef27ca6`;
  locked Django oracle 39,139 bytes SHA-256
  `7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474`;
  static fixture 1,869 bytes SHA-256
  `a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13`;
  두 독립 Go actual은 각각 39,094 bytes SHA-256
  `eb5bf3b6f41855684582f67b3be675da42975b8fc1ed9c7085f6d35a078eac32`
- Notes: Planning의 logical before/after state와 DDL/write/non-SELECT 0 metrics는 실제 DB
  probe가 아니라 backend import가 없는 pure structural adapter에서 산출합니다. Existing
  one-migration Executor/backend, Django runner, locked oracle와 static fixture는 변경하지
  않았습니다.

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go vet ./...
CGO_ENABLED=0 go test -count=1 ./...
go test -count=100 -shuffle=on ./migrations
go test -race -count=5 -shuffle=on \
  ./migrations ./conformance/runners/godj \
  ./conformance/cmd/godjcheck ./conformance/internal/protocol
go test -count=20 ./conformance/runners/godj -run MigrationPlanning
go test -race -count=5 ./conformance/runners/godj -run MigrationPlanning
git diff --check
```

`make check`는 deterministic generation, full Go test/vet/race, focused CGO=0, portable
Python 59 tests/6 skips, exact 59 tests, 다섯 manifest/oracle/static validation과 oracle
regeneration check, 57-contract GoDj differential을 실행했습니다. Full CGO=0와 반복
planner/race는 별도 명령으로 보강했습니다.

두 독립 Go actual과 semantic differential:

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  -actual-output /tmp/godj-planner-actual.BRXLoq/first.json
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  -actual-output /tmp/godj-planner-actual.BRXLoq/second.json
cmp /tmp/godj-planner-actual.BRXLoq/first.json \
  /tmp/godj-planner-actual.BRXLoq/second.json
```

두 actual은 byte-identical하고 각 실행은 `12 contracts` match를 출력했습니다. Django
oracle과 actual의 byte identity를 주장하지 않으며, protocol comparator가 result/error/
DB state/metrics의 계약 의미를 0-diff로 판정한 것입니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-planning-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json \
  -actual conformance/fixtures/godj-migration-planning-not-implemented.json
```

이 명령은 의도한 exit 1과 MIG-005..016 ordered status mismatch 12개를 반환했습니다.
Fixture target/applied/dependency 변이는 echo된 전체 Result가 아니라 `plan` 하위값을 직접
바꾸어야 하고, missing dependency를 self-cycle로 바꾸면 실제 error code가 바뀌어야
합니다. Source guard는 adapter의 `MIG-` literal, oracle/static path와 DB import를
거부합니다. 별도 mutation/property 감사는 ready-set 역순, target별 working-state reset,
history bypass, SCC 선택, canonicalization과 state hardcode 변이를 모두 탐지했으며 최종
독립 제품 감사에서 P0–P3 finding은 없었습니다.
