# 테스트·검증 증거

- 마지막 갱신: 2026-08-10
- 현재 GoDj 코드·호환 계약 테스트 증거: EVID-20260810-039

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

## EVID-20260808-010 — GDJ-0011 Migration Plan Execution Compatibility Contracts

- Date/time: 2026-08-08T07:15:18+09:00
- Work/contract IDs: GDJ-0011, META-001, META-002, MIG-017..MIG-026, Q-012
- Checkout/commit: machine artifact와 gate가 clean `main` commit
  `b721bb6b81ba9a950558c288dcb1a78efd7ff9ab`; 제품 code baseline은
  `31d264ad7c85a23b511a7549d698c1c3b0577e92`. 검증 뒤 status/ADR/GDJ-0012 handoff
  문서는 후속 미커밋 변경이었습니다.
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`; Go regression backend는 modernc SQLite 3.53.3
- Exit status: `make check`, full uncached Go/race/CGO=0/vet, exact two-process generation,
  existing product differential과 artifact/mutation audit가 0. Static execution fixture
  비교는 의도한 exit 1, unsupported product execution은 의도한 exit 2/no output.
- Result summary: MIG-017..026의 linear forward/backward, applied prefix/unrelated branch,
  operation/recorder failure, mixed plan과 empty no-op를 여섯 번째 exact reference set으로
  잠갔습니다. Manifest 10개는 `oracle_locked`, Django oracle 10개는 `observed`, static
  fixture 10개는 `not_implemented`입니다. 기존 다섯 제품 adapter 57개는 계속 semantic
  0-diff이며 총 reference 67개를 제품 pass로 세지 않습니다.
- Failures/skips: 예상하지 않은 실패 없음. Portable Python은 79 pass와 exact-only 7 skip,
  exact Python은 79 pass였습니다. GitHub-hosted workflow는 push하지 않아 실행하지
  않았고 migration execution GoDj product adapter/`ExecutePlan`은 아직 없습니다.
- Artifacts: manifest 8,720 bytes SHA-256
  `f414cd7a495f6e6765df06ca1427485ecc16a8d19c344f190f5f1421dc2a517d`; locked Django
  oracle 47,119 bytes SHA-256
  `641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`; static fixture
  1,685 bytes SHA-256
  `6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`
- Notes: External metrics는 connection summary와 compact ordered steps만 포함합니다. Raw
  render/operation/recorder/transaction trace는 live runner assertion 내부입니다. Historical
  before/after는 MIG-019에만 있고 MIG-023/024는
  `fault_point=before_record_write`입니다. MIG-024는 schema A1, records A1/A2와 `commit`
  phase를 재현했습니다.

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
make godj-conformance
git diff --check
```

`make check`는 format/generation, Go test/vet/race, focused CGO=0, portable/exact Python,
여섯 manifest/oracle/static validation, exact oracle check와 기존 57 product differential을
실행했습니다. Full uncached Go/race/CGO=0는 별도 명령으로 보강했습니다.

두 독립 random-hashseed exact process는 migration-execution manifest를 각각 새 output으로
생성했습니다. 두 output과 checked-in oracle은 모두 47,119 bytes이고 SHA-256이
`641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`로 byte-identical했습니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -actual conformance/fixtures/godj-migration-execution-not-implemented.json
```

이 명령은 의도한 exit 1과 MIG-017..026 ordered status mismatch 정확히 10개를 반환했습니다.
같은 manifest를 product `godjcheck`에 넘기면 exit 2, stdout/actual output 0으로 fail-closed
했습니다. 여섯 set의 ID/scenario는 전역으로 유일하고 30개 ordered cross-binding이 모두
거부됐습니다.

False-green gate는 step order/direction/status, `transaction_model`, schema/recorder
outcome, MIG-019 historical transition, MIG-021/022 failed/not-started split, MIG-023/024
fault point와 schema/record split, MIG-025 preserved state, MIG-026 empty/recorder absence를
각각 변형했을 때 mismatch를 탐지했습니다. Raw trace에서 compact facts가 유도되는지와
setup/cleanup isolation도 live Python test로 검증했습니다. 최종 독립 contract 감사에서
P0–P3 finding은 없었습니다.

## EVID-20260808-011 — GDJ-0012 Migration Plan Execution Orchestrator and Atomic Reverse

- Date/time: 2026-08-08T08:09:04+09:00
- Work/contract IDs: GDJ-0012, MIG-017..MIG-026, Q-012, DEV-0001
- Checkout/commit: 제품·machine implementation은 clean `main` commit
  `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`; 이 evidence와 status/ADR/GDJ-0013
  handoff는 바로 다음 문서 commit에 포함
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  modernc.org/sqlite v1.56.0 / SQLite 3.53.3; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: `make check`, uncached full Go/race/CGO=0/vet, generation check,
  focused repetition/race, six-set conformance, exact oracle checks, two-process actual과
  독립 core/conformance 감사가 0
- Result summary: `Executor.ExecutePlan`의 full preflight, migration별 commit,
  first-failure stop/last durable state, cancellation과 empty no-op를 구현했습니다. SQLite는
  empty table의 no-default non-null non-PK AddField와 reverse RemoveField를 좁게 지원합니다.
  MIG-017/019/021/023/025/026은 Django와 exact `passing`,
  MIG-018/020/022/024는 ADR-0014/DEV-0001의 same-transaction reverse `deviation`으로
  검증됐습니다. 현재 제품 분류는 `63 passing + 4 deviation`입니다.
- Failures/skips: 예상하지 않은 최종 실패 없음. Portable Python은 79 pass와 exact-only
  7 skip, exact Python은 79 pass였습니다. Core 독립 감사가 발견한 state preflight 중
  cancellation→Begin race는 backend I/O 직전 context 재검사와 Apply/Unapply 회귀 test로
  수정한 뒤 재감사 PASS했습니다. GitHub-hosted workflow는 push하지 않아 실행하지
  않았습니다.
- Artifacts: approved manifest 9,120 bytes SHA-256
  `1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9`; locked Django
  oracle 47,119 bytes SHA-256
  `641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`; static fixture
  1,685 bytes SHA-256
  `6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`; sparse deviation
  expectation 4,685 bytes SHA-256
  `568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547`

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
go run ./internal/cmd/m1generate -check
go test -count=20 -shuffle=on ./migrations
go test -race -count=10 -shuffle=on ./migrations
git diff --check
```

`make check`는 generation, full Go/vet/race, focused CGO=0, portable/exact Python 79개,
여섯 oracle/static validation과 여섯 product conformance를 실행했습니다. 기존 다섯 set의
57 exact 결과는 유지됐고 execution set은 다음과 같이 별도 reviewed expectation으로
실행됐습니다.

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -deviation-expected conformance/fixtures/godj-migration-execution-deviation-expected.json \
  -actual-output /tmp/godj-execution-actual.9aDsQX/first.json
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-execution-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json \
  -deviation-expected conformance/fixtures/godj-migration-execution-deviation-expected.json \
  -actual-output /tmp/godj-execution-actual.9aDsQX/second.json
cmp /tmp/godj-execution-actual.9aDsQX/first.json \
  /tmp/godj-execution-actual.9aDsQX/second.json
```

두 actual은 각각 47,446 bytes, SHA-256
`f191d116cc38194e2019df358c31f752101fdacb005d9cc442b701d8d4afde4b`로
byte-identical했습니다. Django oracle과 byte identity를 주장하지 않으며, 원 manifest로
reference oracle을 strict self-compare한 뒤 code-owned DEV-0001 selector가 허용한 차이만
적용한 product expectation과 protocol 의미상 10개 0-diff입니다.

Manifest는 6 `passing` + 4 `deviation`이고 deviation 네 개에만 정확히 하나의
`decision=DEV-0001`, `derived=false` provenance가 있습니다. Missing/wrong expectation,
등록되지 않은 selector, 잘못된 status/provenance는 실제 binary에서 exit 2, stdout 0,
actual file 미생성으로 검증했습니다. Core comparator는 변경된 dimension을 skip하지
않습니다. Static fixture는 계속 MIG-017..026 ordered `not_implemented` mismatch 10개와
exit 1을 내며, 30 ordered cross-binding도 모두 거부됩니다.

GoDj adapter는 live `ExecutePlan`, SQLite schema/recorder snapshot과 transaction trace에서
observation을 만듭니다. Trace는 계획 prefix/order, operation/recorder boundary,
commit XOR rollback, failure 뒤 추가 transaction 부재를 내부 검증합니다. 독립 mutation
감사는 caller input snapshot, nonempty AddField gate, reverse RemoveField capability,
fixture dependency/result/error propagation과 deviation selector를 변형했을 때 회귀가
실패하는지 확인했습니다. 최종 core와 conformance 감사 모두 P0–P3 finding 없음으로
종료했습니다.

## EVID-20260808-012 — GDJ-0013 Recorder-backed Restart Planning Compatibility Contracts

- Date/time: 2026-08-08T08:37:14+09:00
- Work/contract IDs: GDJ-0013, MIG-027..MIG-036, Q-012
- Checkout/commit: machine artifact는 clean `main` commit
  `b6af5056bb67fc1d2d32b2163cb7091d700b1e7e`; 이 evidence와 status/GDJ-0014 handoff는
  바로 다음 문서 commit에 포함
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: stable tree의 `make check`, full Go test/vet/race, focused·full CGO=0,
  portable/exact Python, seven-set contract/oracle validation, two-process regeneration과
  독립 contract 감사가 0
- Result summary: recorder table absent/empty, record/unrecord fresh read, database alias
  isolation, applied-prefix tail, fully-applied empty plan, unknown legacy row, explicit
  inconsistent-history preflight와 middle-failure restart를 MIG-027..036으로 잠갔습니다. 새
  manifest는 10 `oracle_locked`, Django oracle은 10 `observed`, static fixture는 10
  `not_implemented`입니다. 기존 제품 분류는 `63 passing + 4 deviation`이며 reference 총
  77개 전체를 제품 통과로 세지 않습니다.
- Failures/skips: 예상하지 않은 실패 없음. Portable Python은 94 pass와 exact-only 9 skip,
  exact Python은 94/94 pass였습니다. Static comparison은 의도한 exit 1과 MIG-027..036
  ordered mismatch 10개, 제품 binary는 unsupported scenario에서 exit 2와 actual file 미생성을
  반환했습니다. GitHub-hosted workflow는 push하지 않아 실행하지 않았습니다.
- Artifacts: manifest 10,225 bytes SHA-256
  `93e25d02208a765001760f76715ff6e9642451c5823efc62cc40b1d249dbd42b`; locked Django oracle
  33,888 bytes SHA-256
  `90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`; static fixture
  1,715 bytes SHA-256
  `31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
git diff --check
```

두 독립 hash-seed exact process는 `/tmp/godj-restart-oracle.AisvMo`에 oracle을 생성했습니다.
두 output과 checked-in oracle은 모두 33,888 bytes이고 SHA-256이
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`로 byte-identical했습니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  -actual conformance/fixtures/godj-migration-restart-not-implemented.json
```

이 명령은 의도한 exit 1과 MIG-027..036 ordered status mismatch 정확히 10개를 반환했습니다.
같은 manifest의 실제 `godjcheck` binary는 exit 2였고
`/tmp/godj-restart-godjcheck.Oex66v/actual.json`을 만들지 않았습니다.

Seven-set protocol gate는 contract ID/scenario 전역 유일성과 42개 ordered cross-binding 거부를
검증합니다. Semantic mutation gate는 recorder 존재·identity·setup transition, alias partition,
plan order/direction/empty, unknown/known history partition, history error와 pre-plan timing, durable
prefix와 failed-step tail, DDL/write/기타 non-SELECT 0과 before/after 동일성을 각각 변형해
false green을 거부합니다. Fresh loader 생성 자체도 capture 안에 있으며 setup recorder/executor
object를 재사용하지 않습니다.

로컬 Django checkout HEAD는 newer checkout이지만 참조한 source/test 파일은 pinned
`fe0a859f537d4238cf49fca39073513206f83122`와 byte-identical했고, 실행은 uv-locked Django 6.1
wheel SHA-256 `6c132cd980c9392b06807d4ca52d72530d631dc65a85d9dacede00a780cefbbe`를 사용했습니다.
외부 checkout은 수정하지 않았고 최종 독립 contract 감사는 P0–P3 finding 없음으로
종료했습니다.

## EVID-20260808-013 — GDJ-0014 Recorder-backed Restart Planning Product Slice

- Date/time: 2026-08-08T09:18:58+09:00
- Work/contract IDs: GDJ-0014, MIG-027..MIG-036, Q-012
- Checkout/commit: clean `main` product/machine commit
  `a9ce9597551840f1be8e1f27006d427842f38081`; 이 evidence와 GDJ-0015 handoff는
  바로 다음 문서 commit에 포함
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  modernc.org/sqlite v1.56.0 / SQLite 3.53.3; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: stable tree의 `make check`, uncached full Go/race/CGO=0/vet,
  two-process product actual과 semantic comparison이 0. Static fixture comparison은
  의도한 exit 1과 ordered mismatch 10개
- Result summary: backend-neutral applied-history reader, validated `LoadAppliedState`,
  explicit `Planner.CheckHistory`, read-only SQLite recorder snapshot과 MIG-027..036 live
  adapter를 구현했습니다. Restart manifest 10개는 `passing`이고 전체 제품 분류는
  `73 passing + 4 deviation`입니다.
- Failures/skips: 예상하지 않은 최종 실패 없음. Portable Python은 94 pass와 exact-only
  9 skip, exact Python은 94/94 pass였습니다. GitHub-hosted workflow는 push하지 않아
  실행하지 않았고 외부 Django checkout은 수정하지 않았습니다.
- Artifacts: passing manifest 10,165 bytes SHA-256
  `79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290`; locked Django oracle
  33,888 bytes SHA-256
  `90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`; static fixture
  1,715 bytes SHA-256
  `31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`; 두 Go actual은
  각각 33,795 bytes SHA-256
  `f9e4d3dc7078426f06a08374a36a670a36e1fa2ae08562fd08f80e91db1b31cb`

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
git diff --check
```

두 독립 product process와 의미 비교:

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  -actual-output /tmp/godj-restart-actual.l7UpPw/first.json
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-restart-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json \
  -actual-output /tmp/godj-restart-actual.l7UpPw/second.json
cmp /tmp/godj-restart-actual.l7UpPw/first.json \
  /tmp/godj-restart-actual.l7UpPw/second.json
```

두 actual은 byte-identical이고 Django oracle과 protocol 의미상 MIG-027..036 10개가
0-diff였습니다. JSON bytes가 oracle과 같다고 주장하지 않습니다. 현재 passing manifest에
static fixture를 비교한 command는 의도한 exit 1과 ordered status mismatch 10개를
반환했습니다. Seven-set protocol gate는 전역 contract/scenario uniqueness와 42개 ordered
cross-binding 거부를 유지합니다.

제품 경계 감사는 raw recorder identity copy/validation, nil·typed-nil/context/error cause,
known-history preflight, missing-table classification, fresh file record/unrecord, alias isolation,
concurrent read/close와 adapter input propagation을 확인했습니다. 초기 audit에서 literal zero
metrics가 extra non-SELECT를 놓치는 false green을 발견했고, live driver gate가 정확히 한
`SELECT`만 허용하며 `Exec`, transaction, 추가 query를 거부하도록 보강했습니다. 같은 gate에
`PRAGMA query_only` mutation을 주입했을 때 회귀가 실패함을 재확인했으며, 최종 독립
core/SQLite/conformance 감사 모두 P0–P3 finding 없음으로 종료했습니다.

## EVID-20260808-014 — GDJ-0015 Historical ProjectState Reconstruction Compatibility Contracts

- Date/time: 2026-08-08T09:59:38+09:00
- Work/contract IDs: GDJ-0015, MIG-037..MIG-046, Q-012
- Checkout/commit: clean `main` machine artifact commit
  `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`; product baseline은
  `a9ce9597551840f1be8e1f27006d427842f38081`
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: stable tree의 `make check`, uncached full Go/race/CGO=0/vet, portable/exact
  Python, eight-set contract/oracle/checksum과 독립 contract/false-green 감사가 0
- Result summary: explicit empty/latest, first·middle before/after, cross-app dependency,
  multiple target/shared dependency, latest leaves와 applied-prefix/unrelated-known/unknown-legacy
  startup state를 MIG-037..046으로 잠갔습니다. Manifest는 10 `oracle_locked`, Django oracle은
  10 `observed`, static fixture는 10 `not_implemented`입니다. 기존 제품 분류는
  `73 passing + 4 deviation`이고 reference 총 87개 전체를 제품 통과로 세지 않습니다.
- Failures/skips: 예상하지 않은 최종 실패 없음. Portable Python은 114개 중 exact-only
  11 skip, exact Python은 114/114 pass였습니다. Static comparison은 의도한 exit 1과
  MIG-037..046 ordered mismatch 10개, product binary는 unsupported scenario에서 exit 2와
  actual file 미생성을 반환했습니다. GitHub-hosted workflow는 push하지 않아 실행하지
  않았습니다.
- Artifacts: manifest 9,257 bytes SHA-256
  `04b7e92a5bbf9ff50f0247be7708dfb18a5534e40bac86a518a6b744fc0ef728`; locked Django oracle
  89,997 bytes SHA-256
  `bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`; static fixture
  1,715 bytes SHA-256
  `9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
GODJ_EXACT_PROFILE=1 LC_ALL=C TZ=UTC uv run --frozen python -m unittest \
  conformance.runners.django.tests.test_migration_state_reconstruction_scenarios -v
git diff --check
```

두 독립 `PYTHONHASHSEED` process와 checked-in oracle은 모두 89,997 bytes이고 SHA-256이
`bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`로
byte-identical했습니다. Eight-set protocol gate는 87개 contract ID/scenario 전역 유일성과
56개 ordered cross-binding 거부를 검증했습니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  -actual conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json
```

이 명령은 의도한 exit 1과 MIG-037..046 ordered status mismatch 정확히 10개를 반환했습니다.
같은 manifest의 product `godjcheck`는 unsupported scenario에서 내부 exit 2, stdout 0,
actual file 미생성으로 fail-closed했습니다. `go run` wrapper는 child exit 2를 shell exit 1로
표현하므로 binary-level `run()` E2E test에서 exact code 2를 고정합니다.

Payload는 lowercase model key, explicit table/column, declaration-order field kind/PK/null/
max-length와 supported scalar default presence/type/value를 포함합니다. 각 scenario의 live DB에는
historical state와 다른 sentinel schema를 두고 before/after 동일, DDL/write/기타 non-SELECT 0을
같이 비교합니다. Applied startup은 public `MigrationExecutor.migrate(targets=[], plan=[])`를
호출하고 unknown recorder identity는 observation에 보존하지만 schema state로 만들지 않습니다.

