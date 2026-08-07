# 테스트·검증 증거

- 마지막 갱신: 2026-08-07
- 현재 GoDj 코드 테스트 증거: EVID-20260807-002

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
