import copy
import unittest

from admin_browser import CHECKS, verify_native, verify_probe


class AdminBrowserEvidenceTests(unittest.TestCase):
    def test_missing_duplicate_foreign_checks_and_browser_versions_fail(self):
        for flow, checks in CHECKS.items():
            valid = {'passed': len(checks), 'cases': list(checks), 'browser_version': 'synthetic-version'}
            verify_probe(flow, valid, 'synthetic-version')
            failures = [
                valid | {'cases': list(checks[:-1])},
                valid | {'cases': [checks[0], *checks[:-1]]},
                valid | {'cases': ['foreign check', *checks[1:]]},
                valid | {'passed': True},
                valid | {'browser_version': 'other-version'},
                valid | {'unexpected': 'not part of the receipt'},
            ]
            for failure in failures:
                with self.subTest(flow=flow, failure=failure):
                    with self.assertRaises(ValueError):
                        verify_probe(flow, failure, 'synthetic-version')

    def upload_state(self):
        return {
            'initial_rows_unchanged': True,
            'categories': [{'id': 1, 'name': 'Browser category'}],
            'labels': [
                {'id': 1, 'category': 1, 'name': 'Original __prefix__'},
                {'id': 2, 'category': 1, 'name': 'Verified upload'},
            ],
            'tickets': [
                {'id': 1, 'category': 1, 'subject': 'Existing ticket', 'closed': False},
                {'id': 2, 'category': 1, 'subject': 'Second ticket for upload', 'closed': False},
            ],
            'links': 0, 'reports': 0,
        }

    def test_native_upload_proof_rejects_truncation_mutation_and_wrong_wire_types(self):
        state = self.upload_state()
        verify_native('uploads', state)
        for path, value in [
            (('initial_rows_unchanged',), False),
            (('initial_rows_unchanged',), 1),
            (('labels',), state['labels'][:1]),
            (('labels',), state['labels'] + [state['labels'][1]]),
            (('labels', 1, 'category'), 2),
            (('labels', 0, 'name'), 'Changed original'),
            (('tickets', 1, 'closed'), 0),
            (('tickets', 1, 'subject'), 'Uncommitted input'),
            (('reports',), 1),
            (('links',), 0.0),
        ]:
            with self.subTest(path=path, value=value):
                altered = copy.deepcopy(state)
                parent = altered
                for key in path[:-1]:
                    parent = parent[key]
                parent[path[-1]] = value
                with self.assertRaises(ValueError):
                    verify_native('uploads', altered)
        for key in state:
            altered = copy.deepcopy(state)
            del altered[key]
            with self.assertRaises(ValueError):
                verify_native('uploads', altered)

    def test_native_inline_requires_the_new_parent_and_both_complete_child_cohorts(self):
        state = {
            'initial_rows_unchanged': True,
            'categories': [{'id': 1, 'name': 'Browser category'}, {'id': 2, 'name': 'New via browser'}],
            'labels': [
                {'id': 1, 'category': 1, 'name': 'Original __prefix__'},
                {'id': 2, 'category': 1, 'name': 'keep labels-2-name __prefix__'},
                {'id': 3, 'category': 2, 'name': 'New child __prefix__'},
            ],
            'tickets': [
                {'id': 1, 'category': 1, 'subject': 'Existing ticket', 'closed': False},
                {'id': 2, 'category': 1, 'subject': 'Saved ticket __prefix__', 'closed': True},
                {'id': 3, 'category': 2, 'subject': 'First required ticket', 'closed': False},
                {'id': 4, 'category': 2, 'subject': 'Second required ticket', 'closed': False},
            ],
            'links': 0, 'reports': 0,
        }
        verify_native('inline', state)
        for collection in ('categories', 'labels', 'tickets'):
            altered = copy.deepcopy(state)
            altered[collection].pop()
            with self.assertRaises(ValueError):
                verify_native('inline', altered)
        altered = copy.deepcopy(state)
        altered['tickets'][3]['category'] = 1
        with self.assertRaises(ValueError):
            verify_native('inline', altered)
        with self.assertRaises(ValueError):
            verify_native('uploads', state)

    def test_native_choices_reject_missing_numeric_foreign_and_changed_seed_evidence(self):
        labels = [{'id': '-7', 'category': '1', 'name': 'Choice negative'},
                  {'id': '0', 'category': '1', 'name': 'Choice zero'}]
        labels += [{'id': str(key), 'category': '1', 'name': 'Choice %02d' % key}
                   for key in range(1, 31)]
        labels += [
            {'id': '31', 'category': '1', 'name': 'Revoked target'},
            {'id': '32', 'category': '1', 'name': 'Choice <img src=x onerror="window.choiceInjected=true">'},
            {'id': '9007199254740993', 'category': '1', 'name': 'Choice selected outside page'},
            {'id': '9007199254740994', 'category': '2', 'name': 'Choice foreign'},
        ]
        state = {
            'seed_scalars_preserved': True,
            'categories': [{'id': '1', 'name': 'Choice category'}, {'id': '2', 'name': 'Foreign category'}],
            'labels': labels,
            'tickets': [{'id': '1', 'category': '1', 'subject': 'Choice owner', 'closed': False},
                        {'id': '2', 'category': '2', 'subject': 'Foreign owner', 'closed': False},
                        {'id': '3', 'category': '1', 'subject': 'Browser inline', 'closed': False}],
            'links': [{'ticket': '1', 'label': '-7'}, {'ticket': '1', 'label': '9007199254740993'},
                      {'ticket': '2', 'label': '9007199254740994'}, {'ticket': '3', 'label': '0'}],
            'reports': 0,
        }
        verify_native('choices', state)
        for path, value in [
            (('seed_scalars_preserved',), False),
            (('seed_scalars_preserved',), 1),
            (('labels',), state['labels'][:-1]),
            (('tickets',), state['tickets'][:-1]),
            (('links',), state['links'][:-1]),
            (('links',), state['links'] + [state['links'][0]]),
            (('labels', 34, 'id'), 9007199254740993),
            (('labels', 34, 'id'), '9007199254740992'),
            (('links', 1, 'label'), '9007199254740994'),
            (('links', 3, 'label'), 0),
            (('links', 3, 'ticket'), '2'),
            (('labels', 0, 'name'), 'Changed seed'),
            (('labels', 32, 'name'), 'Choice stale'),
            (('tickets', 0, 'subject'), 'Rejected stale overwrite'),
            (('tickets', 2, 'closed'), 0),
            (('reports',), 1),
        ]:
            with self.subTest(path=path, value=value):
                altered = copy.deepcopy(state)
                parent = altered
                for key in path[:-1]:
                    parent = parent[key]
                parent[path[-1]] = value
                with self.assertRaises(ValueError):
                    verify_native('choices', altered)
        for key in state:
            altered = copy.deepcopy(state)
            del altered[key]
            with self.assertRaises(ValueError):
                verify_native('choices', altered)
        with self.assertRaises(ValueError):
            verify_native('choices', state | {'unexpected': []})


if __name__ == '__main__':
    unittest.main()
