"""Read complete GitHub Actions API inventories, retrying transient reads."""
import json
import re
import subprocess
import time


def github_read(*arguments):
    for attempt in range(3):
        try:
            return subprocess.run(["gh", "api", *arguments], check=True, capture_output=True, timeout=90).stdout
        except subprocess.TimeoutExpired:
            if attempt == 2:
                raise
        except subprocess.CalledProcessError as error:
            if attempt == 2 or not re.search(rb"\bHTTP (429|500|502|503|504)\b", error.stderr or b""):
                raise
        time.sleep(2 ** attempt)


def github_records(path, key):
    separator = '&' if '?' in path else '?'
    # Job records include every step, even for jobs retained on a retry.
    # Retry a failed page without repeatedly downloading the preceding pages.
    size = 30 if key == 'jobs' else 100
    records = []
    page = 1
    while True:
        response = json.loads(github_read(f"{path}{separator}per_page={size}&page={page}"))
        batch, total = response[key], response['total_count']
        if not isinstance(batch, list) or len(batch) > size or type(total) is not int or total < 0:
            raise ValueError(f'invalid GitHub {key} page {page}')
        records.extend(batch)
        if len(records) == total:
            return records
        if len(records) > total or len(batch) < size:
            raise ValueError(f'incomplete GitHub {key} page {page}')
        page += 1
