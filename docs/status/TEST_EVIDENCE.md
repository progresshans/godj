# 테스트·검증 증거

- 마지막 갱신: 2026-08-12
- 현재 GoDj 코드·호환 계약 테스트 증거: EVID-20260812-077

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

## EVID-20260810-040 — GDJ-0025 GitHub-hosted exact 26-job implementation-head CI

- Date/time: 2026-08-10T14:01:03+09:00–2026-08-10T14:09:43+09:00
- Work/contract IDs: GDJ-0025, REL-004, Q-013; REL-002/003/005..012 explicit ordered not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@98db55a30ff71a2f2f70722cb569a046208a5403`
  (`feat: add forward relation predicates`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: additive query companion/project bridge, immutable typed/dynamic relation path, SQLite required
  one-hop reusable `INNER JOIN` and oracle-blind REL-004 actual passed on the exact implementation head. Both
  locked cases produce Post IDs `[10,11]` with construction I/O 0, evaluation SELECT 1, INNER JOIN 1 and LEFT JOIN
  0. Product is exact 12 adapter sets/127 contracts=`112 passing + 5 deviation + 10 oracle_locked`, relation actual
  REL-001/004 2/12. The other ten relation contracts remain ordered payload-free `not_implemented` and
  `oracle_locked`.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python's 17 exact-profile-only skips are
  intentional and every compatibility leg passed exact 193/17; exact darwin passed 193/193 with skip 0. Windows,
  PostgreSQL/MySQL, loader/cache/nullable/reverse/eager/write/delete/DDL/migration, broader target types and
  non-SQLite support were not run or claimed. This EVID-040 and completion transition are documentation-only
  changes after the tested implementation head, so their own exact-head hosted CI is `not run/pending`; run
  31357283530 must not be recursively reused as that proof.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=98db55a30ff71a2f2f70722cb569a046208a5403`, completed with `success` at
  2026-08-10T05:09:43Z.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `98db55a...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `d0c248785528e055479c2fa681733917e00eb265` had exact base and head parents.
  Synthetic merge and exact head trees were both `f69d38c66c7fe499db657fb3c3c59037da739878`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93359233758](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233758) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93359233794](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233794) | success |
| Project check (`ubuntu-22.04`) | [93359233864](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233864) | success |
| Project check (`ubuntu-24.04-arm`) | [93359233816](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233816) | success |
| Project check (`macos-15-intel`) | [93359233838](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233838) | success |
| Project check (`macos-26`) | [93359233865](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233865) | success |
| SQLite (`ubuntu-22.04`) | [93359233788](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233788) | success |
| SQLite (`ubuntu-24.04-arm`) | [93359233911](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233911) | success |
| SQLite (`macos-15-intel`) | [93359233905](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233905) | success |
| SQLite (`macos-26`) | [93359233889](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233889) | success |
| Product project check (`ubuntu-22.04`) | [93359233849](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233849) | success |
| Product project check (`ubuntu-24.04-arm`) | [93359233876](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233876) | success |
| Product project check (`macos-15-intel`) | [93359233867](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233867) | success |
| Product project check (`macos-26`) | [93359233872](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233872) | success |
| Python compatibility (`3.12.13`) | [93359233803](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233803) | success |
| Python compatibility (`3.13.15`) | [93359233835](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233835) | success |
| Python compatibility (`3.14.3`) | [93359233839](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233839) | success |
| Python compatibility (`3.14.7`) | [93359233806](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233806) | success |
| Relation binding (`ubuntu-22.04`) | [93359233855](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233855) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93359233815](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233815) | success |
| Relation binding (`macos-15-intel`) | [93359233809](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233809) | success |
| Relation binding (`macos-26`) | [93359233810](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233810) | success |
| Relation product (`ubuntu-22.04`) | [93359233967](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233967) | success |
| Relation product (`ubuntu-24.04-arm`) | [93359233832](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233832) | success |
| Relation product (`macos-15-intel`) | [93359233877](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233877) | success |
| Relation product (`macos-26`) | [93359233906](https://github.com/progresshans/godj/actions/runs/31357283530/job/93359233906) | success |

Hosted gate details:

- Full Ubuntu job `93359233758` passed `make ci`, portable Python 193 tests/17 intentional skips, exactly twelve
  product adapter success lines, all twelve stored checksums, generated artifact no-rewrite and clean-worktree
  gates. Relation stdout was exact
  `GoDj product observations match 2 required contracts; 10 remain not implemented`.
- The full Ubuntu job executed the query, ORM, both relation product fixtures and GoDj runner successfully with
  `GOARCH=386`, `CGO_ENABLED=0`; this closes EVID-039's local compile-only Linux/386 limitation.
- Exact darwin job `93359233794` asserted macOS 15 arm64, Go darwin/arm64, CPython 3.14.3 and historical
  uv 0.10.12, then passed 193/193 with skip 0 plus oracle/no-rewrite gates.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  127 scenarios, encoded payload 498,051 bytes and digest
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg asserted exact GOOS/GOARCH and ran the expanded package set covering Schema IR/DSL,
  query, codegen, ORM, SQLite, both relation product fixtures, protocol/runner/command and external compile. Every
  leg independently recorded exact 492 top-level run events, 492 matching pass events, 0 skips, encoded inventory
  49,902 bytes and SHA-256 `05064a7f0e7a8806d7172fe26a12d846765cdf0d7f991c83b40de07603ba82eb`,
  then passed race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest changed only REL-004 `oracle_locked -> passing`: 10,830 bytes/SHA-256
  `944be1b941b9217ed27c2f6d5a33662cdfafc23f0c7698cad5ebb80849b633f0`. Relation oracle, static fixture,
  twelve-line SHA256SUMS, existing relationproduct bytes, Django reference runner/scenarios, `go.mod` and `go.sum`
  remained byte-preserved.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry and scanned all four raw
relation-product logs. It independently recomputed each 492/492/0 inventory and SHA-256, verified the exact Python
and Ubuntu/Darwin gates, and reported P0/P1/P2/P3=`0/0/0/0`. This evidence accepts only ADR-0025's required
AutoField-target one-hop exact predicate/shared path/SQLite INNER JOIN. It does not establish loader/cache,
nullable/reverse/eager/write/delete/DDL/migration, broader target types or non-SQLite support, and no merge was
performed.

## EVID-20260810-041 — GDJ-0025 GitHub-hosted completion-documentation-head exact 26-job CI

- Date/time: 2026-08-10T14:27:15+09:00–2026-08-10T14:34:38+09:00
- Work/contract IDs: GDJ-0025 final completion-documentation evidence, REL-004, Q-013; REL-002/003/005..012
  explicit ordered not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@7b5cebda7410ae8c096a8c30bd60daad1295bbf2`
  (`docs: complete forward relation predicate slice`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 13-file completion-documentation head containing EVID-040, completed GDJ-0025 and
  Accepted ADR-0025 passed the complete exact-26 topology. Product classification remains exact
  `12 adapter sets/127 contracts = 112 passing + 5 deviation + 10 oracle_locked`, relation actual REL-001/004
  2/12. The other ten relation contracts remain ordered payload-free `not_implemented` and `oracle_locked`.
- Failures/skips/not run: unexpected hosted failure/skip 없음. Portable Python's 17 exact-profile-only skips are
  intentional and every compatibility leg passed exact 193/17; exact darwin passed 193/193 with skip 0. Windows,
  PostgreSQL/MySQL, loader/cache/nullable/reverse/eager/write/delete/DDL/migration, broader target types and
  non-SQLite support were not run or claimed. This EVID-041/final-status patch is a documentation-only change
  after the tested completion-documentation head, so its own exact-head hosted CI is `not run/pending`; run
  31358640776 must not be recursively reused as that proof.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=7b5cebda7410ae8c096a8c30bd60daad1295bbf2`, completed with `success` at
  2026-08-10T05:34:38Z.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `7b5cebda...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `16b176a68497fe34634ddb0e95101710e172c518` had exact base and head parents.
  Synthetic merge and exact head trees were both `311a7813351c8e497ff8dbfa3e9f639878783323`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93362948975](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362948975) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93362949016](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949016) | success |
| Project check (`ubuntu-22.04`) | [93362949101](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949101) | success |
| Project check (`ubuntu-24.04-arm`) | [93362949071](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949071) | success |
| Project check (`macos-15-intel`) | [93362949086](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949086) | success |
| Project check (`macos-26`) | [93362949068](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949068) | success |
| SQLite (`ubuntu-22.04`) | [93362949066](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949066) | success |
| SQLite (`ubuntu-24.04-arm`) | [93362949078](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949078) | success |
| SQLite (`macos-15-intel`) | [93362949034](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949034) | success |
| SQLite (`macos-26`) | [93362949079](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949079) | success |
| Product project check (`ubuntu-22.04`) | [93362948998](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362948998) | success |
| Product project check (`ubuntu-24.04-arm`) | [93362948978](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362948978) | success |
| Product project check (`macos-15-intel`) | [93362949081](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949081) | success |
| Product project check (`macos-26`) | [93362949018](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949018) | success |
| Python compatibility (`3.12.13`) | [93362949088](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949088) | success |
| Python compatibility (`3.13.15`) | [93362949128](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949128) | success |
| Python compatibility (`3.14.3`) | [93362949087](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949087) | success |
| Python compatibility (`3.14.7`) | [93362949080](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949080) | success |
| Relation binding (`ubuntu-22.04`) | [93362949001](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949001) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93362948984](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362948984) | success |
| Relation binding (`macos-15-intel`) | [93362949023](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949023) | success |
| Relation binding (`macos-26`) | [93362949050](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949050) | success |
| Relation product (`ubuntu-22.04`) | [93362949013](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949013) | success |
| Relation product (`ubuntu-24.04-arm`) | [93362949048](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949048) | success |
| Relation product (`macos-15-intel`) | [93362949094](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949094) | success |
| Relation product (`macos-26`) | [93362949069](https://github.com/progresshans/godj/actions/runs/31358640776/job/93362949069) | success |

Hosted gate details:

- Full Ubuntu job `93362948975` passed `make ci`, portable Python 193 tests/17 intentional skips, exactly twelve
  product adapter success lines, all twelve stored checksums and generated/reference no-rewrite gates. Relation
  stdout was exact
  `GoDj product observations match 2 required contracts; 10 remain not implemented`.
- The full Ubuntu job executed the query, ORM, both relation product fixtures and GoDj runner successfully with
  `GOARCH=386`, `CGO_ENABLED=0`.
- Exact darwin job `93362949016` asserted macOS 15 arm64, Go darwin/arm64, CPython 3.14.3 and historical
  uv 0.10.12, then passed 193/193 with skip 0 plus oracle/no-rewrite gates.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  127 scenarios, encoded payload 498,051 bytes and digest
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg asserted exact GOOS/GOARCH and independently recorded exact 492 top-level run events,
  492 matching pass events, 0 skips, encoded inventory 49,902 bytes and SHA-256
  `05064a7f0e7a8806d7172fe26a12d846765cdf0d7f991c83b40de07603ba82eb`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry and scanned the raw relation-product,
Python, Ubuntu and exact Darwin logs. It verified the exact inventories and compatibility gates and reported
P0/P1/P2/P3=`0/0/0/0`. This evidence accepts only ADR-0025's required AutoField-target one-hop exact
predicate/shared path/SQLite INNER JOIN. It does not establish loader/cache, nullable/reverse/eager/write/delete/
DDL/migration, broader target types or non-SQLite support, and no merge was performed.

## EVID-20260810-042 — GDJ-0025 Final Exact-Head CI and GDJ-0026 Activation Baseline

- Date/time: 2026-08-10T14:52:05+09:00–2026-08-10T14:58:13+09:00
- Work/contract IDs: GDJ-0025 final evidence; GDJ-0026/REL-003/REL-006/Q-013 activation baseline only
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@bffc52844de87a2791959ea1e8f99c60dd13d1aa`
  (`docs: record hosted forward relation completion`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite/relation product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7, uv 0.12.3 for routine
  compatibility and historical exact darwin uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: The exact six-file EVID-041/final-status patch was committed as `bffc5284...` and passed the
  complete exact-26 topology. This closes EVID-041's recursive pending state and establishes the clean tested
  baseline for GDJ-0026. Product classification at this checkout remains exact
  `12 adapter sets/127 contracts = 112 passing + 5 deviation + 10 oracle_locked`, relation actual REL-001/004
  2/12. REL-003/006 remain ordered payload-free `not_implemented`/`oracle_locked` at this tested baseline.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0. Portable Python's 17 exact-profile-only skips
  remain intentional; every compatibility leg passed exact 193/17 and exact darwin passed 193/193 with skip 0.
  Windows, PostgreSQL/MySQL, object loader/cache, nullable access/`isnull`, reverse/eager/write/delete/DDL/migration,
  broader target types and non-SQLite support were not run or claimed. The GDJ-0026 activation documentation diff
  appended after this tested head is itself `not run/pending`; run 31359958949 must not be reused as proof of that
  later activation diff.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=bffc52844de87a2791959ea1e8f99c60dd13d1aa`, status `completed`, conclusion `success`.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `bffc5284...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `ddf5506ecab2289f4bee6931d61e6cdedc1f02fb` had exact base and head parents.
  Synthetic merge and exact head trees were both `15f5c41fbd5a865e3189971ff48645702ad83df9`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93366753761](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753761) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93366753748](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753748) | success |
| Project check (`ubuntu-22.04`) | [93366753825](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753825) | success |
| Project check (`ubuntu-24.04-arm`) | [93366753821](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753821) | success |
| Project check (`macos-15-intel`) | [93366753806](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753806) | success |
| Project check (`macos-26`) | [93366753802](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753802) | success |
| SQLite (`ubuntu-22.04`) | [93366753883](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753883) | success |
| SQLite (`ubuntu-24.04-arm`) | [93366753827](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753827) | success |
| SQLite (`macos-15-intel`) | [93366753872](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753872) | success |
| SQLite (`macos-26`) | [93366753862](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753862) | success |
| Product project check (`ubuntu-22.04`) | [93366753830](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753830) | success |
| Product project check (`ubuntu-24.04-arm`) | [93366753874](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753874) | success |
| Product project check (`macos-15-intel`) | [93366753839](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753839) | success |
| Product project check (`macos-26`) | [93366753823](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753823) | success |
| Python compatibility (`3.12.13`) | [93366753863](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753863) | success |
| Python compatibility (`3.13.15`) | [93366753799](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753799) | success |
| Python compatibility (`3.14.3`) | [93366753842](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753842) | success |
| Python compatibility (`3.14.7`) | [93366753886](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753886) | success |
| Relation binding (`ubuntu-22.04`) | [93366753804](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753804) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93366753783](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753783) | success |
| Relation binding (`macos-15-intel`) | [93366753865](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753865) | success |
| Relation binding (`macos-26`) | [93366753779](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753779) | success |
| Relation product (`ubuntu-22.04`) | [93366753769](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753769) | success |
| Relation product (`ubuntu-24.04-arm`) | [93366753764](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753764) | success |
| Relation product (`macos-15-intel`) | [93366753782](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753782) | success |
| Relation product (`macos-26`) | [93366753778](https://github.com/progresshans/godj/actions/runs/31359958949/job/93366753778) | success |

Gate details:

- Every relation-product coordinate retained exact 492 run/492 pass/0 skip, 49,902-byte inventory and SHA-256
  `05064a7f0e7a8806d7172fe26a12d846765cdf0d7f991c83b40de07603ba82eb`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Every Python uv 0.12.3 coordinate passed portable 193/17 and exact 127-scenario 498,051-byte SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Full Ubuntu passed `make ci`, twelve product adapters, stored checksums/generated-reference no-rewrite and actual Linux/386
  relation-query execution. Relation stdout remained exact
  `GoDj product observations match 2 required contracts; 10 remain not implemented`.
- Exact darwin preserved the locked profile and passed 193/193 with skip 0. Relation oracle/static/SHA256SUMS,
  existing relationproduct/relationqueryproduct generated bytes, Django relation runner/scenarios and `go.mod`/
  `go.sum` remained byte-preserved.

This evidence closes GDJ-0025's final documentation-head verification only and establishes the tested baseline for
GDJ-0026. It does not establish REL-003/006, relation object/cache/nullability, broader relation behavior or non-SQLite
backend support. The activation diff must receive its own later exact-head evidence, and no merge was performed.

## EVID-20260810-043 — GDJ-0026 REL-003/006 Object Cache and Nullability Pre-hosted Local Validation

- Date/time: 2026-08-10; activation hosted 16:12:48–16:20:05 KST, local implementation validation afterward
- Work/contract IDs: GDJ-0026, REL-003, REL-006, Q-013; REL-002/005/007..012 explicit ordered
  not-implemented boundary
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `aad4f7ff0d77a1abe16ebddd01782e78c335395f` plus the uncommitted frozen GDJ-0026 implementation and this
  pre-hosted status patch. The implementation commit identity is intentionally unknown/pending at this evidence point.
- Local environment: Go 1.26.5 `darwin/arm64`; CPython 3.14.3; uv 0.12.3. Routine local Python uses only this
  version. Exact CPython 3.12.13/3.13.15/3.14.3/3.14.7 remains CI-only; historical exact darwin remains pinned to
  uv 0.10.12.
- Result summary: sealed immutable descriptor/storage snapshots, required/nullable `RelatedObject` factories and
  bounded target-PK `Limit(2).All` cache, additive v2/v3 relation-object companions/project bridge, relation
  `source_key` provenance and SQLite root-FK JOIN-0 trim are locally implemented. The separate oracle-blind actual
  loads Post 10 through the generated QuerySet, observes Author cold/warm SELECT counts 1/0, observes Post 11's null
  Reviewer with SELECT 0, and returns typed/dynamic `reviewer__isnull=true` result `[11]` with SELECT 1/JOIN 0.
  Local product classification is exact 12 adapter sets/127 contracts=
  `114 passing + 5 deviation + 8 oracle_locked`; relation actual is REL-001/003/004/006 4/12 and the other eight
  REL contracts remain ordered payload-free `not_implemented`.
- Failures/skips/not run: local unexpected failures 0. Portable Python's 17 exact-profile-only skips remain
  intentional. Local Linux/386 is exact-package cross-compile only; actual Ubuntu/386 execution is pending. The
  exact-26 implementation-head workflow and four exact Python compatibility legs have not been committed/pushed or
  run and remain `not run/pending`. Windows, PostgreSQL/MySQL, reverse/eager/write/delete/DDL/migration,
  non-AutoField targets and non-SQLite product support remain unsupported/out of scope.

Activation exact-head hosted evidence:

- Draft PR #1 `pull_request`
  [run 31364944816](https://github.com/progresshans/godj/actions/runs/31364944816), attempt 1, completed with
  `success` at exact `headSha=aad4f7ff0d77a1abe16ebddd01782e78c335395f`.
- Exact 26/26 jobs and all 326/326 recorded steps succeeded; failed/cancelled/skipped jobs and non-success recorded
  steps were 0. Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`.
- Actions synthetic merge `f9b4743307cde40d4e0321b86ce7d02a84f282ac` had the exact base and activation-head
  parents. Its tree and the activation head tree were both `49c0d2ff6006b5e94d605198f1c7e79f156cdcd5`, so
  executed contents were exact-head-equivalent.
- Each relation-product coordinate retained exact 492 run/492 pass/0 skip, 49,902 bytes and SHA-256
  `05064a7f0e7a8806d7172fe26a12d846765cdf0d7f991c83b40de07603ba82eb`. Full Ubuntu and exact Darwin,
  including actual Ubuntu Linux/386 for the pre-implementation fixture, passed. Each uv 0.12.3 compatibility leg
  passed portable 193/17 and 127 scenarios at 498,051 bytes/SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`; exact darwin passed 193/193
  with historical uv 0.10.12.
- An independent hosted audit re-queried run/job/step/PR/ancestry data and raw relation-product logs and reported
  P0/P1/P2/P3=`0/0/0/0`. This run proves the activation documentation only, not the following uncommitted
  implementation.

Local commands and exact gates:

1. `PYTHONDONTWRITEBYTECODE=1 make ci`
   - PASS: format/generate checks, full Go normal/vet/race, focused CGO-disabled packages, portable Python,
     conformance and all twelve product adapters. Portable Python reported 193 tests with 17 intentional skips.
2. Workflow-equivalent `go test -json -count=1` over `./schema/...`, `./query`, `./codegen`, `./orm`,
   `./db/sqlite`, `./migrations`, `./migrations/definition`, three relation product trees, protocol, GoDj runner,
   `godjcheck` and external compile tests, followed by the exact workflow inventory verifier
   - PASS: exact 533 top-level run events, 533 matching pass events, 0 skip events; encoded inventory 54,076 bytes,
     SHA-256 `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`.
3. The same exact package set under race, `CGO_ENABLED=0` and vet, plus focused repeated object/cache/cancellation,
   generator, AST, SQLite compiler/integration and conformance runs
   - PASS. Object-level owner-to-live-waiter retry, waiter-only cancellation, scan/rows/close retry, warm/Fresh
     captured-session lifetime, 0/2-row cached reclassification, typed/dynamic `Plan.Equal`, source-key mutation,
     generated external positive/negative compile and app-to-app Imports/Deps 0 gates all passed.
4. `GOOS=linux GOARCH=386 CGO_ENABLED=0 go test -exec=true -count=1` over the exact relation package set
   - PASS as a bounded Linux/386 cross-compile. `-exec=true` does not execute those binaries; actual Ubuntu/386
     GDJ-0026 execution remains a required implementation-head hosted gate. This local gate does not claim broad
     `./db/sqlite` Linux/386 coverage.
5. `make godj-conformance`
   - PASS for all 12 adapters. Relation stdout was exactly
     `GoDj product observations match 4 required contracts; 8 remain not implemented`.
6. `PYTHONDONTWRITEBYTECODE=1 uv run --frozen python -m unittest
   conformance.runners.django.tests.test_relation_scenarios`
   - PASS: 11/11.

Artifact, scope and false-green evidence:

- Relation manifest changed only REL-003 and REL-006 `oracle_locked -> passing`: 10,818 bytes/SHA-256
  `e548332401932059a87920f90fb7a1300aa02e3c5775335e3b6eda90cc84293a`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. All stored checksums passed.
- Existing v2/v3 main, metadata, query companion and project-query v1 bytes, prior `relationproduct` and
  `relationqueryproduct` fixture bytes, schema/migrations, `go.mod`, `go.sum`, Django runner/scenario implementation,
  oracle/static/SHA256SUMS remained unchanged. The sole Django-side edit is its manifest-status assertion. New
  relation-object generated bytes passed deterministic/gofmt/schema-hash/version/no-rewrite and external union
  compile gates.
- A generator false-green review found that a legal package alias could shadow emitted predeclared literal `false`.
  Before freeze, Proposed ADR-0026/work were amended to reserve exact used identifiers `bool`, `error`, `false`,
  `nil`, generated receivers/locals were underscore-prefixed, and a target-resolved two-package negative generator
  gate asserted the exact alias error with zero output bytes. The prior main/metadata/query/project-query bytes
  remained exact.
- Before this five-document status integration, the frozen working tree had exact 52 changed/untracked paths versus
  activation HEAD, all covered by 51 unique frontmatter allowlist selectors and 0 outside. This status patch touches
  only its exact five allowed documents. After that integration the full tree has exact 56 physical changed/untracked
  paths (the work file was already among the original 52), still 0 outside. Nothing was staged, committed, pushed or
  merged.
- The historical EVID-001..042 byte range remained exact 245,884 bytes/SHA-256
  `3a7acd82bddad910065f0e9000dc72cf229f425047bac67fd497ea53171624cd`; only the top current-evidence pointer was
  updated and this EVID-043 entry was appended.

Four independent read-only audits covered runtime/cache/cancellation, codegen/import/alias false-green boundaries,
SQLite/conformance observations and final integration/security/scope. After the `false` alias negative-gate fix,
all final verdicts were P0/P1/P2/P3=`0/0/0/0`. The primary final-byte `PYTHONDONTWRITEBYTECODE=1 make ci` rerun also
passed. Hosted acceptance remains pending until the frozen implementation and evidence are committed and pushed.
ADR-0026 therefore remains Proposed, GDJ-0026 remains active, and Q-013 remains `Partial`.

## EVID-20260810-044 — GDJ-0026 GitHub-hosted Exact 26-job Implementation-head CI

- Date/time: 2026-08-10T17:29:47+09:00–2026-08-10T17:37:33+09:00
- Work/contract IDs: GDJ-0026, REL-003, REL-006, Q-013; REL-002/005/007..012 explicit ordered
  not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@5be46141d943800a3c621975e3e5070f6d01eaf9`
  (`feat: add forward relation object cache slice`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite relation-product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31370313755](https://github.com/progresshans/godj/actions/runs/31370313755), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: sealed immutable descriptor/storage snapshots, required/nullable target-PK `Limit(2).All`
  object cache, additive v2/v3 relation-object companions/project bridge, relation `source_key` provenance and
  SQLite root-FK JOIN-0 trim passed on the exact implementation head. Oracle-blind REL-003/006 preserve Author
  cold/warm SELECT 1/0, nullable local NULL SELECT 0 and typed/dynamic `reviewer__isnull=true` result `[11]` with
  SELECT 1/JOIN 0. Product is exact 12 adapter sets/127 contracts=`114 passing + 5 deviation + 8 oracle_locked`,
  relation actual REL-001/003/004/006 4/12; the other eight relation contracts remain ordered payload-free
  `not_implemented` and `oracle_locked`.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at the job/recorded-step level. Portable Python's
  17 exact-profile-only skips are intentional and every compatibility leg passed 193/17; exact darwin passed
  193/193 with skip 0. Windows, PostgreSQL/MySQL, reverse/eager/prefetch/write/delete/DDL/migration, cache
  invalidation, LEFT JOIN, nullable target traversal, broader target types and non-SQLite product support were not
  run or claimed. This EVID-044 and the exact 15-file completion-documentation transition are later documentation-
  only changes after the tested implementation head, so their own exact-head hosted CI is `not run/pending`; run
  31370313755 must not be recursively reused as that proof.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=5be46141d943800a3c621975e3e5070f6d01eaf9`, status `completed`, conclusion `success`.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `5be46141...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `f4062feefacd6d217d023ba4ae208de836ca0c32` had the exact base and head parents.
  Synthetic merge and exact head trees were both `33b431c08d338b7188e1e89681e13bff3da44ce0`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93397583554](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583554) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93397583592](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583592) | success |
| Project check (`ubuntu-22.04`) | [93397583572](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583572) | success |
| Project check (`ubuntu-24.04-arm`) | [93397583590](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583590) | success |
| Project check (`macos-15-intel`) | [93397583620](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583620) | success |
| Project check (`macos-26`) | [93397583625](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583625) | success |
| SQLite (`ubuntu-22.04`) | [93397583664](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583664) | success |
| SQLite (`ubuntu-24.04-arm`) | [93397583652](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583652) | success |
| SQLite (`macos-15-intel`) | [93397583694](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583694) | success |
| SQLite (`macos-26`) | [93397583672](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583672) | success |
| Product project check (`ubuntu-22.04`) | [93397583645](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583645) | success |
| Product project check (`ubuntu-24.04-arm`) | [93397583709](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583709) | success |
| Product project check (`macos-15-intel`) | [93397583549](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583549) | success |
| Product project check (`macos-26`) | [93397583613](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583613) | success |
| Python compatibility (`3.12.13`) | [93397583660](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583660) | success |
| Python compatibility (`3.13.15`) | [93397583626](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583626) | success |
| Python compatibility (`3.14.3`) | [93397583641](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583641) | success |
| Python compatibility (`3.14.7`) | [93397583655](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583655) | success |
| Relation binding (`ubuntu-22.04`) | [93397583597](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583597) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93397583743](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583743) | success |
| Relation binding (`macos-15-intel`) | [93397583678](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583678) | success |
| Relation binding (`macos-26`) | [93397583562](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583562) | success |
| Relation product (`ubuntu-22.04`) | [93397583639](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583639) | success |
| Relation product (`ubuntu-24.04-arm`) | [93397583622](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583622) | success |
| Relation product (`macos-15-intel`) | [93397583541](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583541) | success |
| Relation product (`macos-26`) | [93397583644](https://github.com/progresshans/godj/actions/runs/31370313755/job/93397583644) | success |

Hosted gate details:

- Full Ubuntu job `93397583554` passed `make ci`, portable Python 193 tests/17 intentional skips, exactly twelve
  product adapter success lines, all stored checksums and generated/reference no-rewrite gates. Relation stdout was
  exact `GoDj product observations match 4 required contracts; 8 remain not implemented`.
- The full Ubuntu job executed `./query`, `./orm`, all three relation product trees and the GoDj runner successfully
  with `GOARCH=386`, `CGO_ENABLED=0`. This closes EVID-043's local compile-only limitation for that exact GDJ-0026
  package set; it does not claim broad `./db/sqlite/...` Linux/386 coverage.
- Exact darwin job `93397583592` asserted macOS 15 arm64, Go darwin/arm64, CPython 3.14.3 and historical
  uv 0.10.12, then passed 193/193 with skip 0 plus oracle/no-rewrite gates.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg asserted exact GOOS/GOARCH and independently recorded exact 533 top-level run events,
  533 matching pass events, 0 skips, encoded inventory 54,076 bytes and SHA-256
  `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest changed only REL-003 and REL-006 `oracle_locked -> passing`: 10,818 bytes/SHA-256
  `e548332401932059a87920f90fb7a1300aa02e3c5775335e3b6eda90cc84293a`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. Existing main/metadata/query/
  project-query outputs, prior relationproduct/relationqueryproduct bytes, Django runner/scenario implementation,
  schema/migrations, `go.mod` and `go.sum` remained byte-preserved.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, checked the synthetic merge/head
tree identity and scanned the raw relation-product, Python, full Ubuntu and exact Darwin evidence. It confirmed the
exact inventories, compatibility gates, artifact boundaries and reported P0/P1/P2/P3=`0/0/0/0`. This evidence
accepts only ADR-0026's AutoField-target one-hop forward object/cache, nullable local-NULL access and relation-aware
SQLite source-key isnull trim. It does not establish reverse/eager/prefetch/write/delete/DDL/migration, cache
invalidation, broader target types or non-SQLite support, and no merge was performed.

## EVID-20260810-045 — GDJ-0026 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-10T17:57:12+09:00–2026-08-10T18:06:04+09:00
- Work/contract IDs: GDJ-0026, REL-003, REL-006, Q-013; REL-002/005/007..012 explicit ordered
  not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@7f92fcf036d03a5004953d9857a10291f4603efb`
  (`docs: complete forward relation object cache slice`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite relation-product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31372360481](https://github.com/progresshans/godj/actions/runs/31372360481), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 15-file completion-documentation transition from implementation head `5be46141...`
  to `7f92fcf0...` passed unchanged product gates. GDJ-0026 remains completed, ADR-0026 remains Accepted only for
  the bounded AutoField-target one-hop REL-003/006 slice, Q-013 remains `Partial`, and active/ready work remains
  empty. Product classification remains exact 12 adapter sets/127 contracts=
  `114 passing + 5 deviation + 8 oracle_locked`, relation actual REL-001/003/004/006 4/12.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at the job/recorded-step level. Portable Python's
  17 exact-profile-only skips are intentional and every compatibility leg passed 193/17; exact darwin passed
  193/193 with skip 0. Windows, PostgreSQL/MySQL, reverse/eager/prefetch/write/delete/DDL/migration, cache
  invalidation, LEFT JOIN, nullable target traversal, broader target types and non-SQLite product support were not
  run or claimed. This EVID-045 append and its exact eight-file final evidence/status patch are later documentation-
  only changes after the tested completion-documentation head, so that eight-file patch's own exact-head CI is
  `not run/pending`; run 31372360481 must not be recursively reused as that proof.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=7f92fcf036d03a5004953d9857a10291f4603efb`, status `completed`, conclusion `success`.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `7f92fcf0...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `7246e5d319fe3735c7d3d82c889506b788eda64a` had the exact base and head parents.
  Synthetic merge and exact head trees were both `af539d20681a09db99fbd213eb766563f4572b5f`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93403931097](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931097) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93403931187](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931187) | success |
| Project check (`ubuntu-22.04`) | [93403931297](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931297) | success |
| Project check (`ubuntu-24.04-arm`) | [93403931300](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931300) | success |
| Project check (`macos-15-intel`) | [93403931260](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931260) | success |
| Project check (`macos-26`) | [93403931161](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931161) | success |
| SQLite (`ubuntu-22.04`) | [93403931348](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931348) | success |
| SQLite (`ubuntu-24.04-arm`) | [93403931296](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931296) | success |
| SQLite (`macos-15-intel`) | [93403931247](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931247) | success |
| SQLite (`macos-26`) | [93403931405](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931405) | success |
| Product project check (`ubuntu-22.04`) | [93403931349](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931349) | success |
| Product project check (`ubuntu-24.04-arm`) | [93403931308](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931308) | success |
| Product project check (`macos-15-intel`) | [93403931336](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931336) | success |
| Product project check (`macos-26`) | [93403931322](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931322) | success |
| Python compatibility (`3.12.13`) | [93403931257](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931257) | success |
| Python compatibility (`3.13.15`) | [93403931291](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931291) | success |
| Python compatibility (`3.14.3`) | [93403931295](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931295) | success |
| Python compatibility (`3.14.7`) | [93403931356](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931356) | success |
| Relation binding (`ubuntu-22.04`) | [93403931167](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931167) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93403931305](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931305) | success |
| Relation binding (`macos-15-intel`) | [93403931303](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931303) | success |
| Relation binding (`macos-26`) | [93403931248](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931248) | success |
| Relation product (`ubuntu-22.04`) | [93403931148](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931148) | success |
| Relation product (`ubuntu-24.04-arm`) | [93403931249](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931249) | success |
| Relation product (`macos-15-intel`) | [93403931198](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931198) | success |
| Relation product (`macos-26`) | [93403931301](https://github.com/progresshans/godj/actions/runs/31372360481/job/93403931301) | success |

Hosted gate details:

- Full Ubuntu job `93403931097` passed `make ci`, portable Python 193 tests/17 intentional skips, exactly twelve
  product adapter success lines, all stored checksums and generated/reference no-rewrite gates. Relation stdout was
  exact `GoDj product observations match 4 required contracts; 8 remain not implemented`.
- The full Ubuntu job executed `./query`, `./orm`, all three relation product trees and the GoDj runner successfully
  with `GOARCH=386`, `CGO_ENABLED=0`. This is the exact GDJ-0026 relation package set, not broad
  `./db/sqlite/...` Linux/386 coverage.
- Exact darwin job `93403931187` asserted macOS 15 arm64, Go darwin/arm64, CPython 3.14.3 and historical
  uv 0.10.12, then passed 193/193 with skip 0 plus oracle/no-rewrite gates.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg asserted exact GOOS/GOARCH and independently recorded exact 533 top-level run events,
  533 matching pass events, 0 skips, encoded inventory 54,076 bytes and SHA-256
  `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest remained 10,818 bytes/SHA-256
  `e548332401932059a87920f90fb7a1300aa02e3c5775335e3b6eda90cc84293a`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. Existing main/metadata/query/
  project-query outputs, prior relationproduct/relationqueryproduct bytes, Django runner/scenario implementation,
  schema/migrations, `go.mod` and `go.sum` remained byte-preserved by the unchanged gates.
- Before this append, the historical EVID-001..044 body was exact 264,100 bytes/SHA-256
  `c5a484f15587edf270c9ab2dc790cd899a923aecad344e5e000d378dde2deb55`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-045 within this file.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified exact base/head parents and
synthetic-merge/head tree identity, and checked raw full Ubuntu, exact Darwin, Python and four-coordinate relation
logs. It confirmed the exact inventories, compatibility and artifact boundaries and reported
P0/P1/P2/P3=`0/0/0/0`. This evidence closes only the exact 15-file completion-documentation head. It does not widen
ADR-0026 or Q-013, prove this later eight-file evidence/status patch, or authorize merging Draft PR #1; no merge
was performed.

## EVID-20260811-046 — GDJ-0026 Final-status Exact-head CI and GDJ-0027 Activation Baseline

- Date/time: 2026-08-10T18:20:46+09:00–2026-08-10T18:27:05+09:00
- Work/contract IDs: GDJ-0026 final status; GDJ-0027 activation baseline; REL-001/003/004/006 passing,
  REL-002/005/007..012 ordered `oracle_locked`; Q-013
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@9ba1d0ee4cb96c265269000700beb5889fef2206`
  (`docs: record hosted relation object completion`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite relation-product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31374150640](https://github.com/progresshans/godj/actions/runs/31374150640), attempt 1
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact GDJ-0026 eight-file final evidence/status head passed unchanged gates. Product remains
  exact 12 adapter sets/127 contracts=`114 passing + 5 deviation + 8 oracle_locked`, relation actual
  REL-001/003/004/006 4/12. This is the clean tested baseline for GDJ-0027; it does not prove the later activation
  documentation diff, Proposed ADR-0027, REL-005 implementation, or the target `115 + 5 + 7` classification.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at job/recorded-step level. Portable Python's 17
  exact-profile-only skips remain intentional and each compatibility leg passed 193/17; exact darwin passed 193/193
  with skip 0. Reverse accessor/lookup, eager/prefetch/write/delete/DDL/migration and non-SQLite support were not run
  or claimed. The GDJ-0027 activation patch is a later diff and its exact-head CI is `not run/pending`; this run must
  not be reused recursively.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=9ba1d0ee4cb96c265269000700beb5889fef2206`, status `completed`, conclusion `success`.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `9ba1d0ee...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `16d159dde4d0b00986d296edccbe0ff68733c877` had those exact base/head parents.
  Synthetic merge and exact head trees were both `e80dbd0e2e7a623460dbf1369b0527c9685912ad`, so executed contents
  were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93409507446](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507446) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93409507510](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507510) | success |
