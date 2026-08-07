# 테스트·검증 증거

- 마지막 갱신: 2026-08-07
- 현재 GoDj 코드 테스트 증거: 없음

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
