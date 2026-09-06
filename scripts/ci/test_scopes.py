import unittest

from scopes import OWNERS, SCOPES, selected, verify


class ScopeTest(unittest.TestCase):
    def test_full_owns_every_lane_and_partial_never_claims_full(self):
        self.assertEqual(set(selected('full')[1]), set(OWNERS))
        for suite in SCOPES:
            _, jobs = selected(suite)
            results = {name: {'result': 'success' if name in jobs else 'skipped'} for name in OWNERS}
            self.assertEqual(verify(suite, results)['full_platform_verified'], suite == 'full')
            self.assertTrue(jobs)
            # Every selected job must actually finish successfully.
            for name in jobs:
                for state in ('failure', 'cancelled', 'skipped', None):
                    with self.subTest(suite=suite, job=name, state=state), self.assertRaises(ValueError):
                        verify(suite, dict(results, **{name: {'result': state}}))

    def test_missing_unknown_and_invalid_scope_fail_closed(self):
        results = {name: {'result': 'success'} for name in OWNERS}
        results.pop(next(iter(results)))
        with self.assertRaises(ValueError):
            verify('full', results)
        results['unowned'] = {'result': 'success'}
        with self.assertRaises(ValueError):
            verify('full', results)
        for suite in ('', 'all', 'ci:ful', 'orm,cli'):
            with self.assertRaises(ValueError):
                selected(suite)


if __name__ == '__main__':
    unittest.main()