| Project check (`ubuntu-22.04`) | [93409507589](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507589) | success |
| Project check (`ubuntu-24.04-arm`) | [93409507520](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507520) | success |
| Project check (`macos-15-intel`) | [93409507487](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507487) | success |
| Project check (`macos-26`) | [93409507528](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507528) | success |
| SQLite (`ubuntu-22.04`) | [93409507501](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507501) | success |
| SQLite (`ubuntu-24.04-arm`) | [93409507558](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507558) | success |
| SQLite (`macos-15-intel`) | [93409507511](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507511) | success |
| SQLite (`macos-26`) | [93409507749](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507749) | success |
| Product project check (`ubuntu-22.04`) | [93409507522](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507522) | success |
| Product project check (`ubuntu-24.04-arm`) | [93409507369](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507369) | success |
| Product project check (`macos-15-intel`) | [93409507441](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507441) | success |
| Product project check (`macos-26`) | [93409507422](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507422) | success |
| Python compatibility (`3.12.13`) | [93409507563](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507563) | success |
| Python compatibility (`3.13.15`) | [93409507546](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507546) | success |
| Python compatibility (`3.14.3`) | [93409507543](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507543) | success |
| Python compatibility (`3.14.7`) | [93409507480](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507480) | success |
| Relation binding (`ubuntu-22.04`) | [93409507818](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507818) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93409507453](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507453) | success |
| Relation binding (`macos-15-intel`) | [93409507492](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507492) | success |
| Relation binding (`macos-26`) | [93409507536](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507536) | success |
| Relation product (`ubuntu-22.04`) | [93409507489](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507489) | success |
| Relation product (`ubuntu-24.04-arm`) | [93409507509](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507509) | success |
| Relation product (`macos-15-intel`) | [93409507513](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507513) | success |
| Relation product (`macos-26`) | [93409507434](https://github.com/progresshans/godj/actions/runs/31374150640/job/93409507434) | success |

Hosted gate details:

- Full Ubuntu passed `make ci`, portable Python 193/17, exactly twelve product adapter success lines, all stored
  checksums and generated/reference no-rewrite gates. Relation stdout remained exact
  `GoDj product observations match 4 required contracts; 8 remain not implemented`.
- Full Ubuntu executed the exact GDJ-0026 relation package set with `GOARCH=386 CGO_ENABLED=0`; this is not broad
  `./db/sqlite/...` Linux/386 support. Exact darwin asserted macOS 15 arm64, Go darwin/arm64, CPython 3.14.3 and
  uv 0.10.12, then passed 193/193 with skip 0.
- Each uv 0.12.3 Python compatibility leg passed portable 193/17 and verified 127 scenarios, encoded payload
  498,051 bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg recorded exact 533 top-level run events, 533 matching pass events, 0 skips, encoded
  inventory 54,076 bytes and SHA-256
  `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`, then passed race, CGO-disabled, vet,
  generated-fixture no-rewrite and clean-worktree gates.
- Before this append, historical EVID-001..045 body was exact 273,832 bytes/SHA-256
  `d1c8679dee61dee79c61ae0f33cfef366898db30753b6fbb3b01282ec3870b8b`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-046 within this file.

This evidence closes the previous exact final-status head and establishes only GDJ-0027's clean tested baseline.
ADR-0027 remains Proposed, GDJ-0027 remains active, Q-013 remains `Partial`, and the activation documentation diff
requires its own non-reused exact-head CI. No Draft PR merge was performed or authorized.

## EVID-20260811-047 — GDJ-0027 REL-005 Reverse Accessor and Lookup Pre-hosted Local Validation

- Date/time: 2026-08-11; activation hosted 02:26:49–02:36:00 KST, frozen local implementation validation afterward
- Work/contract IDs: GDJ-0027, REL-005, Q-013; REL-002/007..012 explicit ordered not-implemented boundary
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `9dbc2fd2ab3201e8968f65b31db8eedf3f9a845a` plus the uncommitted frozen GDJ-0027 implementation and this
  pre-hosted five-document status sync. The implementation commit identity is intentionally unknown/pending.
- Local environment/backend: Go 1.26.5 `darwin/arm64`; SQLite 3.53.3 through the existing modernc driver.
  The local gate is normal-only. Hosted race, CGO-disabled, vet, actual Linux/386, macOS coordinates and exact
  CPython compatibility legs belong to the still-pending implementation-head CI.
- Result summary: declaration-centric reverse `RelationPath`, query-only typed/dynamic reverse binding, sealed
  primary-key object capability, generated project-owned reverse object/`RelatedSet`, and SQLite reverse INNER JOIN
  compilation are locally implemented. The oracle-blind REL-005 actual freshly loads Author 1, observes generated
  `Posts()` IDs `[10,11]` with evaluation SELECT 1/JOIN 0, and observes typed/dynamic `posts__title=Alpha`
  `Plan.Equal` with Author IDs `[1]`, SELECT 1/INNER JOIN 1 and unchanged database state. Local product
  classification is exact 12 adapter sets/127 contracts=`115 passing + 5 deviation + 7 oracle_locked`; relation
  actual is REL-001/003/004/005/006 5/12 and the remaining seven relation contracts stay ordered payload-free
  `not_implemented`.
- Failures/skips/not run: local unexpected failures 0. Local race, CGO-disabled, vet, repetition, full `make ci`,
  actual Linux/386 execution and the four exact Python compatibility legs were deliberately not rerun; they are
  delegated to the exact implementation-head hosted topology. The frozen implementation and this status sync have
  not been staged, committed, pushed or run on GitHub and remain `not run/pending`. Activation run 31414060387 must
  not be reused as implementation evidence. Windows, PostgreSQL/MySQL, reverse prefetch/eager/write/delete/DDL/
  migration, custom targets and broader relation behavior remain unsupported/out of scope.

Activation exact-head hosted evidence, used only for baseline preservation:

- Draft PR #1 `pull_request`
  [run 31414060387](https://github.com/progresshans/godj/actions/runs/31414060387), attempt 1, completed with
  `success` at exact `headSha=9dbc2fd2ab3201e8968f65b31db8eedf3f9a845a`.
- Exact 26/26 jobs and all 326/326 recorded steps succeeded; failed/cancelled/skipped jobs and non-success recorded
  steps were 0. Live re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact activation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `f0bc42143245a45a425e6ca6d1cccfec8457fcd4` had those exact base/head parents.
  Synthetic merge and exact activation-head trees were both `7c6f9d25d49dd38a6db6fc13dab35b06d0aa8918`,
  so the executed activation contents were exact-head-equivalent.
- Each of the four relation-product coordinates preserved the pre-implementation inventory: exact 533 run/533
  pass/0 skip, 54,076 bytes and SHA-256
  `6d2958b63e68dcbf0a63aa02adb47cdf005a4896af80f22e4acc49e78dd07aee`. This is raw-log baseline-preservation
  proof for `114 + 5 + 8` and relation 4/12 only; it does not establish REL-005 or the local 569-test inventory.

Exact activation job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93538740791](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740791) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93538740762](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740762) | success |
| Project check (`ubuntu-22.04`) | [93538740913](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740913) | success |
| Project check (`ubuntu-24.04-arm`) | [93538740883](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740883) | success |
| Project check (`macos-15-intel`) | [93538740900](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740900) | success |
| Project check (`macos-26`) | [93538740907](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740907) | success |
| SQLite (`ubuntu-22.04`) | [93538740981](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740981) | success |
| SQLite (`ubuntu-24.04-arm`) | [93538740959](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740959) | success |
| SQLite (`macos-15-intel`) | [93538740876](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740876) | success |
| SQLite (`macos-26`) | [93538740901](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740901) | success |
| Product project check (`ubuntu-22.04`) | [93538740829](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740829) | success |
| Product project check (`ubuntu-24.04-arm`) | [93538740837](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740837) | success |
| Product project check (`macos-15-intel`) | [93538740963](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740963) | success |
| Product project check (`macos-26`) | [93538740826](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740826) | success |
| Python compatibility (`3.12.13`) | [93538740872](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740872) | success |
| Python compatibility (`3.13.15`) | [93538740893](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740893) | success |
| Python compatibility (`3.14.3`) | [93538740813](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740813) | success |
| Python compatibility (`3.14.7`) | [93538740941](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740941) | success |
| Relation binding (`ubuntu-22.04`) | [93538740920](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740920) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93538740866](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740866) | success |
| Relation binding (`macos-15-intel`) | [93538740878](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740878) | success |
| Relation binding (`macos-26`) | [93538740857](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740857) | success |
| Relation product (`ubuntu-22.04`) | [93538740898](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740898) | success |
| Relation product (`ubuntu-24.04-arm`) | [93538740961](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740961) | success |
| Relation product (`macos-15-intel`) | [93538740821](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740821) | success |
| Relation product (`macos-26`) | [93538740892](https://github.com/progresshans/godj/actions/runs/31414060387/job/93538740892) | success |

Local commands and exact gates:

1. `make format-check generate-check go-test godj-conformance`
   - PASS: all tracked/untracked Go formatting, existing generator check, full normal `go test ./...` and all twelve
     product adapters. The relation adapter printed exact
     `GoDj product observations match 5 required contracts; 7 remain not implemented`.
2. Workflow-equivalent `go test -json -count=1` over the exact relation-product package set, including
   `./conformance/relationreverseproduct/...`, followed by the exact inventory verifier
   - PASS: exact 569 top-level run events, 569 matching pass events, 0 skip events; encoded inventory 57,738 bytes,
     SHA-256 `739bb6fc4bc3a5665cbaa455bed45d4ddf9683d78c4ff74b02c1d0208862c2d7`.
3. Runtime/object, codegen/publication and final integration/scope read-only audits
   - CLEAN: each final verdict was P0/P1/P2/P3=`0/0/0/0` after remediation. The codegen remediation also amended
     Proposed ADR-0027/work wording from a wrong-version generator guarantee to the exact ABI-incompatible caller
     union-compile boundary; that pre-existing ADR/work delta is not part of this status sync.

Artifact, scope and false-green evidence:

- Relation manifest changed only REL-005 `oracle_locked -> passing`: 10,812 bytes/SHA-256
  `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. All stored checksums passed.
- Existing generators and all three prior `relationproduct`, `relationqueryproduct` and `relationobjectproduct`
  trees remained byte-identical; oracle/static/SHA and existing generated fixtures were preserved. The new
  project-only reverse generator/fixture passed deterministic, gofmt, permutation, nil-byte failure,
  external-union compile and app-to-app import-edge-0 gates.
- Against activation HEAD `9dbc2fd2...`, the frozen implementation had exact 46 physical changed/untracked paths.
  This pre-hosted sync changes exactly five documents: `docs/status/CURRENT.md`,
  `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, this work packet and `work/README.md`.
  Because the work packet was already dirty, the resulting activation-head-relative tree has exact 50 physical
  changed/untracked paths. Against clean GDJ-0027 baseline `9ba1d0ee...`, the union remains exact 59 paths: the 15
  activation documents plus 46 implementation paths overlap in ADR-0027/work, and all 59 match the work allowlist.
  Nothing was staged, committed, pushed or merged.
- Before this append, historical EVID-001..046 body was exact 282,059 bytes/SHA-256
  `2222b024491c07aaaad6def8656d79dc532f505128f412abe3ee6c55b845d641`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-047 within this file.

Hosted acceptance remains pending until the frozen implementation and five-document sync are committed and pushed.
ADR-0027 therefore remains Proposed, GDJ-0027 remains active, Q-013 remains `Partial`, and activation run 31414060387
remains activation-only proof. No Draft PR merge was performed or authorized.

## EVID-20260811-048 — GDJ-0027 GitHub-hosted Exact 26-job Implementation-head CI

- Date/time: 2026-08-11T03:36:35+09:00–2026-08-11T03:44:08+09:00
- Work/contract IDs: GDJ-0027, REL-005, Q-013; REL-002/007..012 explicit ordered not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@7db684159ecfebbcbe1dc0673928e899ab8b0835`
  (`feat: add reverse relation accessor and lookup`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite relation-product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31419940399](https://github.com/progresshans/godj/actions/runs/31419940399), attempt 1, workflow run number 37
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: declaration-centric reverse `RelationPath`, query-only typed/dynamic binding, sealed PK object
  capability, generated project-owned reverse object/`RelatedSet`, project-only split generator and SQLite reverse
  INNER JOIN passed on the exact implementation head. Oracle-blind REL-005 freshly loads Author 1, observes
  generated `Posts()` IDs `[10,11]` with evaluation SELECT 1/JOIN 0, and observes typed/dynamic
  `posts__title=Alpha` Plan.Equal with Author IDs `[1]`, SELECT 1/INNER JOIN 1 and unchanged database state. Product
  is exact 12 adapter sets/127 contracts=`115 passing + 5 deviation + 7 oracle_locked`, relation actual
  REL-001/003/004/005/006 5/12; the remaining seven relation contracts stay ordered payload-free
  `not_implemented` and `oracle_locked`.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at the job/recorded-step level. Portable Python's
  17 exact-profile-only skips are intentional and every compatibility leg passed 193/17; exact darwin passed
  193/193 with skip 0. Windows, PostgreSQL/MySQL, reverse prefetch/eager/write/delete/DDL/migration, broader target
  types and non-SQLite product support were not run or claimed. This EVID-048 and the exact 15-file completion-
  documentation transition are later documentation-only changes after the tested implementation head, so their
  own exact-head hosted CI is `not run/pending`; run 31419940399 must not be recursively reused as that proof.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=7db684159ecfebbcbe1dc0673928e899ab8b0835`, status `completed`, conclusion `success`, started
  2026-08-10T18:36:35Z and completed 2026-08-10T18:44:08Z.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `7db68415...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `d52de329c0f71c5b6cff69393daf1fcf9b86bef2` had the exact base and head parents.
  Synthetic merge and exact head trees were both `3d4e41f6ff9270c211a125764b0b0eb09d0d4fb0`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93557965386](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965386) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93557965343](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965343) | success |
| Project check (`ubuntu-22.04`) | [93557965450](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965450) | success |
| Project check (`ubuntu-24.04-arm`) | [93557965522](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965522) | success |
| Project check (`macos-15-intel`) | [93557965445](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965445) | success |
| Project check (`macos-26`) | [93557965421](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965421) | success |
| SQLite (`ubuntu-22.04`) | [93557965543](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965543) | success |
| SQLite (`ubuntu-24.04-arm`) | [93557965576](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965576) | success |
| SQLite (`macos-15-intel`) | [93557965605](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965605) | success |
| SQLite (`macos-26`) | [93557965524](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965524) | success |
| Product project check (`ubuntu-22.04`) | [93557965330](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965330) | success |
| Product project check (`ubuntu-24.04-arm`) | [93557965363](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965363) | success |
| Product project check (`macos-15-intel`) | [93557965372](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965372) | success |
| Product project check (`macos-26`) | [93557965329](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965329) | success |
| Python compatibility (`3.12.13`) | [93557965476](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965476) | success |
| Python compatibility (`3.13.15`) | [93557965481](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965481) | success |
| Python compatibility (`3.14.3`) | [93557965542](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965542) | success |
| Python compatibility (`3.14.7`) | [93557965509](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965509) | success |
| Relation binding (`ubuntu-22.04`) | [93557965502](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965502) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93557965491](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965491) | success |
| Relation binding (`macos-15-intel`) | [93557965446](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965446) | success |
| Relation binding (`macos-26`) | [93557965600](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965600) | success |
| Relation product (`ubuntu-22.04`) | [93557965373](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965373) | success |
| Relation product (`ubuntu-24.04-arm`) | [93557965309](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965309) | success |
| Relation product (`macos-15-intel`) | [93557965458](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965458) | success |
| Relation product (`macos-26`) | [93557965358](https://github.com/progresshans/godj/actions/runs/31419940399/job/93557965358) | success |

Hosted gate details:

- Full Ubuntu job `93557965386` passed `make ci`, portable Python 193 tests/17 intentional skips, exactly twelve
  product adapter success lines, all stored checksums and generated/reference no-rewrite gates. Relation stdout was
  exact `GoDj product observations match 5 required contracts; 7 remain not implemented`.
- The full Ubuntu job executed the exact GDJ-0027 relation package set, including query/ORM and all four relation
  product fixtures, successfully with `GOARCH=386`, `CGO_ENABLED=0`. This closes EVID-047's local compile/delegation
  limitation for that exact package set; it does not claim broad Windows or non-SQLite coverage.
- Exact darwin job `93557965343` asserted macOS 15.7.7 arm64, Go 1.26.5 darwin/arm64, CPython 3.14.3,
  Django 6.1 and SQLite 3.50.4, then passed 193/193 with skip 0 plus oracle/no-rewrite gates.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg asserted exact GOOS/GOARCH and independently recorded 11,461 JSON events with exact
  569 top-level run events, 569 matching pass events, 0 skips, encoded inventory 57,738 bytes and SHA-256
  `739bb6fc4bc3a5665cbaa455bed45d4ddf9683d78c4ff74b02c1d0208862c2d7`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest changed only REL-005 `oracle_locked -> passing`: 10,812 bytes/SHA-256
  `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. Existing main/metadata/query/object,
  project query/object and all three prior relation product trees, Django runner/scenario implementation,
  schema/migrations, `go.mod` and `go.sum` remained byte-preserved.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, checked the synthetic merge/head
tree identity and scanned all four raw relation-product logs plus Python, full Ubuntu and exact Darwin evidence. It
confirmed the exact inventories, compatibility gates, artifact boundaries and reported P0/P1/P2/P3=`0/0/0/0`.
This evidence accepts only ADR-0027's named one-hop reverse exact predicate, owner related-set accessor and SQLite
reverse INNER JOIN. It does not establish reverse prefetch/eager/write/delete/DDL/migration, broader target types or
non-SQLite support, and no merge was performed.

Before this append, historical EVID-001..047 body was exact 292,825 bytes/SHA-256
`db3b01165cc9396266df4eab1b9c33f4db2e7619467ccb82f14c17609717f24e`; this patch preserves that body prefix,
changes only the top current-evidence pointer and appends EVID-048 within this file.

GDJ-0027 is completed and ADR-0027 is Accepted only for this bounded REL-005 slice; Q-013 remains `Partial` and
active/ready work remains empty. The exact 15-file completion-documentation patch itself requires a separate
non-reused exact-head CI. No Draft PR merge was performed or authorized.

## EVID-20260811-049 — GDJ-0027 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T04:08:25+09:00–2026-08-11T04:15:09+09:00
- Work/contract IDs: GDJ-0027, REL-005, Q-013; REL-002/007..012 explicit ordered not-implemented boundary
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@7998a8351c7668d53b9263bc9a381a815c6c9eb6`
  (`docs: complete reverse relation accessor slice`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite relation-product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31422614250](https://github.com/progresshans/godj/actions/runs/31422614250), attempt 1, workflow run number 38
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 15-file completion-documentation transition from implementation head `7db68415...`
  to `7998a835...` passed unchanged product gates. GDJ-0027 remains completed, ADR-0027 remains Accepted only for
  the named one-hop reverse exact predicate/owner related-set SQLite slice, Q-013 remains `Partial`, and
  active/ready work remains empty. Product classification remains exact 12 adapter sets/127 contracts=
  `115 passing + 5 deviation + 7 oracle_locked`, relation actual REL-001/003/004/005/006 5/12.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at the job/recorded-step level. Portable Python's
  17 exact-profile-only skips are intentional and every compatibility leg passed 193/17; exact darwin passed
  193/193 with skip 0. Windows, PostgreSQL/MySQL, reverse prefetch/eager/write/delete/DDL/migration, broader target
  types and non-SQLite product support were not run or claimed. This EVID-049 append and its exact seven-file
  terminal evidence/status patch are later documentation-only changes after the tested completion-documentation
  head. Run 31422614250 is not reused as proof of that later patch, and no EVID-050 is created merely to prove the
  evidence record itself.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=7998a8351c7668d53b9263bc9a381a815c6c9eb6`, status `completed`, conclusion `success`, started
  2026-08-10T19:08:25Z and completed 2026-08-10T19:15:09Z.
- Evidence re-query found PR #1 `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `7998a835...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `bd9e0f68297904dee257e56aaab412dbbf89a71e` had the exact base and head parents.
  Synthetic merge and exact head trees were both `b61423b86f6469cb0dd7512b7ec1e2e439fcadcf`, so executed
  contents were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93566737703](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737703) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93566737778](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737778) | success |
