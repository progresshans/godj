# 테스트·검증 증거

- 마지막 갱신: 2026-08-08
- 현재 GoDj 코드·호환 계약 테스트 증거: EVID-20260808-004

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
