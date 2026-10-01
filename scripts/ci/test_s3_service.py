import json
from pathlib import Path
import tempfile
import unittest

import s3_service


class S3ServiceProfileTests(unittest.TestCase):
    def receipt(self, directory):
        (directory / 'minio').write_bytes(b'independent binary fixture')
        (directory / 'buildinfo.txt').write_bytes(b'build identity')
        return dict(module=s3_service.MODULE, version=s3_service.VERSION,
                    commit=s3_service.COMMIT, module_sum=s3_service.MODULE_SUM,
                    mod_sum=s3_service.MOD_SUM, go_mod_sha256=s3_service.MOD_SHA,
                    go_sum_sha256=s3_service.SUM_SHA, toolchain=s3_service.TOOLCHAIN, modules_verified=True,
                    binary_sha256=s3_service.sha256(directory / 'minio'),
                    buildinfo_sha256=s3_service.sha256(directory / 'buildinfo.txt'))

    def test_edited_build_outputs_and_profile_cannot_start_a_service(self):
        for changed in ['binary_sha256', 'buildinfo_sha256', 'module', 'version',
                        'commit', 'module_sum', 'mod_sum', 'go_mod_sha256',
                        'go_sum_sha256', 'toolchain', 'modules_verified']:
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as name:
                directory = Path(name)
                receipt = self.receipt(directory)
                (directory / 'build.json').write_text(json.dumps(receipt))
                self.assertEqual(directory / 'minio', s3_service.checked_binary(directory))
                receipt[changed] = 'different'
                (directory / 'build.json').write_text(json.dumps(receipt))
                with self.assertRaises(RuntimeError):
                    s3_service.run(directory, ['unexpected-child'])
                self.assertFalse(list(directory.glob('run-*')))

    def test_module_checksum_rejection_precedes_untrusted_source_path(self):
        download = dict(Path=s3_service.MODULE, Version=s3_service.VERSION,
                        Sum=s3_service.MODULE_SUM, GoModSum=s3_service.MOD_SUM,
                        Dir='/path-that-must-not-be-read')
        for key in ('Path', 'Version', 'Sum', 'GoModSum'):
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                s3_service.validate_download(download | {key: 'different'})

    def test_existing_build_directory_is_preserved(self):
        with tempfile.TemporaryDirectory() as name:
            directory = Path(name)
            existing = directory / 'keep'
            existing.write_text('caller content')
            with self.assertRaises(RuntimeError):
                s3_service.build(directory)
            self.assertEqual('caller content', existing.read_text())


if __name__ == '__main__':
    unittest.main()
