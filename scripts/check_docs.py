#!/usr/bin/env python3
"""Check local Markdown link destinations without traversing historical docs."""
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote, urlsplit


root = Path(__file__).resolve().parents[1]
names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard', '--', '*.md'], cwd=root).decode().split('\0')
failures = []
count = 0
for name in sorted(set(names) - {''}):
    path = root / name
    if not path.is_file():
        continue  # A tracked deletion is not a present document.
    count += 1
    prose = re.sub(r'(?ms)^```.*?^```[^\n]*', '', path.read_text())
    prose = re.sub(r'`[^`]*`', '', prose)
    for match in re.finditer(r'\]\((<?[^\s)]+>?)\)', prose):
        target = match.group(1).strip('<>')
        parsed = urlsplit(target)
        if parsed.scheme or parsed.netloc or not parsed.path:
            continue
        destination = path.parent / unquote(parsed.path)
        if not destination.exists():
            failures.append(f'{name}: missing {target}')
if failures:
    raise SystemExit('\n'.join(failures))
print(f'Local Markdown links checked in {count} documents.')
