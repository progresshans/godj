"""Fill the verified module cache before running offline Go product tests."""

import re
import subprocess
import sys
import time


# Retry transport errors emitted by the Go client. An additional error line,
# including a checksum or authentication failure, makes the download fail.
TRANSIENT = re.compile(
    r"(?:stream error: stream ID [0-9]+; (?:INTERNAL_ERROR|REFUSED_STREAM); received from peer|"
    r"connection reset by peer|unexpected EOF|TLS handshake timeout|i/o timeout|"
    r"429 Too Many Requests|502 Bad Gateway|503 Service Unavailable|504 Gateway Timeout)$"
)


def retryable(stderr):
    errors = [line.strip() for line in stderr.splitlines()
              if line.strip() and not line.startswith("go: downloading ")]
    return bool(errors) and all(line.startswith("go: ") and TRANSIENT.search(line) for line in errors)


def download():
    for attempt in range(3):
        result = subprocess.run(["go", "mod", "download", "all"], stderr=subprocess.PIPE,
                                text=True, encoding="utf-8", errors="replace")
        sys.stderr.write(result.stderr)
        if result.returncode <= 0 or attempt == 2 or not retryable(result.stderr):
            return result.returncode if result.returncode >= 0 else 128 - result.returncode
        delay = 2 ** attempt
        print(f"Retrying Go module download in {delay}s after a transport error", file=sys.stderr, flush=True)
        time.sleep(delay)


if __name__ == "__main__":
    sys.exit(download())