| Project check (`ubuntu-22.04`) | [93566737855](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737855) | success |
| Project check (`ubuntu-24.04-arm`) | [93566737722](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737722) | success |
| Project check (`macos-15-intel`) | [93566738004](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566738004) | success |
| Project check (`macos-26`) | [93566737729](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737729) | success |
| SQLite (`ubuntu-22.04`) | [93566737814](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737814) | success |
| SQLite (`ubuntu-24.04-arm`) | [93566737867](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737867) | success |
| SQLite (`macos-15-intel`) | [93566737925](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737925) | success |
| SQLite (`macos-26`) | [93566737869](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737869) | success |
| Product project check (`ubuntu-22.04`) | [93566737844](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737844) | success |
| Product project check (`ubuntu-24.04-arm`) | [93566737862](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737862) | success |
| Product project check (`macos-15-intel`) | [93566738051](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566738051) | success |
| Product project check (`macos-26`) | [93566737829](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737829) | success |
| Python compatibility (`3.12.13`) | [93566738056](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566738056) | success |
| Python compatibility (`3.13.15`) | [93566737874](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737874) | success |
| Python compatibility (`3.14.3`) | [93566737817](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737817) | success |
| Python compatibility (`3.14.7`) | [93566737755](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737755) | success |
| Relation binding (`ubuntu-22.04`) | [93566737896](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737896) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93566737987](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737987) | success |
| Relation binding (`macos-15-intel`) | [93566738002](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566738002) | success |
| Relation binding (`macos-26`) | [93566737959](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737959) | success |
| Relation product (`ubuntu-22.04`) | [93566737858](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737858) | success |
| Relation product (`ubuntu-24.04-arm`) | [93566737803](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737803) | success |
| Relation product (`macos-15-intel`) | [93566737958](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737958) | success |
| Relation product (`macos-26`) | [93566737835](https://github.com/progresshans/godj/actions/runs/31422614250/job/93566737835) | success |

Hosted gate details:

- Full Ubuntu job `93566737703` passed `make ci`, portable Python 193 tests/17 intentional skips, exactly twelve
  product adapter success lines, all stored checksums and generated/reference no-rewrite gates. Relation stdout was
  exact `GoDj product observations match 5 required contracts; 7 remain not implemented`.
- The full Ubuntu job executed the exact GDJ-0027 relation package set, including query/ORM and all four relation
  product fixtures, successfully with `GOARCH=386`, `CGO_ENABLED=0`. This is not broad Windows or non-SQLite
  coverage.
- Exact darwin job `93566737778` asserted macOS 15 arm64, Go darwin/arm64, CPython 3.14.3 and historical
  uv 0.10.12, then passed 193/193 with skip 0 plus oracle/no-rewrite gates.
- Each uv 0.12.3 Python compatibility leg asserted its exact CPython runtime, passed portable 193/17 and verified
  127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg asserted exact GOOS/GOARCH and independently recorded 11,461 JSON events with exact
  569 top-level run events, 569 matching pass events, 0 skips, encoded inventory 57,738 bytes and SHA-256
  `739bb6fc4bc3a5665cbaa455bed45d4ddf9683d78c4ff74b02c1d0208862c2d7`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest remained 10,812 bytes/SHA-256
  `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136`. Relation oracle remained 33,792
  bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
  bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148
  bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`. Existing generated/product fixtures,
  Django runner/scenario implementation, schema/migrations, `go.mod`, `go.sum` and workflow bytes remained
  byte-preserved by the unchanged gates.
- The tested transition from `7db68415...` to `7998a835...` changed exactly 15 Markdown documentation/work files
  and no source, workflow, generated, manifest, oracle or checksum artifact. Before this append, historical
  EVID-001..048 body was exact 303,286 bytes/SHA-256
  `941cfc6780d4a48418acb546eba17beb24dd6e987509c8470167da4d3452dc14`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-049 within this file.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, checked the synthetic merge/head
tree identity and scanned all four raw relation-product logs plus Python, full Ubuntu and exact Darwin evidence. It
confirmed the exact inventories, compatibility gates, artifact boundaries and reported P0/P1/P2/P3=`0/0/0/0`.
This evidence closes only the exact 15-file completion-documentation head. It does not widen ADR-0027 or Q-013,
prove this later seven-file terminal evidence/status record, or authorize merging Draft PR #1; no merge was
performed.

The terminal record is intentionally non-recursive. Its exact seven allowed Markdown paths, historical prefix,
current-evidence uniqueness, links/frontmatter/fences and whitespace are validated locally, while code, workflow,
generated and contract artifacts remain identical to the exact hosted-tested completion head. No further evidence
entry is created solely to establish the evidence entry itself.

## EVID-20260811-050 — GDJ-0027 Terminal Exact-head CI and GDJ-0028 Activation Baseline

- Date/time: 2026-08-11T04:25:59+09:00–2026-08-11T04:34:13+09:00
- Work/contract IDs: GDJ-0027 terminal status; GDJ-0028 activation baseline; REL-001/003/004/005/006 passing,
  REL-002/007..012 ordered `oracle_locked`; Q-013
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@e9dc361f983f1c02af1f63737a1f282998d5a533`
  (`docs: record reverse relation completion evidence`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64,
  Go 1.26.5, actual SQLite relation-product gates, CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix,
  relation-binding and relation-product four-coordinate matrices. Routine hosted portable/compatibility uses
  uv 0.12.3; historical exact darwin remains uv 0.10.12. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31424055711](https://github.com/progresshans/godj/actions/runs/31424055711), attempt 1, workflow run number 39
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed/cancelled/skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact GDJ-0027 seven-file terminal evidence/status head passed unchanged product gates.
  Product remains exact 12 adapter sets/127 contracts=`115 passing + 5 deviation + 7 oracle_locked`, relation
  actual REL-001/003/004/005/006 5/12. This is the clean tested baseline for GDJ-0028; it does not prove the later
  activation documentation diff, Proposed ADR-0028, REL-012 implementation, or the target
  `116 passing + 5 deviation + 6 oracle_locked` classification.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at the job/recorded-step level. Portable Python's
  17 exact-profile-only skips remain intentional and each compatibility leg passed 193/17; exact darwin passed
  193/193 with skip 0. Reverse prefetch, eager select-related, write/delete/DDL/migration and non-SQLite support
  were not run or claimed. The GDJ-0028 activation patch is a later diff and its exact-head CI is
  `not run/pending`; this run must not be reused recursively.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=e9dc361f983f1c02af1f63737a1f282998d5a533`, status `completed`, conclusion `success`, started
  2026-08-10T19:25:59Z and completed 2026-08-10T19:34:13Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head `e9dc361f...` and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `aec12136cf884188e7cc874c85424a20ff177842` had those exact base/head parents.
  Synthetic merge and exact head trees were both `38ae1935a1e5b30ad04548457170fcd0f1872826`, so executed contents
  were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93571450880](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450880) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93571450704](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450704) | success |
| Project check (`ubuntu-22.04`) | [93571450958](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450958) | success |
| Project check (`ubuntu-24.04-arm`) | [93571450881](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450881) | success |
| Project check (`macos-15-intel`) | [93571450916](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450916) | success |
| Project check (`macos-26`) | [93571450969](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450969) | success |
| SQLite (`ubuntu-22.04`) | [93571450972](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450972) | success |
| SQLite (`ubuntu-24.04-arm`) | [93571450964](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450964) | success |
| SQLite (`macos-15-intel`) | [93571450903](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450903) | success |
| SQLite (`macos-26`) | [93571450890](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450890) | success |
| Product project check (`ubuntu-22.04`) | [93571450949](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450949) | success |
| Product project check (`ubuntu-24.04-arm`) | [93571450931](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450931) | success |
| Product project check (`macos-15-intel`) | [93571450989](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450989) | success |
| Product project check (`macos-26`) | [93571451075](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571451075) | success |
| Python compatibility (`3.12.13`) | [93571450868](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450868) | success |
| Python compatibility (`3.13.15`) | [93571450762](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450762) | success |
| Python compatibility (`3.14.3`) | [93571450797](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450797) | success |
| Python compatibility (`3.14.7`) | [93571451117](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571451117) | success |
| Relation binding (`ubuntu-22.04`) | [93571451005](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571451005) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93571450917](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450917) | success |
| Relation binding (`macos-15-intel`) | [93571450994](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450994) | success |
| Relation binding (`macos-26`) | [93571451132](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571451132) | success |
| Relation product (`ubuntu-22.04`) | [93571450906](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450906) | success |
| Relation product (`ubuntu-24.04-arm`) | [93571450983](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450983) | success |
| Relation product (`macos-15-intel`) | [93571451086](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571451086) | success |
| Relation product (`macos-26`) | [93571450970](https://github.com/progresshans/godj/actions/runs/31424055711/job/93571450970) | success |

Hosted gate details:

- Full Ubuntu job `93571450880` passed `make ci`, portable Python 193/17, exactly twelve product adapter success
  lines, all stored checksums and generated/reference no-rewrite gates. Relation stdout remained exact
  `GoDj product observations match 5 required contracts; 7 remain not implemented`.
- Full Ubuntu executed the exact GDJ-0027 relation package set with `GOARCH=386 CGO_ENABLED=0`; this is not broad
  `./db/sqlite/...` Linux/386 support. Exact darwin job `93571450704` asserted macOS arm64, Go 1.26.5
  darwin/arm64, CPython 3.14.3, Django 6.1, SQLite 3.50.4 and uv 0.10.12, then passed 193/193 with skip 0.
- Each uv 0.12.3 Python compatibility leg passed portable 193/17 and verified 127 scenarios, encoded payload
  498,051 bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg recorded exact 569 top-level run events, 569 matching pass events, 0 skips, encoded
  inventory 57,738 bytes and SHA-256
  `739bb6fc4bc3a5665cbaa455bed45d4ddf9683d78c4ff74b02c1d0208862c2d7`, then passed race, CGO-disabled, vet,
  generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest remained 10,812 bytes/SHA-256
  `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136`; relation oracle/static/SHA256SUMS and
  all generated/product/code/workflow bytes were unchanged from the exact hosted-tested GDJ-0027 completion.
- The tested transition from `7998a835...` to `e9dc361f...` changed exactly seven Markdown documentation/work
  files and no source, workflow, generated, manifest, oracle or checksum artifact. Before this append, historical
  EVID-001..049 body was exact 313,604 bytes/SHA-256
  `1facfc0eb61a439f808238c42221857c7434205986a7ff727b6fa2498449decf`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-050 within this file.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, checked the synthetic merge/head
tree identity and scanned all four raw relation-product logs plus Python, full Ubuntu and exact Darwin evidence. It
confirmed the exact inventories, compatibility gates, artifact boundaries and reported P0/P1/P2/P3=`0/0/0/0`.
This evidence closes only the exact GDJ-0027 terminal head and establishes GDJ-0028's clean tested baseline. It does
not prove the later activation documentation/API, REL-012 implementation or target classification, widen ADR-0027
or Q-013, or authorize merging Draft PR #1. No merge was performed.

## EVID-20260811-051 — GDJ-0028 Activation Hosted CI and REL-012 Pre-hosted Local Validation

- Date/time: 2026-08-11; activation hosted 05:29:03–05:38:36 KST, frozen local implementation validation afterward
- Work/contract IDs: GDJ-0028, REL-012, Q-013; REL-002/007..011 remain ordered `oracle_locked`
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `3ae4a2cecacd31a8cc72fd46ea288568e0071421` plus the uncommitted exact 39-path GDJ-0028 implementation and this
  pre-hosted exact five-document status sync. The implementation commit identity is intentionally unknown/pending.
- Local environment/backend: Go 1.26.5 `darwin/arm64`; SQLite 3.53.3 through the existing modernc driver.
- Result summary: immutable list-backed IN AST, separate sealed `ReversePrefetch`, sorted/deduped max-999 source-FK
  batch, atomic owner-order warm `RelatedSet`, project-only generated companion and SQLite root-table IN are locally
  implemented. Oracle-blind REL-012 observes exact `[(1,[10,11]),(2,[]),(3,[12])]`, primary/batch SELECT 1 each,
  JOIN 0, `author_id` keys `[1,2,3]`, related-access extra query 0 and unchanged database state. Local product is exact
  12 adapter sets/127 contracts=`116 passing + 5 deviation + 6 oracle_locked`; relation actual is
  REL-001/003/004/005/006/012 6/12.
- Failures/skips/not run: local unexpected failures 0. The implementation and this status sync have not been staged,
  committed, pushed or run on GitHub. Actual Ubuntu Linux/386 and four-coordinate implementation inventories, exact
  Darwin/Python hosted legs and implementation clean-worktree gates remain `not run/pending`. Activation run
  `31429245980` must not be reused as implementation evidence. Windows, non-SQLite backends, general eager/custom
  Prefetch/filter/order/write/delete/DDL/migration remain unsupported/out of scope.

Activation exact-head hosted evidence, used only for activation and baseline preservation:

- Draft PR #1 `pull_request`
  [run 31429245980](https://github.com/progresshans/godj/actions/runs/31429245980), attempt 1, completed with
  `success` at exact `headSha=3ae4a2cecacd31a8cc72fd46ea288568e0071421`.
- Exact 26/26 jobs and all 326/326 recorded steps were `completed/success`; non-success jobs/steps were 0. PR #1 was
  re-queried `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact activation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `a5d67301ca62a81d062f9d966934f71b2c03a22a` had those exact base/head parents.
  Synthetic and activation-head trees were both `eb1e44043cba80597b21a905d02d87c718c0209a`.
- Each relation-product coordinate preserved exact 569 run/569 pass/0 skip, 57,738 bytes and SHA-256
  `739bb6fc4bc3a5665cbaa455bed45d4ddf9683d78c4ff74b02c1d0208862c2d7`. This proves only the activation
  `115 + 5 + 7`, relation 5/12 baseline, not the local 594-test implementation.

Exact activation job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93588313510](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313510) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93588313665](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313665) | success |
| Project check (`ubuntu-22.04`) | [93588313676](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313676) | success |
| Project check (`ubuntu-24.04-arm`) | [93588313743](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313743) | success |
| Project check (`macos-15-intel`) | [93588313643](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313643) | success |
| Project check (`macos-26`) | [93588313658](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313658) | success |
| SQLite (`ubuntu-22.04`) | [93588313653](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313653) | success |
| SQLite (`ubuntu-24.04-arm`) | [93588313661](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313661) | success |
| SQLite (`macos-15-intel`) | [93588313662](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313662) | success |
| SQLite (`macos-26`) | [93588313728](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313728) | success |
| Product project check (`ubuntu-22.04`) | [93588313600](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313600) | success |
| Product project check (`ubuntu-24.04-arm`) | [93588313618](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313618) | success |
| Product project check (`macos-15-intel`) | [93588313586](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313586) | success |
| Product project check (`macos-26`) | [93588313609](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313609) | success |
| Python compatibility (`3.12.13`) | [93588313721](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313721) | success |
| Python compatibility (`3.13.15`) | [93588313692](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313692) | success |
| Python compatibility (`3.14.3`) | [93588313902](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313902) | success |
| Python compatibility (`3.14.7`) | [93588313770](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313770) | success |
| Relation binding (`ubuntu-22.04`) | [93588313841](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313841) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93588313684](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313684) | success |
| Relation binding (`macos-15-intel`) | [93588313637](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313637) | success |
| Relation binding (`macos-26`) | [93588313612](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313612) | success |
| Relation product (`ubuntu-22.04`) | [93588313682](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313682) | success |
| Relation product (`ubuntu-24.04-arm`) | [93588313714](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313714) | success |
| Relation product (`macos-15-intel`) | [93588313675](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313675) | success |
| Relation product (`macos-26`) | [93588313617](https://github.com/progresshans/godj/actions/runs/31429245980/job/93588313617) | success |

Activation hosted gate details:

- Python 3.12.13/3.13.15/3.14.3/3.14.7 each passed portable 193 tests with 17 intentional exact-profile skips and
  verified 127 scenarios, 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`. Exact Darwin passed 193/193 with
  macOS 15.7.7 arm64, Python 3.14.3, Django 6.1, SQLite 3.50.4 and Go 1.26.5 darwin/arm64.
- Full Ubuntu used `CGO_ENABLED=0 GOARCH=386` for the exact bounded migration/project compile set and query/ORM/
  relation-product runtime set. This is not broad non-SQLite or all-package 386 support.
- The exact 15-file activation content digest was
  `336bba3941d10ef64f232815d710bfd789486e1416b6d8e9f28042112c0a6b55`; hosted audit reported
  P0/P1/P2/P3=`0/0/0/0`.

Local commands, inventories and independent audits:

1. Root `make ci`
   - Exit 0: format/generator checks, full normal Go tests, vet, race, CGO-disabled bounded packages, portable Python,
     contract checks and all twelve product adapters passed. Relation output matched 6 required contracts with 6 NI.
2. Workflow-equivalent exact relation-product JSON inventory verifier
   - PASS: 594 top-level run events, 594 matching pass events, 0 skips; 60,237 bytes; SHA-256
     `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`.
3. Independent final-byte audits after remediation
   - Runtime/query production three-file freeze SHA
     `577c8c41b684cd8b2ad170760d44d143ea571e5b771f60e632913e3f71477168`; P0..P3=0.
   - Codegen three-file latest SHA `a9abcf79d3506297157877b4b1e91c5f46132adf0ce892d2eda00187ee60d9ac`;
     P0..P3=0.
   - SQLite/conformance owned-30 content SHA
     `bf02ccad0dcf26ce680ff82b1592e400edbf9d1b0d2fac94d1ca4c94435d0365`; P0..P3=0.
   - Final exact physical-39 robust content-manifest SHA
     `3e4c69f127a54460a36600ab48bed8eae3335240323e2b76310cc985e97799a7`; P0..P3=0.

Artifact, scope and false-green evidence:

- Relation manifest changed only REL-012 `oracle_locked -> passing`: 10,806 bytes/SHA-256
  `70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477`. Reverting only REL-012 restores
  the frozen 10,812-byte/SHA-256 `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136` manifest.
- Relation oracle remained 33,792 bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`,
  static fixture 1,859 bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`,
  and 12-line SHA256SUMS 1,148 bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.
- Against activation HEAD, implementation is exact 39 physical changed/untracked paths. This sync changes exactly five
  status/work documents, yielding 44 paths relative to activation and 54 unique paths relative to baseline `e9dc361f...`;
  all remain inside the frozen allowlist. Nothing was staged, committed, pushed or merged.
- Before this append, historical EVID-001..050 body was exact 322,824 bytes/SHA-256
  `33ebc7302f59d0fd4d61d5b2d397dcba42c2b33786f9247838692f781819782b`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-051.

Hosted acceptance remains pending until the exact implementation and five-document sync are intentionally committed and
pushed. ADR-0028 therefore remains Proposed, GDJ-0028 remains active and Q-013 remains `Partial`. Activation run
`31429245980` proves only the activation head; no Draft PR merge was performed or authorized.

## EVID-20260811-052 — GDJ-0028 GitHub-hosted Exact 26-job Implementation-head CI

- Date/time: 2026-08-11T06:09:53+09:00–2026-08-11T06:17:27+09:00
- Work/contract IDs: GDJ-0028, REL-012, Q-013; REL-001/003/004/005/006/012 `passing`,
  REL-002/007/008/009/010/011 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@4858ab88b82647793cd463e9f348e43d3f5e4bb7`
  (`feat: add reverse relation prefetch`), parent `3ae4a2cecacd31a8cc72fd46ea288568e0071421`, tree
  `dfa5f46e39b192b7e7fde35fbc6c3f73f1c150f2`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  actual SQLite relation-product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin
  CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims
  are absent.
- Command: Draft PR #1 `pull_request`
  [run 31432551159](https://github.com/progresshans/godj/actions/runs/31432551159), attempt 1, workflow run number 41
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or
  skipped jobs 0 and non-success recorded steps 0
- Result summary: the immutable IN condition, separate sealed `ReversePrefetch`, sorted/deduplicated max-999
  source-FK batch, all-or-nothing owner-order warm `RelatedSet`, generated project companion and SQLite root-table IN
  implementation passed the exact hosted matrix. Product is exact 12 adapter sets/127 contracts=
  `116 passing + 5 deviation + 6 oracle_locked`; relation actual is 6/12. Each relation-product coordinate reproduced
  594 run/594 pass/0 skip, 60,237 encoded bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips
  remain intentional; exact Darwin passed 193/193 with skip 0. Custom Prefetch/filter/order, eager REL-009..011,
  write/delete/DDL/migration and non-SQLite support remain unsupported/out of scope. This later exact 15-file
  completion-documentation patch was not part of `4858ab88...`; its exact-head CI is `not run/pending`, and this
  implementation run must not be reused as proof of that later tree.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=4858ab88b82647793cd463e9f348e43d3f5e4bb7`, status `completed`, conclusion `success`, started
  2026-08-10T21:09:53Z and completed 2026-08-10T21:17:27Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact implementation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `7362332e8c8fb0d33615d9590ee14c235098ab04` had those exact base/head parents.
  Synthetic merge and exact head trees were both `dfa5f46e39b192b7e7fde35fbc6c3f73f1c150f2`, so executed contents
  were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93599162478](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162478) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93599162414](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162414) | success |
| Project check (`ubuntu-22.04`) | [93599162544](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162544) | success |
| Project check (`ubuntu-24.04-arm`) | [93599162530](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162530) | success |
| Project check (`macos-15-intel`) | [93599162583](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162583) | success |
| Project check (`macos-26`) | [93599162608](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162608) | success |
| SQLite (`ubuntu-22.04`) | [93599162468](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162468) | success |
| SQLite (`ubuntu-24.04-arm`) | [93599162637](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162637) | success |
| SQLite (`macos-15-intel`) | [93599162655](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162655) | success |
| SQLite (`macos-26`) | [93599162665](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162665) | success |
| Product project check (`ubuntu-22.04`) | [93599162580](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162580) | success |
| Product project check (`ubuntu-24.04-arm`) | [93599162549](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162549) | success |
| Product project check (`macos-15-intel`) | [93599162728](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162728) | success |
| Product project check (`macos-26`) | [93599162572](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162572) | success |
| Python compatibility (`3.12.13`) | [93599162595](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162595) | success |
| Python compatibility (`3.13.15`) | [93599162606](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162606) | success |
| Python compatibility (`3.14.3`) | [93599162683](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162683) | success |
| Python compatibility (`3.14.7`) | [93599162566](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162566) | success |
| Relation binding (`ubuntu-22.04`) | [93599162590](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162590) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93599162697](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162697) | success |
| Relation binding (`macos-15-intel`) | [93599162509](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162509) | success |
| Relation binding (`macos-26`) | [93599162560](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162560) | success |
| Relation product (`ubuntu-22.04`) | [93599162565](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162565) | success |
| Relation product (`ubuntu-24.04-arm`) | [93599162589](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162589) | success |
| Relation product (`macos-15-intel`) | [93599162516](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162516) | success |
| Relation product (`macos-26`) | [93599162489](https://github.com/progresshans/godj/actions/runs/31432551159/job/93599162489) | success |

Hosted gate details:

- Full Ubuntu job `93599162478` passed `make ci`, portable Python 193 tests with 17 intentional skips, exactly twelve
  product-adapter success lines, relation stdout exactly 6 required/6 not implemented, all stored checksums and
  generated/reference no-rewrite gates. It also executed the exact bounded relation/project package set, including
  the new prefetch product, with `GOARCH=386 CGO_ENABLED=0`; this is not broad all-package Linux/386 support.
- Exact Darwin job `93599162414` asserted macOS 15.7.7 arm64, Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1
  and SQLite 3.50.4, then passed 193/193 with skip 0.
- Each Python 3.12.13/3.13.15/3.14.3/3.14.7 leg passed portable 193 tests with 17 intentional skips and verified
  127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg reproduced exact 594 run/594 pass/0 skip, encoded inventory 60,237 bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`, then passed race, CGO-disabled,
  vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest is 10,806 bytes/SHA-256
  `70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477`, changing only REL-012 to `passing`;
  reverting only REL-012 restores 10,812 bytes/SHA-256
  `640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136`.
- Frozen relation oracle remained 33,792 bytes/SHA-256
  `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859 bytes/
  `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148 bytes/
  `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.
- The implementation transition from activation head `3ae4a2ce...` to `4858ab88...` changed exact 44 physical paths,
  all inside the frozen allowlist; the robust path/size/content digest was
  `2cc989674b5315df9487053611597a3f065060b260822071a045067792028bf8`. Baseline-to-implementation union was
  54 unique paths. No oracle/static/checksum/schema/migration/go.mod/go.sum or locked reverse-generator bytes moved.
- Before this append, historical EVID-001..051 body was exact 332,994 bytes/SHA-256
  `bfd697e94d5f4bb6197ff74383e5957597c58050ac5ff7383475a129a2574aaa`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-052.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified synthetic-merge/head tree
identity, and checked raw full-Ubuntu, exact-Darwin, Python and all four relation-product logs. It confirmed the exact
inventories, compatibility gates and frozen-artifact boundaries with P0/P1/P2/P3=`0/0/0/0`.

This evidence completes GDJ-0028 and accepts ADR-0028 only for the bounded SQLite reverse-ForeignKey prefetch product
slice. Q-013 remains `Partial`; REL-002/007..011 and the broader eager/write/non-SQLite surface remain open. There is
no active or ready work packet. Draft PR #1 was not merged. The exact 15-file completion-documentation tree created
after this implementation commit still requires its own hosted CI; run `31432551159` is not reused as that proof.

## EVID-20260811-053 — GDJ-0028 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T06:43:16+09:00–2026-08-11T06:49:35+09:00
- Work/contract IDs: GDJ-0028, REL-012, Q-013; REL-001/003/004/005/006/012 `passing`,
  REL-002/007/008/009/010/011 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@9dc4eb1312791ae74b384afbbfdbfef89aaf55bb`
  (`docs: complete reverse relation prefetch slice`)
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64;
  Go 1.26.5; actual SQLite relation-product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix;
  relation-binding and relation-product four-coordinate matrices. PostgreSQL/MySQL service jobs and Windows are absent.
- Command: Draft PR #1 `pull_request`
  [run 31435136950](https://github.com/progresshans/godj/actions/runs/31435136950), attempt 1, workflow run number 42
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or
  skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 15-file completion-documentation transition from implementation head `4858ab88...` to
  `9dc4eb13...` passed unchanged product gates. GDJ-0028 remains completed, ADR-0028 remains Accepted only for the
  bounded SQLite reverse-ForeignKey prefetch slice, Q-013 remains `Partial`, and active/ready work remains empty.
  Product remains exact 12 adapter sets/127 contracts=`116 passing + 5 deviation + 6 oracle_locked`, relation actual
  REL-001/003/004/005/006/012 6/12. Each relation-product coordinate reproduced 594 run/594 pass/0 skip,
  60,237 encoded bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`.
- Failures/skips/not run: unexpected hosted failure/cancel/skip 0 at the job/recorded-step level. Portable Python's
  17 exact-profile-only skips remain intentional and every compatibility leg passed 193/17; exact Darwin passed
  193/193 with skip 0. Windows, PostgreSQL/MySQL, general eager/custom Prefetch/filter/order, write/delete/DDL/
  migration and non-SQLite product support were not run or claimed. This EVID-053 append and its exact seven-file
  terminal evidence/status patch are later documentation-only changes after the tested completion-documentation head.
  Run `31435136950` is not reused as proof of that later patch, and no EVID-054 is created merely to prove this record.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=9dc4eb1312791ae74b384afbbfdbfef89aaf55bb`, status `completed`, conclusion `success`, started
  2026-08-10T21:43:16Z and completed 2026-08-10T21:49:35Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact completion-documentation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `65e14b803403e60e9e738f941c4bc6a5714109e3` had those exact base/head parents.
  Synthetic merge and exact head trees were both `928c7c71d2c27a73aaee8aee5dc41f9ab6afc729`, so executed contents
  were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93607470656](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470656) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93607470661](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470661) | success |
| Project check (`ubuntu-22.04`) | [93607470805](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470805) | success |
| Project check (`ubuntu-24.04-arm`) | [93607470804](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470804) | success |
| Project check (`macos-15-intel`) | [93607470822](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470822) | success |
| Project check (`macos-26`) | [93607470749](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470749) | success |
| SQLite (`ubuntu-22.04`) | [93607470784](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470784) | success |
| SQLite (`ubuntu-24.04-arm`) | [93607470786](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470786) | success |
| SQLite (`macos-15-intel`) | [93607470807](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470807) | success |
| SQLite (`macos-26`) | [93607470813](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470813) | success |
| Product project check (`ubuntu-22.04`) | [93607470790](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470790) | success |
| Product project check (`ubuntu-24.04-arm`) | [93607470770](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470770) | success |
| Product project check (`macos-15-intel`) | [93607470741](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470741) | success |
| Product project check (`macos-26`) | [93607470799](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470799) | success |
| Python compatibility (`3.12.13`) | [93607470748](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470748) | success |
| Python compatibility (`3.13.15`) | [93607470762](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470762) | success |
| Python compatibility (`3.14.3`) | [93607470768](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470768) | success |
| Python compatibility (`3.14.7`) | [93607470758](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470758) | success |
| Relation binding (`ubuntu-22.04`) | [93607470697](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470697) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93607470757](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470757) | success |
| Relation binding (`macos-15-intel`) | [93607470737](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470737) | success |
| Relation binding (`macos-26`) | [93607470745](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470745) | success |
| Relation product (`ubuntu-22.04`) | [93607470694](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470694) | success |
| Relation product (`ubuntu-24.04-arm`) | [93607470800](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470800) | success |
| Relation product (`macos-15-intel`) | [93607470730](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470730) | success |
| Relation product (`macos-26`) | [93607470751](https://github.com/progresshans/godj/actions/runs/31435136950/job/93607470751) | success |

Hosted gate details:

- Full Ubuntu job `93607470656` passed `make ci`, portable Python 193 tests with 17 intentional skips, exactly twelve
  product-adapter success lines, relation stdout exactly 6 required/6 not implemented, all stored checksums and
  generated/reference no-rewrite gates. It also executed the exact bounded relation/project package set, including
  the prefetch product, with `GOARCH=386 CGO_ENABLED=0`; this is not broad all-package Linux/386 support.
- Exact Darwin job `93607470661` asserted macOS 15.7.7 arm64, Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1
  and SQLite 3.50.4, then passed 193/193 with skip 0.
- Each Python 3.12.13/3.13.15/3.14.3/3.14.7 leg passed portable 193 tests with 17 intentional skips and verified
  127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg reproduced exact 594 run/594 pass/0 skip, encoded inventory 60,237 bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`, then passed race, CGO-disabled,
  vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest remained 10,806 bytes/SHA-256
  `70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477`. Frozen relation oracle remained
  33,792 bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859 bytes/
  `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148 bytes/
  `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.
- The tested transition from `4858ab88...` to `9dc4eb13...` changed exactly 15 Markdown documentation/work files and
  no source, workflow, generated, manifest, oracle or checksum artifact. Its robust path/size/content digest was
  `e746e1e0e1565ad5366e3ec31b25dc99877475da32daf57423e40edfafea9513`.
- Before this append, historical EVID-001..052 body was exact 343,024 bytes/SHA-256
  `68ba5f8d45b969105bd8954075428ce44af930df64735ecdec6deaf1824e391e`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-053.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified synthetic-merge/head tree
identity, and checked raw full-Ubuntu, exact-Darwin, Python and all four relation-product logs. It confirmed the exact
inventories, compatibility gates and frozen-artifact boundaries with P0/P1/P2/P3=`0/0/0/0`.

This evidence closes only the exact 15-file GDJ-0028 completion-documentation head. It does not widen ADR-0028 or
Q-013, prove this later seven-file terminal evidence/status record, or authorize merging Draft PR #1; no merge was
performed. The terminal record is intentionally non-recursive: its exact seven allowed Markdown paths, historical
prefix, current-evidence uniqueness, links/frontmatter/fences and whitespace are validated locally while code,
workflow, generated and contract artifacts remain identical to the hosted-tested completion head. No further
evidence entry is created solely to establish this evidence record itself.

## EVID-20260811-054 — GDJ-0028 Terminal Exact-head CI and GDJ-0029 Activation Baseline

- Date/time: 2026-08-11T07:06:33+09:00–2026-08-11T07:13:25+09:00
- Work/contract IDs: GDJ-0028 terminal status; GDJ-0029 activation baseline; REL-009/010/011 remain
  `oracle_locked`; Q-013 remains `Partial`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@5c0efef12560203d720e4c2dd7bda50c0324a228`
  (`docs: record reverse prefetch completion evidence`), parent
  `9dc4eb1312791ae74b384afbbfdbfef89aaf55bb`, tree `3a14122965653bdd0cce88b8852e5b5aab48176c`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64;
  current bounded SQLite relation product; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix.
  Windows, PostgreSQL/MySQL and general non-SQLite support are absent.
- Command: Draft PR #1 `pull_request`
  [run 31436881856](https://github.com/progresshans/godj/actions/runs/31436881856), attempt 1, workflow run number 43
- Exit status: `success`; exact 26/26 jobs and 326/326 recorded steps completed successfully; failed, cancelled,
  skipped jobs and non-success recorded steps 0
- Result summary: unchanged current product exact 12 checked-in manifests/127 contracts=
  `116 passing + 5 deviation + 6 oracle_locked`, relation actual REL-001/003/004/005/006/012 6/12. Each of four
  relation-product coordinates reproduced 594 run/594 pass/0 skip, 60,237 encoded bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only
  skips remain intentional. REL-009/010/011 implementation, proposed projection API, target `119 + 5 + 3`,
  multiple/nested/reverse eager, write/delete/DDL/migration and non-SQLite support were not run or proved.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=5c0efef12560203d720e4c2dd7bda50c0324a228`, status `completed`, conclusion `success`, started
  2026-08-10T22:06:33Z and completed 2026-08-10T22:13:25Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `813afb2e58aef3a801b76afad8d4683e16e9f51a` had exact base/head parents.
  Synthetic merge and exact head trees were both `3a14122965653bdd0cce88b8852e5b5aab48176c`, so executed contents
  were exact-head-equivalent.

Exact job identities and UTC intervals:

| Required execution | Job ID | UTC interval | Result |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93612852772](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852772) | 22:06:36–22:11:13 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93612852458](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852458) | 22:06:37–22:07:53 | success |
| Project check (`ubuntu-22.04`) | [93612852503](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852503) | 22:06:36–22:07:29 | success |
| Project check (`ubuntu-24.04-arm`) | [93612852532](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852532) | 22:06:36–22:07:19 | success |
| Project check (`macos-15-intel`) | [93612852501](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852501) | 22:06:36–22:08:38 | success |
| Project check (`macos-26`) | [93612852486](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852486) | 22:06:36–22:07:42 | success |
| SQLite (`ubuntu-22.04`) | [93612852610](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852610) | 22:06:35–22:08:09 | success |
| SQLite (`ubuntu-24.04-arm`) | [93612852626](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852626) | 22:06:38–22:07:49 | success |
| SQLite (`macos-15-intel`) | [93612852698](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852698) | 22:10:56–22:12:53 | success |
| SQLite (`macos-26`) | [93612852668](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852668) | 22:09:46–22:11:00 | success |
| Product project check (`ubuntu-22.04`) | [93612852633](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852633) | 22:06:36–22:09:58 | success |
| Product project check (`ubuntu-24.04-arm`) | [93612852504](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852504) | 22:06:36–22:08:52 | success |
| Product project check (`macos-15-intel`) | [93612852549](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852549) | 22:08:40–22:13:24 | success |
| Product project check (`macos-26`) | [93612852637](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852637) | 22:07:55–22:10:54 | success |
| Python compatibility (`3.12.13`) | [93612852484](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852484) | 22:06:36–22:06:57 | success |
| Python compatibility (`3.13.15`) | [93612852512](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852512) | 22:06:36–22:07:04 | success |
| Python compatibility (`3.14.3`) | [93612852556](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852556) | 22:06:36–22:07:09 | success |
| Python compatibility (`3.14.7`) | [93612852533](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852533) | 22:06:36–22:07:03 | success |
| Relation binding (`ubuntu-22.04`) | [93612852539](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852539) | 22:06:42–22:08:13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93612852609](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852609) | 22:06:36–22:07:50 | success |
| Relation binding (`macos-15-intel`) | [93612852580](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852580) | 22:07:55–22:11:43 | success |
| Relation binding (`macos-26`) | [93612852664](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852664) | 22:06:36–22:07:52 | success |
| Relation product (`ubuntu-22.04`) | [93612852616](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852616) | 22:06:37–22:09:46 | success |
| Relation product (`ubuntu-24.04-arm`) | [93612852508](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852508) | 22:06:38–22:08:41 | success |
| Relation product (`macos-15-intel`) | [93612852564](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852564) | 22:07:45–22:12:59 | success |
| Relation product (`macos-26`) | [93612852472](https://github.com/progresshans/godj/actions/runs/31436881856/job/93612852472) | 22:06:36–22:09:44 | success |

Hosted gate details:

- The checked-in conformance job passed the existing full gate, exact bounded Ubuntu Linux/386 package/runtime set,
  generated/reference no-rewrite and clean-worktree assertions. Relation stdout remained exact 6 required/6 not
  implemented; this is not broad all-package Linux/386 support.
- Each Python 3.12.13/3.13.15/3.14.3/3.14.7 leg passed 193 portable tests with 17 intentional skips and reproduced
  127 scenarios, 498,051 encoded bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Each relation-product leg reproduced exact 594 run/594 pass/0 skip, 60,237 encoded bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e` and passed existing normal/race/
  CGO-disabled/vet/no-rewrite/clean gates.
- Relation manifest remained 10,806 bytes/SHA-256
  `70fefee1b2e4bb72b7a84ff07e4d9737ee59d3056ca52641668a5915b29da477`; frozen oracle remained
  33,792 bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859 bytes/
  `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line SHA256SUMS 1,148 bytes/
  `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.
- Before this append, historical EVID-001..053 body was exact 353,111 bytes/SHA-256
  `1e7e9259636d2a0c6d74d6c6561ea7fa0290f8d7843e6177e91424a90e66c018`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-054.

Independent hosted audit re-queried run/jobs/steps/PR/ancestry/tree and representative raw logs, and reported
P0/P1/P2/P3=`0/0/0/0`. This EVID proves only the exact GDJ-0028 terminal head and clean GDJ-0029 baseline. The later
exact 16-file activation documentation, Proposed API and implementation require separate exact-head evidence; run
`31436881856` must not be reused for them. Draft PR #1 remains open/draft and was not merged.

## EVID-20260811-055 — GDJ-0029 Activation Hosted CI and REL-009/010/011 Pre-hosted Local Validation

- Date/time: 2026-08-11; activation hosted 15:28:16–15:38:40 KST, frozen local implementation validation afterward
- Work/contract IDs: GDJ-0029, REL-009, REL-010, REL-011, Q-013, Q-017; REL-002/007/008 remain ordered
  `oracle_locked`
- Tested checkout: branch `codex/revision-fenced-migration-lifecycle`; activation commit
  `0a1da373a443527e48a154ca6ccc7284e5e80dc0` plus the uncommitted exact 49-entry GDJ-0029 implementation and this
  pre-hosted exact five-document status sync. The implementation commit identity is intentionally unknown/pending.
- Local environment/backend: Go 1.26.5 `darwin/arm64`; SQLite 3.53.3 through the existing modernc driver.
- Result summary: additive projection scan companions, singular immutable `RelationProjection`, object-factory-attached
  All-only eager bridge, required `INNER JOIN`, nullable `LEFT OUTER JOIN`, success-only ready relation publication and
  same-resolver reverse-path pre-I/O rejection are locally implemented. Oracle-blind REL-009/010/011 reproduce their
  exact result/query/JOIN/error/unchanged-DB contracts. Local product is exact 12 adapter sets/127 contracts=
  `119 passing + 5 deviation + 3 oracle_locked`; relation actual is REL-001/003/004/005/006/009/010/011/012 9/12.
- Failures/skips/not run: local unexpected final failures 0. During independent pre-commit review, a P1 forged
  source-key/projection provenance gap was found, reproduced, fixed and cleanly re-audited before any commit. The final
  implementation and this status sync have not been staged, committed, pushed or run on GitHub. Actual Ubuntu
  Linux/386, all four hosted relation-product coordinates, exact Darwin/Python hosted legs and implementation
  clean-worktree gates remain `not run/pending`. Activation run `31465198903` must not be reused as implementation
  evidence. Windows, non-SQLite backends, multiple/nested/reverse eager, relation-aware facade/chaining,
  write/delete/DDL/migration remain unsupported/out of scope.

Activation exact-head hosted evidence, used only for activation and baseline preservation:

- Draft PR #1 `pull_request`
  [run 31465198903](https://github.com/progresshans/godj/actions/runs/31465198903), workflow run 44, attempt 1,
  completed with `success` at exact `headSha=0a1da373a443527e48a154ca6ccc7284e5e80dc0`.
- Run metadata was re-queried as event `pull_request`, status `completed`, conclusion `success`, created/started
  `2026-08-11T06:28:16Z` and updated/completed `2026-08-11T06:38:40Z`. Exact 26/26 jobs and all 326/326 recorded
  steps were `completed/success`; non-success jobs/steps were 0.
- PR #1 was re-queried `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact activation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` on `main`.
- Actions synthetic merge `c62d885be3fbc2b828f6b80e29cd3f5c347734b6` had those exact base/head parents.
  Synthetic and activation-head trees were both `6569d4e6294a4ca00320df04bfd9f293e4ca8a7c`.
- Raw checkout logs for exact-Darwin job `93696571288`, checked-in conformance job `93696571343` and Ubuntu
  relation-product job `93696571456` each fetched and checked out synthetic merge `c62d885...`, then reported
  `HEAD is now at c62d885 Merge 0a1da373... into f8a5e20c...`. This independently confirms the tree-equivalent
  pull-request checkout rather than assuming `headSha` was directly checked out.
- Each relation-product coordinate preserved exact 594 run/594 pass/0 skip, 60,237 bytes and SHA-256
  `98a0a37b2c59dc3972208eb85d7b6d517aff39077f301d67d6d4c8fe7cb8c47e`. This proves only the activation
  `116 + 5 + 6`, relation 6/12 baseline, not the local 630-test implementation.

Exact activation job identities and UTC intervals:

| Required execution | Job ID | UTC interval | Result |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93696571343](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571343) | 06:28:26–06:34:16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93696571288](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571288) | 06:28:20–06:29:35 | success |
| Project check (`ubuntu-22.04`) | [93696571341](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571341) | 06:28:20–06:29:21 | success |
| Project check (`ubuntu-24.04-arm`) | [93696571329](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571329) | 06:28:19–06:29:06 | success |
| Project check (`macos-15-intel`) | [93696571358](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571358) | 06:29:31–06:30:51 | success |
| Project check (`macos-26`) | [93696571509](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571509) | 06:28:21–06:29:29 | success |
| SQLite (`ubuntu-22.04`) | [93696571454](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571454) | 06:28:26–06:30:07 | success |
| SQLite (`ubuntu-24.04-arm`) | [93696571464](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571464) | 06:28:19–06:29:35 | success |
| SQLite (`macos-15-intel`) | [93696571527](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571527) | 06:31:30–06:33:44 | success |
| SQLite (`macos-26`) | [93696571518](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571518) | 06:30:53–06:32:12 | success |
| Product project check (`ubuntu-22.04`) | [93696571418](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571418) | 06:28:21–06:31:39 | success |
| Product project check (`ubuntu-24.04-arm`) | [93696571396](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571396) | 06:28:19–06:30:39 | success |
| Product project check (`macos-15-intel`) | [93696571428](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571428) | 06:30:52–06:38:39 | success |
| Product project check (`macos-26`) | [93696571452](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571452) | 06:28:20–06:31:28 | success |
| Python compatibility (`3.12.13`) | [93696571369](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571369) | 06:28:20–06:28:39 | success |
| Python compatibility (`3.13.15`) | [93696571455](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571455) | 06:28:19–06:28:42 | success |
| Python compatibility (`3.14.3`) | [93696571346](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571346) | 06:28:20–06:28:50 | success |
| Python compatibility (`3.14.7`) | [93696571536](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571536) | 06:28:19–06:28:45 | success |
| Relation binding (`ubuntu-22.04`) | [93696571372](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571372) | 06:28:26–06:30:01 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93696571395](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571395) | 06:28:19–06:29:34 | success |
| Relation binding (`macos-15-intel`) | [93696571387](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571387) | 06:29:33–06:32:47 | success |
| Relation binding (`macos-26`) | [93696571336](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571336) | 06:28:19–06:29:31 | success |
| Relation product (`ubuntu-22.04`) | [93696571456](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571456) | 06:28:20–06:31:32 | success |
| Relation product (`ubuntu-24.04-arm`) | [93696571378](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571378) | 06:28:19–06:30:32 | success |
| Relation product (`macos-15-intel`) | [93696571413](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571413) | 06:29:37–06:33:30 | success |
| Relation product (`macos-26`) | [93696571356](https://github.com/progresshans/godj/actions/runs/31465198903/job/93696571356) | 06:28:20–06:30:49 | success |

Activation hosted gate details:

- The checked-in conformance job passed the existing full gate, exact bounded Ubuntu Linux/386 package/runtime set,
  generated/reference no-rewrite and clean-worktree assertions. Relation stdout remained exact 6 required/6 not
  implemented; this is not broad all-package Linux/386 support.
- Each Python 3.12.13/3.13.15/3.14.3/3.14.7 leg passed 193 portable tests with 17 intentional skips and reproduced
  127 scenarios, 498,051 encoded bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- The exact activation content was commit `0a1da373a443527e48a154ca6ccc7284e5e80dc0`, parent
  `5c0efef12560203d720e4c2dd7bda50c0324a228`, tree `6569d4e6294a4ca00320df04bfd9f293e4ca8a7c`, subject
  `docs: activate one-hop select related slice`; hosted audit reported P0/P1/P2/P3=`0/0/0/0`.

Local commands, inventories and independent audits:

1. Root `make ci`
   - Exit 0: format/generator checks, full normal Go tests, vet, race, CGO-disabled bounded packages, portable Python,
     contract checks and all twelve product adapters passed. Relation output matched 9 required contracts with 3 NI.
2. Workflow-equivalent exact relation-product JSON inventory verifier
   - PASS: 630 top-level run events, 630 matching pass events, 0 skips; 63,928 bytes; SHA-256
     `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`.
3. Focused compiler/integration/conformance remediation gates
   - Normal, race, CGO-disabled and vet package gates, exact `godjcheck`, Django 11 relation scenarios, generated
     no-rewrite and compile fixtures passed after the provenance fix.
4. Independent final-byte audits
   - Query/runtime audit P0/P1/P2/P3=`0/0/0/0`.
   - Codegen audit P0/P1/P2/P3=`0/0/0/0`.
   - Initial SQLite/conformance audit found the pre-commit forged source-key/projection P1. After the minimal
     provenance-consistency remediation, the integration audit and separate remediation audit both reported
     P0/P1/P2/P3=`0/0/0/0`.

P1 reproduction and remediation evidence:

- The initial compiler validated a terminal relation source-key path and projection independently but did not require
  their provenance metadata to agree when both named the same physical FK edge. A forged plan could therefore retain
  the source field/column while changing source identity, target identity, target table or target primary-key metadata.
- The minimal compiler fix retains validated source-key hops. When projection and source-key share the same
  `Field()+SourceColumn()` edge, full `RelationHop.Equal` is required before I/O. The same exact hop remains valid;
  an unrelated root-local source-key such as `Author` projection plus `Reviewer isnull` remains valid.
- Exact tests mutate source identity, target identity, target table and target primary key separately and require
  structured `query_error/invalid_plan`. Integration proves forged target identity returns nil rows with query-count
  delta 0. Final key file SHA-256 values are compiler
  `317a65bff7e1cfbd80a23028a5d3c836269e9ecd49aa38145c87e297c5eae52a`, compiler tests
  `f24cfd62f37af69d57f11c7cfc7a9ce22764772c976a01a88e65178957824be9`, integration tests
  `39b990c5e854a0c4cc76457526ed7bbe8bd50bbcdc46500ade36130c6b652913`.

Artifact, scope and false-green evidence:

- Relation manifest changed only REL-009/010/011 `oracle_locked -> passing`: 10,788 bytes/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`. REL-002/007/008 and all other statuses
  and payloads remain frozen.
- Exact checked-in twelve-file generated relation-select product union digest is SHA-256
  `3f40133f93d2ac2014276c2e07396a1db74acdb2ebc4b8ff44e29ac1208df535`.
- The final implementation robust dirty-content lock is exact 49 entries, 683,428 content bytes and v1 SHA-256
  `c53bfd4639a187076b8d4eb6b4e23304990412343eea4dad6a2de7b373e921c`, computed over sorted
  status/path/mode/size/content records. This sync edits exactly five allowed status/work documents, yielding 54 paths
  relative to activation HEAD. Nothing was staged, committed, pushed or merged.
- Before this append, historical EVID-001..054 body was exact 361,941 bytes/SHA-256
  `331c245ee05c0a1ebb883d2ad5fc597289e56086524160654797c14f001be37b`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-055.

Hosted implementation acceptance remains pending until the exact implementation and five-document sync are
intentionally committed and pushed. The current local target is `119 passing + 5 deviation + 3 oracle_locked`, relation
9/12, but the hosted-accepted product remains `116 + 5 + 6`, relation 6/12. ADR-0029 therefore remains Proposed,
GDJ-0029 remains active, Q-013 remains `Partial` and Q-017 remains open. Activation run `31465198903` proves only the
activation head; no Draft PR merge was performed or authorized.

## EVID-20260811-056 — GDJ-0029 GitHub-hosted Exact 26-job Implementation-head CI

- Date/time: 2026-08-11T07:45:44Z–2026-08-11T07:54:31Z
- Work/contract IDs: GDJ-0029, REL-009, REL-010, REL-011, Q-013, Q-017; REL-001/003/004/005/006/009/010/011/012
  `passing`, REL-002/007/008 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@c02aab672db5175d7a0886688efb5cc684c67744`
  (`feat: add one-hop select related`), parent `0a1da373a443527e48a154ca6ccc7284e5e80dc0`, tree
  `d8afce9e2dcf0bfb368c895ae000c272b710a88b`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  actual SQLite relation-product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin
  CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims
  are absent.
- Command: Draft PR #1 `pull_request`
  [run 31470292759](https://github.com/progresshans/godj/actions/runs/31470292759), attempt 1, workflow run number 45
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or
  skipped jobs 0 and non-success recorded steps 0
- Result summary: additive app projection scans, singular immutable `RelationProjection`, existing object-factory-
  attached All-only eager bridge, required `INNER JOIN`, nullable `LEFT OUTER JOIN`, success-only ready relation
  publication and same-resolver reverse-path pre-I/O rejection passed the exact hosted matrix. Product is exact
  12 adapter sets/127 contracts=`119 passing + 5 deviation + 3 oracle_locked`; relation actual is 9/12. Each
  relation-product coordinate reproduced 630 run/630 pass/0 skip, 63,928 encoded bytes and SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips
  remain intentional; exact Darwin passed 193/193 with skip 0. Multiple/nested/reverse eager, canonical relation-aware
  facade/chaining, FK mutation/cache policy, write/delete/DDL/migration and non-SQLite support remain unsupported/out of
  scope. This later exact 15-file completion-documentation patch was not part of `c02aab67...`; its exact-head CI is
  `not run/pending`, and this implementation run must not be reused as proof of that later tree.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=c02aab672db5175d7a0886688efb5cc684c67744`, status `completed`, conclusion `success`, started
  2026-08-11T07:45:44Z and completed 2026-08-11T07:54:31Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact implementation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `266ce059e82ac517438decbab173273fa7f12b65` had those exact base/head parents.
  Synthetic merge and exact head trees were both `d8afce9e2dcf0bfb368c895ae000c272b710a88b`, so executed contents
  were exact-head-equivalent. All 26 checkout logs identified the same synthetic merge and exact head/base parents.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93711912843](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912843) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93711912867](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912867) | success |
| Project check (`ubuntu-22.04`) | [93711912976](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912976) | success |
| Project check (`ubuntu-24.04-arm`) | [93711912973](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912973) | success |
| Project check (`macos-15-intel`) | [93711912953](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912953) | success |
| Project check (`macos-26`) | [93711913017](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711913017) | success |
| SQLite (`ubuntu-22.04`) | [93711912985](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912985) | success |
| SQLite (`ubuntu-24.04-arm`) | [93711913011](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711913011) | success |
| SQLite (`macos-15-intel`) | [93711912959](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912959) | success |
| SQLite (`macos-26`) | [93711912980](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912980) | success |
| Product project check (`ubuntu-22.04`) | [93711912886](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912886) | success |
| Product project check (`ubuntu-24.04-arm`) | [93711912945](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912945) | success |
| Product project check (`macos-15-intel`) | [93711912952](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912952) | success |
| Product project check (`macos-26`) | [93711912879](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912879) | success |
| Python compatibility (`3.12.13`) | [93711912901](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912901) | success |
| Python compatibility (`3.13.15`) | [93711912877](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912877) | success |
| Python compatibility (`3.14.3`) | [93711912935](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912935) | success |
| Python compatibility (`3.14.7`) | [93711912926](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912926) | success |
| Relation binding (`ubuntu-22.04`) | [93711912982](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912982) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93711912999](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912999) | success |
| Relation binding (`macos-15-intel`) | [93711913005](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711913005) | success |
| Relation binding (`macos-26`) | [93711913003](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711913003) | success |
| Relation product (`ubuntu-22.04`) | [93711912932](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912932) | success |
| Relation product (`ubuntu-24.04-arm`) | [93711913006](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711913006) | success |
| Relation product (`macos-15-intel`) | [93711912941](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912941) | success |
| Relation product (`macos-26`) | [93711912911](https://github.com/progresshans/godj/actions/runs/31470292759/job/93711912911) | success |

Hosted gate details:

- Full Ubuntu job `93711912843` passed `make ci`, `godjcheck` with exact 9 required/3 not implemented relation
  contracts, portable Python and all stored checksum/generated/reference no-rewrite and clean-worktree gates. It also
  executed the exact bounded relation/project package set, including `relationselectproduct`, with
  `GOARCH=386 CGO_ENABLED=0`; this is not broad all-package Linux/386 support.
- Exact Darwin job `93711912867` asserted Go 1.26.5 darwin/arm64 and passed 193/193 with skip 0.
- Python jobs `93711912901`/`93711912877`/`93711912935`/`93711912926` each passed portable 193 tests with 17
  intentional skips and verified 127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93711912932`/`93711913006`/`93711912941`/`93711912911` each independently reproduced
  exact 630 run/630 pass/0 skip, encoded inventory 63,928 bytes and SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest is 10,788 bytes/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`, changing only REL-009/010/011 to
  `passing`; REL-002/007/008 remain ordered `oracle_locked`. The exact twelve-file checked-in generated union is
  SHA-256 `3f40133f93d2ac2014276c2e07396a1db74acdb2ebc4b8ff44e29ac1208df535`.
- Frozen relation oracle, static fixture, SHA256SUMS, schemas, migrations, non-SQLite sources and existing generated
  artifacts remained unchanged. The pre-commit P1 same-edge source-key/projection provenance gap and its minimal
  pre-I/O full-hop-equality remediation remain documented in EVID-055 and were covered by the exact hosted gates.
- Before this append, historical EVID-001..055 body was exact 374,983 bytes/SHA-256
  `dc4cff29a0f1303f7ee242e12620ae897dc97db07079e8008d05442c46ec669b`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-056.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified synthetic-merge/head tree
identity, and checked all 26 checkout logs plus raw full-Ubuntu, exact-Darwin, Python and all four relation-product
logs. It confirmed exact inventories, compatibility gates and frozen-artifact boundaries with
P0/P1/P2/P3=`0/0/0/0`.

This evidence completes GDJ-0029 and accepts ADR-0029 only for the bounded one-hop forward AutoField-ForeignKey
SQLite REL-009/010/011 engine slice. Q-013 remains `Partial`; Q-017 remains P1/open; REL-002/007/008 and the broader
facade/chaining/eager/write/non-SQLite surface remain open. There is no active or ready work packet. Draft PR #1 was not
merged. The exact 15-file completion-documentation tree created after this implementation commit still requires its own
hosted CI; run `31470292759` is not reused as that proof.

## EVID-20260811-057 — GDJ-0029 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T10:26:55Z–2026-08-11T10:34:21Z
- Work/contract IDs: GDJ-0029, REL-009, REL-010, REL-011, Q-013, Q-017; REL-001/003/004/005/006/009/010/011/012
  `passing`, REL-002/007/008 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@fb9985e20c92f71eaca7bac81bc61466369e0ebd`
  (`docs: record one-hop select related completion`), parent `c02aab672db5175d7a0886688efb5cc684c67744`, tree
  `48e4cdb7c82ae6300c83f15f3646b0881e5fa002`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  actual SQLite relation-product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin
  CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims
  are absent.
- Command: Draft PR #1 `pull_request`
  [run 31482242288](https://github.com/progresshans/godj/actions/runs/31482242288), attempt 1, workflow run number 46
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or
  skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 15-file completion-documentation transition from implementation head `c02aab67...` to
  `fb9985e2...` passed unchanged product gates. GDJ-0029 remains completed and ADR-0029 remains Accepted only for the
  bounded one-hop forward AutoField-ForeignKey SQLite REL-009/010/011 engine slice. Q-013 remains `Partial`, Q-017
  remains P1/open and active/ready work remains empty. Product remains exact 12 adapter sets/127 contracts=
  `119 passing + 5 deviation + 3 oracle_locked`, relation actual 9/12. Each relation-product coordinate reproduced
  630 run/630 pass/0 skip, 63,928 encoded bytes and SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips
  remain intentional; exact Darwin passed 193/193 with skip 0. Multiple/nested/reverse eager, canonical relation-aware
  facade/chaining, FK mutation/cache policy, write/delete/DDL/migration and non-SQLite support remain unsupported/out of
  scope. This EVID-057 append and its exact seven-file terminal evidence/status patch are later documentation-only
  changes after the tested completion-documentation head. Their exact-head hosted CI is `not run/pending`; run
  `31482242288` is not reused as proof of that later tree.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=fb9985e20c92f71eaca7bac81bc61466369e0ebd`, status `completed`, conclusion `success`, started
  2026-08-11T10:26:55Z and completed 2026-08-11T10:34:21Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`, exact completion-documentation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `c5a801b40f24a0ae6b9a8ac22bb02ca027f6f6c9` had those exact base/head parents.
  Synthetic merge and exact head trees were both `48e4cdb7c82ae6300c83f15f3646b0881e5fa002`, so executed contents
  were exact-head-equivalent. All 26 checkout logs identified the same synthetic merge and exact head/base parents.

Exact job identities:

| Required execution | Job ID | Result |
|---|---:|---|
| Validate checked-in conformance artifacts | [93749624697](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624697) | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93749624710](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624710) | success |
| Project check (`ubuntu-22.04`) | [93749624742](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624742) | success |
| Project check (`ubuntu-24.04-arm`) | [93749624711](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624711) | success |
| Project check (`macos-15-intel`) | [93749624744](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624744) | success |
| Project check (`macos-26`) | [93749624800](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624800) | success |
| SQLite (`ubuntu-22.04`) | [93749624915](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624915) | success |
| SQLite (`ubuntu-24.04-arm`) | [93749624934](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624934) | success |
| SQLite (`macos-15-intel`) | [93749624840](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624840) | success |
| SQLite (`macos-26`) | [93749624783](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624783) | success |
| Product project check (`ubuntu-22.04`) | [93749624730](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624730) | success |
| Product project check (`ubuntu-24.04-arm`) | [93749624813](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624813) | success |
| Product project check (`macos-15-intel`) | [93749624782](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624782) | success |
| Product project check (`macos-26`) | [93749624785](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624785) | success |
| Python compatibility (`3.12.13`) | [93749624776](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624776) | success |
| Python compatibility (`3.13.15`) | [93749624852](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624852) | success |
| Python compatibility (`3.14.3`) | [93749624845](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624845) | success |
| Python compatibility (`3.14.7`) | [93749624837](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624837) | success |
| Relation binding (`ubuntu-22.04`) | [93749624763](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624763) | success |
| Relation binding (`ubuntu-24.04-arm`) | [93749624856](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624856) | success |
| Relation binding (`macos-15-intel`) | [93749624766](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624766) | success |
| Relation binding (`macos-26`) | [93749624765](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624765) | success |
| Relation product (`ubuntu-22.04`) | [93749624754](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624754) | success |
| Relation product (`ubuntu-24.04-arm`) | [93749624726](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624726) | success |
| Relation product (`macos-15-intel`) | [93749624760](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624760) | success |
| Relation product (`macos-26`) | [93749624678](https://github.com/progresshans/godj/actions/runs/31482242288/job/93749624678) | success |

Hosted gate details:

- Full Ubuntu job `93749624697` passed `make ci`, `godjcheck` with exact 9 required/3 not implemented relation
  contracts, portable Python and all stored checksum/generated/reference no-rewrite and clean-worktree gates. It also
  executed the exact bounded relation/project package set, including `relationselectproduct`, with
  `GOARCH=386 CGO_ENABLED=0`; this is not broad all-package Linux/386 support.
- Exact Darwin job `93749624710` asserted Go 1.26.5 darwin/arm64 and passed 193/193 with skip 0.
- Python jobs `93749624776`/`93749624852`/`93749624845`/`93749624837` each passed portable 193 tests with 17
  intentional skips and verified 127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93749624754`/`93749624726`/`93749624760`/`93749624678` each independently reproduced
  exact 630 run/630 pass/0 skip, encoded inventory 63,928 bytes and SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates.
- Relation manifest remained 10,788 bytes/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`; REL-001/003/004/005/006/009/010/011/012
  remain `passing` and REL-002/007/008 remain ordered `oracle_locked`. The exact twelve-file checked-in generated union
  remained SHA-256 `3f40133f93d2ac2014276c2e07396a1db74acdb2ebc4b8ff44e29ac1208df535`.
- The tested transition from `c02aab67...` to `fb9985e2...` changed exactly 15 Markdown documentation/work files and
  no source, workflow, generated, manifest, oracle or checksum artifact. Its robust exact-fifteen path/size/content
  digest was `d98b8fe70a01f2c7195eede661a6b84ea71f92327e9c17e95e71960e27ba2a30`.
- Before this append, historical EVID-001..056 body was exact 384,934 bytes/SHA-256
  `61e44b5824a9864fc6f859db23c0e87da14c34206863bd672a555bd7f490b724`; this patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-057.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified synthetic-merge/head tree
identity, and checked all 26 checkout logs plus raw full-Ubuntu, exact-Darwin, Python and all four relation-product
logs. It confirmed exact inventories, compatibility gates and frozen-artifact boundaries with
P0/P1/P2/P3=`0/0/0/0`.

This evidence closes only the exact 15-file GDJ-0029 completion-documentation head. It does not widen ADR-0029,
Q-013 or Q-017, prove this later exact seven-file terminal evidence/status record, or authorize merging Draft PR #1;
no merge was performed. The terminal record is intentionally non-recursive: its exact seven allowed Markdown paths,
historical prefix, current-evidence uniqueness, links/frontmatter/fences and whitespace are validated locally while
code, workflow, generated and contract artifacts remain identical to the hosted-tested completion head. Its exact-head
hosted CI remains `not run/pending`; run `31482242288` is not reused as that proof.

## EVID-20260811-058 — GDJ-0029 Terminal Exact-head CI and GDJ-0030 Activation Baseline

- Date/time: 2026-08-11T10:55:54Z–2026-08-11T11:03:22Z
- Work/contract IDs: GDJ-0029 terminal closure; GDJ-0030 clean activation baseline; REL-001/003/004/005/006/009/010/
  011/012 `passing`, REL-002/007/008 ordered payload-free `oracle_locked`; Q-013 `Partial`, Q-017 P1/open
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@d0396c76d016c0f0335b484fbad56c70b80cf6d4`
  (`docs: finalize one-hop select related evidence`), parent
  `fb9985e20c92f71eaca7bac81bc61466369e0ebd`; observed exact-head tree prefix `20c0dd...`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  actual SQLite relation-product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin
  CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims
  are absent.
- Command: Draft PR #1 `pull_request`
  [run 31484369693](https://github.com/progresshans/godj/actions/runs/31484369693), attempt 1, workflow run number 47
- Exit status: `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or
  skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact GDJ-0029 terminal evidence/status head passed unchanged product gates and is the clean
  baseline from which GDJ-0030 documentation is activated. Product remains exact 12 adapter sets/127 contracts=
  `119 passing + 5 deviation + 3 oracle_locked`, relation actual 9/12. Each relation-product coordinate reproduced
  630 run/630 pass/0 skip, 63,928 encoded bytes and SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips
  remain intentional; exact Darwin passed 193/193 with skip 0. REL-002/007/008 remain `oracle_locked`; GDJ-0030's
  Proposed APIs, REL-007/008 actual, target `121 + 5 + 1`, relation 11/12 and exact thirteen-file delete product are
  not implemented or tested by this run.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=d0396c76d016c0f0335b484fbad56c70b80cf6d4`, status `completed`, conclusion `success`, started
  2026-08-11T10:55:54Z and completed 2026-08-11T11:03:22Z.
- PR #1 was re-queried as `OPEN`/`DRAFT`/`CLEAN`/`MERGEABLE`. The observed Actions synthetic merge prefix was
  `858b342c...`; synthetic merge and exact head had the same observed tree prefix `20c0dd...`. All checkout/source
  comparisons reported source diff 0.

Exact job identities and UTC intervals:

| Required execution | Job ID | UTC interval | Result |
|---|---:|---|---|
| Validate checked-in conformance artifacts | [93756277910](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756277910) | 10:55:57–11:02:07 | success |
| Relation binding (`macos-26`) | [93756277934](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756277934) | 10:55:57–10:57:16 | success |
| Relation product (`macos-15-intel`) | [93756277946](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756277946) | 10:55:57–11:00:41 | success |
| Relation product (`ubuntu-24.04-arm`) | [93756277964](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756277964) | 10:55:57–10:58:21 | success |
| Relation product (`ubuntu-22.04`) | [93756277970](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756277970) | 10:56:03–10:59:31 | success |
| Relation binding (`ubuntu-22.04`) | [93756277996](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756277996) | 10:55:57–10:57:20 | success |
| Project check (`ubuntu-24.04-arm`) | [93756278005](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278005) | 10:55:57–10:56:49 | success |
| Project check (`macos-15-intel`) | [93756278012](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278012) | 10:55:57–10:57:29 | success |
| Project check (`ubuntu-22.04`) | [93756278019](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278019) | 10:55:57–10:57:02 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93756278024](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278024) | 10:55:57–10:57:07 | success |
| Product project check (`ubuntu-22.04`) | [93756278029](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278029) | 10:55:57–10:59:18 | success |
| Python compatibility (`3.14.3`) | [93756278037](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278037) | 10:55:57–10:56:26 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93756278038](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278038) | 10:59:12–11:00:48 | success |
| Relation binding (`macos-15-intel`) | [93756278039](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278039) | 10:55:58–10:59:16 | success |
| SQLite (`ubuntu-22.04`) | [93756278041](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278041) | 10:55:57–10:57:34 | success |
| Relation product (`macos-26`) | [93756278049](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278049) | 10:57:32–11:00:29 | success |
| Project check (`macos-26`) | [93756278052](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278052) | 11:00:31–11:01:30 | success |
| Python compatibility (`3.14.7`) | [93756278054](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278054) | 10:55:57–10:56:25 | success |
| Product project check (`macos-15-intel`) | [93756278088](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278088) | 10:56:01–11:01:09 | success |
| SQLite (`ubuntu-24.04-arm`) | [93756278091](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278091) | 10:55:57–10:57:10 | success |
| Python compatibility (`3.13.15`) | [93756278092](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278092) | 10:55:57–10:56:19 | success |
| Product project check (`ubuntu-24.04-arm`) | [93756278106](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278106) | 10:55:57–10:58:17 | success |
| SQLite (`macos-26`) | [93756278126](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278126) | 10:59:18–11:00:50 | success |
| Python compatibility (`3.12.13`) | [93756278130](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278130) | 10:55:57–10:56:16 | success |
| SQLite (`macos-15-intel`) | [93756278160](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278160) | 10:57:18–10:59:10 | success |
| Product project check (`macos-26`) | [93756278188](https://github.com/progresshans/godj/actions/runs/31484369693/job/93756278188) | 11:00:43–11:03:21 | success |

Hosted gate details:

- Full Ubuntu job `93756277910` passed the checked-in conformance, project, SQLite, generated/reference no-rewrite,
  bounded actual Ubuntu Linux/386 and clean-worktree gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93756278038` passed the exact profile with skip 0.
- Python jobs `93756278037`/`93756278054`/`93756278092`/`93756278130` passed the four pinned compatibility legs.
- Relation-product jobs `93756277946`/`93756277964`/`93756277970`/`93756278049` each independently reproduced
  exact 630 run/630 pass/0 skip, encoded inventory 63,928 bytes and SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`.
- Relation manifest remained 10,788 bytes/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`; REL-001/003/004/005/006/009/010/011/012
  remain `passing` and REL-002/007/008 remain `oracle_locked`. The exact twelve-file checked-in generated union remained
  SHA-256 `3f40133f93d2ac2014276c2e07396a1db74acdb2ebc4b8ff44e29ac1208df535`.
- The hosted-tested terminal transition changed documentation/status only; source diff was exactly 0. Schema, migration,
  module, oracle/static/SHA, generator, generated and conformance product sources remained unchanged.
- Before this activation-doc edit, historical EVID-001..057 body from its first heading was exact 395,207 bytes/SHA-256
  `a41ba1b06351b9f18c7acbf135d111caf4abfdc0c33f069778426d9b41790632`. This patch preserves that body prefix,
  changes only the top current-evidence pointer and appends EVID-058.

This evidence terminally closes GDJ-0029 and establishes only the clean pre-activation baseline for GDJ-0030. It does not
accept ADR-0030, implement REL-007/008, change the manifest, prove the later exact 15-file activation-documentation tree or
authorize merging Draft PR #1. The activation tree requires separate exact-head CI and audit; run `31484369693` is not
reused as that proof. Q-013 remains `Partial`, Q-017 remains P1/open, REL-002 remains locked and no canonical facade is
claimed.

## EVID-20260811-059 — GDJ-0030 Activation Hosted Output-backpressure Failure and Local CI Stabilization

- Date/time: 2026-08-11T13:54:56Z–2026-08-11T14:01:36Z hosted; local diagnosis and stabilization through
  2026-08-11T23:47:08+09:00
- Work/contract IDs: GDJ-0030 activation verification; REL-007/008 remain ordered payload-free `oracle_locked`; no
  product or compatibility promotion
- Checkout/commit: hosted `codex/revision-fenced-migration-lifecycle@83e6ea05e5c224a39f1d1d43aa17a3e58cf81c98`
  (`docs: activate project-bound relation delete slice`); local workflow/protocol/status stabilization is a later
  uncommitted diff at the time of this evidence
- Environment/backend: GitHub-hosted exact 26-execution matrix, Go 1.26.5; focused local macOS darwin/arm64 Go 1.26.5
  output-backpressure reproduction and direct-file controls
- Command: Draft PR #1 `pull_request`
  [run 31498696555](https://github.com/progresshans/godj/actions/runs/31498696555), attempt 1, workflow run number 48;
  local `make ci`; focused protocol test; exact relation-product workflow block; slow-consumer and regular-file ORM
  JSON controls; extracted failure branch with 2 MiB valid and malformed JSON controls
- Exit status: hosted `failure`; exact 25/26 jobs succeeded and one failed, with 319/326 recorded steps success,
  one failure and six skipped. Local activation `make ci`, corrected workflow block and protocol tests succeeded.
- Result summary: the only hosted failure was relation-product macOS Intel job
  [93802788593](https://github.com/progresshans/godj/actions/runs/31498696555/job/93802788593). Every individual ORM
  test passed, then package shutdown reported `Test I/O incomplete 1m0s after exiting` and
  `exec: WaitDelay expired before I/O complete`; test-level failures were zero. ORM has no child-process launch path.
  All four relation-product raw logs still contained exact 630 top-level runs/630 passes/0 skips and the canonical
  63,928-byte/SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`
  inventory, but the failed job did not execute its post-test inventory assertion, race, CGO-disabled, vet,
  no-rewrite or clean-worktree steps. The other 25 jobs succeeded, including exact Darwin, four Python coordinates,
  full Ubuntu validation and three other relation-product coordinates.
- Failures/skips/not run: this run is not GDJ-0030 activation acceptance evidence and is not reused for a corrected
  head. REL-007/008 implementation and target `121 + 5 + 1` remain not run. Draft PR merge was not performed.

Root-cause and control evidence:

- The failing job streamed approximately 12,487 JSON events through `go test -json ... | tee "$log"`. Hosted
  macOS Intel event delivery lag reached 45.391 seconds; the preceding successful Intel run had already reached
  45.932 seconds/35.659-second ORM elapsed. Go 1.26.5 derives a one-minute process-output `WaitDelay` from the default
  ten-minute test timeout.
- A local slow consumer attached to `go test -json -count=1 -timeout=10s ./orm` reproduced the same package-level
  `Test I/O incomplete 5s`/`WaitDelay expired` after all observed tests passed. The captured 230,838-byte log SHA-256
  was `dc4e99a661f169a44370e763bfff9bd4e171c9530d819487124bc841b30d7df4`.
- The same ORM command redirected directly to a regular file passed in 0.92 seconds with 1,144 events; its
  259,948-byte log SHA-256 was `4c7667e3c0c9c2f72bff6b27042531bf17a89bf53ae7e9206beb9a1a2b65c070`.
- The corrected relation-product block writes verbose JSON directly to `$RUNNER_TEMP`, preserves a nonzero Go status,
  prints at most 64 KiB of failed test/package diagnostics on failure while preserving the original status even if the
  formatter fails, and after success publishes 630 canonical top-level run records
  plus one observed inventory summary. The exact extracted block passed locally and emitted 631 lines/83,641 bytes;
  the final summary was 630 runs/630 passes/0 skips, 63,928 payload bytes and SHA-256 `4415fd69...bca`. Output SHA-256
  was `60efd4595b796d9aa3b03f3f96cfa977d229c234d9eb87ae5375da0b8f439d6b`.
- The exact extracted failure branch preserved synthetic Go status 23 for a 2 MiB valid failed-test output while
  publishing 60,063 bytes, preserved status 124 and the final timeout/stack tail for a package-only failure while
  publishing 60,062 bytes, and preserved status 130 when the 2 MiB JSON stream was malformed while publishing a
  60,075-byte fallback. All remained below the 65,536-byte outer bound.
- `go test ./conformance/internal/protocol -count=1`, its focused exact-26 workflow test, shell syntax extraction,
  `gofmt -d` and `git diff --check` passed. Static protocol coverage requires direct-file capture, original status
  propagation, compact canonical evidence and no relation-product live `tee`.

Before this append, `docs/status/TEST_EVIDENCE.md` was exact 404,905 bytes/SHA-256
`1fb564cc108589e783ff4b4452f659f3a084eed082e823be4812b740843fac72`. This entry records a real verification
failure and its local correction without changing ADR-0030 Proposed, GDJ-0030 active, Q-013 Partial, Q-017 P1/open or
the current `119 passing + 5 deviation + 3 oracle_locked` classification. A new commit and exact-head hosted 26/26
run remain required before implementation begins.

## EVID-20260812-060 — GDJ-0030 Corrected Activation Exact-head Hosted Success

- Date/time: 2026-08-11T14:49:10Z–2026-08-11T14:56:56Z hosted; recorded
  2026-08-12T00:00:11+09:00
- Work/contract IDs: GDJ-0030 activation verification; REL-007/008 remain ordered payload-free `oracle_locked`; no
  product or compatibility promotion
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@48472a1cba1ec706939f362ebdb1c4bea7f825eb`
  (`ci: stabilize relation product inventory capture`)
- Environment/backend: GitHub-hosted exact 26-execution matrix, Go 1.26.5; exact Django profile coordinates remain
  CPython 3.14.3/Django 6.1/SQLite 3.50.4 on darwin/arm64
- Command: Draft PR #1 `pull_request`
  [run 31503631942](https://github.com/progresshans/godj/actions/runs/31503631942), attempt 1, workflow run number 49
- Exit status: exact 26/26 jobs and 326/326 recorded steps completed with `success`
- Result summary: four relation-product jobs — Ubuntu 22.04 `93819610981`, macOS Intel `93819611039`, Ubuntu ARM
  `93819611132`, macOS 26 `93819611245` — each published exactly 631 compact lines/83,641 bytes with SHA-256
  `60efd4595b796d9aa3b03f3f96cfa977d229c234d9eb87ae5375da0b8f439d6b`. Each output contained 630 unique sorted
  run records plus one summary. Independent compact-output reconstruction produced the same 630 unique sorted runs,
  63,928 payload bytes and SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`
  on all four coordinates; passes=630/skips=0 came from the raw JSON parser assertions and published summary.
  `Test I/O incomplete` and `WaitDelay` occurred zero times. Full Ubuntu, exact Darwin, four Python coordinates,
  project/relation-binding/product-project/SQLite matrices, race, CGO-disabled, vet, no-rewrite and clean-worktree gates
  all completed successfully.
- Hosted identity: PR #1 was open/draft/CLEAN/MERGEABLE with base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and exact head `48472a1cba1ec706939f362ebdb1c4bea7f825eb`.
  Actions synthetic merge `dbc1d2f4de7e26496918734e94926b99b2b3dcb8` had parents `[base, head]`; synthetic and
  head tree were both `e1d5a378962c1778052131a5a8672fdea33bd662`.
  All 26 checkout logs selected that synthetic merge and recorded the exact base/head identities.
- Failures/skips/not run: job/step failures or skips were zero. The portable full suite and four compatibility Python
  coordinates each retained the expected 17 test-level skips; the exact Darwin profile retained zero. REL-007/008
  implementation, target manifest transition and target `121 + 5 + 1` classification were not part of this checkout
  and remain pending. Draft PR merge was not performed. Local query/db implementation bytes created after this commit
  are not covered by this run.

Before this append, `docs/status/TEST_EVIDENCE.md` was exact 410,205 bytes/SHA-256
`9d09aa9fc218476c5dcbdf6b865a0a7617e81975ade5862af7a38572d4c02dbd`. This entry closes only the corrected
activation gate. ADR-0030 remains Proposed, GDJ-0030 remains active, Q-013 remains Partial, Q-017 remains P1/open,
REL-002 remains locked and product classification remains `119 passing + 5 deviation + 3 oracle_locked`.

## EVID-20260812-061 — GDJ-0030 GitHub-hosted Exact 26-job Implementation-head CI

- Date/time: 2026-08-11T16:06:55Z–2026-08-11T16:14:39Z
- Work/contract IDs: GDJ-0030, REL-007, REL-008, Q-013, Q-017;
  REL-001/003/004/005/006/007/008/009/010/011/012 `passing`, REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@c3803acba1929921f23e4751679dc21d4bba9c0f`
  (`feat: verify project-bound relation deletes`), parent `26683b2bb41ee1f24d3e6d6397162b556cc1bcea`, tree
  `e28773f62e9e5f8158e1d11792b902502d203654`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  GoDj SQLite 3.53.3 relation-delete product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix;
  exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad
  non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request`
  [run 31510689383](https://github.com/progresshans/godj/actions/runs/31510689383), attempt 1
- Exit status: terminal `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed,
  cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: project-bound all-PROTECT scan/distinct typed error, canonical SET_NULL→target exact-one delete,
  declared-universe fingerprint, project-generated deleter aggregate and pinned SQLite `BEGIN IMMEDIATE` transaction/
  cleanup lifecycle passed the exact hosted matrix. Product is exact 12 adapter sets/127 contracts=
  `121 passing + 5 deviation + 1 oracle_locked`; relation actual is 11/12. Each relation-product coordinate
  reproduced 687 run/687 pass/0 skip, 69,597 encoded payload bytes and SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; `WaitDelay` and
  `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only
  test skips remain intentional; exact Darwin had skip 0. REL-002, canonical relation-aware facade/chaining,
  FK assignment/cache invalidation, recursive/bulk/CASCADE delete, DDL/migration and non-SQLite support remain
  unsupported/out of scope. The exact 15-file completion-documentation tree created after this implementation commit
  was not part of this checkout; its exact-head CI is `not run/pending`, and this implementation run is not proof of
  that later tree. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=c3803acba1929921f23e4751679dc21d4bba9c0f`, status `completed`, conclusion `success`, started
  2026-08-11T16:06:55Z and workflow metadata updated 2026-08-11T16:14:39Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, exact implementation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `21457156ffc58e429a53129285f6b3cb4ee9dd11` had those exact base/head parents.
  Synthetic merge and exact head trees were both `e28773f62e9e5f8158e1d11792b902502d203654`, so executed contents
  were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93843439555](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439555) | 16:06:58–16:13:21 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93843439632](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439632) | 16:08:20–16:09:43 | 14 | success |
| Project check (`ubuntu-22.04`) | [93843439650](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439650) | 16:06:59–16:08:08 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93843439663](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439663) | 16:07:01–16:07:50 | 12 | success |
| Project check (`macos-15-intel`) | [93843439927](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439927) | 16:09:34–16:11:08 | 12 | success |
| Project check (`macos-26`) | [93843439741](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439741) | 16:07:00–16:08:10 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93843439700](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439700) | 16:06:58–16:08:42 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93843439825](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439825) | 16:06:58–16:08:09 | 13 | success |
| Relation binding (`macos-15-intel`) | [93843439796](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439796) | 16:07:00–16:09:04 | 13 | success |
| Relation binding (`macos-26`) | [93843439823](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439823) | 16:06:59–16:08:13 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93843439649](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439649) | 16:06:58–16:10:39 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93843439652](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439652) | 16:07:00–16:09:41 | 13 | success |
| Relation product (`macos-15-intel`) | [93843439637](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439637) | 16:06:58–16:12:12 | 13 | success |
| Relation product (`macos-26`) | [93843439937](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439937) | 16:09:45–16:12:24 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93843439743](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439743) | 16:06:59–16:10:18 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93843439626](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439626) | 16:07:01–16:09:20 | 12 | success |
| Product project check (`macos-15-intel`) | [93843439566](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439566) | 16:06:59–16:12:46 | 12 | success |
| Product project check (`macos-26`) | [93843440542](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843440542) | 16:11:10–16:14:29 | 12 | success |
| Python compatibility (`3.12.13`) | [93843439577](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439577) | 16:06:58–16:07:19 | 12 | success |
| Python compatibility (`3.13.15`) | [93843439709](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439709) | 16:07:05–16:07:30 | 12 | success |
| Python compatibility (`3.14.3`) | [93843439659](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439659) | 16:06:59–16:07:30 | 12 | success |
| Python compatibility (`3.14.7`) | [93843439677](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439677) | 16:06:58–16:07:25 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93843439712](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439712) | 16:06:59–16:08:39 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93843439714](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439714) | 16:06:58–16:08:16 | 12 | success |
| SQLite (`macos-15-intel`) | [93843439906](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439906) | 16:09:07–16:11:15 | 12 | success |
| SQLite (`macos-26`) | [93843439849](https://github.com/progresshans/godj/actions/runs/31510689383/job/93843439849) | 16:08:15–16:09:32 | 12 | success |

Hosted and local gate details:

- Full Ubuntu job `93843439555` passed root `make ci`, exact `godjcheck` 11 required/1 not implemented,
  no-rewrite/clean-worktree gates and the bounded actual Ubuntu Linux/386 package set. This is not broad all-package
  Linux/386 support.
- Exact Darwin job `93843439632` passed the exact profile with 193/193 tests and skip 0.
- Python jobs `93843439577`/`93843439709`/`93843439659`/`93843439677` each passed portable 193 tests with 17
  intentional skips after Python 3.14.7 metadata convergence, and verified 127 scenarios, encoded payload 498,051
  bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93843439649`/`93843439652`/`93843439637`/`93843439937` each independently reproduced
  exact 687 run/687 pass/0 skip, encoded inventory 69,597 bytes and SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates. `WaitDelay` was 0 on every coordinate.
- Local implementation validation passed root `make ci`, bounded normal/race/CGO-disabled/vet/Linux-386 compile,
  Django relation status 11/11 and two independent 687/687/0 inventory reconstructions. Independent runtime,
  SQLite, codegen, compile/conformance and shared-integration audits finished with P0/P1/P2/P3=`0/0/0/0`.
- Relation manifest is 10,776 bytes/SHA-256
  `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, changing exactly REL-007/008 to
  `passing`; exact revert restores the 10,788-byte/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141` baseline. The exact thirteen-file
  generated union is SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628` and relation-policy
  fingerprint v1 is `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- Frozen relation oracle, static fixture, SHA256SUMS, schemas, migrations, non-SQLite sources and prior generated
  artifacts remained unchanged.

Before this append, the full `docs/status/TEST_EVIDENCE.md` file was exact 413,393 bytes/SHA-256
`7995e64da36f2f651a3fa7f5c6750268c193e675313b68b4c3c248d413297c1d`. The historical body from exact
`## EVID-20260807-001` through EVID-060 was exact 412,869 bytes/SHA-256
`8202eb084ab252ec8c3de3d16843921e01f1e063db65ac754b95e089fa9eaa3a`; this patch preserves that body
byte-for-byte, changes only the top current-evidence pointer and appends EVID-061.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified synthetic-merge/head tree
identity and checked the full Ubuntu, exact Darwin, four Python and all four relation-product logs. It confirmed the
exact inventories, compatibility gates, callback/rows/transaction lifecycle and frozen-artifact boundaries with
P0/P1/P2/P3=`0/0/0/0`.

This evidence completes GDJ-0030 and accepts ADR-0030 only for the bounded SQLite REL-007/008 low-level delete
engine. Q-013 remains `Partial`; Q-017 remains P1/open; REL-002 and the broader facade/chaining/cache/recursive-delete/
DDL/migration/non-SQLite surface remain open. There is no active or ready work packet. Draft PR #1 remains open/draft
and was not merged. The exact 15-file completion-documentation tree created after this implementation commit still
requires its own hosted CI; run `31510689383` is not reused as that proof.

## EVID-20260812-062 — GDJ-0030 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T16:46:26Z–2026-08-11T16:53:10Z
- Work/contract IDs: GDJ-0030 completion documentation, REL-007, REL-008, Q-013, Q-017;
  REL-001/003/004/005/006/007/008/009/010/011/012 `passing`, REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@635e9c38a4464b98987d56c1b7d796aa42734661`
  (`docs: complete project-bound relation delete slice`), parent `c3803acba1929921f23e4751679dc21d4bba9c0f`, tree
  `4e5f9e55a6ca39ac46ed0022cce04146e528372a`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  GoDj SQLite 3.53.3 relation-delete product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix;
  exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad
  non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request`
  [run 31514159835](https://github.com/progresshans/godj/actions/runs/31514159835), attempt 1, workflow run number 52
- Exit status: terminal `success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed,
  cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 15-file completion-documentation transition from implementation head `c3803acb...` to
  `635e9c38...` passed unchanged product gates. GDJ-0030 remains completed and ADR-0030 remains Accepted only for the
  bounded SQLite REL-007/008 low-level delete engine. Q-013 remains `Partial`, Q-017 remains P1/open and active/ready
  work remains empty. Product remains exact 12 adapter sets/127 contracts=
  `121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate reproduced
  687 run/687 pass/0 skip, 69,597 encoded payload bytes and SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; `WaitDelay` and
  `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only
  skips remain intentional; exact Darwin passed 193/193 with skip 0. REL-002, canonical relation-aware facade/chaining,
  FK assignment/cache invalidation, recursive/bulk/CASCADE delete, DDL/migration and non-SQLite support remain
  unsupported/out of scope. This EVID-062 append and its exact seven-file terminal evidence/status patch are later
  documentation-only changes after the tested completion-documentation head. Their exact-head hosted CI is
  `not run/pending`; run `31514159835` is not reused as proof of that later tree. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=635e9c38a4464b98987d56c1b7d796aa42734661`, status `completed`, conclusion `success`, created
  2026-08-11T16:46:26Z and workflow metadata updated 2026-08-11T16:53:10Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, exact completion-documentation head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `a4ffcecb8c265c1a80bca8dbb3398cc272857b7f` had those exact base/head parents.
  Synthetic merge and exact head trees were both `4e5f9e55a6ca39ac46ed0022cce04146e528372a`, so executed contents
  were exact-head-equivalent.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93855009404](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009404) | 16:46:29–16:53:00 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93855009407](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009407) | 16:46:29–16:48:03 | 14 | success |