Semantic mutation gate는 state/app/model/table/field/default, request/target/position,
graph node/dependency, applied known/unknown partition, DB before/after와 모든 zero metric을
각각 변형해 mismatch를 확인합니다. Producer gate는 동일 arbitrary contract ID에서
operation/target/dependency/applied/live-DB 입력 전파를 검증하고 10개 public scenario의 실제 ID와
arbitrary ID 결과가 ID 필드 외 동일함을 요구합니다. 독립 감사의 private-helper 경로,
lexical-order replay, capture DDL과 contract-ID dispatch/wrong-wrapper 변이는 모두 실패했습니다.
최종 독립 감사는 P0–P3 finding 없음으로 종료했고 외부 Django checkout은 수정하지 않았습니다.

## EVID-20260808-015 — GDJ-0016 Historical ProjectState Reconstruction Product Slice

- Date/time: 2026-08-08T10:39:00+09:00
- Work/contract IDs: GDJ-0016, MIG-037..MIG-046, Q-012
- Checkout/commit: clean `main` product commit
  `3b0e68d6717a9612debc9cb93d03ab0f98005860`; machine artifact baseline은
  `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`; 이 evidence와 완료 handoff는 product
  commit 다음 문서 변경에 포함
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  modernc.org/sqlite v1.56.0 / SQLite 3.53.3; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: stable product tree의 `make check`, uncached full Go/race/CGO=0/vet,
  focused migration/conformance/compile/source/mutation gate와 two-process product comparison이
  0. Static fixture comparison은 의도한 exit 1과 ordered mismatch 10개
- Result summary: immutable tagged-request `StateReconstructor`, loaded definition/operation/
  nested IR deep-copy, existing Planner graph/order kernel 기반 empty/latest/before/after/applied
  replay와 eighth live adapter를 구현했습니다. MIG-037..046은 10 `passing`이고 8 product
  set의 현재 분류는 `83 passing + 4 deviation`, 전체 contract는 87개입니다.
- Failures/skips: 예상하지 않은 최종 실패 없음. `make check`의 portable Python은 114개 중
  exact-only 11 skip, exact Python은 114/114 pass였습니다. GitHub-hosted workflow는
  push하지 않아 실행하지 않았고 외부 Django checkout은 수정하지 않았습니다.
- Artifacts: passing manifest 9,197 bytes SHA-256
  `85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c`; locked Django oracle
  89,997 bytes SHA-256
  `bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`; locked static fixture
  1,715 bytes SHA-256
  `9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`; locked
  `SHA256SUMS` file SHA-256
  `2da1f862ada632a9db2406672f0ac9209c066ae6b822afe1b47f321fdaea40c8`; 두 Go actual은 각각
  89,867 bytes SHA-256
  `a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
git diff --check
```

두 독립 product process와 의미 비교:

```bash
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  -actual-output /tmp/godj-state-evid015.kECZaM/first.json
go run ./conformance/cmd/godjcheck \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-state-reconstruction-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json \
  -actual-output /tmp/godj-state-evid015.kECZaM/second.json
cmp /tmp/godj-state-evid015.kECZaM/first.json \
  /tmp/godj-state-evid015.kECZaM/second.json
```

두 actual은 byte-identical했고 locked Django oracle과 protocol 의미상 MIG-037..046 10개가
0-diff였습니다. Oracle과 JSON byte identity를 주장하지 않습니다. 8-set protocol gate는 87개
contract ID/scenario 전역 유일성과 56개 ordered cross-binding 거부를 유지합니다. Static
fixture comparison은 ordered status mismatch 10개와 exit 1을 반환하고, 등록되지 않은
historical-state scenario는 binary-level test에서 exit 2/no actual로 fail-closed합니다.

Reconstructor는 DB handle을 받지 않고 backend/SQLite/SQL import나 I/O가 없습니다. Applied
MIG-045/046 adapter는 real SQLite recorder를 mode=ro로 열고 injected reader의 raw identity를
`LoadAppliedState`로 검증합니다. Driver gate는 exact one SELECT만 허용하며 `Exec`, transaction,
추가 query와 `PRAGMA`를 거부합니다. Capture/request AST gate는 direct SQL뿐 아니라 exact call
allowlist 밖 helper invocation도 거부하여 non-SELECT metric 0을 source 경계로 고정합니다.
Before/after snapshot은 모든 `godj_state_*` table을 inventory해 unexpected managed table도
놓치지 않습니다.

Core mutation/race gate는 definition/operation/nested IR, request/applied input과 반환 state의
alias를 거부하고 zero request/reconstructor, multiple target first-seen closure union, before의
명시 target set 전체 제외, same-app latest leaves, applied full-forward known replay와 unknown
identity 제외를 검증합니다. Adapter gate는 contract ID/oracle/static payload dispatch 없이
arbitrary ID와 target/dependency/applied/live-DB mutation을 실제 observation에 전파합니다.
Normalizer는 Boolean kind `boolean`, bool default type `bool`, absent default tagged null과
non-char `max_length=null`을 exact하게 보존합니다. Locked oracle/static/SHA256SUMS는 machine
baseline과 byte-identical했고 최종 독립 core/product/conformance 감사의 P0–P3 finding은
없었습니다.

## EVID-20260808-016 — GDJ-0017 Migration Lifecycle Compatibility Contracts and Revision-Fence Spike

- Date/time: 2026-08-08T11:34:41+09:00
- Work/contract IDs: GDJ-0017, MIG-047..MIG-056, Q-012
- Checkout/commit: clean `main` machine artifact commit
  `6e018e00bd9178858db597400ac9d3f98a66acf6`; 제품 baseline은
  `3b0e68d6717a9612debc9cb93d03ab0f98005860`; 이 evidence와 완료 handoff는 machine
  commit 다음 문서 변경에 포함
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  modernc.org/sqlite v1.56.0 / SQLite 3.53.3; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: stable machine tree의 `make check`, uncached full Go/race/CGO=0/vet,
  focused exact lifecycle 13 tests, lifecyclefence count=20/race와 two-process count=100이 0
- Result summary: MIG-047..056의 fresh/prefix/no-op/latest, named forward/reverse/app zero,
  unknown legacy preservation, inconsistent history preflight, middle failure durable prefix와
  fresh file close/reopen resume를 ninth exact set으로 잠갔습니다. Manifest는 10
  `oracle_locked`, Django oracle은 10 `observed`, static fixture는 10 `not_implemented`입니다.
  기존 8 product set은 `83 passing + 4 deviation`이고 reference 총 9 set/97 contract를 제품
  pass로 세지 않습니다. Test-only SQLite spike는 현재 제품의 stale-snapshot gap을 재현하고
  cooperating writer의 per-step revision fence 가능성을 검증했습니다.
- Failures/skips: 예상하지 않은 최종 실패 없음. Static comparison은 의도한 exit 1과
  MIG-047..056 ordered mismatch 10개, product binary는 exit 2와 actual file 미생성을
  반환했습니다. GitHub-hosted workflow와 push는 실행하지 않았고 외부 Django checkout은
  수정하지 않았습니다.
- Artifacts: manifest 13,680 bytes SHA-256
  `23a9e919edff932ae781f0768aeaf7f184fe392ec53598fa18524cf50d979a8e`; locked Django oracle
  98,436 bytes SHA-256
  `7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`; static fixture
  1,681 bytes SHA-256
  `b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`; `SHA256SUMS` file
  853 bytes SHA-256 `520db274a63ed9d192e6ae0a3db224154a84676462e7fd8e49f80f64673c1a90`

실행한 최종 gate:

```bash
make check
go test -count=1 ./...
go test -race -count=1 ./...
CGO_ENABLED=0 go test -count=1 ./...
go vet ./...
GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC \
  uv run --frozen python -m unittest \
  conformance.runners.django.tests.test_migration_lifecycle_scenarios -v
go test -count=20 ./conformance/lifecyclefence/...
go test -race -count=1 ./conformance/lifecyclefence/...
go test -count=100 \
  -run '^TestFenceSerializesSameTokenAcrossTwoProcesses$' \
  ./conformance/lifecyclefence
git diff --check
```

두 독립 `PYTHONHASHSEED` process와 checked-in lifecycle oracle은 모두 98,436 bytes이고
SHA-256이 `7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`로
byte-identical했습니다. Exact-focused 13 tests는 ID-independent producer, public
`check_consistent_history → migration_plan → migrate` route, target/definition/fault와
legacy/prefix/zero live propagation, payload exclusions와 semantic mutation을 검증합니다.

명시적 미구현 baseline:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-lifecycle-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json \
  -actual conformance/fixtures/godj-migration-lifecycle-not-implemented.json
go test -count=1 \
  -run '^TestRunRejectsMigrationLifecycleManifestWithoutWritingActualOutput$' \
  ./conformance/cmd/godjcheck
```

첫 명령은 의도한 exit 1과 ordered status mismatch 정확히 10개를 반환했습니다. 두 번째
binary-level gate는 lifecycle product adapter가 없음을 exit 2, stdout 0과 actual file 미생성으로
고정합니다. `godj-conformance`는 계속 8 product adapter만 실행합니다. Nine-set protocol gate는
97개 contract ID/scenario 전역 유일성과 72 ordered cross-binding 거부를 검증하며 기존 8개
locked payload/checksum entry가 변하지 않았음을 확인했습니다.

`conformance/lifecyclefence/current_gap_test.go`만 실제 제품 package를 조립해 first-step 전과
step 사이 stale acceptance를 재현합니다. Fence 알고리즘은 test-only `database/sql` + SQLite
harness에서 pinned `BEGIN IMMEDIATE`, persistent epoch/monotonic revision CAS, history
fingerprint와 domain DDL/recorder/successor binding을 migration별 transaction에 결속합니다.
Stale-before-write, between-step conflict, two-connection/process single winner,
uninitialized/absent/empty/ABA, busy-stale 분리, fault/cancellation rollback 뒤 success,
unsupported fail-closed와 external fake compile gate가 통과했습니다.

Fingerprint-only fence는 apply/unapply ABA를 구분하지 못합니다. 보증은 모든 cooperating
migration writer가 fence를 사용할 때 완전하며 pre-cutover non-cooperating completed ABA,
live-schema drift, fairness/lease/distributed lock와 crash repair는 범위 밖입니다. Spike의
metadata/token/coordinator는 제품 schema/API가 아닙니다. 이 근거로 ADR-0017의 안전성 방향만
Accepted로 승격했으며 제품 lifecycle 구현 또는 MIG-047..056 `passing`을 주장하지 않습니다.

## EVID-20260809-017 — GDJ-0018 Revision-Fenced Migration Lifecycle Product Slice

- Date/time: 2026-08-09, final code gate after
  `9f51ad0da443d259940d44acbb8c3d095a9a257b`
- Work/contract IDs: GDJ-0018, MIG-047..MIG-056, Q-012, DEV-0002
- Checkout/commit: branch `codex/revision-fenced-migration-lifecycle`; product
  `d076bd20f5964074b7b76b44147ca59f7b3e6eb8`, machine/conformance
  `fd49d5147beefead640f43ae6fd5c83860a17a06`, CI
  `7df6e2ad97d5890610e597277653df0674e8dd52`, repeated-test hygiene/final code
  `9f51ad0da443d259940d44acbb8c3d095a9a257b`; 이 evidence와 completion 문서는 이후 handoff
  commit에 포함
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5,
  modernc.org/sqlite v1.56.0 / SQLite 3.53.3; exact reference는 uv 0.10.12,
  CPython 3.14.3, Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
  SQLite 3.50.4, `LC_ALL=C`, `TZ=UTC`
- Exit status: final `make check`, full CGO-disabled Go, migrations count=50, full db/sqlite
  count=20과 focused race count=10/5/5가 0. Static comparison의 exit 1은 expected result.
- Result summary: `Executor.Migrate`가 loaded definitions와 explicit latest/targeted request를
  exact-one atomic snapshot, history preflight, reconstruction/planning과 per-step fenced transaction에
  결속합니다. SQLite metadata v1/fresh bootstrap/adoption gate, first-write revision claim, atomic
  schema+recorder+successor, commit durability와 empty-table default-bearing AddField가 구현됐습니다.
  MIG-047..051/053..056은 exact `passing`, MIG-052만 exact six-path DEV-0002이며 aggregate는 9
  product adapter/97 contract의 `92 passing + 5 deviation`입니다.
- Failures/skips: 예상하지 않은 최종 local failure 없음. Portable Python은 130 tests 중
  exact-profile-only 13 skipped, exact run은 130/130 pass. GitHub-hosted Actions는 branch push/PR
  전이라 실행하지 않았습니다. PostgreSQL/MySQL 등 non-SQLite backend는 GDJ-0018 범위에 구현이
  없어 실행하지 않았습니다.
- Artifacts: lifecycle manifest 13,735 bytes SHA-256
  `5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`; DEV-0002 fixture
  6,769 bytes SHA-256 `58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`;
  locked lifecycle oracle 98,436 bytes SHA-256
  `7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`; static fixture
  1,681 bytes SHA-256 `b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`;
  `SHA256SUMS` 853 bytes SHA-256
  `520db274a63ed9d192e6ae0a3db224154a84676462e7fd8e49f80f64673c1a90`

실행한 final local gate:

```bash
make check
CGO_ENABLED=0 go test -count=1 ./...
go test -count=50 -shuffle=on ./migrations
go test -count=20 -shuffle=on ./db/sqlite
```

`make check`는 format/generation drift, full Go test/vet/race, focused CGO=0, portable/exact Python,
protocol validation, 9-set GoDj conformance와 locked oracle regeneration check를 포함했습니다.
Portable run은 130 tests/13 skipped, exact run은 130/130 passing이었습니다. 과거
`db/sqlite -count=20` 실패는 production defect가 아니라 test helper가 같은 process에서 고정
shared-memory DSN을 재사용한 invocation-isolation 문제였습니다.
`db/sqlite/backend_internal_test.go`가 invocation-unique DB name을 사용하도록 수정한
`9f51ad0da443d259940d44acbb8c3d095a9a257b` 뒤 full package `-count=20 -shuffle=on`을 다시
실행해 통과했습니다.

실행한 focused race repetition:

```bash
go test -race -count=10 -shuffle=on ./migrations \
  -run 'TestExecutorMigrate|TestExecutorApply|TestExecutorUnapply|TestExecutorExecutePlan'
go test -race -count=5 -shuffle=on ./db/sqlite \
  -run 'TestSQLiteRevision|TestSQLiteMigrationAllowsDefaultAddField|TestSQLiteMigrationRejectsDefaultAddField'
go test -race -count=5 ./conformance/runners/godj \
  -run 'TestMigrationLifecycle'
```

Core gate는 invalid zero/latest/target, definition/dependency/operation/nested IR deep-copy,
exact-one snapshot, history-before-target precedence, full preflight, unsupported no-fallback,
pre-Begin cancellation, raw fence category/code matrix와 primary/secondary cleanup ordering을
검증했습니다. Multi-step durability matrix에서 이전 committed prefix 뒤 RolledBack은 token/state를
advance하지 않고, Unknown/zero는 confirmed pre-step state를 반환하며 tail을 중단합니다.
SQLite는 failed step 뒤 session을 poison하므로 `CommitRolledBack`의 token 보존이 same-session retry
허용을 뜻하지 않습니다.

SQLite gate는 exact metadata/recorder physical shape, fresh-only bootstrap, empty-recorder adoption,
legacy writer fail-closed, 2-connection/2-process single winner, ABA/stale/between-step conflict,
live BUSY/LOCKED call-site classification, format/epoch/revision/fingerprint/recorder corruption,
overflow, declared transition identity/direction/count, cancellation/commit cleanup와 Close/Begin/
concurrent terminal race를 검증했습니다. Empty table의 `BooleanField(default=false)`는 logical
default를 보존하고 physical `BOOLEAN NOT NULL` column에 persistent `DEFAULT`를 남기지 않았으며,
nonempty table은 capability error였습니다.

두 독립 Go actual은 각각 98,304 bytes, SHA-256
`a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical했습니다.
Locked Django oracle에 MIG-052의 reviewed sparse expectation을 적용한 비교는 10 contract/0-diff였고,
DEV-0002가 바꾼 path는 정확히 다음 여섯 개뿐입니다.

- `result.plan[0]`, `result.plan[1]`, `result.plan[2]`
- `metrics.steps[0]`, `metrics.steps[1]`, `metrics.steps[2]`

MIG-052의 resulting state, managed DB schema, recorder history와 phase는 reference와 동일했습니다.
Static comparison은 의도한 exit 1과 MIG-047..056 ordered status mismatch 정확히 10개를
반환했습니다. Manifest decision provenance, sparse selector allowlist, live target/definition/
history/fault propagation, source guard와 semantic mutation gate가 contract/oracle/static dispatch
false green을 거부했습니다. 9 product adapter set은 97 contract ID/scenario의 전역 유일성과
72 ordered cross-binding 거부를 유지했습니다.

Lifecycle manifest만 status/provenance 변화로 13,735 bytes가 됐고 locked Django oracle,
not-implemented static fixture, `SHA256SUMS`와 `conformance/lifecyclefence/**`는 machine baseline과
byte-identical했습니다. Product, SQLite와 conformance를 서로 다른 담당이 교차 감사했고 최종
P0–P3 finding은 없었습니다.

`.github/workflows/ci.yml`에는 기존 Ubuntu 24.04 portable `make ci` job에 더해 `macos-15`,
Go 1.26.5, uv 0.10.12/Python 3.14.3의 exact darwin/arm64 lifecycle job을 추가했습니다. 이 job은
focused pure-Go lifecycle/SQLite/adapter/compile test와 `make python-test-exact oracle-check`를
실행합니다. Workflow definition은 local review됐지만 GitHub-hosted run은 아직 없으므로 hosted
PASS로 기록하지 않습니다.

## EVID-20260809-018 — GDJ-0018 GitHub-hosted Ubuntu와 darwin/arm64 CI

