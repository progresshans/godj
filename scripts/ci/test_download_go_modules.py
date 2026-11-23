import contextlib
import io
import subprocess
import unittest
from unittest.mock import patch

from download_go_modules import download


class GoModuleDownloadTest(unittest.TestCase):
    transport = ("go: example.org/mod@v1.0.0: read https://proxy.golang.org/example.org/mod/@v/v1.0.0.zip: "
                 "stream error: stream ID 109; INTERNAL_ERROR; received from peer\n")

    def check_downloads(self, responses, delays, expected):
        diagnostics = io.StringIO()
        results = [subprocess.CompletedProcess([], code, stderr=message) for code, message in responses]
        with patch("download_go_modules.subprocess.run", side_effect=results) as run, \
                patch("download_go_modules.time.sleep") as sleep, contextlib.redirect_stderr(diagnostics):
            self.assertEqual(download(), expected)
        self.assertEqual([c.args for c in run.call_args_list], [(["go", "mod", "download", "all"],)] * len(responses))
        self.assertEqual([c.args[0] for c in sleep.call_args_list], delays)
        self.assertIn(responses[0][1], diagnostics.getvalue())

    def test_transport_error_retries_the_same_download(self):
        self.check_downloads([(1, self.transport), (0, "")], [1], 0)
        self.check_downloads([(1, self.transport)] * 3, [1, 2], 1)

    def test_integrity_authentication_and_interruption_stop_immediately(self):
        for status, diagnostic in [(1, self.transport + "SECURITY ERROR: checksum mismatch\n"),
                                   (1, "go: reading module: 403 Forbidden\n"), (1, ""), (-15, self.transport)]:
            with self.subTest(status=status, diagnostic=diagnostic):
                self.check_downloads([(status, diagnostic)], [], 143 if status < 0 else status)
