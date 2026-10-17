from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent


class UnicodeGenerationTests(unittest.TestCase):
    def test_checked_in_tables_are_reproducible_offline(self):
        result = subprocess.run([sys.executable, str(ROOT / "generate.py"), "--check"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_bad_or_missing_input_preserves_previous_generated_bytes(self):
        with tempfile.TemporaryDirectory(prefix="godj-unicode-generation-") as directory:
            root = Path(directory)
            (root / "generate.py").write_bytes((ROOT / "generate.py").read_bytes())
            (root / "sources.json").write_bytes((ROOT / "sources.json").read_bytes())
            previous = b"package unicode16\n// previous known-good bytes\n"
            output = root / "tables_generated.go"
            output.write_bytes(previous)
            data = root / "inputs"
            data.mkdir()
            for content in [None, b"truncated public Unicode input"]:
                if content is not None:
                    (data / "UnicodeData.txt").write_bytes(content)
                result = subprocess.run([sys.executable, str(root / "generate.py"), "--data-dir", str(data)], capture_output=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(output.read_bytes(), previous)
                self.assertFalse(list(root.glob(".tables-*")))


if __name__ == "__main__":
    unittest.main()