- Date/time: 2026-08-09 14:04:00–14:06:08 KST
- Work/contract IDs: GDJ-0018, MIG-047..MIG-056, Q-012, DEV-0002
- Pull request/run: Draft [PR #1](https://github.com/progresshans/godj/pull/1),
  [CI run 31295886061](https://github.com/progresshans/godj/actions/runs/31295886061),
  event `pull_request`
- Head/checkout: branch `codex/revision-fenced-migration-lifecycle`, head
  `999e63b42e6ebd89e6f0f5f531a53a9cd2ffd2f3`; Actions checkout merge
  `f911d92a75d7cd79f4adb4e5f5c4e2f64e084401` into
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`
- Exit status: workflow `success`; two jobs successful, cancelled/failing/skipped/pending 0
- Failures/skips: unexpected job/step failure 없음. Ubuntu portable Python은 130 tests 중
  exact-only 13 skipped; macOS exact Python은 130/130 pass.

Hosted job evidence:

1. `Validate checked-in conformance artifacts`
   ([job 93200793945](https://github.com/progresshans/godj/actions/runs/31295886061/job/93200793945))
   - GitHub image `ubuntu-24.04`, Go 1.26.5 `linux/amd64`
   - `make ci`, stored oracle checksum과 reference artifact no-rewrite gate PASS
   - Started 14:04:02 KST, completed 14:06:07 KST
2. `Validate exact darwin/arm64 profile and SQLite lifecycle`
   ([job 93200793933](https://github.com/progresshans/godj/actions/runs/31295886061/job/93200793933))
   - macOS 15.7.7, image `macos-15-arm64`, Go 1.26.5 `darwin/arm64`
   - Focused CGO-disabled migration/SQLite/GoDj adapter/external compile tests PASS
   - uv 0.10.12/Python 3.14.3 exact 130/130, all nine locked oracle `--check`, reference
     artifact no-rewrite gate PASS
   - Started 14:04:02 KST, completed 14:05:11 KST

이 run은 local-only 결과를 hosted pass로 승격한 추정이 아니라 GitHub-hosted 로그에서 직접 확인한
증거입니다. Ubuntu는 broad portable `make ci`, macOS는 reference profile과 같은 darwin/arm64
환경의 exact/focused gate를 맡습니다. 이 제품 단면은 SQLite-only이므로 존재하지 않는 PostgreSQL/
MySQL backend matrix를 성공으로 표현하지 않습니다.

## EVID-20260809-019 — GDJ-0019 Migration Definition Source Compatibility Contracts

- Date/time: 2026-08-09 KST
- Work/contract IDs: GDJ-0019, MIG-057..MIG-064, Q-010, Q-012
- Activation commit: `058bc0aba66c78e344f2d8bc87afa2995b2b585a`
- Machine/conformance commit: `4c7b8390c34ce4f9c4bd9524f22779208cff0df0`
- Feasibility/final code commit: `58c66fdc751867a3c2f1541a8594c6615c9fbb59`
- Environment/backend: macOS darwin/arm64, Go 1.26.5; uv/Python exact profile with pinned Django 6.1,
  SQLite 3.50.4 reference; test-only Go lifecycle proof는 fresh in-memory modernc SQLite 3.53.3
- Exit status: 아래 local/reference gates 모두 0. 의도한 false-green probes는 static comparator 1,
  unsupported product runner 2
- Failures/skips: unexpected failure 없음. Portable Python 164 tests 중 exact-profile-only 15 skipped;
  exact profile 164/164 pass. Hosted CI는 final code commit에서 not run/pending

실행·확인한 주요 명령:

```bash
make check
CGO_ENABLED=0 go test -count=1 ./...
go test -count=1 ./conformance/definitionload
go test -race -count=1 ./conformance/definitionload
CGO_ENABLED=0 go test -count=1 ./conformance/definitionload
go vet ./conformance/definitionload
go test -count=20 ./conformance/definitionload
```

`make check`는 format/generate, full Go normal/vet/race, CGO-disabled SQLite/GoDj adapter, portable와
exact Python, protocol/contract, nine product adapter와 all-oracle no-rewrite gate를 root에서
통과했습니다. 별도 full `CGO_ENABLED=0 ./...`와 definitionload focused normal/race/CGO-disabled/vet/
count-20도 통과했습니다.

Reference 결과는 portable 164 tests/15 exact-only skips와 exact 164/164입니다. Strict JSON framing,
SourceID, tuple, closed operation/normalized IR, canonical RFC 8785-subset digest, atomic publish,
`migrations.NewPlanner` construction exactly once와 public `Executor.Migrate` exactly-once handoff를
검증했습니다. Combined source/document/compatibility/semantic/graph faults의 canonical
category/code/stage/reason/RFC 6901 pointer/operation index는 독립 Go/Python matrix 59/59에서 exact
parity였고 mismatch는 0이었습니다.

Protocol aggregate는 10 reference set, 105 unique contract/scenario와 90 ordered cross-binding입니다.
기존 제품 범위는 9 GoDj adapters/97 contracts와 `92 passing + 5 deviation`으로 불변이고,
MIG-057..064의 8개는 synthetic GoDj decision oracle `oracle_locked`입니다. Test-only
`conformance/definitionload/**`는 importable product loader/API가 아니며 product package의 import도
source gate로 거부합니다.

False-green probes:

```bash
go run ./conformance/cmd/observationcmp \
  -profile conformance/profiles/django-6.1-sqlite-darwin-arm64.json \
  -manifest conformance/contracts/migration-definition-source-manifest.json \
  -expected conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json \
  -actual conformance/fixtures/godj-migration-definition-source-not-implemented.json
go test -count=1 \
  -run '^TestRunRejectsMigrationDefinitionSourceSetWithoutWritingActualOutput$' \
  ./conformance/cmd/godjcheck
```

첫 명령은 의도한 exit 1과 MIG-057..MIG-064 ordered status mismatch 정확히 8개를 반환했습니다.
두 번째 gate는 actual product runner 호출이 exit 2, stdout 0 bytes이며 actual artifact를 쓰지
않음을 검증했습니다. 따라서 reference/test-only proof를 product support로 오인하는 경로가
fail-closed합니다.

고정한 artifact와 source:

| Artifact | Bytes | SHA-256 |
|---|---:|---|
| `conformance/contracts/migration-definition-source-manifest.json` | 5,195 | `8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6` |
| `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json` | 29,851 | `efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f` |
| `conformance/fixtures/godj-migration-definition-source-not-implemented.json` | 1,574 | `41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299` |
| `conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS` | 959 | `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a` |
| `conformance/runners/django/migration_definition_source_scenarios.py` | 102,128 | `53c52e3dbcd8af13e0307e62738383a01d6f307464332942c5c8ad97b71aad77` |
| `conformance/runners/django/tests/test_migration_definition_source_scenarios.py` | 68,504 | `b30b5ed338da16388fc354ecc3cdceef7d8ca8948bc41b46e4f840a0e845605a` |

Independent exact/scope/false-green review의 final P0–P3 finding은 없습니다. Accepted
[ADR-0019](../adr/0019-versioned-migration-definition-source.md)은 source contract 결정이고 product
loader 지원은 아닙니다. Product API/resource limits/error wrapping/discovery/writer/CLI와 contract의
제품 승격은 별도 GDJ-0020 activation 이후 범위입니다.

Hosted CI는 final code commit `58c66fdc751867a3c2f1541a8594c6615c9fbb59`에서 실행하지 않았습니다.
상태는 **pending/not run**이며 URL이나 hosted PASS를 기록하지 않습니다. EVID-20260809-018의
GitHub-hosted run은 GDJ-0018의 이전 head에만 적용됩니다.

## EVID-20260809-020 — GDJ-0019 GitHub-hosted Ubuntu와 darwin/arm64 CI

- Date/time: 2026-08-09 17:13:01–17:15:38 KST
- Work/contract IDs: GDJ-0019, MIG-057..MIG-064
- Tested PR/head: Draft PR [#1](https://github.com/progresshans/godj/pull/1),
  `4d9a64a0c42406bda931820f7eb38a0f737d117c`
- Final code commit contained by the tested head:
  `58c66fdc751867a3c2f1541a8594c6615c9fbb59`
- Workflow run: [31302983804](https://github.com/progresshans/godj/actions/runs/31302983804)
- Event/base: `pull_request`, `main@f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`
- Actions merge SHA: `a9a38bf24aaaa394dc8ea88b59194a7297ade707`
- Exit status: workflow `success`; two jobs successful, cancelled/failing/skipped/pending 0

Ubuntu job
[93218661521](https://github.com/progresshans/godj/actions/runs/31302983804/job/93218661521)은
`ubuntu-24.04`, Go 1.26.5 `linux/amd64`, uv 0.10.12, Python 3.14.3, Django 6.1에서
`make ci`를 실행했습니다. Portable Python은 164 tests 중 exact-only 15 skipped로 통과했고,
10개 checked-in oracle checksum에는 `migration-definition-source-oracle.json: OK`가 포함됐습니다.
Reference artifact no-rewrite gate도 통과했습니다. Job은 17:13:03 KST에 시작해 17:15:37 KST에
성공했습니다.

macOS job
[93218661534](https://github.com/progresshans/godj/actions/runs/31302983804/job/93218661534)은
macOS 15.7.7 `macos-15-arm64`, Go 1.26.5 `darwin/arm64`, uv 0.10.12, Python 3.14.3,
Django 6.1, SQLite 3.50.4에서 focused pure-Go lifecycle gate와 exact Python profile을 실행했습니다.
Exact Python은 164/164 통과했고, migration-definition-source를 포함한 10개 locked oracle `--check`와
reference artifact no-rewrite gate가 모두 통과했습니다. Job은 17:13:03 KST에 시작해
17:13:55 KST에 성공했습니다.

이 hosted 결과는 `4d9a64a` checkout에만 적용합니다. EVID-20260809-019의 당시
`pending/not run` 문구는 그 local evidence가 작성된 시점의 역사적 사실이라 수정하지 않았습니다.
이 evidence를 기록하는 후속 문서 commit은 새 PR-head workflow를 유발하므로, 그 run은 이 항목의
근거로 재귀 사용하지 않고 별도로 완료 여부만 확인합니다. MIG-057..064는 계속 synthetic GoDj
decision `oracle_locked`이며 product loader/adapter/CLI 지원으로 승격되지 않았습니다.

## EVID-20260809-021 — GDJ-0020 Bounded Migration Definition Loader Product Slice

- Date/time: 2026-08-09 KST, final local product gate
- Work/contract IDs: GDJ-0020, MIG-057..MIG-064, Q-010, Q-012
- Checkout/commit: activation
  `5942a0bedd6cca7fe93e52d90219a01193c6f534` 위 exact product diff; byte-identical final product
  commit `6172d843a4bb234592cafc176a8d1191933b141c`
- Environment/backend: macOS 26.6 darwin/arm64, Go 1.26.5; uv 0.10.12, CPython 3.14.3,
  pinned Django 6.1/SQLite 3.50.4 exact reference; lifecycle handoff는 modernc.org/sqlite v1.56.0 /
  SQLite 3.53.3
- Exit status: final `make check`, focused normal/race/CGO-disabled/vet/count-20, 5초 fuzz,
  portable/exact Python, conformance/oracle/checksum/no-rewrite와 Linux/386 cross-compile 모두 0
- Result summary: caller-provided `Source`를 strict tuple `(1,1,1,2)`와 closed codec으로 bounded
  decode하는 `migrations/definition` leaf package를 구현했습니다. Zero-safe immutable Set/report,
  exact 9 source error code, 10 resource cap, raw Planner/lifecycle error ownership, literal Schema IR 2
  drift fence와 actual `Set.Migrate` handoff가 통과했습니다. MIG-057..064는 8 `passing`이고 aggregate는
  10 adapters/105 contracts의 `100 passing + 5 deviation`입니다.
- Failures/skips: 예상하지 않은 local failure 없음. Portable Python의 exact-profile-only 15 skip은
  expected이고 exact profile은 164/164 pass. Local Linux/386는 binary cross-compile까지만 실행했고
  actual 32-bit runtime은 EVID-022의 Ubuntu hosted job에서 실행했습니다. Windows와 PostgreSQL/MySQL/
  non-SQLite DB matrix는 GDJ-0020 범위 밖이라 실행하지 않았습니다.

실행한 주요 gate:

```bash
make check
go test -count=1 ./migrations/definition ./conformance/definitionload \
  ./conformance/runners/godj ./conformance/internal/protocol ./conformance/cmd/godjcheck
go test -race -count=1 ./migrations/definition ./conformance/definitionload \
  ./conformance/runners/godj ./conformance/internal/protocol ./conformance/cmd/godjcheck
CGO_ENABLED=0 go test -count=1 ./migrations/definition ./conformance/definitionload \
  ./conformance/runners/godj ./conformance/internal/protocol ./conformance/cmd/godjcheck
go vet ./migrations/definition ./conformance/definitionload \
  ./conformance/runners/godj ./conformance/internal/protocol ./conformance/cmd/godjcheck
go test -count=20 ./migrations/definition ./conformance/definitionload
go test ./migrations/definition -run '^$' \
  -fuzz FuzzStrictScannerViaLoadNeverPanics -fuzztime=5s
CGO_ENABLED=0 GOOS=linux GOARCH=386 go test -c ./migrations/definition
make conformance-check
make godj-conformance
make python-test
make python-test-exact
make oracle-check
```

`make check`는 full Go normal/vet/race, focused CGO-disabled, portable Python, protocol,
10-set GoDj conformance와 stored-oracle checksum/no-rewrite를 포함해 최종 PASS했습니다. Focused
definition/product-equivalence count-20도 통과했고 scanner fuzz는 5초 동안 150,235 executions에서
panic 없이 통과했습니다. 독립 final review의 별도 10초 fuzz도 통과했습니다.

Public API/ownership gate는 exact exported type/field/method allowlist, external consumer compile,
zero Set, caller/source/accessor mutation, repeated/concurrent read, nested Default/IR deep copy를
검증했습니다. `Load`당 private planner validator와 product direct `migrations.NewPlanner` callsite는
각각 정확히 1이고, `Set.Migrate`의 direct `executor.Migrate` callsite와 instrumented actual handoff도
정확히 1입니다. Raw `*migrations.PlanningError`와 injected lifecycle sentinel identity는
wrap/reclassify되지 않았습니다.

각 10개 resource limit은 maximum-1/equal/+1, overflow-safe aggregate와 combined-fault precedence를
통과했습니다. Strict scanner/numeric/canonical matrix는 invalid UTF-8/BOM/trailing JSON,
any-depth decoded duplicate key, surrogate, decimal/exponent/leading-zero, signed-int64 boundary,
canonical escaping과 long-pointer fan-out를 검증했습니다. RFC 6901 lazy path comparator의 91-path,
8,281 ordered comparison도 rendered pointer byte order와 일치했습니다.

False-green gate에서 source identity/inventory, operation payload와 graph identity/dependency mutation은
actual observation과 non-empty protocol diff를 만들었습니다. Compatibility header mutation은 valid
success를 typed error로 바꾸고 `protocol.Compare`가 success/error shape mismatch로 거부했습니다.
Expected/oracle value를 actual로 되돌리는 경로는 허용되지 않습니다.

독립 `godjcheck` 두 process actual은 각각 29,631 bytes, SHA-256
`a3f40f9bbee06d4edc4af0a00f40a76da259207995ac20d030101aa2ec3aec87`로 서로 byte-identical했고
locked reference oracle과 protocol difference 0입니다. Go actual JSON과 Python oracle raw bytes의
동일성은 계약이 아니며 주장하지 않습니다. Static not-implemented comparison은 의도한 exit 1과
MIG-057..064 ordered mismatch 정확히 8개를 유지했습니다.

Product 변경 뒤 artifact pins:

| Artifact | Bytes | SHA-256 |
|---|---:|---|
| `conformance/contracts/migration-definition-source-manifest.json` | 5,147 | `688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488` |
| `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json` | 29,851 | `efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f` |
| `conformance/fixtures/godj-migration-definition-source-not-implemented.json` | 1,574 | `41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299` |
| `conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS` | 959 | `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a` |

Manifest는 MIG-057..064 status-only 변화입니다. Oracle/static/SHA bytes와 기존 9 product set의
artifact pins/classification은 불변입니다. Independent final code/integration review의 final P0–P3
finding은 없습니다. Hosted product-head 결과는 다음 EVID-022에 분리합니다.

## EVID-20260809-022 — GDJ-0020 GitHub-hosted Product-head CI

- Date/time: 2026-08-09 19:46:31–19:49:32 KST
- Work/contract IDs: GDJ-0020, MIG-057..MIG-064, Q-010, Q-012
- Tested PR/head: Draft [PR #1](https://github.com/progresshans/godj/pull/1), branch
  `codex/revision-fenced-migration-lifecycle`, exact product head
  `6172d843a4bb234592cafc176a8d1191933b141c`
- Workflow run: [31309152526](https://github.com/progresshans/godj/actions/runs/31309152526), event
  `pull_request`, base `main@f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`
- Exit status: workflow `success`; two jobs successful, cancelled/failing/skipped/pending 0
- Result summary: Ubuntu portable `make ci`, actual CGO-disabled Linux/386 definition loader runtime,
  checksum/no-rewrite와 macOS darwin/arm64 focused Go, exact Python 164/164, all-oracle/no-rewrite가
  exact product commit에서 통과했습니다.
- Failures/skips: unexpected job/step failure 없음. Ubuntu portable Python은 164 tests 중 exact-only
  15 skipped; macOS exact Python은 164/164 pass. Windows와 non-SQLite DB matrix는 구성하거나
  실행하지 않았습니다.

Hosted job evidence:

1. `Validate checked-in conformance artifacts`
   ([job 93234148999](https://github.com/progresshans/godj/actions/runs/31309152526/job/93234148999))
   - GitHub image `ubuntu-24.04`, Go 1.26.5 `linux/amd64`
   - Started 19:46:33 KST, completed 19:49:32 KST, duration 2m59s
   - `make ci` PASS; 10 product adapter/105 contract aggregate에서 definition set은
     `locked reference oracle` 8 contract/0-diff
   - 별도 `CGO_ENABLED=0`, `GOARCH=386`,
     `go test -count=1 ./migrations/definition` actual 32-bit runtime PASS
   - Stored oracle checksum 10개와 reference artifact no-rewrite gate PASS
2. `Validate exact darwin/arm64 profile and SQLite lifecycle`
   ([job 93234149051](https://github.com/progresshans/godj/actions/runs/31309152526/job/93234149051))
   - GitHub `macos-15-arm64`, Go 1.26.5 `darwin/arm64`
   - Started 19:46:34 KST, completed 19:47:44 KST, duration 1m10s
   - Focused CGO-disabled `migrations`, `db/sqlite`, GoDj adapter와 external compile gate PASS
   - uv 0.10.12/Python 3.14.3 exact 164/164, migration-definition-source를 포함한 10개 locked
     oracle `--check`, reference artifact no-rewrite gate PASS

이 run은 activation head의 과거 PASS를 재사용한 것이 아니라 product commit
`6172d843a4bb234592cafc176a8d1191933b141c`의 GitHub-hosted 로그에서 직접 확인한 증거입니다.
실제 Linux/386 runtime이 `max_length` host-int conversion을 통과했으므로 local cross-compile만으로
추정하지 않습니다. 이 evidence와 completion 문서를 담을 후속 documentation head는 아직
commit/push되지 않아 hosted CI가 pending입니다. Product-head CI를 그 후속 head의 PASS로
재귀 사용하지 않습니다.

## EVID-20260809-023 — GDJ-0020 GitHub-hosted Completion-documentation-head CI

- Date/time: 2026-08-09 20:07:10–20:10:30 KST
- Work/contract IDs: GDJ-0020, MIG-057..MIG-064, Q-010, Q-012
- Tested PR/head: Draft [PR #1](https://github.com/progresshans/godj/pull/1), branch
  `codex/revision-fenced-migration-lifecycle`, exact completion-documentation head
  `a5422f2c1ba5db34986564fc065e4b8e28ef0115`
  (`docs: complete migration definition loader product slice`)
- Workflow run: [31310002784](https://github.com/progresshans/godj/actions/runs/31310002784), event
  `pull_request`, base `main@f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`
- Runner checkout: GitHub PR merge ref
  `2ae5f2c8e2887271ae81e8c5d463de1a38a9d89c`
  (`Merge a5422f2c1ba5db34986564fc065e4b8e28ef0115 into f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`)
- Exit status: workflow `success`; two jobs and all 27 job steps successful,
  cancelled/failing/skipped/pending 0
- Result summary: EVID-022가 검증한 exact product commit 뒤의 13-file completion-documentation
  head를 current base와 합친 exact PR merge checkout에서 Ubuntu/macOS 두 job이 같은
  portable/exact product gate를 다시 통과했습니다.
- Failures/skips: unexpected job/step failure 없음. Ubuntu portable Python은 164 tests 중 exact-only
  15 skipped; macOS exact Python은 164/164 pass. Windows와 PostgreSQL/MySQL/non-SQLite DB
  matrix는 구성하거나 실행하지 않았습니다.

Hosted job evidence:

1. `Validate checked-in conformance artifacts`
   ([job 93236227654](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227654))
   - Started 20:07:19 KST, completed 20:10:30 KST, duration 3m11s
   - GitHub-hosted Ubuntu 24.04.4 LTS, image `ubuntu-24.04`; Go 1.26.5 `linux/amd64`,
     uv 0.10.12, Python 3.14.3, Django 6.1, SQLite 3.50.4
   - `make ci` PASS: full Go normal/race/vet, CGO-disabled SQLite/GoDj adapter, portable Python
     164 tests/15 skips, 10-set conformance와 definition-source locked reference oracle 8/0-diff
   - 별도 `CGO_ENABLED=0`, `GOARCH=386`,
     `go test -count=1 ./migrations/definition` actual 32-bit runtime PASS (`0.627s`)
   - Stored oracle checksum 10개와 reference artifact no-rewrite gate PASS
2. `Validate exact darwin/arm64 profile and SQLite lifecycle`
   ([job 93236227698](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227698))
   - Started 20:07:13 KST, completed 20:08:30 KST, duration 1m17s
   - GitHub-hosted macOS 15.7.7, image `macos-15-arm64`; Go 1.26.5 `darwin/arm64`,
     uv 0.10.12, Python 3.14.3, Django 6.1, SQLite 3.50.4
   - CGO-disabled focused `migrations`, `db/sqlite`, GoDj adapter와 external compile gate PASS
   - `make python-test-exact oracle-check` PASS: exact Python 164/164,
     migration-definition-source를 포함한 10개 locked oracle `--check` PASS
   - Reference artifact no-rewrite gate PASS

이 run은 product-head EVID-022를 completion 문서 상태에 재사용한 것이 아닙니다. Run metadata의
`headSha`가 exact completion-documentation commit
`a5422f2c1ba5db34986564fc065e4b8e28ef0115`이고, checkout log의 synthetic merge가 그 head와 위
base를 정확히 결합했음을 run/job/log에서 직접 확인했습니다. EVID-022의 product-head 결과와
당시의 pending 문구는 역사 증거로 그대로 보존합니다. 현재 EVID-023을 append하고 관련 상태 문서를
교정하는 이 evidence patch 자체는 아직 commit/push되지 않았고, 그 후속 head의 hosted CI는
`not run/pending`입니다. Run 31310002784를 이 evidence patch 자체의 PASS로 재귀 사용하지 않습니다.

## EVID-20260810-024 — GDJ-0021 Project-Linked Migration Check Compatibility Contracts

- Date/time: 2026-08-09–2026-08-10 KST
- Work/contract IDs: GDJ-0021, MIG-065..MIG-074, Q-010, Q-012
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; final implementation head
  `84ddf109c04acd72992b816aa72140c6e748e5f0`
  (`test: lock project-linked migration check contracts`). Local gates were run against the completed
  implementation tree before that exact tree was committed; the commit contains only GDJ-0021's allowed
  contract/artifact/test/workflow/document paths and no product `cmd/godj`, `project`, `migrations`, `db`, or
  `conformance/runners/godj` implementation change.
- Environment: local macOS darwin/arm64, Go 1.26.5. Exact reference gate used CPython 3.14.3,
  Django 6.1, SQLite 3.50.4, UTC/C and `PYTHONHASHSEED=0`.
- Exit status: all positive local gates below 0. The static not-implemented comparison returned its required
  exit 1 with ten ordered mismatches, and product `godjcheck` returned its required conformance-tool exit 2
  with no actual payload for the reference-only set.
- Result summary: MIG-065..074 are ten independent `oracle_locked` decision contracts. The reference registry
  is 11 sets/115 unique contracts/110 ordered cross-bindings. The product registry remains 10 adapters/105
  contracts, exactly `100 passing + 5 deviation`; no product project-check adapter or CLI was added.
- Failures/skips: unexpected failure 없음. Portable Python ran 174 tests with 16 exact-profile-only skips;
  exact profile ran 174/174. Windows and PostgreSQL/MySQL/non-SQLite backend execution were not run because
  this work has no corresponding product adapter/contract.

Local commands and results:

1. Project-check feasibility and Unix process/filesystem gates
   - `gofmt -d conformance/projectcheck/*.go` and `git diff --check`: PASS, no output
   - `go test -count=1 ./conformance/projectcheck`: PASS (independent final root run `3.354s`)
   - `go test -race -count=1 ./conformance/projectcheck`: PASS (`5.011s`)
   - `CGO_ENABLED=0 go test -count=1 ./conformance/projectcheck`: PASS (`3.054s`)
   - `go vet ./conformance/projectcheck`: PASS
   - `go test -count=20 ./conformance/projectcheck`: PASS (`58.243s`)
   - A Linux/amd64 CGO-disabled test compile and the focused MIG-065..074 actual-build end-to-end gate also
     passed. The latter built the fixture with exact
     `go build -mod=readonly -o <private-output> ./cmd/mysite`, observed one linked `definition.Load`,
     the expected counts/digest and protocol wire,
     left the project tree byte/mode-identical, created no `go.sum`, removed the private root and retained no
     raw diagnostic.
2. Repository-wide and reference gates
   - `make ci`: PASS. It covered generation drift, full Go normal/race/vet, CGO-disabled SQLite and GoDj
     runner packages, portable Python 174/16-skipped, all conformance/protocol/static/product checks and
     artifact no-rewrite checks.
   - `make python-test-exact oracle-check`: PASS; exact Python 174/174 and all 11 stored oracles passed.
   - Two distinct hash-seed generation runs produced byte-identical project-check oracle output.
   - Stored checksum verification passed. New exact pins are manifest 4,580 bytes
     `0cd8d77b03820af75c8bda8434620f40acd1a3cb6319cf4fb732db4b38d44218`, static fixture 1,729 bytes
     `86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`, oracle 19,971 bytes
     `49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, and 11-line `SHA256SUMS`
     1,061 bytes `74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`. Its previous 10-line prefix
     remains 959 bytes `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`.
3. Independent review
   - Contract/protocol/reference/workflow integration audit: P0/P1/P2/P3 findings 0.
   - Filesystem/process/cancellation/security audit: P0/P1/P2/P3 findings 0.

This evidence establishes a contract/test-only feasibility boundary, not an implemented global `godj`
command, public project package, production project runner, DB-aware migration check, or non-SQLite backend.
Q-010 and Q-012 therefore remain `Partial`. Exact implementation-head hosted evidence is recorded separately
in EVID-20260810-025. The status 7 + general 9, exact 16-file completion documentation patch containing
EVID-024/EVID-025 has not yet been committed or pushed, so hosted CI for that later
completion-documentation head is `not run/pending`.

## EVID-20260810-025 — GDJ-0021 GitHub-hosted 10-job Implementation-head CI

- Date/time: 2026-08-10 00:19:27–00:23:14 KST
  (2026-08-09 15:19:27–15:23:14 UTC)
- Work/contract IDs: GDJ-0021, MIG-065..MIG-074, Q-010, Q-012
- Tested PR/head: Draft/open [PR #1](https://github.com/progresshans/godj/pull/1), branch
  `codex/revision-fenced-migration-lifecycle`, exact implementation head
  `84ddf109c04acd72992b816aa72140c6e748e5f0`
  (`test: lock project-linked migration check contracts`)
- PR state at evidence collection: `OPEN`, `DRAFT`, merge state `CLEAN`
- Workflow run: [31320798963](https://github.com/progresshans/godj/actions/runs/31320798963), attempt 1,
  event `pull_request`, base `main@f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`
- Runner checkout: synthetic PR merge
  `641b8f83994647f8e78f44de56997409c22afddd` with parents exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and exact head
  `84ddf109c04acd72992b816aa72140c6e748e5f0`; the synthetic merge tree was independently verified
  byte-identical to the head tree. Run `headSha` and runner checkout commit are intentionally distinguished.
- Exit status: workflow `success`; exact 10 expanded job executions all successful,
  cancelled/failing/skipped/pending 0. Every listed validation and final clean-worktree step succeeded.
- Result summary: the existing Ubuntu full and macOS exact jobs were preserved and focused project-check
  validation was added. Four project-check and four actual SQLite matrix legs then validated Linux/macOS and
  amd64/arm64 coordinates, giving exact `2 + 4 + 4 = 10` hosted executions.
- Failures/skips: unexpected job/step failure or skip 없음. Portable Python's 16 exact-only test skips are
  expected; macOS exact Python passed 174/174. Windows and service-only PostgreSQL/MySQL jobs were deliberately
  absent because there is no actual product adapter/contract to prevent a false green.

Hosted job evidence:

| Job | ID | Started–completed (KST) | Required validation |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93263350766](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350766) | 00:19:30–00:23:13 | Ubuntu 24.04.4 image `20260720.247.2`, linux/amd64; `make ci`, focused project-check, actual Linux/386 definition-loader runtime, 11-line checksum and reference no-rewrite |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93263350749](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350749) | 00:19:29–00:20:41 | macOS 15.7.7 arm64 image `20260727.0256.1`, darwin/arm64; CGO-disabled focused lifecycle, focused project-check, exact Python 174/174, all-oracle and no-rewrite |
| Project check (`ubuntu-22.04`) | [93263350782](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350782) | 00:19:31–00:20:36 | linux/amd64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| Project check (`ubuntu-24.04-arm`) | [93263350790](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350790) | 00:19:29–00:20:18 | linux/arm64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| Project check (`macos-15-intel`) | [93263350811](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350811) | 00:19:30–00:21:50 | darwin/amd64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| Project check (`macos-26`) | [93263350787](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350787) | 00:19:29–00:20:16 | darwin/arm64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-22.04`) | [93263351000](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263351000) | 00:19:29–00:21:02 | linux/amd64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-24.04-arm`) | [93263350809](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350809) | 00:19:29–00:20:42 | linux/arm64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-15-intel`) | [93263350814](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350814) | 00:19:30–00:21:04 | darwin/amd64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-26`) | [93263350816](https://github.com/progresshans/godj/actions/runs/31320798963/job/93263350816) | 00:19:30–00:20:32 | darwin/arm64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |

Both four-leg matrices used pinned checkout/setup-go actions, Go 1.26.5, `strategy.fail-fast: false`, a
20-minute leg timeout and no `continue-on-error`. Each asserted exact `go env GOOS`/`GOARCH`, then ran:

```text
go test -count=1 <package-set>
go test -race -count=1 <package-set>
CGO_ENABLED=0 go test -count=1 <package-set>
go vet <package-set>
git diff --exit-code
test -z "$(git status --porcelain=v1)"
```

For project-check `<package-set>` was `./conformance/projectcheck`; for SQLite it was
`./migrations ./db/sqlite`. Thus the matrix exercised actual tests in every advertised coordinate and did not
use skips or service availability as support evidence. This run applies only to exact implementation head
`84ddf109c04acd72992b816aa72140c6e748e5f0`. The status 7 + general 9, exact 16-file completion
documentation patch that records this evidence is a later working-tree change and has not yet been committed
or pushed; its exact-head hosted CI is `not run/pending`. Run 31320798963 must not be reused as proof for that
later documentation head.

## EVID-20260810-026 — GDJ-0021 GitHub-hosted Completion-documentation-head 10-job CI

- Date/time: 2026-08-10 00:48:59–00:52:35 KST
  (2026-08-09 15:48:59–15:52:35 UTC)
- Work/contract IDs: GDJ-0021, MIG-065..MIG-074, Q-010, Q-012
- Tested PR/head: Draft/open [PR #1](https://github.com/progresshans/godj/pull/1), branch
  `codex/revision-fenced-migration-lifecycle`, exact completion-documentation head
  `34ae58fc2490deb8f884a0b5591520b11bae8669`
  (`docs: complete project-linked migration check contracts`)
- Workflow run: [31322122760](https://github.com/progresshans/godj/actions/runs/31322122760), attempt 1,
  event `pull_request`, base `main@f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`
- Runner checkout: synthetic PR merge
  `6d1ce08495c2ccc4ee01620d565a54a42c6e2c44` with parents exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and exact head
  `34ae58fc2490deb8f884a0b5591520b11bae8669`. The merge and head have the same tree
  `942f6b7bcee0db4fc8d3822122a52c1de09b51cf`; run `headSha` and checkout commit are intentionally
  distinguished.
- Exit status: workflow `success`; exact 10 expanded job executions all successful,
  cancelled/failing/skipped/pending 0. Every named validation and final matrix clean-worktree step succeeded.
- Result summary: EVID-025의 implementation head 뒤 exact status 7 + general 9인 16-file
  completion-documentation commit을 current base와 결합한 PR checkout에서 existing full/exact 2개,
  project-check 4개와 actual SQLite 4개인 `2 + 4 + 4 = 10` hosted execution이 다시 통과했습니다.
- Failures/skips: unexpected job/step failure or skip 없음. Ubuntu portable Python의 16 exact-only
  test skips are expected; macOS exact Python passed 174/174. Windows와 service-only
  PostgreSQL/MySQL job은 actual product adapter/contract가 없어 구성하지 않았습니다.

Hosted job evidence:

| Job | ID | Started–completed (KST) | Required validation |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93266624027](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624027) | 00:49:01–00:52:34 | Ubuntu 24.04.4 image `20260720.247.2`, linux/amd64; `make ci`, focused project-check, actual Linux/386 definition-loader runtime, 11-line checksum and reference no-rewrite |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93266624013](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624013) | 00:50:06–00:51:00 | macOS 15.7.7 arm64 image `20260727.0256.1`, darwin/arm64; CGO-disabled focused lifecycle, focused project-check, exact Python 174/174, all-oracle and no-rewrite |
| Project check (`ubuntu-22.04`) | [93266624008](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624008) | 00:49:01–00:50:05 | linux/amd64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| Project check (`ubuntu-24.04-arm`) | [93266624024](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624024) | 00:49:01–00:49:47 | linux/arm64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| Project check (`macos-15-intel`) | [93266624018](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624018) | 00:49:02–00:50:41 | darwin/amd64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| Project check (`macos-26`) | [93266623997](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266623997) | 00:49:01–00:50:04 | darwin/arm64 coordinate + project-check normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-22.04`) | [93266624032](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624032) | 00:49:01–00:50:39 | linux/amd64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-24.04-arm`) | [93266624029](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624029) | 00:49:01–00:50:21 | linux/arm64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-15-intel`) | [93266624017](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624017) | 00:49:01–00:51:08 | darwin/amd64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-26`) | [93266624023](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624023) | 00:49:01–00:50:12 | darwin/arm64 coordinate + actual `./migrations ./db/sqlite` normal/race/CGO-disabled/vet/clean |

Both four-leg matrices used Go 1.26.5 and asserted exact `GOOS`/`GOARCH`. Every project-check and SQLite
leg ran normal, race, CGO-disabled and vet gates, then passed both `git diff --exit-code` and empty
`git status --porcelain=v1`. The Ubuntu full log records Python 3.14.3, Django 6.1 and SQLite 3.50.4,
portable 174 tests/16 expected skips, focused project-check, actual `CGO_ENABLED=0 GOARCH=386` loader,
all 11 checksum entries and no-rewrite. The macOS exact log records the same Python/Django/SQLite profile,
exact 174/174, all 11 oracle `--check` commands and no-rewrite.

This run applies only to exact completion-documentation head
`34ae58fc2490deb8f884a0b5591520b11bae8669`. EVID-024/EVID-025의 implementation-tree 결과와 당시
pending 문구는 역사 증거로 그대로 보존합니다. EVID-026을 append하고 관련 현재 상태를 교정하는
exact 8-file evidence/status patch는 아직 commit/push되지 않았으므로 그 후속 head의 hosted CI는
`not run/pending`입니다. Run 31322122760을 이 evidence patch 자체의 PASS로 재귀 사용하지 않습니다.

## EVID-20260810-027 — GDJ-0022 Project-Linked Migration Check Product Slice

- Date/time: 2026-08-10 KST
- Work/contract IDs: GDJ-0022, MIG-065..MIG-074, Q-010, Q-012
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation head
  `e4de64645bd93cf5e55c746bb6a109c53916cca8` plus the uncommitted GDJ-0022 implementation/pre-hosted
  documentation working tree. The exact implementation+documentation commit does not exist yet and will be
  pushed to the same Draft PR #1 before hosted verification.
- Environment: local macOS darwin/arm64, Go 1.26.5. Routine portable gate used uv 0.12.3 and CPython
  3.14.3; historical exact reproduction used ephemeral uv 0.10.12 with CPython 3.14.3. Django 6.1,
  asgiref 3.12.1, sqlparse 0.5.5 and SQLite 3.50.4 are the reference dependencies.
- Exit status: all commands below exited 0. Independent global, linked and adapter/CI final audits ended with
  P0/P1/P2/P3 finding 0.
- Result summary: exact global `godj migrations check`, public two-export `project.Config`/`project.Run`,
  independent global/linked/protocol product kernels, flat no-follow discovery and the eleventh actual GoDj
  adapter are implemented. MIG-065..074 are ten `passing` contracts; the product aggregate is exact
  11 adapters/115 contracts, `110 passing + 5 deviation`, while the independent reference remains
  11 sets/115 scenarios/110 ordered cross-bindings.
- Failures/skips/not run: unexpected local failure 없음. Portable Python ran 174 tests with 16 intentional
  exact-profile-only skips; historical exact Python passed 174/174. `make ci` emitted one stale uv interpreter
  cache warning and removed that stale cache automatically; the gate still passed. Linux/386 was compile-only,
  not runtime. The exact 18 hosted executions, including Python 3.12.13/3.13.15/3.14.3/3.14.7 and the four
  Linux/macOS product coordinates, have not run and remain pending. Windows and PostgreSQL/MySQL service-only
  jobs were not configured because corresponding product contracts/adapters do not exist.

Local commands and results:

1. Routine complete repository gate

   ```bash
   make ci
   ```

   PASS under `uv 0.12.3`; `uv run --frozen python --version` reported exact `Python 3.14.3`. This covered
   full Go normal/vet/race, focused CGO-disabled product packages, portable Python `Ran 174` /
   `OK (skipped=16)`, conformance and all eleven product adapters. The 11 locked reference artifacts remained
   unchanged.

2. Historical exact reference, one-time local reproduction

   ```bash
   uvx --from uv==0.10.12 uv run --frozen make python-test-exact oracle-check
   ```

   PASS: exact Python 174/174 and all eleven stored oracle `--check` commands. This intentionally retains the
   uv 0.10.12 manager fingerprint embedded in the exact profile; routine local/portable/compatibility work uses
   uv 0.12.3. Oracle/static/checksum artifacts were unchanged.

3. Focused product regression and portability gates

   ```bash
   go test -count=1 ./internal/projectcheck ./cmd/godj
   go test -race -count=1 ./internal/projectcheck ./cmd/godj
   CGO_ENABLED=0 go test -count=1 ./internal/projectcheck ./cmd/godj
   go vet ./internal/projectcheck ./cmd/godj
   go test -count=20 ./internal/projectcheck
   ```

   PASS. The final global remediation re-audit also passed its macOS case-alias, terminal barrier,
   pre-start cancellation, queued child reap, retained physical identity and diff-check regressions. Separate
   linked and adapter/CI audits were clean at every severity.

4. Linux/386 compile-only product boundary

   ```bash
   build_dir=$(mktemp -d)
   packages=(./cmd/godj ./project ./internal/projectcheck ./internal/projectcheck/linked ./internal/projectcheck/protocol ./conformance/runners/godj)
   for package in $packages; do
     name=$(printf '%s' "$package" | tr '/.' '__')
     GOOS=linux GOARCH=386 CGO_ENABLED=0 go test -c -o "$build_dir/$name.test" "$package"
     test -s "$build_dir/$name.test"
   done
   ```

   PASS: six non-empty Linux/386 test binaries were produced. This is deliberately recorded as compile-only;
   hosted Linux/386 runtime success is not claimed.

5. Dependency and generated/artifact drift

   ```bash
   go mod tidy -diff
   git diff --check
   ```

   PASS/no diff. Existing `golang.org/x/sys v0.47.0` moved from indirect to direct because production Unix
   code imports it; no dependency version/hash changed and `go.sum` is unchanged.

This evidence establishes local implementation and review completion only. The workflow now describes exact
`2 + 4 + 4 + 4 + 4 = 18` required executions, but neither that implementation/completion head nor the later
evidence-only follow-up head has been run on GitHub. A hosted run must be collected after commit/push and
recorded under a new evidence ID; EVID-027 must not be reused as hosted proof.

## EVID-20260810-028 — GDJ-0022 GitHub-hosted Exact 18-job Completion CI

- Date/time: successful run 2026-08-10 03:32:30–03:39:40 KST
  (2026-08-09 18:32:30–18:39:40 UTC); initial diagnostic run 03:31:02–03:32:54 KST
  (2026-08-09 18:31:02–18:32:54 UTC)
- Work/contract IDs: GDJ-0022, MIG-065..MIG-074, Q-010, Q-012
- Tested PR/head: Draft/open [PR #1](https://github.com/progresshans/godj/pull/1), branch
  `codex/revision-fenced-migration-lifecycle`; initial implementation head
  `06858dd6aafeb20449bc4fbfa9aeac78c7a794ce` (`feat: add project-linked migration check product`) and
  successful fix head `3dfeff2a881a3313883729943519896798d92afc`
  (`ci: accept uv version metadata`)
- PR state at evidence collection: `OPEN`, `DRAFT`, merge state `CLEAN`; successful run `headSha` exactly
  `3dfeff2a881a3313883729943519896798d92afc`
- Successful workflow run: [31329294154](https://github.com/progresshans/godj/actions/runs/31329294154),
  attempt 1, event `pull_request`, exact 18 jobs
- Runner checkout: synthetic PR merge `fa120f96b6659a86a5d2729eda0a7f78b155747e` with parents exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and exact head
  `3dfeff2a881a3313883729943519896798d92afc`. GitHub commit API verification found both the synthetic merge
  and head tree equal to `dd1628992b169bace6bd9dac6348608e0f2e8578`; run `headSha` and checkout commit
  are intentionally distinguished.
- Exit status: successful workflow conclusion `success`; exact 18/18 jobs successful, failure/cancelled 0,
  and no non-success job step. Every matrix clean-worktree step succeeded.
- Result summary: existing Ubuntu full/macOS exact 2, independent project-check proof 4 and actual SQLite 4
  were preserved. Actual product project-check 4 and exact Python compatibility 4 also passed, giving exact
  `2 + 4 + 4 + 4 + 4 = 18` hosted executions. MIG-065..074 remain ten `passing` contracts and the product
  aggregate remains 11 adapters/115 contracts, `110 passing + 5 deviation`.
- Failures/skips/not run: the successful run had no unexpected failure, cancelled job or skipped job.
  Portable Python intentionally skipped 16 exact-profile-only tests in the Ubuntu full job and each
  compatibility leg; exact darwin Python passed 174/174. Linux/386 was compile-only. Windows and
  PostgreSQL/MySQL service-only jobs were deliberately absent because corresponding actual support contracts
  do not exist. The initial run failure and cancellation are recorded below rather than hidden.

Initial run and remediation:

- [Run 31329231255](https://github.com/progresshans/godj/actions/runs/31329231255), attempt 1, had exact
  `headSha` `06858dd6aafeb20449bc4fbfa9aeac78c7a794ce` and exact 18 jobs. Its final conclusion was
  `cancelled`: 5 jobs succeeded, all 4 Python jobs failed, and the remaining 9 jobs were cancelled.
- All four failures were the same pre-test step, `Assert isolated compatibility runtime`: Python 3.12.13
  [job 93284640931](https://github.com/progresshans/godj/actions/runs/31329231255/job/93284640931),
  3.13.15 [job 93284640894](https://github.com/progresshans/godj/actions/runs/31329231255/job/93284640894),
  3.14.3 [job 93284640914](https://github.com/progresshans/godj/actions/runs/31329231255/job/93284640914),
  and 3.14.7 [job 93284640893](https://github.com/progresshans/godj/actions/runs/31329231255/job/93284640893).
  Each failed at exact shell assertion `test "$(uv --version)" = "uv 0.12.3"` with exit 1, before portable
  Python tests or digest checks ran. This was a CI assertion failure, not evidence of a Python/Django failure.
- Commit `3dfeff2a881a3313883729943519896798d92afc` retained the exact uv 0.12.3 pin but changed the assertion
  to accept an allowed whitespace-delimited version metadata suffix:
  `uv --version | grep -Eq '^uv 0[.]12[.]3([[:space:]]|$)'`. The protocol static expectation changed in the
  same commit, and the successful run below re-executed the entire 18-job topology rather than only rerunning
  the four failed legs.

Successful hosted job evidence:

| Job | ID | Started–completed (KST) | Required validation |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93284842203](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842203) | 03:32:56–03:37:53 | Ubuntu 24.04.4 image `20260720.247.2`, linux/amd64; Go 1.26.5, CPython 3.14.3, `make ci`, product focus, Linux/386 compile-only, 11 checksums and no-rewrite |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93284842168](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842168) | 03:32:56–03:33:49 | macOS 15.7.7 arm64 image `20260727.0256.1`; Go 1.26.5, CPython 3.14.3, historical exact uv 0.10.12, exact 174/174, 11 oracle checks and no-rewrite |
| Project check (`ubuntu-22.04`) | [93284842178](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842178) | 03:32:56–03:33:44 | linux/amd64 independent proof normal/race/CGO-disabled/vet/clean |
| Project check (`ubuntu-24.04-arm`) | [93284842170](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842170) | 03:32:56–03:33:39 | linux/arm64 independent proof normal/race/CGO-disabled/vet/clean |
| Project check (`macos-15-intel`) | [93284842166](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842166) | 03:32:57–03:34:54 | darwin/amd64 independent proof normal/race/CGO-disabled/vet/clean |
| Project check (`macos-26`) | [93284842163](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842163) | 03:32:56–03:33:50 | darwin/arm64 independent proof normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-22.04`) | [93284842207](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842207) | 03:32:57–03:34:45 | linux/amd64 actual SQLite normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-24.04-arm`) | [93284842214](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842214) | 03:32:56–03:34:10 | linux/arm64 actual SQLite normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-15-intel`) | [93284842219](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842219) | 03:33:52–03:37:04 | darwin/amd64 actual SQLite normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-26`) | [93284842221](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842221) | 03:32:56–03:34:46 | darwin/arm64 actual SQLite normal/race/CGO-disabled/vet/clean |
| Product project check (`ubuntu-22.04`) | [93284842213](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842213) | 03:32:56–03:36:02 | linux/amd64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Product project check (`ubuntu-24.04-arm`) | [93284842210](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842210) | 03:32:56–03:35:11 | linux/arm64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Product project check (`macos-15-intel`) | [93284842215](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842215) | 03:33:55–03:39:39 | darwin/amd64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Product project check (`macos-26`) | [93284842211](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842211) | 03:32:56–03:35:10 | darwin/arm64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Python compatibility (`3.12.13`) | [93284842179](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842179) | 03:32:56–03:33:12 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |
| Python compatibility (`3.13.15`) | [93284842160](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842160) | 03:32:56–03:33:18 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |
| Python compatibility (`3.14.3`) | [93284842196](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842196) | 03:32:56–03:33:26 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |
| Python compatibility (`3.14.7`) | [93284842190](https://github.com/progresshans/godj/actions/runs/31329294154/job/93284842190) | 03:32:56–03:33:25 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |

All four Python logs directly asserted their exact `EXPECTED_PYTHON`, then reported `Ran 174 tests` and
`OK (skipped=16)`. Each also asserted the exact 115-scenario registry and generated the same canonical payload:
464,087 bytes, SHA-256 `aa2ed24d41434b9756e4a4669a04ea44f2a457a94a4bdd31dcab9ff3d6b7afe8`.
All four actual product jobs ran the separate normal, race, CGO-disabled and vet gates over
`./cmd/godj ./project ./internal/projectcheck/... ./conformance/runners/godj`, including external project E2E
and actual adapter checks, then passed tracked-diff and porcelain-empty gates. The independent proof and SQLite
matrices remained separate required executions.

The Ubuntu full log also records portable Python 174/16 expected skips, the 11-product-adapter conformance
gate, the six product-boundary Linux/386 package checks in compile/no-test mode, all 11 checksum entries and
artifact no-rewrite. The macOS exact job records exact Python 174/174 and all 11 locked oracle `--check`
commands. These results establish the GDJ-0022 implementation/fix head's required hosted acceptance; they do
not establish Windows or PostgreSQL/MySQL support.

This EVID-028/completion-status documentation patch is later than tested head
`3dfeff2a881a3313883729943519896798d92afc` and is currently uncommitted/unpushed. Its own exact-head hosted CI
is therefore `not run/pending`. Run 31329294154 must not be recursively reused as proof of this later evidence
patch; after the patch is committed and pushed, its final exact-head result must be recorded separately.

## EVID-20260810-029 — GDJ-0022 Final GitHub-hosted Process Stabilization CI

- Date/time: successful stabilization run 2026-08-10 04:39:14–04:44:55 KST
  (2026-08-09 19:39:14–19:44:55 UTC); failed completion-documentation run
  04:02:32–04:07:23 KST (2026-08-09 19:02:32–19:07:23 UTC)
- Work/contract IDs: GDJ-0022, MIG-065..MIG-074, Q-010, Q-012
- Tested PR/heads: Draft/open [PR #1](https://github.com/progresshans/godj/pull/1), branch
  `codex/revision-fenced-migration-lifecycle`; completion-documentation head
  `68b408add3b050d0938ccebc6c83200499f57b2a` (`docs: complete project migration check product`) and final
  stabilization head `385382efffd1872ae7fb427192bab27b95dc57e2`
  (`fix: harden project process synchronization`)
- PR state at evidence collection: `OPEN`, `DRAFT`, merge state `CLEAN`; current PR head and local `HEAD`
  exactly `385382efffd1872ae7fb427192bab27b95dc57e2`
- Exit status: completion-documentation run
  [31330601427](https://github.com/progresshans/godj/actions/runs/31330601427) completed `failure` with exact
  18 jobs: 16 success, 2 failure, cancelled/skipped 0. Final stabilization run
  [31332208055](https://github.com/progresshans/godj/actions/runs/31332208055) completed `success` with exact
  18/18 success, failure/cancelled/skipped 0 and no non-success job step.
- Result summary: the first run exposed two distinct macOS product-test synchronization assumptions. The
  three-file follow-up made test-helper readiness atomic, made the actual SIGINT E2E harness cold-build-aware
  with bounded early-exit/kill/reap behavior, and added product process reconciliation for a directly reaped
  child whose Wait result publication is delayed. Local focused repetition, full `make ci`, independent
  P0/P1/P2/P3 audit and the final exact 18-job hosted run all passed.
- Failures/skips/not run: both failed jobs and their exact logs are retained below. All four Python jobs and
  twelve of the other fourteen jobs passed in the failed run. The final run had no unexpected failure or
  skipped job;
  portable Python retained 16 intentional exact-profile-only skips and exact darwin passed 174/174.
  Linux/386 remained compile-only. Windows and PostgreSQL/MySQL service-only jobs remained absent because
  corresponding actual support contracts do not exist.

Failed completion-documentation-head checkout and jobs:

- Run `31330601427` `headSha` was exactly
  `68b408add3b050d0938ccebc6c83200499f57b2a`. Hosted checkout used synthetic PR merge
  `e8e999232783f74455e66bcf4d3e637457784539`, with parents exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and exact head
  `68b408add3b050d0938ccebc6c83200499f57b2a`. GitHub commit API verification found both merge and head tree
  equal to `8c68cc726df6c3b9ef1c13a3252c1f9a126330b4`.

| Job | ID | Started–completed (KST) | Result |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93288120025](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120025) | 04:02:34–04:07:21 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93288120040](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120040) | 04:02:35–04:03:41 | success |
| Project check (`ubuntu-22.04`) | [93288120067](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120067) | 04:02:34–04:03:36 | success |
| Project check (`ubuntu-24.04-arm`) | [93288120062](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120062) | 04:02:34–04:03:20 | success |
| Project check (`macos-15-intel`) | [93288120102](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120102) | 04:02:35–04:04:05 | success |
| Project check (`macos-26`) | [93288120090](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120090) | 04:02:35–04:03:45 | success |
| SQLite (`ubuntu-22.04`) | [93288120112](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120112) | 04:02:36–04:04:16 | success |
| SQLite (`ubuntu-24.04-arm`) | [93288120099](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120099) | 04:02:34–04:04:01 | success |
| SQLite (`macos-15-intel`) | [93288120131](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120131) | 04:03:44–04:05:52 | success |
| SQLite (`macos-26`) | [93288120130](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120130) | 04:03:47–04:05:09 | success |
| Product project check (`ubuntu-22.04`) | [93288120049](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120049) | 04:02:34–04:05:45 | success |
| Product project check (`ubuntu-24.04-arm`) | [93288120037](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120037) | 04:02:35–04:04:46 | success |
| Product project check (`macos-15-intel`) | [93288120074](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120074) | 04:02:35–04:06:01 | **failure** |
| Product project check (`macos-26`) | [93288120041](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120041) | 04:02:35–04:03:52 | **failure** |
| Python compatibility (`3.12.13`) | [93288120104](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120104) | 04:02:34–04:02:54 | success |
| Python compatibility (`3.13.15`) | [93288120101](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120101) | 04:02:34–04:03:01 | success |
| Python compatibility (`3.14.3`) | [93288120115](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120115) | 04:02:34–04:03:03 | success |
| Python compatibility (`3.14.7`) | [93288120159](https://github.com/progresshans/godj/actions/runs/31330601427/job/93288120159) | 04:02:34–04:03:06 | success |

Both failures occurred in `Run normal product project-check tests`; setup/coordinate assertions had passed:

1. `macos-26` job `93288120041` failed
   `TestOwnedProcessCancelAfterDirectReapClosesDescendantHeldPipes` in 0.03 seconds with
   `process_test.go:169: invalid helper process pair ""`. The helper created the readiness path before its
   PID-pair payload was completely visible, so the reader could observe an empty file. This was a non-atomic
   test-helper publication bug, not a product protocol result.
2. `macos-15-intel` job `93288120074` failed
   `TestActualGodjMigrationCheckProcess/handled_SIGINT_reaps_runner_and_cleans_private_workspace` after
   20.01 seconds with `timed out waiting for .../runner-ready`. The parent test took 141.84 seconds and its
   cold private GOCACHE/GOMODCACHE project build exceeded the harness's fixed 20-second readiness assumption.
   The log did not report a wrong exit/category or leaked workspace; the test stopped before issuing SIGINT.

Fix scope and local stabilization evidence:

- Commit `385382efffd1872ae7fb427192bab27b95dc57e2` changed exactly three GDJ-0022-allowed files:
  `cmd/godj/main_test.go`, `internal/projectcheck/process_test.go`, and
  `internal/projectcheck/process_unix.go` (115 insertions, 18 deletions).
- Helper readiness now writes a complete payload to a sibling temporary file, closes it, then atomically
  renames it into place. The SIGINT E2E readiness wait now allows the intentionally cold private build up to
  two minutes, observes early process exit, uses `WaitDelay`, and on harness failure performs bounded
  interrupt followed by kill and mandatory reap.
- The race audit also found a production timing window: the direct child may already be reaped while the
  buffered Wait-result send has not yet become visible to the cancellation coordinator. Product code now
  checks the queued result first, probes `Signal(0)` for `os.ErrProcessDone`, and synchronously consumes the
  delayed Wait publication before deciding whether process-group SIGINT/SIGKILL is needed. Active-child
  escalation behavior remains unchanged.

Exact local commands and results on final head:

```bash
go test -race -count=50 ./internal/projectcheck -run '^(TestOwnedProcessCancelAfterDirectReapClosesDescendantHeldPipes|TestAlreadyReapedDirectChildWaitPublicationIsReconciled)$'
go test -race -count=5 ./internal/projectcheck -run '^TestOwnedProcessCancellationSignalsGroupEscalatesAndReaps$'
go test -count=20 ./cmd/godj -run '^TestActualGodjMigrationCheckProcess/handled_SIGINT_reaps_runner_and_cleans_private_workspace$'
go test -count=1 ./internal/projectcheck ./cmd/godj
go test -race -count=1 ./internal/projectcheck ./cmd/godj
CGO_ENABLED=0 go test -count=1 ./internal/projectcheck ./cmd/godj
go vet ./internal/projectcheck ./cmd/godj
make ci
git diff --check
```

All exited 0. The two-test race count-50 gate passed in 105.291 seconds; active-child escalation race
count-5 passed; the actual SIGINT E2E count-20 gate passed in 102.585 seconds. `make ci` passed under local
uv 0.12.3 / CPython 3.14.3, including full Go normal/race/vet, focused CGO-disabled products, portable Python
174/16 intentional skips and all eleven product adapters. The independent hosted-flake final audit reported
P0/P1/P2/P3 = 0/0/0/0, and no helper/runner/`godj` process remained.

Final exact-head hosted checkout and jobs:

- Run `31332208055` `headSha` was exactly
  `385382efffd1872ae7fb427192bab27b95dc57e2`. Hosted checkout used synthetic PR merge
  `0893e7c38916e88ba33008f0e68898d5711c6ef9`, with parents exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and exact head
  `385382efffd1872ae7fb427192bab27b95dc57e2`. GitHub commit API verification found both merge and head tree
  equal to `f149be8870b01ea9492a7b8b257550e2ac10d071`.

| Job | ID | Started–completed (KST) | Required validation |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93292142084](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142084) | 04:39:16–04:42:35 | Ubuntu 24.04.4 image `20260720.247.2`, linux/amd64; Go 1.26.5, CPython 3.14.3, `make ci`, product focus, Linux/386 compile-only, 11 checksums and no-rewrite |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93292142012](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142012) | 04:39:17–04:40:36 | macOS 15.7.7 arm64 image `20260727.0256.1`; Go 1.26.5, CPython 3.14.3, historical exact uv 0.10.12, exact 174/174, 11 oracle checks and no-rewrite |
| Project check (`ubuntu-22.04`) | [93292142120](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142120) | 04:39:16–04:40:15 | linux/amd64 independent proof normal/race/CGO-disabled/vet/clean |
| Project check (`ubuntu-24.04-arm`) | [93292142090](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142090) | 04:39:16–04:39:59 | linux/arm64 independent proof normal/race/CGO-disabled/vet/clean |
| Project check (`macos-15-intel`) | [93292142082](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142082) | 04:39:16–04:40:32 | darwin/amd64 independent proof normal/race/CGO-disabled/vet/clean |
| Project check (`macos-26`) | [93292142103](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142103) | 04:39:17–04:40:07 | darwin/arm64 independent proof normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-22.04`) | [93292142045](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142045) | 04:39:16–04:40:52 | linux/amd64 actual SQLite normal/race/CGO-disabled/vet/clean |
| SQLite (`ubuntu-24.04-arm`) | [93292142071](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142071) | 04:39:16–04:40:34 | linux/arm64 actual SQLite normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-15-intel`) | [93292142047](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142047) | 04:39:18–04:41:34 | darwin/amd64 actual SQLite normal/race/CGO-disabled/vet/clean |
| SQLite (`macos-26`) | [93292142110](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142110) | 04:39:17–04:40:39 | darwin/arm64 actual SQLite normal/race/CGO-disabled/vet/clean |
| Product project check (`ubuntu-22.04`) | [93292142059](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142059) | 04:39:16–04:42:13 | linux/amd64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Product project check (`ubuntu-24.04-arm`) | [93292142048](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142048) | 04:39:16–04:41:33 | linux/arm64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Product project check (`macos-15-intel`) | [93292142077](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142077) | 04:40:09–04:44:54 | darwin/amd64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Product project check (`macos-26`) | [93292142115](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142115) | 04:40:34–04:43:28 | darwin/arm64 actual CLI/public project/adapter normal/race/CGO-disabled/vet/clean |
| Python compatibility (`3.12.13`) | [93292142114](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142114) | 04:39:17–04:39:34 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |
| Python compatibility (`3.13.15`) | [93292142107](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142107) | 04:39:16–04:39:40 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |
| Python compatibility (`3.14.3`) | [93292142123](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142123) | 04:39:17–04:39:46 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |
| Python compatibility (`3.14.7`) | [93292142096](https://github.com/progresshans/godj/actions/runs/31332208055/job/93292142096) | 04:39:22–04:39:49 | exact runtime; portable 174/16 expected skips; 115 scenarios, canonical payload/digest, clean |

All four Python logs asserted exact `EXPECTED_PYTHON`, reported `Ran 174 tests` and `OK (skipped=16)`, then
verified 115 scenarios, canonical payload 464,087 bytes and SHA-256
`aa2ed24d41434b9756e4a4669a04ea44f2a457a94a4bdd31dcab9ff3d6b7afe8`. All four product jobs passed
normal, race, CGO-disabled, vet, tracked-diff and porcelain-empty gates. In particular, both previously failing
macOS product coordinates completed normal and race steps. The Ubuntu full job passed portable Python,
eleven-product-adapter conformance, seven-package Linux/386 compile/no-test loading, all eleven checksums and
artifact no-rewrite; exact darwin passed Python 174/174, all eleven locked oracles and no-rewrite. Independent
proof and SQLite four-leg matrices remained separate required executions.

This EVID-029/status documentation patch is later than tested head
`385382efffd1872ae7fb427192bab27b95dc57e2` and is currently uncommitted/unpushed. Its own exact-head hosted CI
is therefore `not run/pending`. Run 31332208055 must not be recursively reused as proof for this later evidence
patch; after commit/push, its exact-head result must be recorded separately. No merge was performed.

## EVID-20260810-030 — GDJ-0022 final evidence-documentation exact-head CI and GDJ-0023 activation baseline

- Date/time: 2026-08-10T05:06:55+09:00–2026-08-10T05:12:07+09:00
- Work/contract IDs: GDJ-0022, MIG-065..MIG-074; GDJ-0023 activation baseline only
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@1f161f311daa775e6a386ec0df568ff85d681f15`
  (`docs: record project process stabilization`)
- Environment/backend: GitHub-hosted exact 18 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix;
  PostgreSQL/MySQL service jobs absent
- Command: Draft PR #1 `pull_request` run
  [31333420261](https://github.com/progresshans/godj/actions/runs/31333420261), attempt 1
- Exit status: `success`; exact 18/18 jobs completed successfully, failed/cancelled/skipped jobs 0 and
  non-success required steps 0
- Result summary: EVID-029의 append/status 문서 head 자체가 같은 18-job topology에서 다시 통과했습니다.
  따라서 EVID-029의 마지막 `not run/pending` 문구는 당시 checkout의 역사 기록이며 이 evidence가
  해소합니다. 제품 분류는 11 adapters/115 contracts=`110 passing + 5 deviation`로 유지됩니다.
- Failures/skips/not run: portable Python의 exact-profile-only 16 skips는 의도한 기존 분리입니다.
  Windows와 PostgreSQL/MySQL은 actual support contract가 없어 실행하지 않았습니다. 이 EVID-030과
  GDJ-0023 activation 문서 변경 자체는 tested head보다 뒤이므로 exact-head hosted CI가 아직
  `not run/pending`입니다.

Hosted identity and checkout evidence:

- PR #1은 evidence 수집 시 `OPEN`/`DRAFT`/`CLEAN`, head는 exact
  `1f161f311daa775e6a386ec0df568ff85d681f15`였습니다.
- Run metadata는 event `pull_request`, `headSha=1f161f311daa775e6a386ec0df568ff85d681f15`,
  conclusion `success`였습니다.
- Actions checkout은 synthetic merge
  `e21509599e30ef52802a4ee43a1867f9e74f4e79`였고 parents는 exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`와 exact head `1f161f3`입니다. Merge tree와 exact-head
  tree는 모두 `3300acfa54144a7a9c2d8dde654841cbde3bb896`로 같아 실행 contents는 exact-head-equivalent입니다.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | `93295160418` | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | `93295160394` | success |
| Project check (`ubuntu-22.04`) | `93295160491` | success |
| Project check (`ubuntu-24.04-arm`) | `93295160476` | success |
| Project check (`macos-15-intel`) | `93295160485` | success |
| Project check (`macos-26`) | `93295160517` | success |
| SQLite (`ubuntu-22.04`) | `93295160482` | success |
| SQLite (`ubuntu-24.04-arm`) | `93295160432` | success |
| SQLite (`macos-15-intel`) | `93295160455` | success |
| SQLite (`macos-26`) | `93295160488` | success |
| Product project check (`ubuntu-22.04`) | `93295160437` | success |
| Product project check (`ubuntu-24.04-arm`) | `93295160439` | success |
| Product project check (`macos-15-intel`) | `93295160454` | success |
| Product project check (`macos-26`) | `93295160451` | success |
| Python compatibility (`3.12.13`) | `93295160495` | success |
| Python compatibility (`3.13.15`) | `93295160513` | success |
| Python compatibility (`3.14.3`) | `93295160500` | success |
| Python compatibility (`3.14.7`) | `93295160504` | success |

The full Ubuntu job reran `make ci`, product focus, Linux/386 compile-only, all 11 checksums and artifact
no-rewrite. Exact darwin reran focused products, historical exact Python 174/174, all 11 oracle checks and
no-rewrite. Four proof, four actual SQLite and four product project-check jobs retained normal/race/
CGO-disabled/vet/clean gates; four Python jobs retained exact runtime, portable suite, 115-scenario canonical
digest and clean-worktree gates. No merge was performed.

## EVID-20260810-031 — GDJ-0023 ForeignKey Reference and Binding Pre-hosted Local Validation

- Date/time: 2026-08-10 KST
- Work/contract IDs: GDJ-0023, REL-001..REL-012, Q-013
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `d5d00d9e803c637a78961ed6f7dac0b415ce7901` plus the uncommitted GDJ-0023 implementation/pre-hosted
  documentation working tree. The exact implementation+documentation commit does not exist yet.
- Environment/backend: local macOS darwin/arm64, Go 1.26.5. Routine portable gate used uv 0.12.3 and
  CPython 3.14.3; historical exact profile reproduction used ephemeral uv 0.10.12 with CPython 3.14.3.
  Django 6.1 and SQLite 3.50.4 are the locked relation reference. The Go feasibility package is test-only and
  backend-free except its explicit SQLite SET_NULL rollback safety proof.
- Exit status: all local commands below exited 0. Two independent final audits ended with P0/P1/P2/P3
  finding 0.
- Result summary: Phase A locally locks REL-001..012 as 12 `oracle_locked` observations and expands the
  reference aggregate to exact 12 sets/127 contracts/132 ordered cross-bindings. Phase B locally implements
  the product-free symbolic identity/atomic binder, mutual and self cross-app external compile/import graph,
  immutable typed/dynamic shared relation AST, explicit Schema IR vNext candidate comparison, v2 fail-closed
  and injected SET_NULL rollback proof. No product relation adapter was added: the product aggregate remains
  exact 11 adapters/115 contracts=`110 passing + 5 deviation`.
- Failures/skips/not run: unexpected local failure 없음. Portable Python ran 193 tests with 17 intentional
  exact-profile-only skips; historical exact Python passed 193/193 with zero skips and all 12 stored oracle
  checks. The activation-only head's exact 18 hosted run is recorded below, but the current implementation
  tree has not been committed or pushed. Its exact 22 hosted executions, including Python
  3.12.13/3.13.15/3.14.3/3.14.7 and four relation-proof OS/architecture legs, are `not run/pending`.
  Windows and PostgreSQL/MySQL support are not implemented or claimed.

Activation-head hosted evidence:

- Activation commit `d5d00d9e803c637a78961ed6f7dac0b415ce7901` passed the provided verified Draft PR #1
  [run 31335315454](https://github.com/progresshans/godj/actions/runs/31335315454) with exact 18/18 required
  executions and non-success 0. This proves the activation head only and is not reused as implementation-head
  exact 22 evidence.

Local commands and results:

1. Routine repository gate

   ```bash
   PYTHONDONTWRITEBYTECODE=1 make ci
   ```

   PASS under exact CPython 3.14.3 and uv 0.12.3. Portable Python reported 193 tests with 17 intentional
   exact-profile skips. Full Go normal/vet/race, focused CGO-disabled tests, twelve-set reference conformance,
   eleven product adapters, checksum/artifact drift and product no-adapter fail-closed gates passed.

2. Historical exact reference reproduction

   ```bash
   PYTHONDONTWRITEBYTECODE=1 uvx --from uv==0.10.12 uv run --frozen \
     make python-test-exact oracle-check
   ```

   PASS: exact Python 193/193 with zero skips and all 12 oracle `--check` commands. The ephemeral uv 0.10.12
   invocation reproduces the profile-owned historical tool identity; routine local and compatibility work
   continues to use uv 0.12.3.

3. Focused test-only relation binding gates

   ```bash
   go test -count=1 ./conformance/relationbinding
   go test -race -count=1 ./conformance/relationbinding
   CGO_ENABLED=0 go test -count=1 ./conformance/relationbinding
   go vet ./conformance/relationbinding
   go test -race -count=20 ./conformance/relationbinding
   ```

   PASS. The package exposes exact 18 top-level Test/Example definitions and verifies deterministic/atomic
   binding, last-good preservation, immutable concurrent reads, app-to-app import edge 0, typed/dynamic AST
   convergence, both explicit vNext candidates, v2 and migration tuple rejection, and full SET_NULL rollback
   after an injected target-delete fault.

4. Independent final review

   Two separate final audits of the reference/semantic/integration boundary and relation-binding
   security/import/immutability boundary both reported P0/P1/P2/P3 finding 0. The audits also confirmed product
   package and `conformance/runners/godj/**` changes 0, relation product adapter 0, and no status-only promotion
   from `oracle_locked` to `passing`.

Final machine pins:

| Artifact/payload | Bytes | SHA-256 |
|---|---:|---|
| `conformance/contracts/relation-manifest.json` | 10,842 | `08124b420e6313e4c2c1a5be32a3bdd29d831f02f1479bc3591af6f8f7da1522` |
| `conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json` | 33,792 | `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290` |
| `conformance/fixtures/godj-relation-not-implemented.json` | 1,859 | `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209` |
| 12-line `conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS` | 1,148 | `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056` |
| Canonical all-scenario payload, 127 scenarios | 498,051 | `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995` |

This evidence establishes local reference and test-only feasibility completion only. ADR-0023 remains
Proposed and GDJ-0023 remains active. The implementation+documentation tree must be scope-checked,
committed/pushed and pass an exact-head 22/22 hosted run before completion or ADR acceptance is considered.

## EVID-20260810-032 — GDJ-0023 GitHub-hosted exact 22-job implementation-head CI

- Date/time: 2026-08-10T06:57:14+09:00–2026-08-10T07:02:48+09:00
- Work/contract IDs: GDJ-0023, REL-001..REL-012, Q-013
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`
  (`test: lock foreign key relation contracts`)
- Environment/backend: GitHub-hosted exact 22 required executions; Ubuntu/Linux and macOS,
  amd64/arm64, Go 1.26.5, actual SQLite product gates, CPython
  3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix, test-only relation binding four-coordinate
  matrix; PostgreSQL/MySQL service jobs and Windows absent
- Command: Draft PR #1 `pull_request` run
  [31338151743](https://github.com/progresshans/godj/actions/runs/31338151743), attempt 1
- Exit status: `success`; exact 22/22 jobs and all 273 recorded steps completed successfully
- Result summary: Existing exact 18 executions remained separate and the four relation-binding executions
  passed on Linux/macOS x64/arm64. Phase A remained exact 12 reference sets/127 contracts/132 ordered
  cross-bindings and 12 `oracle_locked`; Phase B passed its independent compile/AST/IR/rollback gates. No
  product relation adapter was added, so product classification remains 11 adapters/115 contracts=
  `110 passing + 5 deviation`.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python's 17 exact-profile-only skips
  are intentional and asserted in each compatibility leg; exact darwin ran 193/193 with zero skips.
  Windows and PostgreSQL/MySQL were not run because no actual product contract/adapter exists. This EVID-032
  and completion-status patch is later than the tested implementation head, so its own exact-head hosted CI
  is `not run/pending` and run 31338151743 must not be recursively reused as its proof.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, `headSha` exactly
  `b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`, conclusion `success`.
- Evidence collection found PR #1 `OPEN`/`DRAFT`/`CLEAN` with exact head `b56ccf52...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions used synthetic merge `167a0477698beff1a45adcf67925b47ada7983b5`; its parents were exact base
  `f8a5e20c...` and exact head `b56ccf52...`. Synthetic merge and exact head shared tree
  `5d0f6028847938f90d93d1a7d7c382e6f17ca990`, so executed contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93307286608](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286608) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93307286596](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286596) | success |
| Project check (`ubuntu-22.04`) | [93307286614](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286614) | success |
| Project check (`ubuntu-24.04-arm`) | [93307286618](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286618) | success |
| Project check (`macos-15-intel`) | [93307286637](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286637) | success |
| Project check (`macos-26`) | [93307286636](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286636) | success |
| SQLite (`ubuntu-22.04`) | [93307286660](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286660) | success |
| SQLite (`ubuntu-24.04-arm`) | [93307286650](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286650) | success |
| SQLite (`macos-15-intel`) | [93307286629](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286629) | success |
| SQLite (`macos-26`) | [93307286654](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286654) | success |
| Product project check (`ubuntu-22.04`) | [93307286659](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286659) | success |
| Product project check (`ubuntu-24.04-arm`) | [93307286613](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286613) | success |
| Product project check (`macos-15-intel`) | [93307286632](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286632) | success |
| Product project check (`macos-26`) | [93307286617](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286617) | success |
| Python compatibility (`3.12.13`) | [93307286628](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286628) | success |
| Python compatibility (`3.13.15`) | [93307286625](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286625) | success |
| Python compatibility (`3.14.3`) | [93307286615](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286615) | success |
| Python compatibility (`3.14.7`) | [93307286624](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286624) | success |
| Relation binding (`ubuntu-22.04`) | [93307286655](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286655) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93307286644](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286644) | success |
| Relation binding (`macos-15-intel`) | [93307286626](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286626) | success |
| Relation binding (`macos-26`) | [93307286640](https://github.com/progresshans/godj/actions/runs/31338151743/job/93307286640) | success |

The Ubuntu full job passed `make ci`, portable Python 193 tests/17 intentional skips, all 12 checksums,
artifact no-rewrite and product fail-closed gates. Exact darwin preserved its historical uv 0.10.12 profile,
passed Python 193/193 with zero skips, all 12 oracle checks and no-rewrite. Each uv 0.12.3 Python compatibility
leg asserted its exact runtime, passed 193/17, and verified 127 scenarios, canonical payload 498,051 bytes and
SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.

Each relation-binding job asserted its exact GOOS/GOARCH, executed exact 18 top-level Test/Example entries with
skip 0, then passed race, CGO-disabled, vet, artifact no-rewrite and clean-worktree gates. This includes atomic
all-app binding/last-good publication, mutual/self external compile with app-to-app import edge 0, immutable
typed/dynamic shared AST, field-union vNext candidate evidence, Schema IR v2/migration tuple rejection and
SET_NULL injected-fault rollback. Independent hosted evidence audit reported P0/P1/P2/P3 finding 0. No merge
was performed.

## EVID-20260810-033 — GDJ-0023 GitHub-hosted completion-documentation-head exact 22-job CI

- Date/time: 2026-08-10T07:26:48+09:00–2026-08-10T07:32:01+09:00
- Work/contract IDs: GDJ-0023, REL-001..REL-012, Q-013
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@31784ae1e8261ad0698921b93803aa35e9b63f93`
  (`docs: accept relation binding architecture`)
- Environment/backend: GitHub-hosted exact 22 required executions; Ubuntu/Linux and macOS,
  amd64/arm64, Go 1.26.5, actual SQLite product gates, CPython
  3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix, test-only relation binding four-coordinate
  matrix; PostgreSQL/MySQL service jobs and Windows absent
- Command: Draft PR #1 `pull_request` run
  [31339409336](https://github.com/progresshans/godj/actions/runs/31339409336), attempt 1
- Exit status: `success`; exact 22/22 jobs and all 273 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: EVID-032와 Accepted ADR-0023을 기록한 completion-documentation head 자체가 같은
  22-job topology에서 다시 통과해 EVID-032의 당시 `not run/pending`을 해소했습니다. Reference는
  12 sets/127 contracts/132 ordered cross-bindings와 12 `oracle_locked`, 제품은 11 adapters/115
  contracts=`110 passing + 5 deviation`, relation actual adapter 0으로 유지됩니다.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python의 17 exact-profile-only
  skips는 의도한 기존 분리이고 exact darwin은 193/193과 skip 0을 유지했습니다. Windows와
  PostgreSQL/MySQL은 actual product contract/adapter가 없어 실행하지 않았습니다. 이 EVID-033/status
  patch 자체는 tested head보다 뒤의 미커밋·미푸시 변경이므로 exact-head hosted CI가
  `not run/pending`이며 run 31339409336을 그 증거로 재사용하지 않습니다.

Hosted identity and checkout evidence:

- Run metadata는 event `pull_request`, attempt 1, `headSha` exactly
  `31784ae1e8261ad0698921b93803aa35e9b63f93`, conclusion `success`였습니다.
- Evidence 수집 시 PR #1은 `OPEN`/`DRAFT`/`CLEAN`, exact head `31784ae1...`, base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`였습니다.
- Evidence 수집 시 Actions synthetic merge는 `26453de9aca342c7d3b3c83e33c0014bf1c19a38`이고 parents는 exact
  base `f8a5e20c...`와 exact head `31784ae1...`였습니다. Synthetic merge와 exact head의 tree는 모두
  `0326d597340cfbcba19274045e7ff65b6e11f87d`로 같아 실행 contents는 exact-head-equivalent입니다.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93310598553](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598553) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93310598517](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598517) | success |
| Project check (`ubuntu-22.04`) | [93310598565](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598565) | success |
| Project check (`ubuntu-24.04-arm`) | [93310598586](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598586) | success |
| Project check (`macos-15-intel`) | [93310598591](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598591) | success |
| Project check (`macos-26`) | [93310598585](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598585) | success |
| SQLite (`ubuntu-22.04`) | [93310598583](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598583) | success |
| SQLite (`ubuntu-24.04-arm`) | [93310598611](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598611) | success |
| SQLite (`macos-15-intel`) | [93310598602](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598602) | success |
| SQLite (`macos-26`) | [93310598575](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598575) | success |
| Product project check (`ubuntu-22.04`) | [93310598571](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598571) | success |
| Product project check (`ubuntu-24.04-arm`) | [93310598563](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598563) | success |
| Product project check (`macos-15-intel`) | [93310598569](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598569) | success |
| Product project check (`macos-26`) | [93310598579](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598579) | success |
| Python compatibility (`3.12.13`) | [93310598596](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598596) | success |
| Python compatibility (`3.13.15`) | [93310598580](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598580) | success |
| Python compatibility (`3.14.3`) | [93310598577](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598577) | success |
| Python compatibility (`3.14.7`) | [93310598607](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598607) | success |
| Relation binding (`ubuntu-22.04`) | [93310598566](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598566) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93310598610](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598610) | success |
| Relation binding (`macos-15-intel`) | [93310598582](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598582) | success |
| Relation binding (`macos-26`) | [93310598573](https://github.com/progresshans/godj/actions/runs/31339409336/job/93310598573) | success |

The exact two full jobs, four project-check, four SQLite, four product, four Python compatibility and four
relation-binding executions retained the same required step inventory as EVID-032. All checkout, runtime,
suite/digest, normal/race/CGO-disabled/vet, artifact no-rewrite and clean-worktree steps succeeded. The tested
commit changed documentation only from the implementation head; no product/reference artifact or workflow
was changed, no product relation support was added and no merge was performed.

## EVID-20260810-034 — GDJ-0023 Final Evidence-Documentation Exact-Head CI and GDJ-0024 Activation Baseline

- Date/time: 2026-08-10T07:44:54+09:00–2026-08-10T07:50:57+09:00
- Work/contract IDs: GDJ-0023, REL-001..REL-012, Q-013; GDJ-0024 activation baseline only
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@50578ddc4756452b2a9a0d2afd75711a35b76d8a`
  (`docs: record hosted relation completion`)
- Environment/backend: GitHub-hosted exact 22 required executions; Ubuntu/Linux and macOS,
  amd64/arm64, Go 1.26.5, actual SQLite product gates, CPython
  3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix, test-only relation binding four-coordinate
  matrix; PostgreSQL/MySQL service jobs and Windows absent
- Command: Draft PR #1 `pull_request` run
  [31340170361](https://github.com/progresshans/godj/actions/runs/31340170361), attempt 1
- Exit status: `success`; exact 22/22 jobs and all 273/273 recorded steps succeeded; failed, cancelled,
  skipped jobs and non-success step records 0
- Result summary: EVID-033과 completion status를 기록한 exact head 자체가 동일한 exact 22-job topology를
  통과해 GDJ-0023의 마지막 recursive pending evidence를 닫았습니다. 따라서 이 clean tested checkout을
  GDJ-0024 activation baseline으로 사용할 수 있습니다. 이 run은 GDJ-0024 activation/implementation diff의
  hosted evidence로 재사용하지 않습니다. Reference는 12 sets/127 contracts/132 ordered cross-bindings,
  제품은 11 adapters/115 contracts=`110 passing + 5 deviation`, relation actual adapter 0입니다.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python의 17 exact-profile-only
  skips는 의도한 기존 분리이고 exact darwin은 193/193과 skip 0을 유지했습니다. Windows와
  PostgreSQL/MySQL은 actual product contract/adapter가 없어 실행하지 않았습니다. 이 EVID-034 및
  GDJ-0024 activation patch는 tested baseline보다 뒤의 변경이므로 그 exact-head hosted CI는
  `not run/pending`입니다.

Hosted identity and checkout evidence:

- Run metadata는 event `pull_request`, attempt 1, `headSha` exactly
  `50578ddc4756452b2a9a0d2afd75711a35b76d8a`, conclusion `success`였습니다.
- Evidence 수집 시 PR #1은 `OPEN`/`DRAFT`/`CLEAN`, exact head `50578ddc...`, base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`였습니다.
- Actions synthetic merge는 `ee81cd3062cdc908e50498c8e869f74f274026f9`이고 parents는 exact base와
  exact head였습니다. Synthetic merge와 exact head의 tree는 모두
  `aa970d11b6fcdcdcc8d543c54e7c94c41cc39110`으로 같아 실행 contents는 exact-head-equivalent입니다.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93312531044](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531044) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93312530992](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312530992) | success |
| Project check (`ubuntu-22.04`) | [93312531065](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531065) | success |
| Project check (`ubuntu-24.04-arm`) | [93312531081](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531081) | success |
| Project check (`macos-15-intel`) | [93312531101](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531101) | success |
| Project check (`macos-26`) | [93312531102](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531102) | success |
| SQLite (`ubuntu-22.04`) | [93312531001](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531001) | success |
| SQLite (`ubuntu-24.04-arm`) | [93312530998](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312530998) | success |
| SQLite (`macos-15-intel`) | [93312531002](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531002) | success |
| SQLite (`macos-26`) | [93312530987](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312530987) | success |
| Product project check (`ubuntu-22.04`) | [93312531059](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531059) | success |
| Product project check (`ubuntu-24.04-arm`) | [93312531060](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531060) | success |
| Product project check (`macos-15-intel`) | [93312531039](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531039) | success |
| Product project check (`macos-26`) | [93312531048](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531048) | success |
| Python compatibility (`3.12.13`) | [93312531090](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531090) | success |
| Python compatibility (`3.13.15`) | [93312531072](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531072) | success |
| Python compatibility (`3.14.3`) | [93312531047](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531047) | success |
| Python compatibility (`3.14.7`) | [93312531042](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531042) | success |
| Relation binding (`ubuntu-22.04`) | [93312531043](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531043) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93312531007](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531007) | success |
| Relation binding (`macos-15-intel`) | [93312531033](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531033) | success |
| Relation binding (`macos-26`) | [93312531070](https://github.com/progresshans/godj/actions/runs/31340170361/job/93312531070) | success |

All 22 required executions retained the EVID-032/033 inventory and passed their checkout, exact runtime,
normal/race/CGO-disabled/vet, artifact no-rewrite and clean-worktree gates. This is the final hosted evidence
for the completed GDJ-0023 evidence-documentation head and only the clean baseline evidence for activating
GDJ-0024. No relation product code, product adapter, workflow expansion or merge was part of this run.

## EVID-20260810-035 — GDJ-0024 REL-001 Metadata Product Pre-hosted Local Validation

- Date/time: 2026-08-10; activation hosted 09:37:57–09:43:46 KST, local implementation validation after recovery
- Work/contract IDs: GDJ-0024, REL-001, Q-013; REL-002..REL-012 explicit not-implemented boundary
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `758cd0931fe489e3cde81ca8d12e35e68183c40a` plus the uncommitted frozen GDJ-0024 implementation and
  pre-hosted status patch. The implementation commit identity is intentionally unknown/pending at this evidence point.
- Local environment: Go 1.26.5 `darwin/arm64`; CPython 3.14.3; uv 0.12.3; routine local Python one-version
  policy. Exact Python 3.12.13/3.13.15/3.14.3/3.14.7 remains CI-only; historical exact darwin oracle profile
  remains uv 0.10.12.
- Result summary: local product implementation adds explicit relation IR v3, v2-byte-preserving mixed v2/v3
  codegen companions and project bridge, immutable `orm.BindProject`, relation-aware clone and migration
  fail-closed boundaries, REL-001 actual metadata observation and a mixed product comparator. Product is now
  locally `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`; relation actual
  coverage is REL-001 1/12. REL-002..REL-012 remain ordered payload-free `not_implemented`.
- Failures/skips/not run: local expected failures 0. The exact-26 implementation-head GitHub workflow has not
  been committed/pushed or run and remains `not run/pending`; activation run 31344980929 must not be reused as
  implementation-hosted proof. Windows, PostgreSQL/MySQL, relation query/write/delete/DDL/migration codec and
  non-AutoField targets remain unsupported/out of scope.

Activation exact-head hosted evidence:

- Draft PR #1 `pull_request` run
  [31344980929](https://github.com/progresshans/godj/actions/runs/31344980929), attempt 1, completed with
  `success` at exact `headSha=758cd0931fe489e3cde81ca8d12e35e68183c40a`.
- Exact 22/22 jobs and all 273/273 recorded steps succeeded; failed/cancelled/skipped jobs and non-success
  step records were 0. Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`, base `f8a5e20c...`, head exact
  `758cd093...`.
- Actions synthetic merge `b91432ff8273eb9a44838a77bcab925739d9a030` had exact base+head parents;
  its tree and the activation head tree were both `5fbaefd1f369ca6d98180d6aad3bacc0ad61ee69`, so executed
  contents were exact-head-equivalent.
- Exact successful job IDs were: full conformance `93325125045`; exact darwin `93325125024`; project check
  `93325125040`, `93325125055`, `93325125082`, `93325125042`; relation binding `93325125070`,
  `93325125030`, `93325125080`, `93325125065`; product project check `93325125053`, `93325125038`,
  `93325125048`, `93325125068`; Python compatibility `93325125079`, `93325125073`, `93325125059`,
  `93325125077`; SQLite `93325125043`, `93325125069`, `93325125060`, `93325125067`.
- This exact-22 result establishes only the activation head and existing pre-GDJ-0024 product topology.

Local commands and exact gates:

1. `PYTHONDONTWRITEBYTECODE=1 make ci`
   - PASS: format/generate checks, full Go normal/vet/race, focused CGO-disabled packages, portable Python,
     conformance checks and all twelve product adapters.
   - Portable Python reported `Ran 193 tests`, `OK (skipped=17)` under CPython 3.14.3 + uv 0.12.3.
2. `go test -json -count=1 ./schema/... ./codegen ./orm ./migrations ./migrations/definition
   ./conformance/relationproduct/... ./conformance/internal/protocol ./conformance/runners/godj
   ./conformance/cmd/godjcheck ./internal/compiletest` plus the workflow's exact JSON inventory verifier
   - PASS: 394 top-level run events, 394 matching pass events, 0 skip events.
   - Encoded inventory was 40,630 bytes, SHA-256
     `2eb1fe8c963ee23c2ac779f04a3809bb3689e2ecac579ffb25da95113bb420ce`.
3. The same relation-product package set under race, `CGO_ENABLED=0`, vet and repeated execution; external
   mixed v2/v3, mutual and self generated projects; Linux/386 compile-only gate
   - PASS. Generated app-to-app import edges remained 0 and the project bridge held the exact app+ORM imports.
4. `make godj-conformance`
   - PASS for all 12 adapters. Relation stdout was exactly
     `GoDj product observations match 1 required contract; 11 remain not implemented`.
5. `PYTHONDONTWRITEBYTECODE=1 uv run --frozen python -m unittest
   conformance.runners.django.tests.test_relation_scenarios`
   - PASS: `Ran 11 tests`, `OK`.
6. Artifact/scope checks
   - Relation manifest changed only REL-001 `oracle_locked -> passing`; relation oracle 33,792 bytes/
     `6b7d138d...8290`, static 1,859 bytes/`2450dcb9...209`, SHA256SUMS 1,148 bytes/
     `067b7d89...056`, `go.mod` and `go.sum` remained unchanged. All twelve stored checksums verified.
   - Frozen implementation had 53 allowed paths; forbidden `query/**`, `db/**`,
     `conformance/relationbinding/**`, Django relation runner/oracle/static/SHA and dependency files had zero diff.
   - `git diff --check` and generated Python cache cleanup passed.

Four independent read-only audits covered IR/migration, codegen/binder, conformance/CI false-green and final
integration/security boundaries. Each final verdict was P0/P1/P2/P3 = 0/0/0/0. The CI definition expands the
existing 22 required executions with four relation-product coordinates for exact 26, but hosted acceptance is
deliberately pending until this frozen implementation and evidence are committed and pushed.

## EVID-20260810-036 — GDJ-0024 GitHub-hosted exact 26-job implementation-head CI

- Date/time: 2026-08-10T10:53:30+09:00–2026-08-10T10:59:15+09:00
- Work/contract IDs: GDJ-0024, REL-001, Q-013; REL-002..REL-012 explicit ordered not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@05e6e218db16e17ce13f7b504a01c603041e4a2a`
  (`feat: add foreign key relation metadata`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual existing SQLite product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility
  matrix, test-only relation-binding four-coordinate matrix and actual relation-product metadata
  four-coordinate matrix. Routine hosted portable/compatibility uses uv 0.12.3; the historical exact darwin
  artifact profile remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request` run
  [31348285559](https://github.com/progresshans/godj/actions/runs/31348285559), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: Existing exact 22 executions remained required and four relation-product executions passed on
  Linux/macOS x64/arm64. Reference remains exact 12 sets/127 contracts/132 ordered cross-bindings. Product is exact
  `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`, relation actual coverage
  REL-001 1/12. REL-001 is generated/project-bound metadata actual; REL-002..REL-012 remain ordered payload-free
  `not_implemented` and `oracle_locked`.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python의 17 exact-profile-only skips는
  기존 의도적 분리이며 각 compatibility leg가 exact 193/17을 검증했습니다. Exact darwin은 193/193과
  skip 0을 유지했습니다. Windows, PostgreSQL/MySQL, relation query/load/cache/write/delete/DDL/migration
  codec, non-AutoField target과 OneToOne/ManyToMany는 product contract/adapter가 없어 실행하거나 지원으로
  세지 않았습니다. 이 EVID-036과 completion-status patch는 tested implementation head보다 뒤의
  documentation-only 변경이므로 자체 exact-head hosted CI는 `not run/pending`이며 run 31348285559를
  재귀적으로 그 증거로 사용하지 않습니다.

Hosted identity and checkout evidence:

- Run metadata는 event `pull_request`, attempt 1, `headSha` exactly
  `05e6e218db16e17ce13f7b504a01c603041e4a2a`, conclusion `success`였습니다.
- Evidence 수집 시 PR #1은 `OPEN`/`DRAFT`/`CLEAN`, exact head `05e6e218...`, base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`였습니다.
- Actions synthetic merge는 `e2a2bcb5909fd9369ee5a55f45b4562876c56d90`이고 parents는 exact base와
  exact head였습니다. Synthetic merge와 exact head의 tree는 모두
  `a25501d0246fe6a1dc6f819cb7bd106179298c91`로 같아 실행 contents는 exact-head-equivalent입니다.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93334244606](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244606) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93334244601](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244601) | success |
| Project check (`ubuntu-22.04`) | [93334244650](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244650) | success |
| Project check (`ubuntu-24.04-arm`) | [93334244714](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244714) | success |
| Project check (`macos-15-intel`) | [93334244667](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244667) | success |
| Project check (`macos-26`) | [93334244699](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244699) | success |
| SQLite (`ubuntu-22.04`) | [93334244656](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244656) | success |
| SQLite (`ubuntu-24.04-arm`) | [93334244609](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244609) | success |
| SQLite (`macos-15-intel`) | [93334244622](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244622) | success |
| SQLite (`macos-26`) | [93334244610](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244610) | success |
| Product project check (`ubuntu-22.04`) | [93334244645](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244645) | success |
| Product project check (`ubuntu-24.04-arm`) | [93334244624](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244624) | success |
| Product project check (`macos-15-intel`) | [93334244644](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244644) | success |
| Product project check (`macos-26`) | [93334244655](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244655) | success |
| Python compatibility (`3.12.13`) | [93334244620](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244620) | success |
| Python compatibility (`3.13.15`) | [93334244635](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244635) | success |
| Python compatibility (`3.14.3`) | [93334244651](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244651) | success |
| Python compatibility (`3.14.7`) | [93334244680](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244680) | success |
| Relation binding (`ubuntu-22.04`) | [93334244673](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244673) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93334244678](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244678) | success |
| Relation binding (`macos-15-intel`) | [93334244742](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244742) | success |
| Relation binding (`macos-26`) | [93334244677](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244677) | success |
| Relation product (`ubuntu-22.04`) | [93334244685](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244685) | success |
| Relation product (`ubuntu-24.04-arm`) | [93334244626](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244626) | success |
| Relation product (`macos-15-intel`) | [93334244652](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244652) | success |
| Relation product (`macos-26`) | [93334244608](https://github.com/progresshans/godj/actions/runs/31348285559/job/93334244608) | success |

Hosted gate details:

- The Ubuntu full job passed `make ci`, portable Python 193 tests/17 intentional skips, all twelve product
  adapters and stored checksums, artifact no-rewrite and clean-worktree gates. Relation product stdout was exact
  `GoDj product observations match 1 required contract; 11 remain not implemented`.
- The full Ubuntu job additionally passed relation metadata packages with `GOARCH=386`, `CGO_ENABLED=0`; the
  exact darwin job preserved its locked profile and passed 193/193 with skip 0.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  the 127-scenario semantic digest. No local multi-Python execution was used as a substitute.
- Each relation-product job asserted exact GOOS/GOARCH and ran the exact package set covering schema IR/DSL,
  codegen/bridge, ORM binder, migration rejection, relation product/observer, protocol/runner/command and external
  compile. All four recorded exact 394 top-level run events, matching 394 pass events, 0 skip events, encoded
  inventory 40,630 bytes and SHA-256
  `2eb1fe8c963ee23c2ac779f04a3809bb3689e2ecac579ffb25da95113bb420ce`, then passed race,
  CGO-disabled, vet, artifact/generated-fixture no-rewrite and clean-worktree gates.
- Relation oracle `6b7d138d...8290`, static fixture `2450dcb9...209`, twelve-line SHA256SUMS
  `067b7d89...056`, Django relation runner/scenarios, `go.mod` and `go.sum` remained byte-preserved. Only REL-001
  manifest status is `passing`; REL-002..012 remain `oracle_locked`.

This evidence accepts only ADR-0024's bounded relation metadata architecture and the completed GDJ-0024 REL-001
product slice. It does not establish relation query/load/cache/write/delete/DDL/migration codec, broader relation
types or non-SQLite backend support, and no merge was performed.

## EVID-20260810-037 — GDJ-0024 GitHub-hosted completion-documentation-head exact 26-job CI

- Date/time: 2026-08-10T11:26:17+09:00–2026-08-10T11:34:39+09:00
- Work/contract IDs: GDJ-0024, REL-001, Q-013; REL-002..REL-012 explicit ordered not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`
  (`docs: complete foreign key relation metadata slice`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual existing SQLite product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility
  matrix, test-only relation-binding four-coordinate matrix and actual relation-product metadata
  four-coordinate matrix. Routine hosted portable/compatibility uses uv 0.12.3; the historical exact darwin
  artifact profile remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request` run
  [31349791188](https://github.com/progresshans/godj/actions/runs/31349791188), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: The completion-documentation head containing EVID-036, Accepted ADR-0024 and synchronized
  GDJ-0024 completion state passed the same exact-26 topology. Product classification remains exact
  `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`, relation actual coverage
  REL-001 1/12. REL-002..REL-012 remain ordered payload-free `not_implemented` and `oracle_locked`.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python의 17 exact-profile-only skips는
  기존 의도적 분리이며 four compatibility legs가 각각 exact 193/17을 검증했습니다. Exact darwin은
  193/193과 skip 0을 유지했습니다. Windows, PostgreSQL/MySQL, relation query/load/cache/write/delete/DDL/
  migration codec, non-AutoField target과 OneToOne/ManyToMany는 product contract/adapter가 없어 실행하거나
  지원으로 세지 않았습니다. 이 EVID-037과 final-status patch 자체는 tested completion-documentation head보다
  뒤의 미커밋·미푸시 documentation-only 변경이므로 자체 exact-head hosted CI는 `not run/pending`이며
  run 31349791188을 재귀적으로 그 증거로 사용하지 않습니다.

Hosted identity and checkout evidence:

- Run metadata는 event `pull_request`, attempt 1, `headSha` exactly
  `e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`, conclusion `success`였습니다.
- Evidence 수집 시 PR #1은 `OPEN`/`DRAFT`/`CLEAN`, exact head `e9498a67...`, base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`였습니다.
- Actions checkout log는 exact synthetic merge
  `da7fdf02d002014a147588444f30f5e799259231`을 사용했습니다. 그 parents는 exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`와 exact head
  `e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`이고, synthetic merge와 exact head의 tree는 모두
  `7f390e66098f93b283cfee9d2528639e246baac0`로 같아 실행 contents는 exact-head-equivalent입니다.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93338336185](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336185) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93338336167](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336167) | success |
| Project check (`ubuntu-22.04`) | [93338336267](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336267) | success |
| Project check (`ubuntu-24.04-arm`) | [93338336235](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336235) | success |
| Project check (`macos-15-intel`) | [93338336257](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336257) | success |
| Project check (`macos-26`) | [93338336260](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336260) | success |
| SQLite (`ubuntu-22.04`) | [93338336288](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336288) | success |
| SQLite (`ubuntu-24.04-arm`) | [93338336305](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336305) | success |
| SQLite (`macos-15-intel`) | [93338336291](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336291) | success |
| SQLite (`macos-26`) | [93338336303](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336303) | success |
| Product project check (`ubuntu-22.04`) | [93338336256](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336256) | success |
| Product project check (`ubuntu-24.04-arm`) | [93338336234](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336234) | success |
| Product project check (`macos-15-intel`) | [93338336301](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336301) | success |
| Product project check (`macos-26`) | [93338336283](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336283) | success |
| Python compatibility (`3.12.13`) | [93338336220](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336220) | success |
| Python compatibility (`3.13.15`) | [93338336221](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336221) | success |
| Python compatibility (`3.14.3`) | [93338336249](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336249) | success |
| Python compatibility (`3.14.7`) | [93338336233](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336233) | success |
| Relation binding (`ubuntu-22.04`) | [93338336262](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336262) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93338336265](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336265) | success |
| Relation binding (`macos-15-intel`) | [93338336293](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336293) | success |
| Relation binding (`macos-26`) | [93338336228](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336228) | success |
| Relation product (`ubuntu-22.04`) | [93338336198](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336198) | success |
| Relation product (`ubuntu-24.04-arm`) | [93338336217](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336217) | success |
| Relation product (`macos-15-intel`) | [93338336211](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336211) | success |
| Relation product (`macos-26`) | [93338336226](https://github.com/progresshans/godj/actions/runs/31349791188/job/93338336226) | success |

Hosted gate details:

- The Ubuntu full job passed `make ci`, portable Python 193 tests/17 intentional skips, all twelve product
  adapters and stored checksums, artifact no-rewrite and clean-worktree gates. It additionally passed relation
  metadata packages with `GOARCH=386`, `CGO_ENABLED=0`; the exact darwin job preserved its locked profile and
  passed Python 193/193 with skip 0.
- Each uv 0.12.3 Python compatibility leg asserted exact CPython 3.12.13, 3.13.15, 3.14.3 or 3.14.7,
  passed portable 193/17, and verified the canonical 127-scenario payload of 498,051 bytes with SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product job asserted exact GOOS/GOARCH and completed exact 394 top-level run events,
  394 matching pass events, 0 skips, encoded inventory 40,630 bytes and SHA-256
  `2eb1fe8c963ee23c2ac779f04a3809bb3689e2ecac579ffb25da95113bb420ce`. All four then passed race,
  CGO-disabled, vet, artifact/generated-fixture no-rewrite and clean-worktree gates.
- Existing project-check, SQLite and relation-binding four-coordinate matrices retained their normal/race/
  CGO-disabled/vet and clean/no-rewrite gates. The tested commit changed documentation only from the accepted
  implementation head; product/reference artifact and workflow bytes were unchanged.

This evidence closes the completion-documentation head verification for GDJ-0024's bounded metadata-only
product slice. It does not establish relation query/load/cache/write/delete/DDL/migration codec, broader relation
types, non-AutoField targets or non-SQLite backend support. No new product work was activated and no merge was
performed.

## EVID-20260810-038 — GDJ-0024 Final Exact-Head CI and GDJ-0025 Activation Baseline

- Date/time: 2026-08-10T11:56:34+09:00–2026-08-10T12:04:02+09:00
- Work/contract IDs: GDJ-0024 final evidence; GDJ-0025/REL-004/Q-013 activation baseline only
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@5bf143575e9b703117a328c1fc5b7eb5823fbfd6`
  (`docs: record foreign key relation completion`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite/product/relation metadata gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7,
  uv 0.12.3 for routine compatibility and historical exact darwin uv 0.10.12. PostgreSQL/MySQL service jobs and
  Windows are absent.
- Command: Draft PR #1 `pull_request` run
  [31351169780](https://github.com/progresshans/godj/actions/runs/31351169780), attempt 1; local clean-baseline
  `PYTHONDONTWRITEBYTECODE=1 make ci`
- Exit status: hosted `success`, exact 26/26 jobs and 326/326 recorded steps success, non-success job/step 0;
  local `make ci` PASS with portable Python 193 tests and 17 intentional exact-profile skips
- Result summary: The exact five-file EVID-037/final-status patch was committed as `5bf14357...` and passed the
  complete exact-26 topology. This closes EVID-037's historical recursive pending state and establishes a clean,
  tested GDJ-0024 final baseline for activating GDJ-0025. Product classification at this checkout is still exact
  `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`, relation actual REL-001 1/12.
- Failures/skips/not run: unexpected failure/cancel/skip 0. Portable Python's 17 skips remain intentional and exact
  darwin passed 193/193 with skip 0. Relation query/join REL-004 is not part of this checkout or run. The GDJ-0025
  activation document patch appended after this tested head is itself `not run/pending`; this run must not be reused as
  proof of that later activation diff.

Hosted identity and checkout evidence:

- Run metadata: `pull_request`, attempt 1, exact `headSha=5bf143575e9b703117a328c1fc5b7eb5823fbfd6`,
  status `completed`, conclusion `success`.
- PR #1 was `OPEN`/`DRAFT`/`CLEAN`, exact head `5bf14357...`, base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions checked out synthetic merge `26129b3b631cd7d460e1686fb9fff0e3077bdf84`; its parents are exact base
  `f8a5e20c...` and head `5bf14357...`. Synthetic merge and head tree are both
  `13e4ec651d0d12bb9c5e1ff73c8c6d27cbdd7f90`, so executed contents are exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93342148575](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148575) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93342148642](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148642) | success |
| Project check (`ubuntu-22.04`) | [93342148634](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148634) | success |
| Project check (`ubuntu-24.04-arm`) | [93342148649](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148649) | success |
| Project check (`macos-15-intel`) | [93342148663](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148663) | success |
| Project check (`macos-26`) | [93342148637](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148637) | success |
| SQLite (`ubuntu-22.04`) | [93342148593](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148593) | success |
| SQLite (`ubuntu-24.04-arm`) | [93342148617](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148617) | success |
| SQLite (`macos-15-intel`) | [93342148627](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148627) | success |
| SQLite (`macos-26`) | [93342148626](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148626) | success |
| Product project check (`ubuntu-22.04`) | [93342148732](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148732) | success |
| Product project check (`ubuntu-24.04-arm`) | [93342148781](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148781) | success |
| Product project check (`macos-15-intel`) | [93342148643](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148643) | success |
| Product project check (`macos-26`) | [93342148746](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148746) | success |
| Python compatibility (`3.12.13`) | [93342148625](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148625) | success |
| Python compatibility (`3.13.15`) | [93342148606](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148606) | success |
| Python compatibility (`3.14.3`) | [93342148605](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148605) | success |
| Python compatibility (`3.14.7`) | [93342148630](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148630) | success |
| Relation binding (`ubuntu-22.04`) | [93342148767](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148767) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93342148749](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148749) | success |
| Relation binding (`macos-15-intel`) | [93342148744](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148744) | success |
| Relation binding (`macos-26`) | [93342148758](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148758) | success |
| Relation product (`ubuntu-22.04`) | [93342148776](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148776) | success |
| Relation product (`ubuntu-24.04-arm`) | [93342148787](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148787) | success |
| Relation product (`macos-15-intel`) | [93342148668](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148668) | success |
| Relation product (`macos-26`) | [93342148595](https://github.com/progresshans/godj/actions/runs/31351169780/job/93342148595) | success |

Gate details:

- Each relation-product coordinate retained exact 394 run/394 pass/0 skip, 40,630-byte inventory and SHA-256
  `2eb1fe8c963ee23c2ac779f04a3809bb3689e2ecac579ffb25da95113bb420ce`, then passed race,
  CGO-disabled, vet, no-rewrite and clean-worktree gates.
- Every Python uv 0.12.3 coordinate passed portable 193/17 and exact 127-scenario 498,051-byte SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Full Ubuntu passed `make ci`, twelve product adapters, checksums/no-rewrite/clean and Linux/386 relation product;
  exact darwin preserved the locked profile and passed 193/193 with skip 0.

This evidence closes GDJ-0024's final documentation-head verification only. It does not establish REL-004, relation
query/load/cache/write/delete/DDL/migration, broader target types or non-SQLite support. GDJ-0025 remains a later active
work diff until its own exact-head evidence exists, and no merge was performed.

## EVID-20260810-039 — GDJ-0025 REL-004 Forward Predicate Pre-hosted Local Validation

- Date/time: 2026-08-10; activation hosted 12:57:17–13:05:22 KST, local implementation validation afterward
- Work/contract IDs: GDJ-0025, REL-004, Q-013; REL-002/003/005..012 explicit not-implemented boundary
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `cf8cb589575836cb1393079ce04ff06fc549800a` plus the uncommitted frozen GDJ-0025 implementation and
  pre-hosted status patch. The implementation commit identity is intentionally unknown/pending at this evidence point.
- Local environment: Go 1.26.5 `darwin/arm64`; CPython 3.14.3; uv 0.12.3. Routine local Python uses only this
  version. Exact CPython 3.12.13/3.13.15/3.14.3/3.14.7 remains CI-only; the historical exact darwin oracle job
  remains pinned to uv 0.10.12.
- Result summary: additive app query companion/project bridge, immutable project-bound typed/dynamic relation AST,
  SQLite required one-hop reusable `INNER JOIN` and a separate oracle-blind actual fixture are locally implemented.
  Both locked cases return Post IDs `[10,11]` with construction I/O 0, evaluation SELECT 1, INNER JOIN 1 and LEFT
  JOIN 0. Product is locally exact 12 adapter sets/127 contracts=`112 passing + 5 deviation + 10 oracle_locked`;
  relation actual coverage is REL-001/004 2/12. The other ten REL contracts remain ordered payload-free
  `not_implemented`.
- Failures/skips/not run: local unexpected failures 0. Portable Python's 17 exact-profile-only skips remain intentional.
  Local Linux/386 is compile-only; actual Ubuntu/386 execution is pending. The exact-26 implementation-head workflow
  and its four exact Python compatibility legs have not been committed/pushed or run and remain `not run/pending`.
  Windows, PostgreSQL/MySQL, relation loader/cache/nullable/reverse/eager/write/delete/DDL/migration and non-AutoField
  targets remain unsupported/out of scope.

Activation exact-head hosted evidence:

- Draft PR #1 `pull_request` [run 31354040515](https://github.com/progresshans/godj/actions/runs/31354040515),
  attempt 1, completed with `success` at exact
  `headSha=cf8cb589575836cb1393079ce04ff06fc549800a`.
- Exact 26/26 jobs and all 326/326 recorded steps succeeded; failed/cancelled/skipped jobs and non-success recorded
  steps were 0. Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`.
- Actions synthetic merge `9710862dd07c1bb764c5ed2d3d391bc867d64fb8` had exact base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and activation-head parents. Its tree and the activation head tree
  were both `56612ab9e862b82a26f0404437f2bfc77de8447f`, so executed contents were exact-head-equivalent.
- Full Ubuntu passed portable Python 193 tests/17 intentional skips and all twelve product adapters; exact darwin
  passed 193/193 with skip 0. Each of the four existing relation-product legs retained exact 394 run/394 pass/0 skip,
  40,630 bytes and SHA-256 `2eb1fe8c963ee23c2ac779f04a3809bb3689e2ecac579ffb25da95113bb420ce`.
  This run establishes only the activation head and pre-REL-004 implementation topology.

Local commands and exact gates:

1. `PYTHONDONTWRITEBYTECODE=1 make ci`
   - PASS: format/generate, full Go normal/vet/race, focused CGO-disabled packages, portable Python, conformance and
     all twelve product adapters. Portable Python reported 193 tests with 17 intentional exact-profile skips.
2. Workflow-equivalent `go test -json -count=1` over `./schema/...`, `./query`, `./codegen`, `./orm`,
   `./db/sqlite`, `./migrations`, `./migrations/definition`, both relation product trees, protocol, GoDj runner,
   `godjcheck` and external compile tests, followed by the workflow inventory verifier
   - PASS: exact 492 top-level run events, 492 matching pass events, 0 skip events; encoded inventory 49,902 bytes,
     SHA-256 `05064a7f0e7a8806d7172fe26a12d846765cdf0d7f991c83b40de07603ba82eb`.
3. `go test -count=20 ./query ./orm ./db/sqlite ./conformance/relationqueryproduct -run 'Relation|REL004|ParseDynamic|Compile'`
   - PASS for all 20 repetitions.
4. `go test -shuffle=on -count=10 ./query ./orm ./db/sqlite ./conformance/relationqueryproduct ./conformance/cmd/godjcheck`
   - PASS for all 10 shuffled repetitions.
5. Exact relation package set under normal, race, `CGO_ENABLED=0` and vet
   - PASS. Existing scalar SQLite SQL/error behavior, typed/dynamic `Plan.Equal`, immutable reads, pre-I/O structured
     rejection, generated external positive/negative compilation and app-to-app Imports/Deps 0 gates passed.
6. `GOOS=linux GOARCH=386 CGO_ENABLED=0 go test -exec=true -count=1 ./query ./orm ./conformance/relationproduct/... ./conformance/relationqueryproduct/... ./conformance/runners/godj`
   - PASS as a bounded real Linux/386 ELF compile. `-exec=true` does not execute the binaries; actual Ubuntu/386
     relation-query execution remains a required hosted gate.
7. `make godj-conformance`
   - PASS for all 12 adapters. Relation stdout was exactly
     `GoDj product observations match 2 required contracts; 10 remain not implemented`.
8. `PYTHONDONTWRITEBYTECODE=1 uv run --frozen python -m unittest conformance.runners.django.tests.test_relation_scenarios`
   - PASS: 11/11.

Artifact, scope and false-green evidence:

- Relation manifest changed only REL-004 `oracle_locked -> passing`: 10,830 bytes/SHA-256
  `944be1b941b9217ed27c2f6d5a33662cdfafc23f0c7698cad5ebb80849b633f0`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. All stored checksums passed.
- Existing v2/v3 main, metadata and `Bind()` generator bytes, prior `conformance/relationproduct/**`, scalar compiler
  body, schema/migrations, `go.mod` and `go.sum` remained unchanged. Generated relation-query fixture no-rewrite,
  oracle blindness, exact result/DB-state/metrics mismatch gates and independent mutations all passed.
- The frozen audit input contained exactly 50 unstaged/untracked paths versus activation HEAD and 62 total changed
  paths versus the `5bf14357...` work baseline, with 0 paths outside the 53 unique frontmatter allowlist selectors.
  This evidence/status integration adds only its allowed status files; nothing was staged, committed, pushed or merged.
- `gofmt`, YAML parse, Markdown/frontmatter/link checks and `git diff --check` passed at the implementation freeze.

Four independent read-only audits covered query/ORM API and immutable AST, codegen/import graph, SQLite/conformance
false-green boundaries, and final integration/security/scope. Their final verdicts were all
P0/P1/P2/P3=`0/0/0/0`. Hosted acceptance is deliberately pending until this frozen implementation and evidence are
committed and pushed. ADR-0025 therefore remains Proposed, GDJ-0025 remains active, and Q-013 remains `Partial`.
