import tempfile
import unittest
from pathlib import Path

from capture_artifact import pack, verify


class CaptureArtifactTest(unittest.TestCase):
    def test_provenance_and_payload_are_both_required(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            capture = directory / 'capture.json'
            capture.write_text('{"scenario": "observed"}')
            run = {'checkout': 'a' * 40, 'GITHUB_RUN_ID': '1', 'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_REPOSITORY': 'org/repo'}
            pack(directory, capture.name, run)
            verify(directory, capture.name, run)
            for key in run:
                with self.subTest(key=key), self.assertRaises(ValueError):
                    verify(directory, capture.name, dict(run, **{key: 'different'}))
            capture.write_text('{"scenario": "forged"}')
            with self.assertRaises(ValueError):
                verify(directory, capture.name, run)

    def test_path_and_symlink_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / 'capture.json').write_text('{}')
            (directory / 'link.json').symlink_to(directory / 'capture.json')
            for name in ('../capture.json', 'link.json'):
                with self.subTest(name=name), self.assertRaises(ValueError):
                    pack(directory, name, {})


if __name__ == '__main__':
    unittest.main()