| Project check (`ubuntu-22.04`) | [93855009514](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009514) | 16:46:29–16:47:39 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93855009479](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009479) | 16:46:28–16:47:18 | 12 | success |
| Project check (`macos-15-intel`) | [93855009588](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009588) | 16:50:21–16:52:06 | 12 | success |
| Project check (`macos-26`) | [93855009613](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009613) | 16:49:41–16:50:32 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93855009416](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009416) | 16:46:29–16:48:18 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93855009464](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009464) | 16:46:28–16:47:42 | 13 | success |
| Relation binding (`macos-15-intel`) | [93855009550](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009550) | 16:50:25–16:52:00 | 13 | success |
| Relation binding (`macos-26`) | [93855009424](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009424) | 16:46:29–16:48:06 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93855009392](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009392) | 16:46:28–16:50:14 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93855009304](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009304) | 16:46:28–16:48:59 | 13 | success |
| Relation product (`macos-15-intel`) | [93855009374](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009374) | 16:46:30–16:50:22 | 13 | success |
| Relation product (`macos-26`) | [93855009489](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009489) | 16:50:34–16:53:09 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93855009361](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009361) | 16:46:29–16:50:25 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93855009449](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009449) | 16:46:28–16:48:42 | 12 | success |
| Product project check (`macos-15-intel`) | [93855009354](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009354) | 16:46:29–16:51:10 | 12 | success |
| Product project check (`macos-26`) | [93855009369](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009369) | 16:46:30–16:50:37 | 12 | success |
| Python compatibility (`3.12.13`) | [93855009813](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009813) | 16:46:35–16:46:55 | 12 | success |
| Python compatibility (`3.13.15`) | [93855009650](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009650) | 16:46:28–16:46:53 | 12 | success |
| Python compatibility (`3.14.3`) | [93855009595](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009595) | 16:46:29–16:47:14 | 12 | success |
| Python compatibility (`3.14.7`) | [93855009534](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009534) | 16:46:28–16:46:57 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93855009475](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009475) | 16:46:29–16:48:17 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93855009585](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009585) | 16:46:31–16:47:44 | 12 | success |
| SQLite (`macos-15-intel`) | [93855009538](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009538) | 16:48:05–16:50:17 | 12 | success |
| SQLite (`macos-26`) | [93855009516](https://github.com/progresshans/godj/actions/runs/31514159835/job/93855009516) | 16:48:08–16:49:39 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93855009404` passed root `make ci`, exact `godjcheck` 11 required/1 not implemented,
  no-rewrite/clean-worktree gates and the bounded actual Ubuntu Linux/386 package set. This is not broad all-package
  Linux/386 support.
- Exact Darwin job `93855009407` passed the exact profile with 193/193 tests and skip 0.
- Python jobs `93855009813`/`93855009650`/`93855009595`/`93855009534` each passed portable 193 tests with 17
  intentional skips after Python 3.14.7 metadata convergence, and verified 127 scenarios, encoded payload 498,051
  bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93855009392`/`93855009304`/`93855009374`/`93855009489` each independently reproduced
  exact 687 run/687 pass/0 skip, encoded inventory 69,597 bytes and SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`, then passed race,
  CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree gates. `WaitDelay` and `Test I/O incomplete`
  were 0 on every coordinate.
- Relation manifest remained 10,776 bytes/SHA-256
  `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; REL-001/003/004/005/006/007/008/009/
  010/011/012 remain `passing` and REL-002 remains ordered `oracle_locked`. The exact thirteen-file checked-in
  generated union remained SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`, and relation-policy fingerprint v1
  remained `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- The tested transition from `c3803acb...` to `635e9c38...` changed exactly 15 Markdown documentation/work files,
  with 336 insertions and 146 deletions. It changed no source, workflow, generated, manifest, oracle or checksum
  artifact. The exact `git diff` SHA-256 was
  `7aca1d12447e11d2ed30fbb8727870b5b35fa02ddb31cb8c0d75f56928f68bc9`.

Before this append, the full `docs/status/TEST_EVIDENCE.md` file was exact 424,523 bytes/SHA-256
`8b5977796a29ed91555a1efc5845ca92aea1e45fb7f497b5b965645ed0ac0ea5`. The historical body beginning at byte
offset 524 with exact `## EVID-20260807-001` through EVID-061 was exact 423,999 bytes/SHA-256
`f10f5134ce906512bcb9ffe95bbaaaa49bf0bb34e2087edf0bf779f98a87cfc9`; this patch preserves that body
byte-for-byte, changes only the top current-evidence pointer and appends EVID-062.

Independent hosted evidence audit re-queried run/jobs/steps/PR/commit ancestry, verified synthetic-merge/head tree
identity and checked the full Ubuntu, exact Darwin, four Python and all four relation-product logs. It confirmed the
exact inventories, compatibility gates, callback/rows/transaction lifecycle and frozen-artifact boundaries with
P0/P1/P2/P3=`0/0/0/0`.

This evidence closes only the exact 15-file GDJ-0030 completion-documentation head. It does not widen ADR-0030,
Q-013 or Q-017, prove this later exact seven-file terminal evidence/status record, or authorize merging Draft PR #1;
no merge was performed. The terminal record is intentionally non-recursive: its exact seven allowed Markdown paths,
historical EVID-001..061 body, current-evidence uniqueness, links/frontmatter/fences and whitespace are validated
locally while code, workflow, generated and contract artifacts remain identical to the hosted-tested completion head.
Its exact-head hosted CI remains `not run/pending`; run `31514159835` is not reused as that proof.

## EVID-20260812-063 — GDJ-0030 Terminal Exact-head CI and GDJ-0031 Activation Baseline

- Date/time: 2026-08-11T17:10:02Z–2026-08-11T17:19:56Z; last job completed at 17:19:55Z
- Work/contract IDs: GDJ-0030 terminal closure; GDJ-0031 clean activation baseline; Q-013 `Partial`, Q-017 P1/open;
  REL-001/003/004/005/006/007/008/009/010/011/012 `passing`, REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@ceff9e534e541edb0bd19cd6a1a61682b5435454`
  (`docs: record terminal relation delete evidence`), parent `635e9c38a4464b98987d56c1b7d796aa42734661`, tree
  `b52c251b4eff776233170d6563e92778f93d1d18`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5;
  GoDj SQLite 3.53.3 relation-delete product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix;
  exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad
  non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request`
  [run 31516174741](https://github.com/progresshans/godj/actions/runs/31516174741), attempt 1, workflow run number 53
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully;
  failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact seven-file GDJ-0030 terminal evidence/status head passed unchanged product gates and is the
  clean baseline from which GDJ-0031 documentation is activated. Product remains exact 12 adapter sets/127 contracts=
  `121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate reproduced
  687 run/687 pass/0 skip, 69,597 encoded payload bytes and SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; `WaitDelay` and
  `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only
  skips remain intentional; exact Darwin passed 193/193 with skip 0. REL-002 remains `oracle_locked`. GDJ-0031's
  Proposed overlay, candidate facade compile, public names, generated upgrade decision and any reverse/write/cache/
  lifetime behavior were not present or tested by this run.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact
  `headSha=ceff9e534e541edb0bd19cd6a1a61682b5435454`, status `completed`, conclusion `success`, created
  2026-08-11T17:10:02Z and completed 2026-08-11T17:19:56Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, exact head and base
  `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `332f961b5eaf53a16d69da412a9aa903b2601556` had those exact base/head parents.
  Synthetic merge and exact head trees were both `b52c251b4eff776233170d6563e92778f93d1d18`, so executed contents
  were exact-head-equivalent. All 26 raw checkout logs selected that synthetic merge and contained the exact
  synthetic/head/base identities.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93861752088](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752088) | 17:10:05–17:16:16 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93861752224](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752224) | 17:12:11–17:13:58 | 14 | success |
| Project check (`ubuntu-22.04`) | [93861752057](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752057) | 17:10:05–17:11:14 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93861752220](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752220) | 17:10:05–17:10:49 | 12 | success |
| Project check (`macos-15-intel`) | [93861752177](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752177) | 17:11:11–17:12:37 | 12 | success |
| Project check (`macos-26`) | [93861752099](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752099) | 17:10:06–17:11:08 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93861752079](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752079) | 17:10:06–17:11:45 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93861752143](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752143) | 17:10:08–17:11:25 | 13 | success |
| Relation binding (`macos-15-intel`) | [93861752230](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752230) | 17:12:52–17:15:56 | 13 | success |
| Relation binding (`macos-26`) | [93861752228](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752228) | 17:10:05–17:11:23 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93861752361](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752361) | 17:10:06–17:13:49 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93861752878](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752878) | 17:10:10–17:12:42 | 13 | success |
| Relation product (`macos-15-intel`) | [93861752272](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752272) | 17:13:13–17:19:55 | 13 | success |
| Relation product (`macos-26`) | [93861752225](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752225) | 17:10:05–17:13:24 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93861752274](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752274) | 17:10:06–17:13:29 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93861752218](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752218) | 17:10:05–17:12:25 | 12 | success |
| Product project check (`macos-15-intel`) | [93861752231](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752231) | 17:12:40–17:17:10 | 12 | success |
| Product project check (`macos-26`) | [93861752301](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752301) | 17:10:05–17:13:10 | 12 | success |
| Python compatibility (`3.12.13`) | [93861752351](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752351) | 17:10:06–17:10:27 | 12 | success |
| Python compatibility (`3.13.15`) | [93861752925](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752925) | 17:10:09–17:10:35 | 12 | success |
| Python compatibility (`3.14.3`) | [93861752268](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752268) | 17:10:05–17:10:33 | 12 | success |
| Python compatibility (`3.14.7`) | [93861752290](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752290) | 17:10:07–17:10:36 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93861752300](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752300) | 17:10:12–17:11:50 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93861752265](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752265) | 17:10:06–17:11:21 | 12 | success |
| SQLite (`macos-15-intel`) | [93861752323](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752323) | 17:10:07–17:12:09 | 12 | success |
| SQLite (`macos-26`) | [93861752335](https://github.com/progresshans/godj/actions/runs/31516174741/job/93861752335) | 17:11:25–17:12:43 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93861752088` passed root `make ci`, exact `godjcheck` 11 required/1 not implemented,
  no-rewrite/clean-worktree gates and the bounded actual Ubuntu Linux/386 package set. This is not broad all-package
  Linux/386 support.
- Exact Darwin job `93861752224` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4
  with exact 193/193 tests and skip 0.
- Python jobs `93861752351`/`93861752925`/`93861752268`/`93861752290` each passed portable 193 tests with 17
  intentional skips and verified 127 scenarios, encoded payload 498,051 bytes and SHA-256
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93861752361`/`93861752878`/`93861752272`/`93861752225` each independently reproduced
  exact 687 actual/unique records, 687 run/687 pass/0 skip, encoded inventory 69,597 bytes and SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`. `WaitDelay` and
  `Test I/O incomplete` were 0 on every coordinate, including the slower macOS Intel leg.
- Relation manifest remained 10,776 bytes/SHA-256
  `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; REL-001/003/004/005/006/007/008/009/
  010/011/012 remain `passing` and REL-002 remains ordered `oracle_locked`. The exact thirteen-file checked-in
  generated union remained SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`, and relation-policy fingerprint v1
  remained `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- Parent to head changed exactly seven Markdown paths with 183 insertions and 29 deletions. It changed no source,
  workflow, generated, manifest, oracle or checksum artifact. The exact `git diff` SHA-256 was
  `55f10049cd1977da8dac06632baf81109fddd23c8a1a0f6015fc174d394c8ac7`.

Before this append, the full `docs/status/TEST_EVIDENCE.md` file at `ceff9e5...` was exact 435,904 bytes/SHA-256
`acc322b00a9c9b6642405eb25c9ddce7cb42432a9afc748e8ba9c922ca53b1ee`. The historical body beginning at byte
offset 524 with exact EVID-001 through EVID-062 was exact 435,380 bytes/SHA-256
`2dd357482571ec82260f591b5532884a2fcd51e45800443b5fed659fbf46a29e`; this patch preserves that body
byte-for-byte, changes only the top current-evidence pointer and appends EVID-063.

Independent hosted evidence audit re-queried the live run/jobs/steps and PR/ancestry/tree, inspected all 26 checkout
logs, the full Ubuntu, exact Darwin, four Python and four raw relation logs, and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence terminally closes only the exact seven-file GDJ-0030 terminal head and establishes only the clean
pre-activation baseline for GDJ-0031. It does not itself activate, accept or implement GDJ-0031, prove this later
EVID-063 append/activation-documentation tree, or authorize merging Draft PR #1; no merge was performed. The later
activation tree is `not run/pending`, needs separate exact-head CI and must not reuse run `31516174741` as its proof.

## EVID-20260812-064 — GDJ-0031 Activation-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T17:59:33Z–2026-08-11T18:11:15Z; last job completed at 18:11:14Z
- Work/contract IDs: GDJ-0031 activation documentation; Q-013 remains `Partial`, Q-017 remains P1/open; ADR-0031 is `Proposed`; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@624347e15e6d6e6b6981fe14b75974226f72f9df` (`docs: activate relation facade compile spike`), parent `ceff9e534e541edb0bd19cd6a1a61682b5435454`, tree `890e2f0aeebdfc8d248684f22c1b2b58415f2526`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31520396606](https://github.com/progresshans/godj/actions/runs/31520396606), attempt 1, workflow run number 54. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact twelve-path GDJ-0031 activation-documentation head passed unchanged product gates. It activates only a bounded test-only compile-usability work packet and Proposed ADR; it does not implement the overlay, candidate facade, generated source or product runtime. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate reproduced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. The compile-spike implementation, logical 17-file overlay, candidate names, runtime behavior, generated upgrade policy, REL-002, reverse/write/cache/session-lifetime behavior and non-SQLite support were not present or proven by this activation run. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=624347e15e6d6e6b6981fe14b75974226f72f9df`, status `completed`, conclusion `success`, created/started 2026-08-11T17:59:33Z and updated 2026-08-11T18:11:15Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact activation head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `5c81295077cffbf002b72f70a67786cd6063f042` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `624347e15e6d6e6b6981fe14b75974226f72f9df`. Synthetic merge and exact head trees were both `890e2f0aeebdfc8d248684f22c1b2b58415f2526`, so executed contents were exact-head-equivalent. All 26 raw checkout logs selected that synthetic merge and contained the exact synthetic/head/base identities.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93875748763](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748763) | 17:59:43–18:06:18 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93875748769](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748769) | 18:01:52–18:03:05 | 14 | success |
| Project check (`ubuntu-22.04`) | [93875748751](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748751) | 17:59:36–18:00:43 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93875748600](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748600) | 17:59:39–18:00:31 | 12 | success |
| Project check (`macos-15-intel`) | [93875748691](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748691) | 18:00:28–18:02:11 | 12 | success |
| Project check (`macos-26`) | [93875748661](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748661) | 17:59:36–18:00:27 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93875748703](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748703) | 17:59:36–18:00:51 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93875748686](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748686) | 17:59:38–18:00:54 | 13 | success |
| Relation binding (`macos-15-intel`) | [93875748706](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748706) | 17:59:37–18:01:50 | 13 | success |
| Relation binding (`macos-26`) | [93875748713](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748713) | 18:02:14–18:03:41 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93875748727](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748727) | 17:59:36–18:02:10 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93875748815](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748815) | 17:59:36–18:02:07 | 13 | success |
| Relation product (`macos-15-intel`) | [93875748804](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748804) | 18:03:10–18:11:14 | 13 | success |
| Relation product (`macos-26`) | [93875748743](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748743) | 17:59:36–18:04:08 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93875748663](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748663) | 17:59:36–18:03:01 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93875748842](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748842) | 17:59:38–18:01:57 | 12 | success |
| Product project check (`macos-15-intel`) | [93875748737](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748737) | 17:59:40–18:05:52 | 12 | success |
| Product project check (`macos-26`) | [93875748793](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748793) | 18:01:27–18:04:57 | 12 | success |
| Python compatibility (`3.12.13`) | [93875748883](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748883) | 17:59:36–17:59:56 | 12 | success |
| Python compatibility (`3.13.15`) | [93875748780](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748780) | 17:59:36–18:00:03 | 12 | success |
| Python compatibility (`3.14.3`) | [93875749013](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875749013) | 17:59:36–18:00:05 | 12 | success |
| Python compatibility (`3.14.7`) | [93875748854](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748854) | 17:59:36–17:59:58 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93875748734](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748734) | 17:59:43–18:01:25 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93875748875](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748875) | 17:59:38–18:00:55 | 12 | success |
| SQLite (`macos-15-intel`) | [93875748733](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748733) | 17:59:36–18:01:23 | 12 | success |
| SQLite (`macos-26`) | [93875748939](https://github.com/progresshans/godj/actions/runs/31520396606/job/93875748939) | 18:03:43–18:04:50 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93875748763` passed root `make ci`, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the bounded actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93875748769` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4 with exact 193/193 tests and skip 0.
- Python jobs `93875748883`/`93875748780`/`93875749013`/`93875748854` each passed portable 193 tests with 17 intentional skips and verified 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93875748727`/`93875748815`/`93875748804`/`93875748743` each independently emitted exact 687 actual records and 687 unique records, reconstructed 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate, including the 8m04s macOS Intel leg; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; the exact thirteen-file checked-in generated union remained 26,140 bytes/SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`; the physical exact sixteen-file relation-delete fixture remained 62,538 content bytes/SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`; relation-policy fingerprint v1 remained `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- Parent-to-head changed exactly twelve allowed Markdown documentation/work paths: `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/TESTING.md`, `docs/adr/0031-relation-aware-project-facade-and-generated-upgrade-boundary.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0030-project-bound-protect-and-set-null-delete.md`, `work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md`, and `work/README.md`; 707 insertions and 72 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. Exact `git diff` SHA-256 was `28a78e544e84296a5367b30c8f3339fdcfd8b60e7136c12757b5413266cc88b6`; `git diff --check` was clean.
- Before this EVID-064 append, the activation-head `docs/status/TEST_EVIDENCE.md` file was exact 446,532 bytes/SHA-256 `1c7a4f277b7bbdf0d12484371e17721ebd8e063ee6c23f702abc3883f1240978`. Its historical body beginning at byte offset 524 with exact EVID-001..063 was 446,008 bytes/SHA-256 `8a4fae2234efb843ba8834b78be2ce666ab1d4e27d57a904fdb05615cb36e5ed`; the prior EVID-001..062 prefix within it was independently compared byte-identical.

Independent hosted evidence audit re-queried the unique live run, all 26 jobs and 326 steps, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It reconstructed the 69,597-byte relation payload independently on every coordinate and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence proves only the exact twelve-document GDJ-0031 activation head. ADR-0031 remains Proposed, GDJ-0031 remains active, Q-013 remains Partial and Q-017 remains P1/open. It does not prove the later compile implementation, accept candidate public names, claim product behavior, prove this later EVID-064 append, or authorize merging Draft PR #1. EVID-063/run `31516174741` is not reused as activation proof; activation run `31520396606` is not reused as implementation proof. No rerun or merge was performed.

## EVID-20260812-065 — GDJ-0031 GitHub-hosted Exact 26-job Compile-spike Implementation-head CI

- Date/time: 2026-08-11T19:28:35Z–2026-08-11T19:38:17Z; last job completed at 19:38:16Z
- Work/contract IDs: GDJ-0031 compile-usability spike; Q-013 remains `Partial`, Q-017 remains P1/open; ADR-0031 remains `Proposed`; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@065390275ee7b69e224eeaeda57e4731321d7a44` (`test: prove relation facade compile usability`), parent `624347e15e6d6e6b6981fe14b75974226f72f9df`, tree `6750ae505296d9284b08e57f5724ba9a8311b015`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31528039746](https://github.com/progresshans/godj/actions/runs/31528039746), attempt 1, workflow run number 55. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the test-only relation-facade compile spike passed on the exact implementation head. The existing physical 16-file product fixture remained byte-identical while exactly one virtual project source produced a logical 17-file overlay view. Hosted `internal/compiletest` proved overlay-backed forward read-only candidate compilation, no-overlay failure, typed negative cases, session callback assignability and AST/source/inventory gates. This does not create or accept a production facade or generated API. Product classification remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. Runtime query counts, cache behavior, session lifetime, REL-002 mutation/cache, reverse facade, write/delete facade, generated upgrade/public names, Windows, PostgreSQL/MySQL and broad non-SQLite support remain unimplemented or unproven. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=065390275ee7b69e224eeaeda57e4731321d7a44`, status `completed`, conclusion `success`, created 2026-08-11T19:28:35Z and updated 2026-08-11T19:38:17Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `efdb2e865dbb7326534295a226c9d9b8c6e4d300` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `065390275ee7b69e224eeaeda57e4731321d7a44`. Synthetic merge and exact head trees were both `6750ae505296d9284b08e57f5724ba9a8311b015`, so executed contents were exact-head-equivalent. All 26 raw checkout logs selected that synthetic merge and contained the exact synthetic/head/base identities.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93901058550](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058550) | 19:28:38–19:34:09 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93901058487](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058487) | 19:28:38–19:29:47 | 14 | success |
| Project check (`ubuntu-22.04`) | [93901058651](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058651) | 19:28:38–19:29:41 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93901058820](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058820) | 19:28:38–19:29:25 | 12 | success |
| Project check (`macos-15-intel`) | [93901058659](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058659) | 19:28:39–19:30:33 | 12 | success |
| Project check (`macos-26`) | [93901058725](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058725) | 19:28:38–19:29:54 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93901058667](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058667) | 19:28:38–19:30:23 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93901058705](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058705) | 19:28:41–19:29:51 | 13 | success |
| Relation binding (`macos-15-intel`) | [93901058628](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058628) | 19:28:39–19:32:26 | 13 | success |
| Relation binding (`macos-26`) | [93901058750](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058750) | 19:29:57–19:31:15 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93901058596](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058596) | 19:28:38–19:31:37 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93901058614](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058614) | 19:28:38–19:31:05 | 13 | success |
| Relation product (`macos-15-intel`) | [93901058666](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058666) | 19:29:49–19:34:03 | 13 | success |
| Relation product (`macos-26`) | [93901058706](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058706) | 19:30:33–19:33:37 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93901058652](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058652) | 19:28:38–19:31:17 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93901058719](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058719) | 19:28:39–19:30:53 | 12 | success |
| Product project check (`macos-15-intel`) | [93901058780](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058780) | 19:31:18–19:38:16 | 12 | success |
| Product project check (`macos-26`) | [93901058776](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058776) | 19:30:35–19:34:15 | 12 | success |
| Python compatibility (`3.12.13`) | [93901058985](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058985) | 19:28:38–19:29:07 | 12 | success |
| Python compatibility (`3.13.15`) | [93901058764](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058764) | 19:28:38–19:28:57 | 12 | success |
| Python compatibility (`3.14.3`) | [93901058786](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058786) | 19:28:38–19:29:07 | 12 | success |
| Python compatibility (`3.14.7`) | [93901058871](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058871) | 19:28:38–19:29:10 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93901058891](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058891) | 19:28:38–19:30:19 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93901058609](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058609) | 19:28:41–19:30:00 | 12 | success |
| SQLite (`macos-15-intel`) | [93901058540](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058540) | 19:28:39–19:30:30 | 12 | success |
| SQLite (`macos-26`) | [93901058843](https://github.com/progresshans/godj/actions/runs/31528039746/job/93901058843) | 19:32:29–19:33:47 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93901058550` passed root `make ci`; its raw log shows `internal/compiletest` passing in the normal and race phases, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the bounded actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93901058487` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `93901058985`/`93901058764`/`93901058786`/`93901058871` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93901058596`/`93901058614`/`93901058666`/`93901058706` each independently emitted exact 687 actual records and 687 unique records, reconstructed 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- The physical `conformance/relationdeleteproduct/**` fixture remained exact 16 files, 62,538 content bytes and inventory SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`. Its checked-in `zz_godj_*.go` union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`.
- The one-file virtual overlay created a logical exact 17-file view, 65,970 content bytes and SHA-256 `29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f`. The virtual target `project/zz_godj_relation_facade_spike.go` was absent from the physical fixture after validation. `project_facade_spike.go.txt` is 3,432 bytes/SHA-256 `2b67c5888b125a48dde536d1e8dd2bdb4028239d10dd33e70514817f35514fe7`; `external_consumer.go.txt` is 1,877 bytes/SHA-256 `248fb25ac710d5c7469ecd89954ad2d3e2466e85f95571ac0ab5874dff891756`.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, and relation-policy fingerprint v1 remained `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- Parent-to-head changed exactly three allowed internal compile-test paths: `internal/compiletest/compile_test.go`, `internal/compiletest/testdata/relation_facade/external_consumer.go.txt`, and `internal/compiletest/testdata/relation_facade/project_facade_spike.go.txt`; 1,967 insertions and 3 deletions. It changed no production source, workflow, generated product fixture, manifest, oracle, checksum or documentation path. Exact `git diff` SHA-256 was `df24aa3f564d9bd19c3b095797f96053cabe2de4ee3eb186c2377999190d4d01`; `git diff --check` was clean.
- At the tested implementation head, `docs/status/TEST_EVIDENCE.md` remained the activation-tree version: exact 446,532 bytes/SHA-256 `1c7a4f277b7bbdf0d12484371e17721ebd8e063ee6c23f702abc3883f1240978`, containing EVID-001..063. The later EVID-064 activation record and this EVID-065 append are documentation changes after the tested head and are not recursively proven by run `31528039746`.

Independent hosted evidence audit re-queried the unique live run, all 26 jobs and 326 steps, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It reconstructed the 69,597-byte relation payload independently on every coordinate and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence proves only the exact test-only compile-spike implementation head. It does not accept the candidate names as public API, claim runtime facade behavior, widen ADR-0031/Q-013/Q-017, prove the later completion-documentation/evidence tree, or authorize merging Draft PR #1. Activation run `31520396606` is not reused as implementation proof; run `31528039746` is not reused as proof of a later documentation head. No rerun or merge was performed.

## EVID-20260812-066 — GDJ-0031 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T20:09:15Z–2026-08-11T20:17:43Z; last job completed at 20:17:43Z
- Work/contract IDs: GDJ-0031 completion documentation; ADR-0031 is Accepted only for the test-only compile-feasibility method and false-green boundary; Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@e9b2c0e4812e7619d0b5ffd3862731714b00273d` (`docs: complete relation facade compile spike`), parent `065390275ee7b69e224eeaeda57e4731321d7a44`, tree `f2c4a324c0bcdfa1e712bc4c38594a9a63e46919`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31531470440](https://github.com/progresshans/godj/actions/runs/31531470440), attempt 1, workflow run number 56. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact eleven-path completion-documentation transition from implementation head `0653902...` to `e9b2c0e...` passed unchanged product gates. GDJ-0031 is completed and ADR-0031 is Accepted only for the test-only overlay compile-feasibility method; it does not accept a production facade, generator, generated output or candidate public names. Q-013 remains `Partial`, Q-017 remains P1/open, all candidate names remain noncanonical and active/ready work remains empty. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: unexpected hosted failures/cancellations/skips 0. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. Runtime query counts, cache behavior, callback-after-return session lifetime, REL-002 mutation/cache, reverse/write/delete facade, production generated upgrade/public names, Windows, PostgreSQL/MySQL and broad non-SQLite support remain unimplemented or unproven. This EVID-066 append is a later documentation change after the tested completion-documentation head and is not recursively proven by run `31531470440`; its own later tree exact-head CI remains `not run/pending`. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=e9b2c0e4812e7619d0b5ffd3862731714b00273d`, status `completed`, conclusion `success`, created/started 2026-08-11T20:09:15Z and updated 2026-08-11T20:17:43Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact completion-documentation head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `e25d753d6d32f062d852bb683cc2a1b023d2d40c` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `e9b2c0e4812e7619d0b5ffd3862731714b00273d`. Synthetic merge and exact head trees were both `f2c4a324c0bcdfa1e712bc4c38594a9a63e46919`, so executed contents were exact-head-equivalent. All 26 raw checkout logs selected that synthetic merge and contained the exact synthetic/head/base identities.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93912274361](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274361) | 20:09:18–20:14:27 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93912274457](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274457) | 20:09:19–20:11:02 | 14 | success |
| Project check (`ubuntu-22.04`) | [93912274446](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274446) | 20:09:18–20:10:16 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93912274612](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274612) | 20:09:21–20:10:07 | 12 | success |
| Project check (`macos-15-intel`) | [93912274432](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274432) | 20:09:19–20:10:50 | 12 | success |
| Project check (`macos-26`) | [93912274496](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274496) | 20:09:18–20:10:14 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93912274572](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274572) | 20:09:17–20:10:52 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93912274515](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274515) | 20:09:20–20:10:28 | 13 | success |
| Relation binding (`macos-15-intel`) | [93912274653](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274653) | 20:11:04–20:12:42 | 13 | success |
| Relation binding (`macos-26`) | [93912274615](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274615) | 20:12:45–20:14:12 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93912274563](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274563) | 20:09:19–20:13:09 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93912274604](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274604) | 20:09:18–20:11:44 | 13 | success |
| Relation product (`macos-15-intel`) | [93912274506](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274506) | 20:09:19–20:14:29 | 13 | success |
| Relation product (`macos-26`) | [93912274623](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274623) | 20:10:16–20:13:26 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93912274803](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274803) | 20:09:18–20:12:34 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93912274538](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274538) | 20:09:18–20:11:40 | 12 | success |
| Product project check (`macos-15-intel`) | [93912274562](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274562) | 20:09:19–20:17:43 | 12 | success |
| Product project check (`macos-26`) | [93912274544](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274544) | 20:12:15–20:15:19 | 12 | success |
| Python compatibility (`3.12.13`) | [93912274630](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274630) | 20:09:19–20:09:39 | 12 | success |
| Python compatibility (`3.13.15`) | [93912274595](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274595) | 20:09:21–20:09:50 | 12 | success |
| Python compatibility (`3.14.3`) | [93912274684](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274684) | 20:09:18–20:09:45 | 12 | success |
| Python compatibility (`3.14.7`) | [93912274543](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274543) | 20:09:17–20:09:55 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93912274696](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274696) | 20:09:18–20:11:04 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93912274545](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274545) | 20:09:18–20:10:39 | 12 | success |
| SQLite (`macos-15-intel`) | [93912274698](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274698) | 20:13:30–20:17:14 | 12 | success |
| SQLite (`macos-26`) | [93912274650](https://github.com/progresshans/godj/actions/runs/31531470440/job/93912274650) | 20:10:52–20:12:13 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93912274361` passed root `make ci`; its raw log shows `internal/compiletest` passing in normal and race phases, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the bounded actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93912274457` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `93912274630`/`93912274595`/`93912274684`/`93912274543` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93912274563`/`93912274604`/`93912274506`/`93912274623` each independently emitted exact 687 actual records and 687 unique records. Independent raw-log reconstruction on every coordinate produced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- The physical `conformance/relationdeleteproduct/**` fixture remained exact 16 files, 62,538 content bytes and inventory SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`. Its checked-in `zz_godj_*.go` union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`; the test-only one-file overlay retained the logical exact 17-file view, 65,970 bytes and SHA-256 `29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f`, with no physical virtual-target residue.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, and relation-policy fingerprint v1 remained `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- Parent-to-head changed exactly eleven allowed Markdown documentation/work paths: `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/TESTING.md`, `docs/adr/0031-relation-aware-project-facade-and-generated-upgrade-boundary.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md`, and `work/README.md`; 315 insertions and 123 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. Exact `git diff` SHA-256 was `62ec2d4445cfd2efc1d63b1a543089dce683b699efd6d413a8c59c29d994d02d`; `git diff --check` was clean.
- Before this EVID-066 append, the completion-documentation-head `docs/status/TEST_EVIDENCE.md` file was exact 470,625 bytes/SHA-256 `3df2e8b1bee4931a08c517a01480cc83a3bc7ccd5da57e80ce483de6df2c3139`. Its historical body beginning at byte offset 524 with exact EVID-001..065 was 470,101 bytes/SHA-256 `4eea3771912ae67b8cc8978d80eeb85c08a7a20741537dec0bd112216bd674af`; the EVID-001..063 sub-prefix within it remained byte-identical at 446,008 bytes/SHA-256 `8a4fae2234efb843ba8834b78be2ce666ab1d4e27d57a904fdb05615cb36e5ed`.

Independent hosted evidence audit re-queried the unique live run, all 26 jobs and 326 steps, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the 69,597-byte relation payload on every coordinate and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence closes only the exact eleven-document GDJ-0031 completion-documentation head. It does not widen ADR-0031 beyond test-only compile feasibility, accept candidate names as public API, claim runtime facade behavior, close Q-013/Q-017, prove this later EVID-066 append or any later terminal record, or authorize merging Draft PR #1. EVID-064/run `31520396606` and EVID-065/run `31528039746` are not reused as completion proof; run `31531470440` is not reused as proof of a later documentation head. No rerun or merge was performed.

## EVID-20260812-067 — GDJ-0031 Terminal Exact-head CI and GDJ-0032 Activation Baseline

- Date/time: 2026-08-11T20:37:39Z–2026-08-11T20:46:14Z; last job completed at 20:46:13Z
- Work/contract IDs: GDJ-0031 terminal closure; GDJ-0032 clean activation baseline; Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@3d6612512e8887de8868a319650d54ad0721471b` (`docs: record terminal relation facade evidence`), parent `e9b2c0e4812e7619d0b5ffd3862731714b00273d`, tree `387752dd8d9191832f3006260855e2ad29bf7515`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31533890720](https://github.com/progresshans/godj/actions/runs/31533890720), attempt 1, workflow run number 57. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact seven-path GDJ-0031 terminal evidence/status head passed unchanged product gates and is the clean baseline from which GDJ-0032 documentation may be activated. GDJ-0031 remains completed and ADR-0031 remains Accepted only for the test-only overlay compile-feasibility method; candidate names remain noncanonical, Q-013 remains `Partial`, Q-017 remains P1/open and active/ready work remains empty at this head. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: terminal job failures/cancellations/skips and non-success recorded steps were 0. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. The exact Darwin checkout raw log contained one first-fetch `##[error]` for `SSL certificate problem: self signed certificate`; `actions/checkout` waited 16 seconds, retried successfully, fetched and checked out the exact synthetic merge, and the checkout step/job plus all following gates concluded success. This was a recovered hosted transport retry, not a recorded failed step, and had no checkout-identity or executed-content impact. GDJ-0032's work/ADR/scope was not present or tested by this run. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=3d6612512e8887de8868a319650d54ad0721471b`, status `completed`, conclusion `success`, created/started 2026-08-11T20:37:39Z and updated 2026-08-11T20:46:14Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact terminal head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `bd8e23fef34f4c9d2243779d37ddbefb1c62929a` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `3d6612512e8887de8868a319650d54ad0721471b`. Synthetic merge and exact head trees were both `387752dd8d9191832f3006260855e2ad29bf7515`, so executed contents were exact-head-equivalent. All 26 raw checkout logs selected that synthetic merge and contained the exact synthetic/head/base identities; the exact Darwin log did so after the bounded recovered first-fetch retry described above.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93920239275](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239275) | 20:37:42–20:43:51 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93920239378](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239378) | 20:40:47–20:42:40 | 14 | success |
| Project check (`ubuntu-22.04`) | [93920239534](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239534) | 20:37:43–20:38:45 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93920239499](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239499) | 20:37:42–20:38:27 | 12 | success |
| Project check (`macos-15-intel`) | [93920239323](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239323) | 20:37:42–20:39:06 | 12 | success |
| Project check (`macos-26`) | [93920239429](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239429) | 20:42:03–20:43:10 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93920239411](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239411) | 20:37:42–20:39:27 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93920239394](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239394) | 20:37:42–20:38:56 | 13 | success |
| Relation binding (`macos-15-intel`) | [93920239375](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239375) | 20:37:42–20:40:30 | 13 | success |
| Relation binding (`macos-26`) | [93920239293](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239293) | 20:39:08–20:40:35 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93920239289](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239289) | 20:37:42–20:41:34 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93920239441](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239441) | 20:37:42–20:40:12 | 13 | success |
| Relation product (`macos-15-intel`) | [93920239264](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239264) | 20:37:42–20:42:11 | 13 | success |
| Relation product (`macos-26`) | [93920239443](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239443) | 20:42:17–20:46:13 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93920239419](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239419) | 20:37:42–20:41:00 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93920239354](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239354) | 20:37:42–20:40:02 | 12 | success |
| Product project check (`macos-15-intel`) | [93920239444](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239444) | 20:37:43–20:43:55 | 12 | success |
| Product project check (`macos-26`) | [93920239331](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239331) | 20:40:32–20:43:23 | 12 | success |
| Python compatibility (`3.12.13`) | [93920239501](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239501) | 20:37:42–20:38:06 | 12 | success |
| Python compatibility (`3.13.15`) | [93920239369](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239369) | 20:37:42–20:38:11 | 12 | success |
| Python compatibility (`3.14.3`) | [93920239401](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239401) | 20:37:42–20:38:09 | 12 | success |
| Python compatibility (`3.14.7`) | [93920239348](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239348) | 20:37:42–20:38:09 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93920239405](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239405) | 20:37:48–20:39:32 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93920239333](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239333) | 20:37:42–20:38:57 | 12 | success |
| SQLite (`macos-15-intel`) | [93920239359](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239359) | 20:37:43–20:40:45 | 12 | success |
| SQLite (`macos-26`) | [93920239358](https://github.com/progresshans/godj/actions/runs/31533890720/job/93920239358) | 20:40:39–20:42:00 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93920239275` passed root `make ci`; its raw log shows `internal/compiletest` passing in normal and race phases, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the bounded actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93920239378` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. After the recovered checkout retry, its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `93920239501`/`93920239369`/`93920239401`/`93920239348` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93920239289`/`93920239441`/`93920239264`/`93920239443` each independently emitted exact 687 actual records and 687 unique records. Independent raw-log reconstruction on every coordinate produced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- The physical `conformance/relationdeleteproduct/**` fixture remained exact 16 files, 62,538 content bytes and inventory SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`. Its checked-in `zz_godj_*.go` union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`; the test-only one-file overlay retained the logical exact 17-file view, 65,970 bytes and SHA-256 `29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f`, with no physical virtual-target residue.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, and relation-policy fingerprint v1 remained `eb6914dc35eb53e3df8c392f7a6dac52dc81f9bfd00910adf5fda3bcf99c9a58`.
- Parent-to-head changed exactly seven allowed Markdown documentation/work paths: `docs/ROADMAP.md`, `docs/TESTING.md`, `docs/adr/0031-relation-aware-project-facade-and-generated-upgrade-boundary.md`, `docs/status/CURRENT.md`, `docs/status/TEST_EVIDENCE.md`, `work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md`, and `work/README.md`; 141 insertions and 44 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. Exact `git diff` SHA-256 was `0434dd746a8eb9f0dda3f4c068f0829f406579fbeded8e8239b297180efd0ba0`; `git diff --check` was clean.
- Before this EVID-067 append, the terminal-head `docs/status/TEST_EVIDENCE.md` file was exact 483,614 bytes/SHA-256 `bf3125b2b0ebbd798a7678c6786bb5c3b9834c63fbdb531aa16d89d8ec508b1f`. Its historical body beginning at byte offset 524 with exact EVID-001..066 was 483,090 bytes/SHA-256 `ce8a2fbb184ae4989a2e14b44dec3ad8af31590c393a2ce89de432ed43a34930`.

Independent hosted evidence audit re-queried the unique live run, all 26 jobs and 326 steps, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the 69,597-byte relation payload on every coordinate, bounded the recovered exact-Darwin first-fetch SSL retry to the successful checkout action, and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence terminally closes only the exact seven-document GDJ-0031 terminal head and establishes only the clean pre-activation baseline for GDJ-0032. It does not itself activate, accept or implement GDJ-0032, prove this later EVID-067 append or the later activation-documentation tree, or authorize merging Draft PR #1. EVID-066/run `31531470440` is not reused as terminal proof; run `31533890720` must not be reused as proof of the later activation tree. No rerun or merge was performed; the later activation tree is `not run/pending` and needs separate exact-head CI.

## EVID-20260812-068 — GDJ-0032 Activation-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T21:24:23Z–2026-08-11T21:32:36Z; last job completed at 21:32:35Z
- Work/contract IDs: GDJ-0032 activation documentation only; ADR-0032 remains `Proposed`, GDJ-0032 remains active, Q-013 remains `Partial` and Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@2399cc44f6da975f154806f91eeee06dcca3b5a8` (`docs: activate production project facade`), parent `3d6612512e8887de8868a319650d54ad0721471b`, tree `7e1d15289c1ccdd2820885b4d2e339156ad7980b`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31537726792](https://github.com/progresshans/godj/actions/runs/31537726792), attempt 1, workflow run number 58. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact twelve-path documentation-only activation head passed all unchanged product gates. It proposes the bounded production project-facade first-publication packet and freezes its work scope without publishing a production companion or claiming implementation. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: hosted job failures/cancellations/skips and non-success recorded steps were 0; all 26 raw logs contained no `##[error]` or fatal checkout line. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. The production facade generator, checked-in companion, exact-14/physical-17 publication, runtime cache/query-count/session behavior and implementation diff were not present and are not proved by this activation run. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=2399cc44f6da975f154806f91eeee06dcca3b5a8`, status `completed`, conclusion `success`, created/started 2026-08-11T21:24:23Z and updated 2026-08-11T21:32:36Z.
- The PR has since advanced to a later head, so the mutable current PR-head field was not used as historical activation identity. Immutable run metadata and raw checkout logs identify `pull/1/merge`, exact activation head `2399cc44f6da975f154806f91eeee06dcca3b5a8` and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `2d3ee507c20c57185616c8b538847db4b9ae781f` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and activation head `2399cc44f6da975f154806f91eeee06dcca3b5a8`. Synthetic merge and exact head trees were both `7e1d15289c1ccdd2820885b4d2e339156ad7980b`, so executed contents were exact-head-equivalent. Every one of the 26 raw checkout logs contained exactly one fetch of that synthetic merge to `refs/remotes/pull/1/merge`, the exact synthetic/head/base checkout message, and the exact synthetic SHA from `git log -1`.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93932754261](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754261) | 21:24:26–21:30:33 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93932754312](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754312) | 21:24:26–21:25:48 | 14 | success |
| Project check (`ubuntu-22.04`) | [93932754317](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754317) | 21:24:26–21:25:33 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93932754448](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754448) | 21:24:26–21:25:11 | 12 | success |
| Project check (`macos-15-intel`) | [93932754491](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754491) | 21:26:34–21:28:32 | 12 | success |
| Project check (`macos-26`) | [93932754475](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754475) | 21:25:54–21:27:15 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93932754495](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754495) | 21:24:27–21:25:49 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93932754506](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754506) | 21:24:26–21:25:38 | 13 | success |
| Relation binding (`macos-15-intel`) | [93932754529](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754529) | 21:24:27–21:26:31 | 13 | success |
| Relation binding (`macos-26`) | [93932754398](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754398) | 21:24:26–21:25:52 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93932754467](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754467) | 21:24:27–21:28:16 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93932754580](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754580) | 21:24:26–21:27:03 | 13 | success |
| Relation product (`macos-15-intel`) | [93932754452](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754452) | 21:24:27–21:30:55 | 13 | success |
| Relation product (`macos-26`) | [93932754486](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754486) | 21:25:50–21:29:04 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93932754355](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754355) | 21:24:33–21:28:11 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93932754345](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754345) | 21:24:28–21:26:46 | 12 | success |
| Product project check (`macos-15-intel`) | [93932754445](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754445) | 21:26:57–21:32:35 | 12 | success |
| Product project check (`macos-26`) | [93932754338](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754338) | 21:24:27–21:26:55 | 12 | success |
| Python compatibility (`3.12.13`) | [93932754488](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754488) | 21:24:27–21:24:52 | 12 | success |
| Python compatibility (`3.13.15`) | [93932754606](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754606) | 21:24:26–21:24:49 | 12 | success |
| Python compatibility (`3.14.3`) | [93932754524](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754524) | 21:24:27–21:25:04 | 12 | success |
| Python compatibility (`3.14.7`) | [93932754411](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754411) | 21:24:27–21:24:58 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93932754443](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754443) | 21:24:26–21:26:13 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93932754400](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754400) | 21:24:29–21:25:49 | 12 | success |
| SQLite (`macos-15-intel`) | [93932754597](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754597) | 21:27:17–21:30:07 | 12 | success |
| SQLite (`macos-26`) | [93932754627](https://github.com/progresshans/godj/actions/runs/31537726792/job/93932754627) | 21:28:34–21:30:08 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93932754261` passed root `make ci`; its raw log shows the unchanged test-only overlay compiletest passing in normal and race phases, the bounded CGO-disabled package gate, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93932754312` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `93932754488`/`93932754606`/`93932754524`/`93932754411` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93932754467`/`93932754580`/`93932754452`/`93932754486` each independently emitted exact 687 actual records and 687 unique records. Independent raw-log reconstruction on every coordinate produced exact 687 run/687 pass/0 skip, 69,597 payload bytes and SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- The activation head retained the test-only GDJ-0031 compile shape: checked-in generated union exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`; physical `conformance/relationdeleteproduct/**` exact 16 Go files, 62,538 content bytes and SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`; virtual overlay logical exact 17 files, 65,970 bytes and SHA-256 `29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f`. `project_facade_spike.go.txt` remained 3,432 bytes/SHA-256 `2b67c5888b125a48dde536d1e8dd2bdb4028239d10dd33e70514817f35514fe7`; `external_consumer.go.txt` remained 1,877 bytes/SHA-256 `248fb25ac710d5c7469ecd89954ad2d3e2466e85f95571ac0ab5874dff891756`.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; product aggregate remains 127=`121 passing + 5 deviation + 1 oracle_locked`, relation 11 passing plus REL-002 `oracle_locked`. No source, workflow, generated product fixture, manifest, oracle or checksum artifact changed.
- Parent-to-head changed exactly twelve allowed documentation/work paths: `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/TESTING.md`, new `docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md`, new `work/0032-production-forward-project-facade-and-additive-first-publication.md`, and `work/README.md`; 786 insertions and 82 deletions. Exact `git diff` SHA-256 was `2af6b12bd71e8c41e01769ddd4873992c1c072550d688af89bc09b10a241b026`; `git diff --check` was clean.
- At the tested activation head, `docs/status/TEST_EVIDENCE.md` was exact 496,484 bytes/SHA-256 `685cd2787cbc8a3f5a97b2eb626a51f3deaddd187b861d6df3163e9f3d59e293`, with EVID-067 as its last evidence section. This EVID-068 append and all later implementation/completion records are documentation changes after the tested head and are not recursively proven by run `31537726792`.

Independent hosted evidence audit re-queried the unique exact-head run, all 26 jobs and 326 steps, the immutable run head, synthetic ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the 69,597-byte relation payload on every coordinate and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence proves only the exact twelve-document GDJ-0032 activation head. It does not accept or implement ADR-0032, publish a production facade/generator, prove runtime cache/session behavior, alter product classification, prove this later EVID-068 append or any implementation/completion tree, or authorize merging Draft PR #1. Terminal baseline run `31533890720` is not reused as activation proof; activation run `31537726792` is not reused as implementation proof. No rerun or merge was performed.

## EVID-20260812-069 — GDJ-0032 GitHub-hosted Exact 26-job Production-facade Implementation-head CI

- Date/time: 2026-08-11T22:19:08Z–2026-08-11T22:27:15Z; last job completed at 22:27:15Z
- Work/contract IDs: GDJ-0032 production forward project facade first publication; ADR-0032 remains `Proposed` and GDJ-0032 remains active at this exact tested head pending completion documentation; Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@ba2fa0fa30f32abf3d70598c7a3a4e4334a43020` (`feat: publish production project facade`), parent `2399cc44f6da975f154806f91eeee06dcca3b5a8`, tree `7387693e344efc3d8acb17767f4d0fc7a9a79ae4`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31541883680](https://github.com/progresshans/godj/actions/runs/31541883680), attempt 1, workflow run number 59. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact implementation head publishes one deterministic project-only production facade companion, removes the test-only overlay, and passes generated-source, no-overlay external-consumer, runtime cache/query-count, invalid-state, binder-precedence and callback-local session gates. Existing generated exact 13 remains byte-identical and the additive generated union is exact 14; the physical relation-delete product is exact 17. Product classification remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: hosted job failures/cancellations/skips and non-success recorded steps were 0; all 26 raw logs contained no `##[error]` or fatal checkout line. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. Reverse managers/chaining, multi-hop or multiple-edge eager selection, target-wrapper pointer identity/downstream cache, REL-002 FK assignment/save/cache invalidation, write/delete facade, general `godj generate` coordinated upgrade/repair, Windows, PostgreSQL/MySQL and broad non-SQLite support remain outside this bounded implementation. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=ba2fa0fa30f32abf3d70598c7a3a4e4334a43020`, status `completed`, conclusion `success`, created/started 2026-08-11T22:19:08Z and updated 2026-08-11T22:27:15Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `3fffc1ef41c31bd19b2f6fa228fff6a7df516f28` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `ba2fa0fa30f32abf3d70598c7a3a4e4334a43020`. Synthetic merge and exact head trees were both `7387693e344efc3d8acb17767f4d0fc7a9a79ae4`, so executed contents were exact-head-equivalent. Every one of the 26 raw checkout logs contained exactly one fetch of that synthetic merge to `refs/remotes/pull/1/merge`, the exact synthetic/head/base checkout message, and the exact synthetic SHA from `git log -1`.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93945905628](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905628) | 22:19:11–22:25:19 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93945905596](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905596) | 22:19:11–22:20:11 | 14 | success |
| Project check (`ubuntu-22.04`) | [93945905668](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905668) | 22:19:11–22:20:21 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93945905646](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905646) | 22:19:10–22:19:56 | 12 | success |
| Project check (`macos-15-intel`) | [93945905798](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905798) | 22:20:32–22:22:13 | 12 | success |
| Project check (`macos-26`) | [93945905691](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905691) | 22:23:25–22:24:32 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93945905728](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905728) | 22:19:11–22:20:50 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93945905743](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905743) | 22:19:11–22:20:22 | 13 | success |
| Relation binding (`macos-15-intel`) | [93945905742](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905742) | 22:19:11–22:21:43 | 13 | success |
| Relation binding (`macos-26`) | [93945905741](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905741) | 22:19:12–22:20:30 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93945905801](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905801) | 22:19:11–22:22:59 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93945905739](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905739) | 22:19:10–22:21:43 | 13 | success |
| Relation product (`macos-15-intel`) | [93945905598](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905598) | 22:19:13–22:27:15 | 13 | success |
| Relation product (`macos-26`) | [93945905796](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905796) | 22:20:12–22:23:48 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93945905917](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905917) | 22:19:11–22:22:32 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93945905933](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905933) | 22:19:10–22:21:31 | 12 | success |
| Product project check (`macos-15-intel`) | [93945905647](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905647) | 22:19:11–22:25:38 | 12 | success |
| Product project check (`macos-26`) | [93945905672](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905672) | 22:21:46–22:24:06 | 12 | success |
| Python compatibility (`3.12.13`) | [93945905757](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905757) | 22:19:11–22:19:33 | 12 | success |
| Python compatibility (`3.13.15`) | [93945905821](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905821) | 22:19:11–22:19:40 | 12 | success |
| Python compatibility (`3.14.3`) | [93945905721](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905721) | 22:19:11–22:19:43 | 12 | success |
| Python compatibility (`3.14.7`) | [93945905793](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905793) | 22:19:11–22:19:39 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93945905790](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905790) | 22:19:17–22:20:58 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93945905782](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905782) | 22:19:13–22:20:32 | 12 | success |
| SQLite (`macos-15-intel`) | [93945906276](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945906276) | 22:23:51–22:26:51 | 12 | success |
| SQLite (`macos-26`) | [93945905788](https://github.com/progresshans/godj/actions/runs/31541883680/job/93945905788) | 22:22:15–22:23:23 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93945905628` passed root `make ci`; its raw log shows the new codegen, relation-delete product and physical no-overlay compiletest in normal and race phases, the bounded CGO-disabled package gate, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93945905596` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `93945905757`/`93945905821`/`93945905721`/`93945905793` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93945905801`/`93945905739`/`93945905598`/`93945905796` each independently emitted exact 697 actual records and 697 unique records. Independent raw-log reconstruction on every coordinate produced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- Existing legacy generated union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`. The additive generated union is exact 14 files, 39,243 bytes and SHA-256 `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`; the physical `conformance/relationdeleteproduct/**` fixture is exact 17 Go files, 94,439 content bytes and SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd` under the canonical sorted path/NUL/decimal-length/NUL/content encoding.
- The published `project/zz_godj_relation_facade.go` is exact 13,103 bytes/SHA-256 `82b6bf53c2b5b470fef2b33695b638c41316d6054c004ff5bcdc5243617dfed4`, embeds generator version `godj-codegen-rel-facade-project-v1` and input SHA-256 `476f27b868f6e2d917d25ffaa9ec2bd3a978ffee16354e3e6b3bcc387c2d05c7`. `product_test.go` is 35,610 bytes/SHA-256 `fc6608b97c50f8cace8a484f7869b81faaeda19f8996527b380eecb60bdf3aba`. Generated-candidate comparison, exact-13 preservation, all-model roots/wrappers, lazy/eager cache and evaluation ownership, nil/typed-nil/copy I/O-zero failures, binder-before-backend precedence, and callback-local `db.Session` all passed.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; protocol retained separate exact-13 and exact-14 rows. Product aggregate remains 127=`121 passing + 5 deviation + 1 oracle_locked`, relation 11 passing plus REL-002 `oracle_locked`; no contract, actual-output, oracle or checksum artifact changed.
- Parent-to-head changed exactly sixteen allowed implementation paths: `.github/workflows/ci.yml`, `codegen/project_relation_facade.go`, `codegen/project_relation_facade_test.go`, `codegen/project_relation_object.go`, `codegen/project_relation_object_test.go`, `codegen/testdata/relation_facade/project.golden`, `conformance/README.md`, `conformance/internal/protocol/migration_project_check_artifacts_test.go`, `conformance/internal/protocol/relation_artifacts_test.go`, `conformance/relationdeleteproduct/product_test.go`, `conformance/relationdeleteproduct/project/zz_godj_relation_facade.go`, `docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md`, `internal/compiletest/compile_test.go`, `internal/compiletest/testdata/relation_facade/external_consumer.go.txt`, deleted `internal/compiletest/testdata/relation_facade/project_facade_spike.go.txt`, and `work/0032-production-forward-project-facade-and-additive-first-publication.md`; 3,771 insertions and 1,068 deletions. Exact `git diff` SHA-256 was `563b283f84165d0dd9cf928e4a07d6d7b1a415e30f4be1b094ef3a268658c0f4`; `git diff --check` was clean.
- At the tested implementation head, `docs/status/TEST_EVIDENCE.md` remained exact 496,484 bytes/SHA-256 `685cd2787cbc8a3f5a97b2eb626a51f3deaddd187b861d6df3163e9f3d59e293`, with EVID-067 as its last evidence section. The later EVID-068 activation record and this EVID-069 append are documentation changes after the tested head and are not recursively proven by run `31541883680`.

Independent hosted evidence audit re-queried the unique live run, all 26 jobs and 326 steps, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the 70,659-byte relation payload on every coordinate and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence proves only the exact GDJ-0032 production-facade implementation head. It proves the bounded generated companion, actual product runtime/compile behavior and unchanged contract classification; it does not by itself transition ADR-0032 from Proposed or GDJ-0032 from active, prove later completion documentation/evidence, implement the excluded reverse/REL-002/write/upgrade/backend scope, or authorize merging Draft PR #1. Activation run `31537726792` is not reused as implementation proof; implementation run `31541883680` must not be reused as proof of a later documentation head. No rerun or merge was performed.

## EVID-20260812-070 — GDJ-0032 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-11T22:53:13Z–2026-08-11T23:05:11Z; last job completed at 23:05:10Z
- Work/contract IDs: GDJ-0032 completion documentation; GDJ-0032 is completed and ADR-0032 is Accepted only for the bounded Gate 0 production forward facade and additive single-companion first-publication; Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@6089e214ee7a0b564f6636e65e6d6f96c167e2c6` (`docs: complete production project facade`), parent `ba2fa0fa30f32abf3d70598c7a3a4e4334a43020`, tree `44bc595f780cc44f868d85f47a199415798860ec`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31544273477](https://github.com/progresshans/godj/actions/runs/31544273477), attempt 1, workflow run number 60. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact eleven-path completion-documentation transition from implementation head `ba2fa0fa...` to `6089e214...` passed all unchanged product gates. GDJ-0032 is completed and ADR-0032 is Accepted only for the bounded Gate 0 production forward facade and additive single-companion first-publication; Q-013 remains `Partial`, Q-017 remains P1/open, and active/ready work remains empty. Existing generated exact 13 remains byte-identical, the additive generated union remains exact 14 and the physical relation-delete product remains exact 17. Product classification remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: hosted job failures/cancellations/skips and non-success recorded steps were 0; all 26 raw logs contained no `##[error]` or fatal checkout line. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. Reverse managers/chaining, multi-hop or multiple-edge eager selection, target-wrapper pointer identity/downstream cache, REL-002 FK assignment/save/cache invalidation, write/delete facade, general `godj generate` coordinated upgrade/repair, callback-after-return lifetime enforcement, Windows, PostgreSQL/MySQL and broad non-SQLite support remain outside the bounded Accepted/completed scope. This EVID-070 append and its later exact seven-path terminal evidence/status tree are documentation changes after the tested completion-documentation head and are not recursively proved by run `31544273477`; that later tree requires separate exact-head CI. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=6089e214ee7a0b564f6636e65e6d6f96c167e2c6`, status `completed`, conclusion `success`, created/started 2026-08-11T22:53:13Z and updated 2026-08-11T23:05:11Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact completion-documentation head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `0ce93b83a366d393ab52ebb9ce9a4efa145f6121` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `6089e214ee7a0b564f6636e65e6d6f96c167e2c6`. Synthetic merge and exact head trees were both `44bc595f780cc44f868d85f47a199415798860ec`, so executed contents were exact-head-equivalent. Every one of the 26 raw checkout logs contained exactly one fetch of that synthetic merge to `refs/remotes/pull/1/merge`, exactly one checkout of that ref, the exact synthetic/head/base checkout message, and the exact synthetic SHA from `git log -1`.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [93953298412](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298412) | 22:53:17–22:59:52 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [93953298311](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298311) | 22:53:16–22:54:56 | 14 | success |
| Project check (`ubuntu-22.04`) | [93953298455](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298455) | 22:53:16–22:54:04 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [93953298544](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298544) | 22:53:18–22:54:06 | 12 | success |
| Project check (`macos-15-intel`) | [93953298496](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298496) | 22:54:59–22:57:19 | 12 | success |
| Project check (`macos-26`) | [93953298688](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298688) | 22:57:14–22:58:02 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [93953298497](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298497) | 22:53:17–22:54:52 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [93953298515](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298515) | 22:53:16–22:54:29 | 13 | success |
| Relation binding (`macos-15-intel`) | [93953298586](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298586) | 22:56:27–22:58:10 | 13 | success |
| Relation binding (`macos-26`) | [93953298448](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298448) | 22:53:17–22:54:15 | 13 | success |
| Relation product (`ubuntu-22.04`) | [93953298577](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298577) | 22:53:16–22:57:17 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [93953298431](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298431) | 22:53:16–22:55:52 | 13 | success |
| Relation product (`macos-15-intel`) | [93953298426](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298426) | 22:56:08–23:05:10 | 13 | success |
| Relation product (`macos-26`) | [93953298378](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298378) | 22:53:17–22:56:05 | 13 | success |
| Product project check (`ubuntu-22.04`) | [93953298387](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298387) | 22:53:16–22:56:35 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [93953298443](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298443) | 22:53:16–22:55:48 | 12 | success |
| Product project check (`macos-15-intel`) | [93953298421](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298421) | 22:54:18–23:03:56 | 12 | success |
| Product project check (`macos-26`) | [93953298433](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298433) | 22:53:16–22:55:42 | 12 | success |
| Python compatibility (`3.12.13`) | [93953298568](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298568) | 22:53:16–22:53:36 | 12 | success |
| Python compatibility (`3.13.15`) | [93953298401](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298401) | 22:53:16–22:53:41 | 12 | success |
| Python compatibility (`3.14.3`) | [93953298467](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298467) | 22:53:16–22:53:46 | 12 | success |
| Python compatibility (`3.14.7`) | [93953298418](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298418) | 22:53:16–22:53:47 | 12 | success |
| SQLite (`ubuntu-22.04`) | [93953298440](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298440) | 22:53:16–22:55:04 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [93953298436](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298436) | 22:53:16–22:54:32 | 12 | success |
| SQLite (`macos-15-intel`) | [93953298420](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298420) | 22:53:17–22:56:25 | 12 | success |
| SQLite (`macos-26`) | [93953298468](https://github.com/progresshans/godj/actions/runs/31544273477/job/93953298468) | 22:55:45–22:57:12 | 12 | success |

Hosted gate details:

- Full Ubuntu job `93953298412` passed root `make ci`; its raw log shows the production facade codegen, relation-delete product and physical no-overlay `internal/compiletest` passing in normal and race phases, the bounded CGO-disabled package gate, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `93953298311` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `93953298568`/`93953298401`/`93953298467`/`93953298418` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `93953298577`/`93953298431`/`93953298426`/`93953298378` each independently emitted exact 697 actual records and 697 unique records. Independent raw-log reconstruction on every coordinate produced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- Existing legacy generated union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`. The additive generated union remained exact 14 files, 39,243 bytes and SHA-256 `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`; the physical `conformance/relationdeleteproduct/**` fixture remained exact 17 Go files, 94,439 content bytes and SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd` under the canonical sorted path/NUL/decimal-length/NUL/content encoding.
- The published `project/zz_godj_relation_facade.go` remained exact 13,103 bytes/SHA-256 `82b6bf53c2b5b470fef2b33695b638c41316d6054c004ff5bcdc5243617dfed4`, embeds generator version `godj-codegen-rel-facade-project-v1` and input SHA-256 `476f27b868f6e2d917d25ffaa9ec2bd3a978ffee16354e3e6b3bcc387c2d05c7`. `product_test.go` remained 35,610 bytes/SHA-256 `fc6608b97c50f8cace8a484f7869b81faaeda19f8996527b380eecb60bdf3aba`. The completion transition changed no product/codegen/workflow byte from the separately tested implementation head.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; protocol retained separate exact-13 and exact-14 rows. Product aggregate remains 127=`121 passing + 5 deviation + 1 oracle_locked`, relation 11 passing plus REL-002 `oracle_locked`; no contract, actual-output, oracle or checksum artifact changed.
- Parent-to-head changed exactly eleven allowed Markdown documentation/work paths: `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/TESTING.md`, `docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0032-production-forward-project-facade-and-additive-first-publication.md`, and `work/README.md`; 316 insertions and 147 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. Exact `git diff` SHA-256 was `b00a6ba0a914275f26a4dbda25303de91636b6428e57241f64887e45f33db2d3`; `git diff --check` was clean. Under the independent sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/content encoding, the exact eleven-path head-content aggregate was 917,136 content bytes/SHA-256 `c2f78289f18b961e1541179cfd700e48f4802f8bbfcf1125640c4a2cf44fc71b`.
- Before this EVID-070 append, the completion-documentation-head `docs/status/TEST_EVIDENCE.md` file was exact 523,761 bytes/SHA-256 `bc62f8b5d221a9338a56aa921e2198c28edfdba0fe801cb5d87d2d9d0b43b0a5`. Its historical body beginning at byte offset 524 with exact EVID-001..069 was 523,237 bytes/SHA-256 `e88c4325d95cdc742b1fd5b4bf3a8dcf0f2b09560dbf33f860d4878a9a18701f`; the EVID-001..067 sub-prefix ending after EVID-067's final content newline remained byte-identical at 495,960 bytes/SHA-256 `d8024da575bc8463b30007eda3eb43659b0523696a35c8634745e908aa919b86`.

Independent hosted evidence audit re-queried the unique exact-head run, all 26 jobs and 326 steps, the immutable run head, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the 70,659-byte relation payload on every coordinate and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence closes only the exact eleven-document GDJ-0032 completion-documentation head. It does not widen ADR-0032 beyond the bounded Gate 0 production forward facade and additive single-companion first-publication, close Q-013/Q-017, implement the excluded reverse/REL-002/write/upgrade/backend scope, prove this later EVID-070 append or the later exact seven-path terminal evidence/status tree, or authorize merging Draft PR #1. EVID-068/run `31537726792` and EVID-069/run `31541883680` are not reused as completion proof; run `31544273477` must not be reused as proof of a later documentation head. No rerun or merge was performed.

## EVID-20260812-071 — GDJ-0032 Terminal Exact-head CI and GDJ-0033 Activation Baseline

- Date/time: 2026-08-12T04:33:05Z–2026-08-12T04:39:51Z; last job completed at 04:39:50Z
- Work/contract IDs: GDJ-0032 terminal closure; GDJ-0033 clean activation baseline; Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed` (`docs: record production facade terminal evidence`), parent `6089e214ee7a0b564f6636e65e6d6f96c167e2c6`, tree `b14494f3f04a6ee6106124ee13a1025cfd06547b`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31563615648](https://github.com/progresshans/godj/actions/runs/31563615648), attempt 1, workflow run number 61. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact seven-path GDJ-0032 terminal evidence/status head passed unchanged product gates and is the clean baseline from which GDJ-0033 documentation may be activated. GDJ-0032 remains completed and ADR-0032 remains Accepted only for the bounded Gate 0 production forward facade and additive single-companion first-publication; Q-013 remains `Partial`, Q-017 remains P1/open and active/ready work remains empty at this head. Existing generated exact 13 remains byte-identical, the additive generated union remains exact 14 and the physical relation-delete product remains exact 17. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: terminal job failures/cancellations/skips and non-success recorded steps were 0; all 26 raw logs contained no `##[error]` or fatal checkout line. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. Reverse managers/chaining, multi-hop or multiple-edge eager selection, target-wrapper pointer identity/downstream cache, REL-002 FK assignment/save/cache invalidation, write/delete facade, general `godj generate` coordinated upgrade/repair, callback-after-return lifetime enforcement, Windows, PostgreSQL/MySQL and broad non-SQLite support remain outside the bounded Accepted/completed scope. GDJ-0033's work/ADR/scope was not present or tested by this run. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`, status `completed`, conclusion `success`, created/started 2026-08-12T04:33:05Z and updated 2026-08-12T04:39:51Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact terminal head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `a903305ccfb133325058a3962724d13e198e6471` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`. Synthetic merge and exact head trees were both `b14494f3f04a6ee6106124ee13a1025cfd06547b`, so executed contents were exact-head-equivalent. Every one of the 26 raw checkout logs contained exactly one fetch of that synthetic merge to `refs/remotes/pull/1/merge`, exactly one checkout of that ref, the exact synthetic/head/base checkout message, and the exact synthetic SHA from `git log -1`.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [94010834687](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834687) | 04:33:08–04:39:50 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [94010834603](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834603) | 04:35:39–04:37:03 | 14 | success |
| Project check (`ubuntu-22.04`) | [94010834709](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834709) | 04:33:08–04:34:23 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [94010834677](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834677) | 04:33:11–04:34:03 | 12 | success |
| Project check (`macos-15-intel`) | [94010834774](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834774) | 04:36:25–04:38:13 | 12 | success |
| Project check (`macos-26`) | [94010834733](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834733) | 04:38:09–04:39:16 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [94010834764](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834764) | 04:33:08–04:34:52 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [94010834681](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834681) | 04:33:08–04:34:15 | 13 | success |
| Relation binding (`macos-15-intel`) | [94010834685](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834685) | 04:33:11–04:35:37 | 13 | success |
| Relation binding (`macos-26`) | [94010834688](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834688) | 04:36:21–04:38:08 | 13 | success |
| Relation product (`ubuntu-22.04`) | [94010834686](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834686) | 04:33:08–04:37:12 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [94010834576](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834576) | 04:33:08–04:35:49 | 13 | success |
| Relation product (`macos-15-intel`) | [94010834634](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834634) | 04:33:08–04:39:20 | 13 | success |
| Relation product (`macos-26`) | [94010834722](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834722) | 04:37:04–04:39:50 | 13 | success |
| Product project check (`ubuntu-22.04`) | [94010834683](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834683) | 04:33:08–04:36:23 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [94010834623](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834623) | 04:33:08–04:35:29 | 12 | success |
| Product project check (`macos-15-intel`) | [94010834616](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834616) | 04:33:09–04:38:07 | 12 | success |
| Product project check (`macos-26`) | [94010834643](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834643) | 04:33:09–04:36:23 | 12 | success |
| Python compatibility (`3.12.13`) | [94010834775](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834775) | 04:33:08–04:33:26 | 12 | success |
| Python compatibility (`3.13.15`) | [94010834673](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834673) | 04:33:08–04:33:36 | 12 | success |
| Python compatibility (`3.14.3`) | [94010834702](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834702) | 04:33:08–04:33:34 | 12 | success |
| Python compatibility (`3.14.7`) | [94010834632](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834632) | 04:33:07–04:33:29 | 12 | success |
| SQLite (`ubuntu-22.04`) | [94010834696](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834696) | 04:33:08–04:34:44 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [94010834653](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834653) | 04:33:08–04:34:27 | 12 | success |
| SQLite (`macos-15-intel`) | [94010834675](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834675) | 04:33:08–04:36:18 | 12 | success |
| SQLite (`macos-26`) | [94010834736](https://github.com/progresshans/godj/actions/runs/31563615648/job/94010834736) | 04:38:10–04:39:33 | 12 | success |

Hosted gate details:

- Full Ubuntu job `94010834687` passed root `make ci`; its raw log shows the production facade codegen, relation-delete product and physical no-overlay `internal/compiletest` passing in normal and race phases, the bounded CGO-disabled package gate, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `94010834603` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `94010834775`/`94010834673`/`94010834702`/`94010834632` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `94010834686`/`94010834576`/`94010834634`/`94010834722` each independently emitted exact 697 actual records and 697 unique records. Independent raw-log reconstruction on every coordinate produced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- Existing legacy generated union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`. The additive generated union remained exact 14 files, 39,243 bytes and SHA-256 `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`; the physical `conformance/relationdeleteproduct/**` fixture remained exact 17 Go files, 94,439 content bytes and SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd` under the canonical sorted path/NUL/decimal-length/NUL/content encoding.
- The published `project/zz_godj_relation_facade.go` remained exact 13,103 bytes/SHA-256 `82b6bf53c2b5b470fef2b33695b638c41316d6054c004ff5bcdc5243617dfed4`, embeds generator version `godj-codegen-rel-facade-project-v1` and input SHA-256 `476f27b868f6e2d917d25ffaa9ec2bd3a978ffee16354e3e6b3bcc387c2d05c7`. `product_test.go` remained 35,610 bytes/SHA-256 `fc6608b97c50f8cace8a484f7869b81faaeda19f8996527b380eecb60bdf3aba`.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; protocol retained separate exact-13 and exact-14 rows. Product aggregate remains 127=`121 passing + 5 deviation + 1 oracle_locked`, relation 11 passing plus REL-002 `oracle_locked`; no contract, actual-output, oracle or checksum artifact changed.
- Parent-to-head changed exactly seven allowed Markdown documentation/work paths: `docs/ROADMAP.md`, `docs/TESTING.md`, `docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md`, `docs/status/CURRENT.md`, `docs/status/TEST_EVIDENCE.md`, `work/0032-production-forward-project-facade-and-additive-first-publication.md`, and `work/README.md`; 138 insertions and 49 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. Exact `git diff` SHA-256 was `27f63c554efc31a7b3d1ffe87caa97e29353a41127638028d36ff92e927483a1`; `git diff --check` was clean. Under the independent sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/content encoding, the exact seven-path head-content aggregate was 845,418 content bytes/SHA-256 `3e921e487450106e48a27678db731cd1be2d973fc24cfef8ffbe37f14910754f`.
- Before this EVID-071 append, the terminal-head `docs/status/TEST_EVIDENCE.md` file was exact 538,482 bytes/SHA-256 `fa7763c6b5c1393103f094d6df8e6748c7ce0d273f64c048c89e0d996157a1d5`. Its historical body beginning at byte offset 524 with exact EVID-001..070 was 537,958 bytes/SHA-256 `a0a21377de7a5bd92e59daa30d794ffb5353ee50aa6b6621843d37e9ff3520c7`. Within that body, the EVID-001..069 prefix was exact 523,237 bytes/SHA-256 `e88c4325d95cdc742b1fd5b4bf3a8dcf0f2b09560dbf33f860d4878a9a18701f`, followed by one LF separator and the exact 14,720-byte EVID-070 section/SHA-256 `5bd3c24ed03723405beca2b91164e3521242598c7c4b35f9d2375a4c2c973aa6`.

Independent hosted evidence audit re-queried the unique live run, all 26 jobs and 326 steps, the immutable run head, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the 70,659-byte relation payload on every coordinate, reproduced the exact seven-path diff and EVID-070 byte identity, confirmed the exact local head was clean and upstream-aligned, and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence terminally closes only the exact seven-document GDJ-0032 terminal head and establishes only the clean pre-activation baseline for GDJ-0033. It does not itself activate, accept or implement GDJ-0033, prove this later EVID-071 append or the later activation-documentation tree, or authorize merging Draft PR #1. EVID-070/run `31544273477` is not reused as terminal proof; run `31563615648` must not be reused as proof of the later activation tree. No rerun or merge was performed; the later activation tree is `not run/pending` and needs separate exact-head CI.

## EVID-20260812-072 — GDJ-0033 Activation-documentation Exact-head CI

- Date/time: 2026-08-12T05:26:08Z–2026-08-12T05:35:12Z; last job completed at 05:35:11Z
- Work/contract IDs: GDJ-0033 activation documentation only; ADR-0033 remains `Proposed`; Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@a4a627a5702ac9db4ee8c39706ff098783a9c5e6` (`docs: activate Django-first relation assignment`), parent `8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`, tree `76cee6a545943c7bdaefc471a10505bd60e6a6d1`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; GoDj SQLite 3.53.3 existing product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31566524953](https://github.com/progresshans/godj/actions/runs/31566524953), attempt 1, workflow run number 62. GitHub returned exactly one `pull_request` workflow run for this head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 14-path GDJ-0033 activation-documentation head passed unchanged product gates. GDJ-0033 is active/ready only for the documented Phase A exact-object Django audit, Phase B compile prototype and Phase C decision freeze; ADR-0033 remains `Proposed`, REL-002 remains `oracle_locked`, and no assignment/save product implementation is claimed. Existing generated exact 13 remains byte-identical, the additive generated union remains exact 14 and the physical relation-delete product remains exact 17. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: terminal job failures/cancellations/skips and non-success recorded steps were 0; all 26 raw logs contained no `##[error]` or fatal checkout line. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. Phase A findings, the Phase B non-product prototype, the Phase C decision document, ADR acceptance, REL-002 implementation and publication were not present or tested by this run. The separate generated `select_related` resolve/bind cause-loss P2, reverse managers/chaining, multi-hop or multiple-edge eager selection, write/delete facade, coordinated all-project generation repair, callback-after-return lifetime enforcement, Windows, PostgreSQL/MySQL and broad non-SQLite support remain outside this activation proof. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=a4a627a5702ac9db4ee8c39706ff098783a9c5e6`, status `completed`, conclusion `success`, created/started 2026-08-12T05:26:08Z and updated 2026-08-12T05:35:12Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact activation head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `ad723cb431dbd634cf5af1f597152d138522c241` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`. Synthetic merge and exact head trees were both `76cee6a545943c7bdaefc471a10505bd60e6a6d1`, so executed contents were exact-head-equivalent.
- Every one of the 26 raw checkout logs contained exactly one fetch of `+ad723cb431dbd634cf5af1f597152d138522c241` to `refs/remotes/pull/1/merge`, exactly one checkout of that ref, the matching `HEAD is now at ad723cb Merge a4a627... into f8a5e20...` message and the exact synthetic SHA from `git log -1`. No log showed checkout of another tree.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [94019341893](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341893) | 05:26:11–05:32:28 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [94019341907](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341907) | 05:26:11–05:27:27 | 14 | success |
| Project check (`ubuntu-22.04`) | [94019342072](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342072) | 05:26:11–05:27:16 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [94019341898](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341898) | 05:26:10–05:26:56 | 12 | success |
| Project check (`macos-15-intel`) | [94019341920](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341920) | 05:27:29–05:29:03 | 12 | success |
| Project check (`macos-26`) | [94019341937](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341937) | 05:27:44–05:28:36 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [94019342005](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342005) | 05:26:11–05:27:41 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [94019342024](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342024) | 05:26:11–05:27:24 | 13 | success |
| Relation binding (`macos-15-intel`) | [94019341973](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341973) | 05:28:07–05:30:12 | 13 | success |
| Relation binding (`macos-26`) | [94019341988](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341988) | 05:26:11–05:27:42 | 13 | success |
| Relation product (`ubuntu-22.04`) | [94019342085](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342085) | 05:26:11–05:30:09 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [94019342090](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342090) | 05:26:13–05:28:45 | 13 | success |
| Relation product (`macos-15-intel`) | [94019341977](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341977) | 05:26:12–05:35:11 | 13 | success |
| Relation product (`macos-26`) | [94019342039](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342039) | 05:30:12–05:33:50 | 13 | success |
| Product project check (`ubuntu-22.04`) | [94019342010](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342010) | 05:26:10–05:29:29 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [94019341970](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341970) | 05:26:13–05:28:33 | 12 | success |
| Product project check (`macos-15-intel`) | [94019341926](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341926) | 05:26:11–05:33:24 | 12 | success |
| Product project check (`macos-26`) | [94019342006](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019342006) | 05:28:39–05:31:55 | 12 | success |
| Python compatibility (`3.12.13`) | [94019341924](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341924) | 05:26:11–05:26:34 | 12 | success |
| Python compatibility (`3.13.15`) | [94019341887](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341887) | 05:26:11–05:26:30 | 12 | success |
| Python compatibility (`3.14.3`) | [94019341931](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341931) | 05:26:11–05:26:39 | 12 | success |
| Python compatibility (`3.14.7`) | [94019341951](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341951) | 05:26:11–05:26:41 | 12 | success |
| SQLite (`ubuntu-22.04`) | [94019341963](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341963) | 05:26:11–05:27:53 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [94019341950](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341950) | 05:26:11–05:27:29 | 12 | success |
| SQLite (`macos-15-intel`) | [94019341890](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341890) | 05:26:11–05:28:04 | 12 | success |
| SQLite (`macos-26`) | [94019341934](https://github.com/progresshans/godj/actions/runs/31566524953/job/94019341934) | 05:29:06–05:30:09 | 12 | success |

Hosted gate details:

- Full Ubuntu job `94019341893` passed root `make ci` on Go 1.26.5 linux/amd64. Its raw log shows the production facade codegen, relation-delete product and physical no-overlay `internal/compiletest` passing in normal and race phases, the bounded CGO-disabled package gate, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `94019341907` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `94019341924`/`94019341887`/`94019341931`/`94019341951` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `94019342085`/`94019342090`/`94019341977`/`94019342039` each independently emitted exact 697 actual records and 697 unique records. Independent raw-log reconstruction on every coordinate produced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- Existing legacy generated union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`. The additive generated union remained exact 14 files, 39,243 bytes and SHA-256 `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`; the physical `conformance/relationdeleteproduct/**` fixture remained exact 17 Go files, 94,439 content bytes and SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd` under the canonical sorted path/NUL/decimal-length/NUL/content encoding.
- The published `project/zz_godj_relation_facade.go` remained exact 13,103 bytes/SHA-256 `82b6bf53c2b5b470fef2b33695b638c41316d6054c004ff5bcdc5243617dfed4`, embeds generator version `godj-codegen-rel-facade-project-v1` and input SHA-256 `476f27b868f6e2d917d25ffaa9ec2bd3a978ffee16354e3e6b3bcc387c2d05c7`. `product_test.go` remained 35,610 bytes/SHA-256 `fc6608b97c50f8cace8a484f7869b81faaeda19f8996527b380eecb60bdf3aba`.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; protocol retained separate exact-13 and exact-14 rows. Product aggregate remains 127=`121 passing + 5 deviation + 1 oracle_locked`, relation 11 passing plus REL-002 `oracle_locked`; no contract, actual-output, oracle or checksum artifact changed.
- Parent-to-head changed exactly 14 allowed Markdown documentation/work paths: `docs/COMPATIBILITY.md`, `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/SOURCES.md`, `docs/TESTING.md`, `docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0032-production-forward-project-facade-and-additive-first-publication.md`, `work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, and `work/README.md`; 916 insertions and 81 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. Exact `git diff --binary 8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed..a4a627a5702ac9db4ee8c39706ff098783a9c5e6 | shasum -a 256` was `80b64b7ca225da700834b9d7a4e347e61306c515eb28bd7092e75dfac16378c8`; `git diff --check` was clean. Under the independent canonical sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/content encoding, the exact 14-path head-content aggregate was 1,059,404 content bytes, 1,059,995 encoded bytes and SHA-256 `4bbb6aea3a5d737e130b29e03b388508b416c02381fb3b2e6d8dee35869bf92e`.
- Before this EVID-072 append, activation-head `docs/status/TEST_EVIDENCE.md` was exact 552,808 bytes/SHA-256 `f47bb72b0448fed1f6939a0f0706507bd8fa35810708e12c6d6d2e9fd0f44ab9`. It contained the exact EVID-001..071 history; EVID-071 began at byte offset 538,483 and its section was exact 14,325 bytes/SHA-256 `965e6be6ac113479a7682f4b3456d14222bdb1219d86ec53f27902a39fafb0ff`.

Independent activation evidence audit re-queried the unique live run, all 26 jobs and 326 steps, the immutable run head, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. It independently reconstructed the identical 70,659-byte relation payload on every coordinate, reproduced the exact 14-path activation diff and content aggregate, confirmed the exact local head was clean and upstream-aligned, and reported P0/P1/P2/P3=`0/0/0/0`.

This evidence proves only the exact GDJ-0033 activation-documentation head `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`. It does not prove or accept the later Phase A/B/C decision-freeze document, does not prove a non-product prototype or REL-002 implementation, does not change Q-013/Q-017 or any product classification, and does not authorize merging Draft PR #1. EVID-071/run `31563615648` is not reused as activation proof; run `31566524953` must not be reused for the later decision-document head or any implementation head. No rerun, stage, commit, push or merge was performed for a later tree; later exact-head CI remains `not run/pending`.

## EVID-20260812-073 — GDJ-0033 Django Semantics and No-product Forward-write Feasibility

- Date/time: 2026-08-12 (Asia/Seoul environment); individual command wall-clock interval not retained
- Work/contract IDs: GDJ-0033 Phase A/B/C decision freeze; REL-002 only; ADR-0033 decision evidence. Q-013 remains `Partial`, Q-017 remains P1/open and product remains unchanged at REL-001/003/004/005/006/007/008/009/010/011/012 `passing` plus REL-002 `oracle_locked`.
- Primary checkout: `codex/revision-fenced-migration-lifecycle@a4a627a5702ac9db4ee8c39706ff098783a9c5e6`, tree `76cee6a545943c7bdaefc471a10505bd60e6a6d1`; primary `git status --porcelain=v1` was empty, with exact empty-stream SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`.
- Scope/result: Phase A audited the exact pinned Django 6.1 object, not the moving local Django checkout, and Phase B compiled and exercised a deliberately unpublished five-file prototype in a detached temporary worktree. The prototype closed all P0/P1/P2/P3 findings on its frozen diff (`0/0/0/0`) and established feasibility for the accepted decision surface. It did not edit primary source or checked-in product fixtures, publish generated bytes, implement REL-002, change any product classification, stage, commit, push or merge.

Phase A — exact-object Django provenance:

- The authoritative Django object was exact commit `fe0a859f537d4238cf49fca39073513206f83122` (`[6.1.x] Bumped version for 6.1 release.`), tree `7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`, parent `62770454c1beb3e9b0ef1f7c35c5670b4df935d8`. The local `/Users/hanhyeonjin/Documents/django` working HEAD was `4243ab11dc957fd14a1875e6b715ff5e6114a415` and had unrelated `.DS_Store` dirt items; no working-tree source was used as Django 6.1 evidence.
- Source was read with exact-object commands of the form `git -C /Users/hanhyeonjin/Documents/django show fe0a859f537d4238cf49fca39073513206f83122:<path>` and object identities were obtained with `git -C /Users/hanhyeonjin/Documents/django rev-parse fe0a859f537d4238cf49fca39073513206f83122:<path>` plus `git -C /Users/hanhyeonjin/Documents/django cat-file -s <blob>`. No checkout, source edit or Django test run was performed.

| Exact Django 6.1 object | Blob | Bytes | Audited observations |
|---|---|---:|---|
| `django/db/models/fields/related_descriptors.py` | `eafcb63ceb7c41e6bbf40d4f1f0165c3119b6374` | 69,743 | lines 87–93 invalidate the related-object cache only when the raw FK scalar actually changes; lines 293–367 validate relation assignment, copy the target key into the source scalar, warm the exact assigned object and support nullable clear |
| `django/db/models/base.py` | `7b7a8833cc6f5a4b3dd4329f0a3cad4374ee8808` | 99,654 | lines 710–723 make only `None`/`DatabaseDefault` unset, so integer PK `0` is present; lines 1270–1307 reject an assigned no-PK target before save, allow manually assigned PK values to reach the database, reconcile a later-saved pending target only while the source scalar remains empty, and invalidate a changed-key cache without overwriting the changed source scalar |
| `tests/basic/tests.py` | `a6609f0f30d8d5a2f43271952fe5c5084998e77b` | 42,583 | lines 563–577 cover PK value `0` as set |
| `tests/many_to_one/tests.py` | `e0def73db01621103f5ae7161e962bb373a04e40` | 39,954 | lines 600–703 cover exact assigned-object identity/cache, nullable assignment/clear, no-PK preflight and target-save-after-assignment reconciliation |
| `docs/topics/db/transactions.txt` | `4733a95bf823e22fc9b9027bfdaffec8498c782b` | 28,481 | lines 190–194 state that transaction rollback does not restore application/model field values; callers restore them explicitly when needed |

- The locked GoDj/Django boundary was independently re-read without rewriting it: `conformance/contracts/relation-manifest.json` 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; `conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json` 33,792 bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`; `conformance/fixtures/godj-relation-not-implemented.json` 1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`; `conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS` 1,148 bytes/SHA-256 `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.
- REL-002 remains the ordered payload-free `oracle_locked` observation: `model_state_error/unsaved_related_object`, phase `evaluation`, query count 0, no statements or joins, author/post row deltas 0, database unchanged and message non-contractual.
- Phase A independent review reported P0/P1/P2/P3=`0/3/1/0`. The three P1 items were decision gaps, not newly observed current-product corruption: pending-only later-key reconciliation; same-versus-different raw-scalar cache behavior; and manual-PK absent-row error taxonomy. All three were made mandatory in the decision freeze and Phase B gates. The P2 is the pre-existing generated `select_related` resolve/bind cause loss at `codegen/project_relation_select_related.go:446` and generated companion paths; it remains an explicitly separate remediation and is not represented as fixed by GDJ-0033.

Phase B — detached no-product prototype identity:

- Temporary worktree: `/tmp/godj-gdj0033-phaseb.TLQk3N/worktree`; detached baseline and HEAD were both exact `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`.
- `git diff --check` was clean. Literal `git diff --binary | shasum -a 256` owner digest was `8329bb0ae76dc3297ad692cd447d11f11cc6578b574202c72dc7c0d754b6c566`. Literal stdout bytes of `git status --porcelain=v1 | shasum -a 256` produced `7479a163c955771d859fe1a6571620c2d89b8571a250dc2fb15f00c09475fa12`.
- The prototype modified exactly five temporary files, no others:

| Temporary file | Numstat | Bytes | SHA-256 |
|---|---:|---:|---|
| `codegen/project_relation_facade.go` | `758 / 27` | 67,652 | `35faacd74179a5650637e52016a9140c26900b0d81d71e20111f7b55ff735572` |
| `codegen/project_relation_facade_test.go` | `518 / 2` | 55,299 | `639e3516eabd0fdfb0d1538a9eee2a0bee33b1c51937e4e80d81d744ca8d6e80` |
| `codegen/testdata/relation_facade/project.golden` | `603 / 10` | 32,384 | `8172d39bf82031a45447741f83ea380e01413806615b9b365f8a8ba6c7a68475` |
| `orm/write.go` | `1 / 1` | 9,694 | `f02f8621c98904743d295b42003621f7d2b100048b23e2087382401ed7118a76` |
| `query/error.go` | `1 / 0` | 3,377 | `4d90857eed40961718acec24e3dd164a5a374e269bd26a495a8e8329dc904b79` |

- Aggregate numstat was 1,881 insertions/40 deletions. The exact five file contents were 168,406 bytes; canonical sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/content encoding was 168,602 bytes/SHA-256 `3269d1491cf91405866bf4ef514cb53d8311509e27f14c101d8a10e85775153f`. As a secondary reproducibility check, literal five-line `shasum -a 256 <five paths>` output piped to `shasum -a 256` produced `53186e73ae8d27ebd54ce8c3b3ab0208786d71f6c77050313d9e10c80a0a48e4`.

Phase B frozen API and semantics exercised by the prototype:

```go
func (q AuthorsAuthorQuery) New(value authors.Author) (*AuthorsAuthor, error)
func (q BlogPostQuery) New(value blog.Post) (*BlogPost, error)

func (m *AuthorsAuthor) Save(ctx context.Context) error
func (m *BlogPost) Save(ctx context.Context) error

func (m *BlogPost) WithAuthor(target *AuthorsAuthor) (*BlogPost, error)
func (m *BlogPost) WithReviewer(target *AuthorsAuthor) (*BlogPost, error)
func (m *BlogPost) WithAuthorID(id int64) (*BlogPost, error)
func (m *BlogPost) WithReviewerID(id int64) (*BlogPost, error)
func (m *BlogPost) ClearReviewer() (*BlogPost, error)
```

- `New` constructs an unpersisted wrapper from a raw generated value; assignment/clear methods return fresh derived wrappers; `Save` is a wrapper-receiver operation. Nil wrapper/target and mismatched project origins fail structurally before backend I/O. There is no `ClearAuthor` for a required edge. `New` is a pure construction method even when called on a filtered/ordered/limited query value; that plan has no effect and causes no I/O.
- Source FK scalar presence is independent of relation-cache state. For a required scalar-format FK, `New` with raw zero means unset, `New` with raw nonzero means present, `WithAuthorID(0)` is explicitly present, and a row loaded from the database has present scalar state even when its decoded key is zero. A required-unset `Author`, `Unwrap` or `Save` fails `field_error/required_field` before I/O; an assigned pending no-PK target takes REL-002 `model_state_error/unsaved_related_object` precedence over required-field validation.
- Every pending relation, required or nullable, makes `Unwrap` fail closed with `model_state_error/unsaved_related_object`; nullable pending assignment cannot silently collapse to raw `nil`. `ClearReviewer` explicitly removes pending ownership and makes nullable raw `nil` unwrap-able.
- Save validates the complete relation state before any write. Validation and first-error precedence are canonical normalized source-model identity followed by relation field `Name`, never schema input/declaration order. Permuting input relation declaration changes provenance hash only; generated public surface and preflight order remain invariant. Reversed-declaration plus two-simultaneously-invalid fixtures passed and rejected before I/O without partial state publication.
- A target saved after assignment is reconciled only while that edge is still pending and its source scalar remains empty. A later explicit source-key mutation owns the scalar: the pending exact-object cache is invalidated and Save never silently overwrites the chosen key.
- Comparing source-key state uses the full `(present, value)` tuple. Writing the same tuple preserves the selected edge cache; changing presence or value invalidates only that edge. Required zero-unset, explicit zero-present and loaded zero-present remain distinct.
- Derived wrappers use per-edge copy-on-write cache cells. The changed edge receives a replacement/invalidated cell; unrelated ready or absent snapshots are preserved in independent cells, while cold/in-flight state is copied as independent cold state. Immutable target pointer values may be shared, but mutable cache cells are not; no identity map and no same-wrapper concurrent mutation support is claimed. Actual `SelectRelated(Reviewer).All` hydration followed by `WithAuthor` preserved zero-query reviewer access.
- A key-present manually constructed scalar-format target is not REL-002. Missing referenced rows reach the database, preserve the backend/driver cause, leave the database unchanged and introduce no new stable SQLite-specific error code. Constructing a key-present relation-format target is a bounded non-goal pending a separate exact public API decision.
- Rollback does not rewind wrapper/application state; callers retain or restore their chosen wrapper value explicitly, matching the pinned Django observable guidance.

Commands and results:

```text
REGEX='^(TestGenerateProjectRelationFacadeIsCanonicalAndByteLocked|TestGenerateProjectRelationFacadeRejectsInvalidInputsBeforeBytes|TestGeneratedProjectRelationFacadeInvalidStatesAndBindingPrecedence|TestPhaseBProjectRelationFacadeRuntimeSpike|TestPhaseBProjectRelationFacadeReservedImports|TestPhaseBProjectRelationFacadeEagerCOWSpike|TestPhaseBProjectRelationFacadeEmptyUniverseCompiles|TestGeneratedProjectRelationFacadeBroadUniversesCompile)$'

go test ./codegen -run "$REGEX" -count=1
go test -race ./codegen -run "$REGEX" -count=1
CGO_ENABLED=0 go test ./codegen -run "$REGEX" -count=1
CGO_ENABLED=0 go test ./orm ./query -count=1
go vet ./codegen ./orm ./query
go test ./codegen ./orm ./query -count=1
go test -count=1 ./codegen ./orm ./query
go test -race -count=1 ./codegen ./orm ./query
CGO_ENABLED=0 go test -count=1 ./codegen ./orm ./query
```

- Every command above passed. Owner timings were focused normal 6.362s, focused race 8.977s, focused CGO0 7.003s, CGO0 orm 0.415s/query 0.757s, full codegen 27.989s/orm 0.423s/query 0.558s; vet emitted no output. Root independently reran final full normal (codegen 24.099s/orm 0.403s/query 0.536s), full race (codegen 30.469s/orm 1.834s/query 2.072s) and full CGO0 (codegen 26.667s/orm 0.382s/query 0.523s). Independent audit also reproduced focused normal 7.461s, race 9.424s, CGO0 7.791s and package codegen 25.731s/orm 0.357s/query 0.496s.
- The focused/broad generated-runtime gates include canonical byte lock and invalid-before-bytes, reversed relation declaration with both edges invalid, scalar-presence tuples including manual PK `0`, all-pending `Unwrap`, canonical two-pass preflight, no partial publication, cache-tuple corruption rejection, actual eager hydration plus per-edge COW, reserved-import, empty-universe and broad-universe compile fixtures.
- Bounded Linux/386 compile command `phaseb_386_dir=$(mktemp -d /tmp/godj-phaseb-final386.XXXXXX) && GOOS=linux GOARCH=386 CGO_ENABLED=0 go test -c -o "$phaseb_386_dir/codegen.test" ./codegen` exited 0/PASS. Output `/tmp/godj-phaseb-final386.L2WHoC/codegen.test` was 6,956,252 bytes/SHA-256 `db04bfdc960dfddd68d71045c14896c0d56e2ad51e0bc25fb6dbbb8a9abf453a`; `file` reported `ELF 32-bit LSB executable, Intel 80386, SYSV, statically linked, with debug_info, not stripped`. This is a bounded compile artifact, not a Linux/386 runtime execution or broad platform-support claim.
- A final `go test ./... -count=1` rerun passed every package except the sole expected publication-boundary failure `conformance/relationdeleteproduct.TestCheckedInGeneratedRelationDeleteProjectPreservesExactThirteenAndAddsFacade`: `project/zz_godj_relation_facade.go` differs from the deterministic Phase B candidate. That failure is deliberate proof that the no-product prototype did not overwrite the checked-in product facade; all other packages passed. Reported package durations included `codegen` 39.798s and the publication check 4.858s; no exact per-command UTC interval was retained.
- Independent final review audited the frozen owner digest `8329bb0ae76dc3297ad692cd447d11f11cc6578b574202c72dc7c0d754b6c566`, made no edits and reported P0/P1/P2/P3=`0/0/0/0`. The separate Phase A `select_related` cause-loss P2 remains unresolved because it is outside this prototype diff, not because the prototype audit cleared it.

Source/product boundary and non-claims:

- The primary checkout remained clean at exact `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`; primary source edits 0, checked-in product/generated edits 0, contract/oracle/checksum edits 0, stage/commit/push/merge 0. The temporary prototype's generator marker remained deliberately noncanonical (`godj-codegen-rel-facade-project-v2-phase-b-spike`) and was not published.
- This evidence is sufficient only to accept the documented GDJ-0033 assignment/save/cache-ownership decision and begin its bounded implementation. It does not implement or verify REL-002 in product, does not change Q-013 from `Partial`, does not close Q-017, does not change product aggregate `121 passing + 5 reviewed deviation + 1 oracle_locked` or relation 11/12, and does not repair the separate generated `select_related` cause-loss P2.
- EVID-072/run `31566524953` proves only the activation head and is not reused for this later decision-document tree. The decision-document head and any implementation/publication head require their own exact-head evidence; no later-head CI or merge is claimed here.
## EVID-20260812-074 — GDJ-0033 GitHub-hosted Decision-documentation-head Exact 26-job CI

- Date/time: 2026-08-12T07:36:00Z–2026-08-12T07:44:36Z; last job completed at 07:44:36Z
- Work/contract IDs: GDJ-0033 Phase A/B/C decision-documentation head; REL-002 decision only; ADR-0033 Accepted for the bounded assignment/save/cache-ownership decision and GDJ-0033 remains active. Q-013 remains `Partial`, Q-017 remains P1/open; product relation classification remains REL-001/003/004/005/006/007/008/009/010/011/012 `passing` and REL-002 ordered payload-free `oracle_locked`.
- Checkout/commit: `codex/revision-fenced-migration-lifecycle@9d728610acbe037bab73fde8910cc80ae8411691` (`docs: accept Django-first relation write boundary`), parent `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`, tree `b7d67f6b31d2f1c78e41d45921eeb2e24f4899f7`
- Environment/backend: GitHub-hosted exact 26 required executions; Ubuntu/Linux and macOS, amd64/arm64; Go 1.26.5; existing GoDj SQLite product gates; CPython 3.12.13/3.13.15/3.14.3/3.14.7 compatibility matrix; exact Darwin CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Windows, PostgreSQL/MySQL service jobs and broad non-SQLite claims are absent.
- Command: Draft PR #1 `pull_request` [run 31574653183](https://github.com/progresshans/godj/actions/runs/31574653183), attempt 1, workflow run number 63. GitHub returned exactly one `pull_request` workflow run for this exact head.
- Exit status: terminal `completed/success`; exact 26/26 jobs and all 326/326 recorded steps completed successfully; failed, cancelled or skipped jobs 0 and non-success recorded steps 0
- Result summary: the exact 13-path GDJ-0033 decision-documentation head passed all unchanged product gates. Phase A pins the Django 6.1 observable assignment/save semantics, Phase B records the detached no-product feasibility proof, and Phase C accepts only the bounded Go `New`/`Save`/`With*`/clear, PK-presence, pending-only reconciliation, canonical preflight and per-edge COW decision. It does not publish product bytes or implement REL-002. Existing generated exact 13 remains byte-identical, the additive generated union remains exact 14 and the physical relation-delete product remains exact 17. Product remains exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation actual 11/12. Each relation-product coordinate independently reproduced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; `WaitDelay` and `Test I/O incomplete` occurred zero times.
- Failures/skips/not run: terminal job failures/cancellations/skips and non-success recorded steps were 0; all 26 raw logs contained no `##[error]` or fatal checkout line. Portable Python's 17 exact-profile-only skips remain intentional; exact Darwin passed 193/193 with skip 0. The detached Phase-B prototype itself was not copied into this checkout or rerun by hosted CI. REL-002 implementation/publication, reverse managers/chaining, multi-hop or multiple-edge eager selection, write/delete facade expansion, coordinated all-project generation repair, callback-after-return lifetime enforcement, relation-capable migration, Windows, PostgreSQL/MySQL and broad non-SQLite support remain outside this decision-head proof. Draft PR #1 was not merged.

Hosted identity and checkout evidence:

- Run metadata was event `pull_request`, attempt 1, exact `headSha=9d728610acbe037bab73fde8910cc80ae8411691`, status `completed`, conclusion `success`, created/started 2026-08-12T07:36:00Z and updated 2026-08-12T07:44:36Z.
- PR #1 was re-queried as `OPEN`/`Draft`/`MERGEABLE`/`CLEAN`, with exact decision head and base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821`.
- Actions synthetic merge `6f258270dcbbea127600ffd842009a2f7071311d` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and head `9d728610acbe037bab73fde8910cc80ae8411691`. Synthetic merge and exact head trees were both `b7d67f6b31d2f1c78e41d45921eeb2e24f4899f7`, so executed contents were exact-head-equivalent.
- Every one of the 26 raw checkout logs contained exactly one fetch of `+6f258270dcbbea127600ffd842009a2f7071311d` to `refs/remotes/pull/1/merge`, exactly one checkout of that ref, the matching `HEAD is now at 6f25827 Merge 9d728610... into f8a5e20...` message and the exact synthetic SHA from `git log -1`. No log showed checkout of another tree.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [94043947791](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947791) | 07:36:03–07:40:56 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [94043947674](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947674) | 07:36:03–07:37:24 | 14 | success |
| Project check (`ubuntu-22.04`) | [94043947855](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947855) | 07:36:03–07:37:02 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [94043947891](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947891) | 07:36:02–07:36:48 | 12 | success |
| Project check (`macos-15-intel`) | [94043947905](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947905) | 07:36:03–07:37:33 | 12 | success |
| Project check (`macos-26`) | [94043948020](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043948020) | 07:42:07–07:43:21 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [94043947742](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947742) | 07:36:03–07:37:38 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [94043947783](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947783) | 07:36:02–07:37:13 | 13 | success |
| Relation binding (`macos-15-intel`) | [94043947799](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947799) | 07:36:04–07:38:47 | 13 | success |
| Relation binding (`macos-26`) | [94043947858](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947858) | 07:38:49–07:39:58 | 13 | success |
| Relation product (`ubuntu-22.04`) | [94043947948](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947948) | 07:36:03–07:39:21 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [94043947780](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947780) | 07:36:02–07:38:46 | 13 | success |
| Relation product (`macos-15-intel`) | [94043947829](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947829) | 07:36:03–07:40:57 | 13 | success |
| Relation product (`macos-26`) | [94043947874](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947874) | 07:37:26–07:40:31 | 13 | success |
| Product project check (`ubuntu-22.04`) | [94043947838](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947838) | 07:36:03–07:39:49 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [94043947978](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947978) | 07:36:03–07:38:14 | 12 | success |
| Product project check (`macos-15-intel`) | [94043947903](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947903) | 07:37:36–07:42:27 | 12 | success |
| Product project check (`macos-26`) | [94043948157](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043948157) | 07:40:59–07:44:36 | 12 | success |
| Python compatibility (`3.12.13`) | [94043947798](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947798) | 07:36:02–07:36:20 | 12 | success |
| Python compatibility (`3.13.15`) | [94043947918](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947918) | 07:36:03–07:36:33 | 12 | success |
| Python compatibility (`3.14.3`) | [94043947917](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947917) | 07:36:10–07:36:41 | 12 | success |
| Python compatibility (`3.14.7`) | [94043947795](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947795) | 07:36:03–07:36:32 | 12 | success |
| SQLite (`ubuntu-22.04`) | [94043947968](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947968) | 07:36:04–07:37:45 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [94043947889](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947889) | 07:36:02–07:37:16 | 12 | success |
| SQLite (`macos-15-intel`) | [94043947962](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043947962) | 07:40:00–07:42:04 | 12 | success |
| SQLite (`macos-26`) | [94043948034](https://github.com/progresshans/godj/actions/runs/31574653183/job/94043948034) | 07:40:33–07:42:10 | 12 | success |

Hosted gate details:

- Full Ubuntu job `94043947791` passed root `make ci` on Go 1.26.5 linux/amd64 with uv 0.12.3. Its raw log shows the production facade codegen, relation-delete product and physical no-overlay `internal/compiletest` passing in normal and race phases, the bounded CGO-disabled package gate, portable 193 tests with 17 intentional skips, exact `godjcheck` output `11 required contracts; 1 remain not implemented`, the actual Ubuntu Linux/386 relation package set, stored-oracle checksum and reference no-rewrite gates. This is not broad all-package Linux/386 support.
- Exact Darwin job `94043947674` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. Its raw log records `internal/compiletest` success and exact 193/193 tests with skip 0.
- Python jobs `94043947798`/`94043947918`/`94043947917`/`94043947795` each set up the exact requested CPython, passed portable 193 tests with 17 intentional skips and independently passed the exact semantic-digest step: 127 scenarios, 498,051 payload bytes and SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `94043947948`/`94043947780`/`94043947829`/`94043947874` each independently emitted exact 697 actual records and 697 unique records. Independent raw-log reconstruction on every coordinate produced exact 697 run/697 pass/0 skip, 70,659 payload bytes and SHA-256 `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`; all four inventories were identical. `WaitDelay` and `Test I/O incomplete` occurred zero times on every coordinate; race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed.
- Existing legacy generated union remained exact 13 files, 26,140 bytes and SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`. The additive generated union remained exact 14 files, 39,243 bytes and SHA-256 `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`; the physical `conformance/relationdeleteproduct/**` fixture remained exact 17 Go files, 94,439 content bytes and SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd` under the canonical sorted path/NUL/decimal-length/NUL/content encoding.
- The published `project/zz_godj_relation_facade.go` remained exact 13,103 bytes/SHA-256 `82b6bf53c2b5b470fef2b33695b638c41316d6054c004ff5bcdc5243617dfed4`, embeds generator version `godj-codegen-rel-facade-project-v1` and input SHA-256 `476f27b868f6e2d917d25ffaa9ec2bd3a978ffee16354e3e6b3bcc387c2d05c7`. `product_test.go` remained 35,610 bytes/SHA-256 `fc6608b97c50f8cace8a484f7869b81faaeda19f8996527b380eecb60bdf3aba`.
- Relation manifest remained 10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; protocol retained separate exact-13 and exact-14 rows. Product aggregate remains 127=`121 passing + 5 deviation + 1 oracle_locked`, relation 11 passing plus REL-002 `oracle_locked`. Relation oracle 33,792 bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static NI fixture 1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209` and 12-line `SHA256SUMS` 1,148 bytes/SHA-256 `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056` remained byte-identical.
- Parent-to-head changed exactly 13 allowed Markdown documentation/work paths: `docs/COMPATIBILITY.md`, `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/SOURCES.md`, `docs/TESTING.md`, `docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, `docs/adr/README.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, and `work/README.md`; 712 insertions and 561 deletions. It changed no source, workflow, generated product fixture, manifest, oracle or checksum artifact. The literal binary diff was exact 148,894 bytes/SHA-256 `af74b836e17784c8ef5576bd6fa89dad3896c088a66c831e25b99a406e256172`; `git diff --check` was clean. Under the canonical sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/Git-object-content encoding, the exact 13-path head-content aggregate was 1,060,716 content bytes, 1,061,220 encoded bytes and SHA-256 `d402486cc060cc3a0bc8cbce2220af123bf09723dfef450b45507daba7ce29d5`.
- Decision-head `docs/status/TEST_EVIDENCE.md` was exact 582,854 bytes/SHA-256 `a9c0e0a25f78245550e7ffe8e24c30a7ebae44ded16d6a0b7037f9c1147d99b3`. Its evidence history starts at byte offset 524. The parent activation head's exact EVID-001..071 body was 552,284 bytes/SHA-256 `8c5b6ed0241d53de9d5eab23ae9698621c70eecdfede019fb2602ce27434925a` and remained byte-identical; EVID-071 itself began at offset 538,483 and its pre-existing exact 14,325 bytes/SHA-256 `965e6be6ac113479a7682f4b3456d14222bdb1219d86ec53f27902a39fafb0ff` remained unchanged. EVID-072 began at offset 552,809 and was exact 14,602 bytes/SHA-256 `1a197c8a66582046421b6b2b3963171646c5680669f64cd36c98cd941a4e71a8`; after one LF separator, EVID-073 began at offset 567,412 and ran through EOF as exact 15,442 bytes/SHA-256 `416360eb4ebe8353e2e47ab6cf2e71fb3e984900defa755bf45e911f11b14195`.

Independent hosted evidence reconstruction re-queried the unique live run, all 26 jobs and 326 steps, the immutable run head, PR/ancestry/tree, all 26 raw checkout logs, the full Ubuntu and exact Darwin logs, all four Python logs and all four raw relation-product inventories. Independent Git-object audit reproduced the exact 13-path diff/content aggregate and every TEST_EVIDENCE boundary above. It reported P0/P1/P2/P3=`0/0/0/0` and made no repository edits.

This evidence proves only the exact GDJ-0033 decision-documentation head `9d728610acbe037bab73fde8910cc80ae8411691`. EVID-072/run `31566524953` is not reused as decision proof, and run `31574653183` must not be reused for any later REL-002 implementation/publication, completion-documentation or terminal evidence/status head. This later EVID-074 append is itself a documentation change and is not recursively proved by run `31574653183`. It does not implement REL-002, change the product classification, close Q-013/Q-017, repair the separate generated `select_related` cause-loss P2, or authorize merging Draft PR #1. Any later source or combined documentation tree requires its own unique exact-head hosted CI; Draft PR #1 remains open/draft and unmerged.

## EVID-20260812-075 — GDJ-0033 REL-002 Forward-write Pre-hosted Local Validation

- Date/time: 2026-08-12 (Asia/Seoul environment); individual command wall-clock intervals and one aggregate command interval were not retained
- Work/contract IDs: GDJ-0033 bounded product implementation; REL-002 only. ADR-0033 is `Implemented locally` after its Accepted decision; Q-013 remains `Partial`, Q-017 remains P1/open.
- Baseline/checkout: primary branch `codex/revision-fenced-migration-lifecycle`, committed parent and decision-documentation head `9d728610acbe037bab73fde8910cc80ae8411691`, tree `b7d67f6b31d2f1c78e41d45921eeb2e24f4899f7`; EVID-074/run `31574653183` is decision-only and is not reused as implementation proof.
- Environment/backend: local macOS darwin/arm64, Go 1.26.5 and the existing GoDj SQLite product boundary. The pinned Django 6.1 relation test suite reported 11/11 PASS; its individual command interval was not retained. Windows, PostgreSQL/MySQL, hosted exact-Darwin, four hosted Python legs and four hosted relation-product coordinates were not run for this working tree.
- Scope/result: exact 23 allowed source/product paths implement the published `New`/`Save`/`WithAuthor`/`WithReviewer`/ID helpers/`ClearReviewer` facade, project-private write descriptor, explicit PK presence, pending-only reconciliation, per-edge COW cache and REL-002 actual. The local product classification is exact 12 adapters/127 contracts=`122 passing + 5 reviewed deviation + 0 oracle_locked`, relation 12/12; REL-002 alone changed from `oracle_locked` to `passing`.

Exact source/product diff and path boundary:

- Literal `git diff --binary 9d728610acbe037bab73fde8910cc80ae8411691 -- <exact 23 paths> | shasum -a 256` was `b760d6d7d3848e7549848b95cb20b083588f7c7d6e812d14009d4eb0e1c23172`; exact aggregate numstat was 3,870 insertions/190 deletions. `git diff --check` was clean before this documentation transition.
- The exact 23 paths were `.github/workflows/ci.yml`; `codegen/project_relation_facade.go`, `codegen/project_relation_facade_test.go`, `codegen/testdata/relation_facade/project.golden`; `conformance/README.md`, `conformance/cmd/godjcheck/main_test.go`, `conformance/contracts/relation-manifest.json`, `conformance/internal/protocol/migration_project_check_artifacts_test.go`, `conformance/internal/protocol/relation_artifacts_test.go`, `conformance/internal/protocol/write_migration_artifacts_test.go`, `conformance/relationdeleteproduct/observer.go`, `conformance/relationdeleteproduct/product_test.go`, `conformance/relationdeleteproduct/project/zz_godj_relation_facade.go`, `conformance/runners/django/tests/test_relation_scenarios.py`, `conformance/runners/godj/relation_scenarios.go`, `conformance/runners/godj/runner_test.go`; `internal/compiletest/compile_test.go`, `internal/compiletest/testdata/relation_facade/external_consumer.go.txt`; `orm/save_test.go`, `orm/write.go`, `orm/write_test.go`; `query/error.go`, `query/relation_mutation_test.go`.
- No `schema/**`, `db/**`, migration source/codec, `go.mod`, `go.sum`, `Makefile`, Django scenario implementation, oracle payload, `SHA256SUMS` or static historical not-implemented fixture changed. Other app-generated exact 13 files remained byte-identical.

Corrected three-phase fail-closed preflight:

1. Phase 1 validates and snapshots every relation-cache tuple in canonical normalized source-model identity + relation field `Name` order.
2. Phase 2 validates every assigned target origin and snapshots each target PK exactly once in that same canonical order.
3. Phase 3 returns the first no-PK target as `model_state_error/unsaved_related_object` in the same canonical order.
4. Only after all three phases succeed does Save check required-unset, build all raw/write/object/cache candidates, publish all-success state, and enter Manager plan/I/O.

Adversarial gates prove that an earlier Author no-PK target cannot mask a later Reviewer corrupt cache, self-target or origin error, and the reverse ordering cannot mask the structural error with `unsaved_related_object`. Every such failure leaves source state unchanged and performs backend I/O 0. Target PK values are snapshotted once, declaration permutation does not alter public/error order, required zero-unset remains distinct from explicit/loaded zero-present, and pending reconciliation occurs only while the source scalar remains empty.

Generated/publication and measured hard locks:

- Legacy generated exact 13 remained 13 files/26,140 bytes/SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`.
- Deterministic generated exact 14 was 14 files/58,680 bytes/SHA-256 `90e0e6cc5abf471a078107d58acf2e091fcf10d8252444c5e1efc671a45fb8ec`; physical relation-delete product exact 17 was 17 files/140,188 bytes/SHA-256 `bb7456ff57e37f0b665da4519c8804292d010b0da2464290c7eebd28ceb70021`. These inventories use sorted relative path + NUL + decimal content length + NUL + content encoding.
- `conformance/contracts/relation-manifest.json` was 10,770 bytes/SHA-256 `791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b`; only REL-002 status changed. The Django oracle remains 33,792 bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static historical NI fixture 1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`, and 12-line `SHA256SUMS` 1,148 bytes/SHA-256 `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056` remained byte-identical.
- Final top-level relation-product JSON roster was exact 715 run/715 pass/0 skip. Its sorted package-NUL-test-newline payload was 72,621 bytes/SHA-256 `85575c84e202fb88570ab44d6ef1ca0df8ef4443cfd63dabfa70f65130f9e237`; the raw JSON log was 3,573,010 bytes/SHA-256 `03bc2fac64c332cb72c81efb6a5d53bc6e62ef08b35b33fecb1b51ffd72999e6`.

Final local commands and results:

```text
go test -count=1 ./codegen ./orm ./query
go test -race -count=1 ./codegen ./orm ./query
CGO_ENABLED=0 go test -count=1 ./codegen ./orm ./query

go test -count=1 ./internal/compiletest ./conformance/internal/protocol
go test -race -count=1 ./internal/compiletest ./conformance/internal/protocol
CGO_ENABLED=0 go test -count=1 ./internal/compiletest ./conformance/internal/protocol

go vet ./...
go test -count=1 ./...
```

- Every listed command passed. Final core normal package durations were codegen 32.104s, orm 1.985s and query 1.251s; race 35.008s/3.751s/2.865s; CGO-disabled 32.796s/0.918s/1.603s. Final compiletest/protocol normal durations were 5.392s/1.763s; race 9.930s/14.143s; CGO-disabled 8.004s/3.169s. `go vet ./...` emitted no finding.
- Final full `go test -count=1 ./...` passed every package. Relevant reported durations included codegen 30.892s, internal/compiletest 9.749s, conformance/internal/protocol 2.682s, conformance/relationdeleteproduct 2.104s, orm 1.429s and query 1.405s.
- Bounded `GOOS=linux GOARCH=386 CGO_ENABLED=0 go test -c ... ./codegen` compile passed. Its codegen test artifact was 6,964,319 bytes with SHA-256 prefix `e1bdb78b`; the complete digest/command interval was not retained, so no exact full digest or runtime support claim is made.
- Final source review and corrected three-phase semantic review reported P0/P1/P2/P3=`0/0/0/0`. The pre-existing typed generated `select_related` Resolve/Bind cause-loss P2 remains explicitly outside GDJ-0033 and is not represented as fixed.

After EVID-074/075 and the other status/design changes were present, an independent pre-commit audit also ran the following
commands on the combined exact 31-path working tree. Exact UTC/Asia-Seoul command intervals were not retained.

```text
go test -count=1 ./codegen ./orm ./query ./internal/compiletest ./conformance/internal/protocol ./conformance/relationdeleteproduct ./conformance/runners/godj
go test -race -count=1 ./codegen ./orm ./query ./internal/compiletest ./conformance/relationdeleteproduct ./conformance/runners/godj

audit_roster_log=$(mktemp /tmp/godj-exact31-roster-final.XXXXXX)
go test -json -count=1 ./schema/... ./query ./codegen ./orm ./db/sqlite ./migrations ./migrations/definition ./conformance/relationproduct/... ./conformance/relationqueryproduct/... ./conformance/relationobjectproduct/... ./conformance/relationreverseproduct/... ./conformance/relationprefetchproduct/... ./conformance/relationselectproduct/... ./conformance/relationdeleteproduct/... ./conformance/internal/protocol ./conformance/runners/godj ./conformance/cmd/godjcheck ./internal/compiletest > "$audit_roster_log"
```

- Focused normal and race both exited 0. The normal command included all seven listed product/core packages; the focused race command did not include `conformance/internal/protocol`, whose race result remains part of the earlier exact-23 source proof above.
- The CI-equivalent JSON run exited 0 and independently reconstructed exact 715 run/715 pass/0 skip, 72,621 payload bytes and SHA-256 `85575c84e202fb88570ab44d6ef1ca0df8ef4443cfd63dabfa70f65130f9e237`, with failure events 0. Its raw JSON log was 3,572,886 bytes/SHA-256 `fc3ac2441aa48372307824aabc307e29fc4a2117fbee8cf452453da0153ba382`.
- This independent run is local mutable-working-tree evidence, not an exact-head hosted proof. The documentation lines that record it were necessarily written afterward and are not recursively proved by those commands; their final validation is limited to scope, semantics, links, anchors, fences, frontmatter, formatting and diff checks before commit.

Implementation and recursive evidence boundary:

- EVID-072/run `31566524953` proves only activation. EVID-074/run `31574653183` proves only the committed decision-documentation head. Neither is reused as proof of these product bytes.
- EVID-075's source proof applies to the exact 23-path source/product diff before its accompanying documentation was added. The later independent pre-commit commands above additionally validate the combined 31-path mutable working tree locally, but do not create an immutable exact-head or hosted proof. The tree still requires a new commit, unique exact-head hosted 26-job/326-step run and independent terminal audit.
- Hosted implementation acceptance, work completion, completion-documentation and terminal evidence/status remain pending and require later distinct exact-head CI boundaries. Q-013 remains `Partial`, Q-017 remains open, non-SQLite/reverse/general generated-upgrade/migration support is not claimed, and Draft PR #1 remains unmerged.

## EVID-20260812-076 — GDJ-0033 GitHub-hosted REL-002 Implementation-head Exact 26-job CI

- Date/time: 2026-08-12; run created/started 10:19:37Z, terminal updated 10:33:13Z
- Work/contract IDs: GDJ-0033 bounded REL-002 product implementation; Q-013 remains `Partial`, Q-017 remains P1/open
- Baseline/checkout: branch `codex/revision-fenced-migration-lifecycle`; parent `9d728610acbe037bab73fde8910cc80ae8411691`; exact implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`; exact tree `f23dd8e1c84b2e22b3fafcc5f3303aaf25df804c`; subject `feat: add Django-first relation assignment`
- Hosted command boundary: unique GitHub Actions [run 31586910749](https://github.com/progresshans/godj/actions/runs/31586910749), workflow `CI`, event `pull_request`, attempt 1
- Result: exact 26/26 jobs and 326/326 steps completed with conclusion `success`; no skipped, cancelled or failed job/step
- Product result: exact 12 adapters/127 contracts=`122 passing + 5 deviation + 0 oracle_locked`; relation exact 12/12 passing, including REL-002
- Publication boundary: PR #1 remained `OPEN`/`Draft`/`CLEAN`/`MERGEABLE`, head exact, `mergedAt=null`

Hosted identity and checkout evidence:

- A fail-closed exact-head query returned exactly one `pull_request` run: database ID `31586910749`, attempt 1, `headSha=be6f3d4e0838929fe96ec156ec0647845d905ea6`, status `completed`, conclusion `success`.
- Actions synthetic merge `8694365cb33de20413e4d29b2bf92c9e70bb060f` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`.
- Synthetic merge and exact implementation head trees were both `f23dd8e1c84b2e22b3fafcc5f3303aaf25df804c`; therefore the executed contents were exact-head-equivalent.
- All 26 raw checkout logs independently contained exactly one fetch of `+8694365cb33de20413e4d29b2bf92c9e70bb060f:refs/remotes/pull/1/merge`, one checkout of that ref, the matching `HEAD is now at 8694365 Merge be6f3d4e... into f8a5e20...` message and one exact synthetic SHA from `git log -1`. No checkout log showed another `HEAD`.
- PR #1 was re-queried after terminal completion as branch `codex/revision-fenced-migration-lifecycle`, exact head `be6f3d4e0838929fe96ec156ec0647845d905ea6`, base `main`, `OPEN`, Draft, `CLEAN`, `MERGEABLE`, and unmerged.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [94082742539](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742539) | 10:19:46–10:26:16 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [94082742609](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742609) | 10:22:06–10:23:43 | 14 | success |
| Project check (`ubuntu-22.04`) | [94082742547](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742547) | 10:19:40–10:20:49 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [94082742672](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742672) | 10:19:42–10:20:33 | 12 | success |
| Project check (`macos-15-intel`) | [94082742580](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742580) | 10:19:42–10:21:39 | 12 | success |
| Project check (`macos-26`) | [94082742714](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742714) | 10:22:37–10:23:31 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [94082742645](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742645) | 10:19:40–10:21:13 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [94082742727](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742727) | 10:19:40–10:20:56 | 13 | success |
| Relation binding (`macos-15-intel`) | [94082742704](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742704) | 10:21:18–10:23:19 | 13 | success |
| Relation binding (`macos-26`) | [94082742675](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742675) | 10:19:40–10:21:14 | 13 | success |
| Relation product (`ubuntu-22.04`) | [94082742705](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742705) | 10:19:40–10:23:53 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [94082742701](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742701) | 10:19:39–10:22:24 | 13 | success |
| Relation product (`macos-15-intel`) | [94082742777](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742777) | 10:23:07–10:33:12 | 13 | success |
| Relation product (`macos-26`) | [94082742848](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742848) | 10:23:20–10:26:17 | 13 | success |
| Product project check (`ubuntu-22.04`) | [94082742545](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742545) | 10:19:39–10:23:00 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [94082742643](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742643) | 10:19:42–10:22:01 | 12 | success |
| Product project check (`macos-15-intel`) | [94082742605](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742605) | 10:19:40–10:24:11 | 12 | success |
| Product project check (`macos-26`) | [94082742577](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742577) | 10:19:40–10:22:35 | 12 | success |
| Python compatibility (`3.12.13`) | [94082742776](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742776) | 10:19:40–10:20:03 | 12 | success |
| Python compatibility (`3.13.15`) | [94082742740](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742740) | 10:19:39–10:20:02 | 12 | success |
| Python compatibility (`3.14.3`) | [94082742683](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742683) | 10:19:39–10:20:06 | 12 | success |
| Python compatibility (`3.14.7`) | [94082742660](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742660) | 10:19:39–10:20:17 | 12 | success |
| SQLite (`ubuntu-22.04`) | [94082742664](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742664) | 10:19:40–10:21:23 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [94082742768](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742768) | 10:19:39–10:20:56 | 12 | success |
| SQLite (`macos-15-intel`) | [94082742614](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742614) | 10:19:41–10:22:04 | 12 | success |
| SQLite (`macos-26`) | [94082742694](https://github.com/progresshans/godj/actions/runs/31586910749/job/94082742694) | 10:21:41–10:23:04 | 12 | success |

Hosted gate details:

- All 20 project/product-project/relation-binding/relation-product/SQLite matrix jobs emitted and asserted the intended Go 1.26.5 coordinate: Linux amd64, Linux arm64, Darwin amd64 and Darwin arm64. Every coordinate’s normal, race, CGO-disabled, vet and clean-worktree gates succeeded where configured.
- Full Ubuntu artifact job `94082742539` passed root `make ci` on Go 1.26.5 linux/amd64 with uv 0.12.3. Normal all-package tests, vet, race, bounded CGO-disabled packages, production facade codegen, relation-delete product and physical no-overlay compiletest all passed. Portable Python reported exact 193 tests with 17 intentional skips. The scoped Linux/386 migration/project-check compile and relation package runtime gates, stored-oracle checksums and reference no-rewrite gate also passed; this does not claim general Linux/386 product support.
- Exact Darwin job `94082742609` passed Go 1.26.5 darwin/arm64, CPython 3.14.3, Django 6.1 and SQLite 3.50.4. It passed the bounded migration/SQLite/runner/physical compiletest gate and exact 193/193 Python tests with skip 0, followed by all oracle `--check` and no-rewrite gates.
- Python jobs `94082742776`/`94082742740`/`94082742683`/`94082742660` each asserted their exact CPython version and Django 6.1/asgiref 3.12.1/sqlparse 0.5.5 isolation. Each passed 193 tests with 17 intentional skips and independently passed the exact all-scenario semantic digest: 127 scenarios, 498,051 payload bytes, SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `94082742705`/`94082742701`/`94082742777`/`94082742848` each independently emitted 715 actual records and 715 unique records. Independent reconstruction from every raw coordinate produced exact 715 run/715 pass/0 skip, 72,621 payload bytes and SHA-256 `85575c84e202fb88570ab44d6ef1ca0df8ef4443cfd63dabfa70f65130f9e237`; all four inventories were identical.
- Those inventories include the public product tests `TestObserveUnsavedRelatedTargetFailsBeforeExactOperationIO` and `TestProjectFacadePendingTargetSaveReconcilesAndPublishesBothRows`. `WaitDelay`, `Test I/O incomplete`, Actions error annotations, warning annotations and data-race reports occurred zero times. Race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed on all four relation coordinates.

Generated/publication and contract locks:

- Legacy generated exact 13 remained 13 files/26,140 content bytes/SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`.
- Deterministic generated exact 14 was 14 files/58,680 bytes/SHA-256 `90e0e6cc5abf471a078107d58acf2e091fcf10d8252444c5e1efc671a45fb8ec`; physical relation-delete product exact 17 was 17 files/140,188 bytes/SHA-256 `bb7456ff57e37f0b665da4519c8804292d010b0da2464290c7eebd28ceb70021`. These inventories use sorted relative path + NUL + decimal content length + NUL + content encoding.
- Generated golden was exact 32,480 bytes/SHA-256 `21f2b61767a82a28ed917061306a8f37847537057d3422dc09ebd34617cdff74`; the checked-in production companion was exact 32,540 bytes/SHA-256 `956defa9ae1ba25c713c2a224fb81888caed17650a581c5011a3f0610f369135`.
- Both embed generator version `godj-codegen-rel-facade-project-v2`; golden input SHA-256 is `f49958eb74b49399372923ea8898f235a85ebb1c1b0f407bac9425e98d83964c`, production input SHA-256 is `d5a67c078a346bed346837a0660a9d4872f0eb0b622c7270e580cda71015bdfc`.
- Relation manifest was exact 10,770 bytes/SHA-256 `791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b`; all 12 relation contracts are `passing`, and parent-to-head changed only REL-002 from `oracle_locked` to `passing`.
- Across the 12 unique manifests, the exact aggregate is 127 contracts=`122 passing + 5 deviation + 0 oracle_locked`.
- Django relation oracle remained 33,792 bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`; static historical NI fixture remained 1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`; 12-line `SHA256SUMS` remained 1,148 bytes/SHA-256 `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.

Exact implementation diff and content boundary:

- Parent-to-head changed exactly 31 allowlisted paths: the exact 23 source/product paths from EVID-075 plus eight documentation/work paths: `docs/ROADMAP.md`, `docs/TESTING.md`, `docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, and `work/README.md`.
- The exact 23 source/product portion remained 3,870 insertions/190 deletions; its literal binary diff was 223,347 bytes/SHA-256 `b760d6d7d3848e7549848b95cb20b083588f7c7d6e812d14009d4eb0e1c23172`.
- The exact eight documentation/work paths were 295 insertions/115 deletions. Combined exact 31 was 4,165 insertions/305 deletions.
- Literal `git diff --binary 9d728610acbe037bab73fde8910cc80ae8411691 be6f3d4e0838929fe96ec156ec0647845d905ea6` was exact 312,763 bytes/SHA-256 `4199fe258b17d827395f81d861fb07f741201994b848723620cb895e8fe9efc6`; `git diff --check` was clean.
- Under canonical sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/Git-object-content encoding, exact 31 head content was 1,724,577 content bytes, 1,726,096 encoded bytes and SHA-256 `4c89947e0ee484a4615508870888c8209e5b43e277cf979f63b890858c337e49`.

Evidence-history boundary:

- Implementation-head `docs/status/TEST_EVIDENCE.md` was exact 608,710 bytes/SHA-256 `e0646f0813bca9ef4d458779f8f7de8e4b4394226c4771dda01ea946392e8618`.
- Its historical EVID-001..073 body occupies zero-based byte offsets 524 through 582,853: exact 582,330 bytes/SHA-256 `80164a3d223a1c2620d8a11b9f66bb31c32fcd023bf802a7a8a0c6fbfa11e19a`, byte-identical to the decision head. Only the fixed-length top pointer changed from EVID-073 to EVID-075.
- EVID-074 begins at offset 582,854 and is exact 15,401 bytes/SHA-256 `0899f23c217036b6e5ad0eba8c494a26c8379104db28b3f643400a14e40e4e8d`. After one LF separator, EVID-075 begins at offset 598,256 and runs through EOF as exact 10,454 bytes/SHA-256 `7d207021202e108b40a90c90768d9001aa613bf7d0b0d2e2509bd1bb1784000e`.

Independent hosted audit:

- Raw terminal evidence was captured under `/tmp/godj-ci-31586910749.3dd9gg`. Terminal run JSON was 60,100 bytes/SHA-256 `ded9b41292ea8ab2aca323af398d99230507825feb68fec98944552eb859c739`; combined 14,785-line raw log was 2,658,487 bytes/SHA-256 `bbf0a262eff3eddff280e8b17177ab029c66a0f46806d6c6f5e56995eb2fda83`.
- The audit re-queried run uniqueness and terminal coordinates, all 26 jobs/326 steps, PR state, synthetic ancestry/tree equivalence, all 26 checkout traces, every matrix coordinate, the full Ubuntu and exact Darwin logs, all four Python jobs, all four relation inventories, exact generated/product locks, exact 31-path diff/content aggregate and TEST_EVIDENCE boundaries.
- Hosted evidence inconsistencies were P0/P1/P2/P3=`0/0/0/0`. The audit performed no repository edit, stage, commit, push, rerun or merge.

Evidence and product boundary:

- EVID-072/run `31566524953` proves activation only. EVID-074/run `31574653183` proves the decision-documentation head only. This unique run `31586910749` proves the combined exact 31-path REL-002 implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`.
- This hosted acceptance proves the bounded SQLite/AutoField forward-assignment/save product, including unsaved-target failure before backend I/O and exact 12/12 relation classification. It does not close Q-013/Q-017 or claim reverse assignment, relation migrations, general generated upgrade, non-SQLite backends, callback-after-return lifetime, or repair of the separately tracked generated `select_related` cause-loss P2.
- The later EVID-076 append and completion/status transitions are documentation changes and are not recursively proved by run `31586910749`; they require their own unique completion-documentation exact-head CI before GDJ-0033 terminal closure.
- Draft PR #1 remains open, draft and unmerged.

## EVID-20260812-077 — GDJ-0033 GitHub-hosted Completion-documentation-head Exact 26-job CI

- Date/time: 2026-08-12; run created/started 11:13:45Z, terminal updated 11:21:57Z
- Work/contract IDs: GDJ-0033 exact 15-document completion/status transition; REL-002 remains `passing`; Q-013 remains `Partial`, Q-017 remains P1/open
- Baseline/checkout: branch `codex/revision-fenced-migration-lifecycle`; parent implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`; exact completion-documentation head `81f4aacb7338e0ea96fa1494c902b2a14e768fcb`; exact tree `d62a29933252bf85a632a212020a788e50e73519`; subject `docs: complete Django-first relation assignment`
- Hosted command boundary: unique GitHub Actions [run 31590911735](https://github.com/progresshans/godj/actions/runs/31590911735), workflow `CI`, event `pull_request`, attempt 1
- Result: exact 26/26 jobs and 326/326 steps completed with conclusion `success`; no skipped, cancelled or failed job/step
- Product result: unchanged exact 12 adapters/127 contracts=`122 passing + 5 deviation + 0 oracle_locked`; relation exact 12/12 passing, including REL-002
- Publication boundary: PR #1 remained `OPEN`/`Draft`/`CLEAN`/`MERGEABLE`, head exact, `merged=false`, `mergedAt=null`

Hosted identity and checkout evidence:

- A fail-closed exact-head query returned exactly one `pull_request` run: database ID `31590911735`, run number 65, attempt 1, workflow ID `329824900`, `headSha=81f4aacb7338e0ea96fa1494c902b2a14e768fcb`, status `completed`, conclusion `success`.
- Actions synthetic merge `708897936c9bb274ca601a376e7e6b053bcf6a51` had exact parents base `f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` and completion head `81f4aacb7338e0ea96fa1494c902b2a14e768fcb`.
- Synthetic merge and exact completion head trees were both `d62a29933252bf85a632a212020a788e50e73519`; therefore the executed contents were exact-head-equivalent.
- All 26 raw checkout traces independently contained exactly one fetch of `+708897936c9bb274ca601a376e7e6b053bcf6a51:refs/remotes/pull/1/merge`, one checkout of that ref, one matching `HEAD is now at 7088979 Merge 81f4aacb7338e0ea96fa1494c902b2a14e768fcb into f8a5e20c0211a81ee7d3ef002f2f34bcbbb6c821` message, one `git log -1 --format=%H` command and one exact synthetic-SHA result. No checkout trace showed another `HEAD`.
- PR #1 was re-queried after terminal completion as branch `codex/revision-fenced-migration-lifecycle`, exact head `81f4aacb7338e0ea96fa1494c902b2a14e768fcb`, base `main`, `OPEN`, Draft, `CLEAN`, `MERGEABLE`, and unmerged.

Exact job identities:

| Required execution | Job ID | UTC interval | Steps | Result |
|---|---:|---|---:|---|
| Validate checked-in conformance artifacts | [94095387601](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387601) | 11:13:48–11:20:10 | 16 | success |
| Validate exact darwin/arm64 profile and SQLite lifecycle | [94095387671](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387671) | 11:16:04–11:17:47 | 14 | success |
| Project check (`ubuntu-22.04`) | [94095387659](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387659) | 11:13:48–11:14:39 | 12 | success |
| Project check (`ubuntu-24.04-arm`) | [94095387578](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387578) | 11:13:51–11:14:35 | 12 | success |
| Project check (`macos-15-intel`) | [94095387608](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387608) | 11:13:50–11:16:27 | 12 | success |
| Project check (`macos-26`) | [94095387688](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387688) | 11:18:28–11:19:36 | 12 | success |
| Relation binding (`ubuntu-22.04`) | [94095387521](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387521) | 11:13:48–11:15:07 | 13 | success |
| Relation binding (`ubuntu-24.04-arm`) | [94095387582](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387582) | 11:13:48–11:14:58 | 13 | success |
| Relation binding (`macos-15-intel`) | [94095387556](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387556) | 11:13:49–11:15:43 | 13 | success |
| Relation binding (`macos-26`) | [94095387655](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387655) | 11:16:30–11:17:59 | 13 | success |
| Relation product (`ubuntu-22.04`) | [94095387694](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387694) | 11:13:55–11:17:57 | 13 | success |
| Relation product (`ubuntu-24.04-arm`) | [94095387764](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387764) | 11:13:48–11:16:32 | 13 | success |
| Relation product (`macos-15-intel`) | [94095387737](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387737) | 11:13:48–11:20:19 | 13 | success |
| Relation product (`macos-26`) | [94095387779](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387779) | 11:18:02–11:21:56 | 13 | success |
| Product project check (`ubuntu-22.04`) | [94095387841](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387841) | 11:13:49–11:17:11 | 12 | success |
| Product project check (`ubuntu-24.04-arm`) | [94095387748](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387748) | 11:13:48–11:16:07 | 12 | success |
| Product project check (`macos-15-intel`) | [94095387695](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387695) | 11:13:48–11:19:27 | 12 | success |
| Product project check (`macos-26`) | [94095387858](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387858) | 11:15:45–11:18:26 | 12 | success |
| Python compatibility (`3.12.13`) | [94095387899](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387899) | 11:13:49–11:14:11 | 12 | success |
| Python compatibility (`3.13.15`) | [94095387830](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387830) | 11:13:48–11:14:11 | 12 | success |
| Python compatibility (`3.14.3`) | [94095387628](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387628) | 11:13:48–11:14:18 | 12 | success |
| Python compatibility (`3.14.7`) | [94095387678](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387678) | 11:13:48–11:14:17 | 12 | success |
| SQLite (`ubuntu-22.04`) | [94095387751](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387751) | 11:13:49–11:15:31 | 12 | success |
| SQLite (`ubuntu-24.04-arm`) | [94095387714](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387714) | 11:13:51–11:15:06 | 12 | success |
| SQLite (`macos-15-intel`) | [94095387796](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387796) | 11:13:50–11:16:01 | 12 | success |
| SQLite (`macos-26`) | [94095387712](https://github.com/progresshans/godj/actions/runs/31590911735/job/94095387712) | 11:17:49–11:18:50 | 12 | success |

Hosted gate details:

- All 20 project/product-project/relation-binding/relation-product/SQLite matrix jobs emitted and asserted the intended Go 1.26.5 coordinate: Linux amd64, Linux arm64, Darwin amd64 and Darwin arm64. Every coordinate’s configured normal, race, CGO-disabled, vet, no-rewrite and clean-worktree gates succeeded.
- Full Ubuntu artifact job `94095387601` passed root `make ci` on Go 1.26.5 linux/amd64 with locked Python dependencies. Normal all-package tests, vet, race, bounded CGO-disabled packages, production facade codegen, relation-delete product and physical no-overlay compiletest all passed. Portable Python reported exact 193 tests with 17 intentional skips. The scoped Linux/386 migration/project-check compile and relation-package runtime gates, stored-oracle checksums and reference no-rewrite gate also passed; this does not claim general Linux/386 product support.
- Exact Darwin job `94095387671` passed Go 1.26.5 darwin/arm64 and pinned CPython 3.14.3/Django 6.1/SQLite profile gates. It passed the bounded migration/SQLite/runner/physical compiletest gate and exact 193/193 Python tests with skip 0, followed by all oracle `--check` and no-rewrite gates.
- Python jobs `94095387899`/`94095387830`/`94095387628`/`94095387678` each asserted their exact CPython version and pinned compatibility environment. Each passed 193 tests with 17 intentional skips and independently passed the exact all-scenario semantic digest: 127 scenarios, 498,051 payload bytes, SHA-256 `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`.
- Relation-product jobs `94095387694`/`94095387764`/`94095387737`/`94095387779` each emitted 715 actual top-level records. Independent reconstruction from every raw coordinate produced exact 715 run/715 pass/0 skip, 72,621 payload bytes and SHA-256 `85575c84e202fb88570ab44d6ef1ca0df8ef4443cfd63dabfa70f65130f9e237`; all four inventories were byte-identical.
- Those inventories include `TestObserveUnsavedRelatedTargetFailsBeforeExactOperationIO` and `TestProjectFacadePendingTargetSaveReconcilesAndPublishesBothRows`. `WaitDelay`, `Test I/O incomplete`, Actions error annotations, warning annotations, panic markers and data-race reports occurred zero times. Race, CGO-disabled, vet, generated-fixture no-rewrite and clean-worktree steps all passed on all four relation coordinates.

Product and source invariants:

- Parent-to-head changed no source, workflow, generated product fixture, manifest, oracle, checksum or non-Markdown file. The non-Markdown change count was 0 and the source/product literal binary diff was the empty 0-byte stream. The worktree was clean and local HEAD/upstream both exactly `81f4aacb7338e0ea96fa1494c902b2a14e768fcb`.
- The relation manifest remained exact 10,770 bytes/SHA-256 `791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b`; all 12 relation contracts remained `passing`. Across all 12 unique manifests, exact aggregate remained 127 contracts=`122 passing + 5 deviation + 0 oracle_locked`.
- Generated golden remained exact 32,480 bytes/SHA-256 `21f2b61767a82a28ed917061306a8f37847537057d3422dc09ebd34617cdff74`; checked-in production companion remained exact 32,540 bytes/SHA-256 `956defa9ae1ba25c713c2a224fb81888caed17650a581c5011a3f0610f369135`. Both retained generator version `godj-codegen-rel-facade-project-v2`, with golden input SHA-256 `f49958eb74b49399372923ea8898f235a85ebb1c1b0f407bac9425e98d83964c` and production input SHA-256 `d5a67c078a346bed346837a0660a9d4872f0eb0b622c7270e580cda71015bdfc`.
- The EVID-076 generated locks therefore remained byte-identical: legacy exact 13=`13 files/26,140 bytes/a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`; generated exact 14=`14 files/58,680 bytes/90e0e6cc5abf471a078107d58acf2e091fcf10d8252444c5e1efc671a45fb8ec`; physical exact 17=`17 files/140,188 bytes/bb7456ff57e37f0b665da4519c8804292d010b0da2464290c7eebd28ceb70021`.
- Django relation oracle remained 33,792 bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`; static historical NI fixture remained 1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`; 12-line `SHA256SUMS` remained 1,148 bytes/SHA-256 `067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`.

Exact completion-documentation diff and content boundary:

- Parent-to-head changed exactly 15 allowed Markdown documentation/work paths: `docs/ARCHITECTURE.md`, `docs/CAPABILITY_CATALOG.md`, `docs/COMPATIBILITY.md`, `docs/CONCURRENCY.md`, `docs/DEVELOPER_EXPERIENCE.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/SOURCES.md`, `docs/TESTING.md`, `docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`, `docs/status/TEST_EVIDENCE.md`, `work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md`, and `work/README.md`.
- Exact 15-path numstat was 344 insertions/142 deletions. Literal `git diff --binary be6f3d4e0838929fe96ec156ec0647845d905ea6 81f4aacb7338e0ea96fa1494c902b2a14e768fcb` was exact 111,397 bytes/SHA-256 `cbfd1c83763d852ae136c869533f6b00fe3b8962ee2892907801645a819682ae`; `git diff --check` was clean.
- Under canonical sorted path/NUL/unsigned-64-bit-big-endian-length/NUL/Git-object-content encoding, exact 15-path head content was 1,178,649 content bytes, 1,179,220 encoded bytes and SHA-256 `70898d38f457ea1843634c65f4bc5baae1d63aaf655d16f358bbba33729185b7`.
- Independent local exact-head documentation validation found zero missing relative targets or GitHub-style anchors across the exact 15 files, no unbalanced fenced-code block, exactly one EVID-076 heading and current-evidence pointer, GDJ-0033 frontmatter `completed`, and zero `active`/`ready` work packets.

Evidence-history boundary:

- Completion-head `docs/status/TEST_EVIDENCE.md` was exact 623,713 bytes/SHA-256 `f7e7f402daf81e92925b5b523334ad4a6d189e6464ecf55a1ac85ba922953a68`.
- Historical EVID-001..075 body occupies zero-based byte offsets 524 through 608,709: exact 608,186 bytes/SHA-256 `8f6328cbcc0272c1f2d8644a378added1a8dfa856b60d17fd12acf84b16422e9`, byte-identical to the implementation head. Only the fixed-length top pointer changed from EVID-075 to EVID-076.
- After one LF separator at offset 608,710, EVID-076 begins at offset 608,711 and runs through EOF as exact 15,002 bytes/SHA-256 `bd969aba587dccc1f60a10d0dc841faa1957daf4a65b5a19f3b02cedde2ec6b0`.
- The complete EVID-001..076 body from offset 524 through EOF was exact 623,189 bytes/SHA-256 `db96af99f93a6dfa123d763d81dd311334b74a84fcca2bc65791b5be540148f2`.

Independent hosted audit:

- Raw terminal evidence was captured under `/tmp/godj-ci-31590911735.7SjRBd`. Terminal run API JSON was 12,506 bytes/SHA-256 `56a09695411cccc9342a2025a40a243fade1a8eb6fd1d6956becda6b0e6d9a07`; 26-job API JSON was 77,140 bytes/SHA-256 `d8284eec307f50f2df69e3bce49a1a42989dbe86ebe9ce1e9234b3b6f1b8e909`; raw log ZIP was 735,390 bytes/SHA-256 `ced0ad49101175526d5009fb8c2f58e5d650a751cc7b9d285f1074dc7a38780a`; combined 14,856-line raw log was 2,665,817 bytes/SHA-256 `f5fc27bca72515a3500ee9b41c88021b592bf39a9328ff30fa98e537a3642ec2`.
- The audit re-queried run uniqueness and terminal coordinates, all 26 jobs/326 steps, PR state, synthetic ancestry/tree equivalence, all 26 checkout traces, every matrix coordinate, the full Ubuntu and exact Darwin logs, all four Python jobs, all four relation inventories, unchanged generated/product locks, exact 15-path diff/content aggregate and TEST_EVIDENCE boundaries.
- Hosted evidence inconsistencies were P0/P1/P2/P3=`0/0/0/0`. The audit performed no repository edit, stage, commit, push, rerun or merge.

Evidence and product boundary:

- EVID-076/run `31586910749` proves the exact 31-path implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`. This unique run `31590911735` separately proves the exact 15-document completion/status head `81f4aacb7338e0ea96fa1494c902b2a14e768fcb`; the implementation run was not reused.
- The product boundary is unchanged: bounded SQLite/AutoField forward assignment/save is Implemented/Verified and REL-002 is `passing`, while Q-013 remains `Partial`, Q-017 remains P1/open, and reverse assignment, relation migrations, general generated upgrade, non-SQLite backends, callback-after-return lifetime and the separately tracked generated `select_related` cause-loss P2 remain outside this packet.
- The later EVID-077 append and exact terminal evidence/status tree are documentation changes not recursively proved by run `31590911735`; they require their own distinct exact-head boundary before GDJ-0033 terminal-baseline closure. No next work is active/ready.
- Draft PR #1 remains open, draft and unmerged.
