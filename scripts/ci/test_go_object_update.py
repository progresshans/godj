import hashlib
from pathlib import Path
import tempfile
import unittest

from go_object_update import apply, stage


class GoObjectUpdateTests(unittest.TestCase):
    def test_seed_and_bounded_updates_remain_independent(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            objects, update = directory / 'objects', directory / 'update'
            objects.mkdir()
            index = directory / 'seed.json'

            def action(label, value):
                key = hashlib.sha256(label.encode()).hexdigest()
                digest = hashlib.sha256(value).hexdigest()
                data = objects / digest[:2] / (digest + '-d')
                data.parent.mkdir(exist_ok=True)
                data.write_bytes(value)
                entry = objects / key[:2] / (key + '-a')
                entry.parent.mkdir(exist_ok=True)
                entry.write_text(f'v1 {key} {digest} {len(value)} 0\n')
                return entry, data

            seed, shared = action('seed', b'shared')
            apply(objects, update, index, 16000)
            new, fresh = action('new', b'new input')
            alias, _ = action('alias', b'shared')
            large, big = action('oversize', b'x' * 20000)
            self.assertTrue(stage(objects, update, index, 16000))
            names = {p.name for p in update.glob('*/*')}
            self.assertEqual(names, {new.name, fresh.name, alias.name})
            new.unlink()
            fresh.unlink()
            apply(objects, update, index, 1)
            self.assertFalse(new.exists())
            index.unlink()
            apply(objects, update, index, 16000, snapshot=False)
            self.assertFalse(index.exists())
            self.assertEqual(fresh.read_bytes(), b'new input')
            self.assertTrue(all(p.is_file() for p in (new, seed, shared, large, big)))
