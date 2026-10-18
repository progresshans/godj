"""Data provenance guards; real rules/query execution belongs to native CI."""
import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

PROFILE = Path(__file__).resolve().parents[1] / 'zoned_calendar_reference'
SPEC = importlib.util.spec_from_file_location('zoned_reference_guards', PROFILE / 'observe.py')
OBSERVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(OBSERVER)


class ZonedReferenceDataGuards(unittest.TestCase):
    def test_pinned_inventory_rejects_changed_missing_extra_and_unsafe_sources(self):
        # Synthetic bytes exercise the verifier without loading tzdata or any
        # native database. They are never used as calendar expectation data.
        source = json.loads((PROFILE / 'data-source.json').read_text())
        original = {}
        for name in source['files']:
            raw = (b'TZif' if name.startswith('zoneinfo/') and name != 'zoneinfo/tzdata.zi' else b'fixture') + name.encode()
            original[name] = raw
            source['files'][name] = {'bytes': len(raw), 'sha256': OBSERVER.digest(raw)}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, raw in original.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(raw)
            OBSERVER.verify_zone_data(root, source)
            for mutation in ('missing', 'changed', 'shortened', 'extra', 'duplicate_names', 'missing_name',
                             'name_hash', 'unbound_file', 'unsafe_path', 'wrong_contract'):
                with self.subTest(mutation=mutation):
                    altered = copy.deepcopy(source)
                    selected = root / 'zoneinfo/UTC'
                    if mutation == 'missing':
                        selected.unlink()
                    elif mutation == 'changed':
                        selected.write_bytes(b'TZif' + b'X' * (len(original['zoneinfo/UTC']) - 4))
                    elif mutation == 'shortened':
                        selected.write_bytes(original['zoneinfo/UTC'][:-1])
                    elif mutation == 'extra':
                        (root / 'zoneinfo/Unexpected').write_bytes(b'TZifunexpected')
                    elif mutation == 'duplicate_names':
                        altered['names'].append('UTC')
                    elif mutation == 'missing_name':
                        altered['names'].remove('UTC')
                    elif mutation == 'name_hash':
                        altered['names_sha256'] = '0' * 64
                    elif mutation == 'unbound_file':
                        del altered['files']['zoneinfo/UTC']
                    elif mutation == 'unsafe_path':
                        altered['files']['../outside'] = {'bytes': 0, 'sha256': OBSERVER.digest(b'')}
                    elif mutation == 'wrong_contract':
                        altered['kind'] = 'other'
                    try:
                        with self.assertRaises((ValueError, FileNotFoundError)):
                            OBSERVER.verify_zone_data(root, altered)
                    finally:
                        selected.write_bytes(original['zoneinfo/UTC'])
                        (root / 'zoneinfo/Unexpected').unlink(missing_ok=True)
            OBSERVER.verify_zone_data(root, source)

    def test_input_has_complete_function_family_and_distinct_boundaries(self):
        inputs = json.loads((PROFILE / 'input.json').read_text())
        self.assertEqual(inputs['kind'], 'independent-zoned-calendar-inputs-v1')
        names = [fixture['name'] for fixture in inputs['fixtures']]
        self.assertEqual(len(names), len(set(names)))
        self.assertEqual(len(names), 11)
        self.assertEqual(len(inputs['operations']), len(set(inputs['operations'])))
        self.assertEqual(len(inputs['operations']), 28)
        self.assertEqual(set(inputs['operations'][:21]), {'date', 'time', 'year', 'iso_year', 'quarter', 'month', 'week',
            'week_day', 'iso_week_day', 'day', 'hour', 'minute', 'second', 'trunc_year', 'trunc_quarter', 'trunc_month',
            'trunc_week', 'trunc_day', 'trunc_hour', 'trunc_minute', 'trunc_second'})
        for fixture in inputs['fixtures']:
            self.assertTrue(fixture['instants'][0])
            self.assertIsNone(fixture['instants'][-1])
            self.assertEqual(len(fixture['instants']), len(set(fixture['instants'])))
        self.assertEqual({fixture['name'] for fixture in inputs['failures']},
                         {'lower_date', 'lower_year', 'upper_date', 'upper_year', 'inverse_lower',
                          'corrupt_month', 'unselected_corruption', 'filtered_corruption'})


if __name__ == '__main__':
    unittest.main()
