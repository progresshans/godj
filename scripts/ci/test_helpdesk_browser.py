import json
import unittest

from helpdesk_browser import DEADLINE_CALENDAR_CHECKS, verify_deadline_calendar, DEADLINE_PREVIEW_CHECKS, verify_deadline_preview, COST_PREVIEW_CHECKS, verify_cost_preview, LABEL_USAGE_CHECKS, verify_label_usage, REPORT_STATUS_CHECKS, SUMMARY_CHECKS, result_document, verify_committed, verify_report_status, verify_summary


class BrowserEvidenceTest(unittest.TestCase):
    def test_cli_process_success_cannot_hide_a_tool_error_or_missing_result(self):
        result = {'passed': 30, 'cases': list(SUMMARY_CHECKS)}
        output = '### Result\n' + json.dumps(result) + '\n### Ran Playwright code\nprobe();\n'
        self.assertEqual(verify_summary(result_document(output)), result)
        for bad in ('', '### Error\ntimeout\n', output + '### Error\nlate failure\n',
                    output + output, '### Result\nnull\n', '### Result\n{}\ntruncated result'):
            with self.subTest(output=bad), self.assertRaises(ValueError):
                result_document(bad)
        for bad in ({}, dict(result, passed=29), dict(result, passed=30.0),
                    dict(result, cases=result['cases'][:-1]), dict(result, cases=['same'] * 30), dict(result,cases=['foreign-'+str(i) for i in range(30)])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_summary(bad)

    def test_stored_result_requires_full_read_only_proof_and_no_audit(self):
        result = {'deadline_calendar_read_only_unchanged': True, 'deadline_preview_read_only_unchanged': True, 'cost_preview_read_only_unchanged': True, 'summary_read_only_unchanged': True, 'initial_summary_tickets': 30,
                  'committed_tickets': [{'id': i + 1, 'audit': [], 'expected_cost': {2: '0.1', 3: '0.2'}.get(i + 1),
                                         'due_at': {2: '2024-02-28T23:59:59.999999Z', 3: '2024-02-29T00:00:00.000001Z'}.get(i + 1)} for i in range(30)],
                  'committed_links': [{'ID': index + 1, 'TicketID': ticket, 'LabelID': label}
                                      for index, (ticket, label) in enumerate([(1, 1), (2, 1), (3, 1)] + [(1, label) for label in range(3, 27)])],
                  'label_usage_read_only_unchanged': True, 'initial_usage_labels': 26, 'initial_usage_links': 27,
                  'committed_labels': [{'id': i + 1, 'category': 2 if i == 25 else 1, 'audit': []} for i in range(26)],
                  'report_status_read_only_unchanged': True, 'initial_status_reports': 30,
                  'committed_reports': [{'id': i + 1, 'ticket': i + 1, 'audit': []} for i in range(30)]}
        verify_committed(result)
        for bad in ({}, dict(result, cost_preview_read_only_unchanged=False),
                    {key: value for key, value in result.items() if key != 'deadline_calendar_read_only_unchanged'},
                    dict(result, deadline_calendar_read_only_unchanged=False),
                    {key: value for key, value in result.items() if key != 'deadline_preview_read_only_unchanged'},
                    dict(result, deadline_preview_read_only_unchanged=False),
                    dict(result, committed_tickets=[{key: value for key, value in row.items() if key != 'due_at'} for row in result['committed_tickets']]),
                    dict(result, committed_tickets=[dict(row, due_at=None) for row in result['committed_tickets']]),
                    dict(result, committed_tickets=[dict(row, due_at='2024-02-29T00:00:00.000002Z') if row['id'] == 3 else row for row in result['committed_tickets']]),
                    dict(result, committed_tickets=[dict(row, due_at='2024-02-29T09:00:00.000001+09:00') if row['id'] == 3 else row for row in result['committed_tickets']]),
                    dict(result, committed_tickets=[dict(row, due_at='2024-02-29T00:00:00.000001Z') if row['id'] == 1 else row for row in result['committed_tickets']]),
                    dict(result, committed_tickets=[dict(row, expected_cost='0') for row in result['committed_tickets']]),
                    dict(result, summary_read_only_unchanged=False),
                    dict(result, initial_summary_tickets=29),
                    dict(result, committed_tickets=result['committed_tickets'][:-1]),
                    dict(result, committed_links=[{'ticket': 1, 'label': 1}]),
                    dict(result, committed_tickets=[{'id': 1, 'audit': []}] * 30),
                    dict(result, committed_tickets=[{'id': i + 1, 'audit': ['change']} for i in range(30)]),
                    dict(result, label_usage_read_only_unchanged=False),
                    dict(result, initial_usage_labels=25), dict(result, initial_usage_links=26),
                    dict(result, committed_labels=result['committed_labels'][:-1]),
                    dict(result, committed_labels=[dict(row, audit=['change']) for row in result['committed_labels']]),
                    dict(result, committed_links=result['committed_links'][:-1]),
                    dict(result, committed_links=[dict(row, LabelID=2) for row in result['committed_links']]),
                    dict(result, report_status_read_only_unchanged=False),
                    dict(result, initial_status_reports=29), dict(result, committed_reports=result['committed_reports'][:-1]),
                    dict(result, committed_reports=[{'id': i + 1, 'ticket': i + 2, 'audit': []} for i in range(30)]),
                    dict(result, committed_reports=[{'id': i + 1, 'ticket': i + 1, 'audit': ['change']} for i in range(30)])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_committed(bad)

    def test_report_status_requires_the_exact_checklist(self):
        result = {'passed': len(REPORT_STATUS_CHECKS), 'cases': list(REPORT_STATUS_CHECKS)}
        self.assertEqual(verify_report_status(result), result)
        for bad in ({}, dict(result, passed=True), dict(result, passed=result['passed'] - 1),
                    dict(result, cases=result['cases'][:-1]),
                    dict(result, cases=[result['cases'][0]] * result['passed']),
                    dict(result, cases=['foreign-' + str(i) for i in range(result['passed'])])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_report_status(bad)

    def test_label_usage_requires_the_exact_checklist(self):
        result = {'passed': len(LABEL_USAGE_CHECKS), 'cases': list(LABEL_USAGE_CHECKS)}
        self.assertEqual(verify_label_usage(result), result)
        for bad in ({}, dict(result, passed=True), dict(result, passed=result['passed'] - 1),
                    dict(result, cases=result['cases'][:-1]),
                    dict(result, cases=[result['cases'][0]] * result['passed']),
                    dict(result, cases=['foreign-' + str(i) for i in range(result['passed'])])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_label_usage(bad)


    def test_cost_preview_requires_every_browser_check(self):
        result = {'passed': len(COST_PREVIEW_CHECKS), 'cases': list(COST_PREVIEW_CHECKS)}
        self.assertEqual(verify_cost_preview(result), result)
        for bad in ({}, dict(result, passed=True), dict(result, passed=result['passed'] - 1),
                    dict(result, cases=result['cases'][:-1]),
                    dict(result, cases=[result['cases'][0]] * result['passed']),
                    dict(result, cases=['foreign-' + str(i) for i in range(result['passed'])])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_cost_preview(bad)

    def test_deadline_preview_requires_every_check_once(self):
        result = {'passed': len(DEADLINE_PREVIEW_CHECKS), 'cases': list(DEADLINE_PREVIEW_CHECKS)}
        self.assertEqual(verify_deadline_preview(result), result)
        for bad in ({}, dict(result, passed=True), dict(result, passed=float(result['passed'])),
                    dict(result, passed=result['passed'] - 1), dict(result, cases=result['cases'][:-1]),
                    dict(result, cases=result['cases'] + [result['cases'][0]]),
                    dict(result, cases=[result['cases'][0]] * result['passed']),
                    dict(result, cases=['foreign-' + str(i) for i in range(result['passed'])]),
                    dict(result, cases=[None] + result['cases'][1:])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_deadline_preview(bad)


    def test_deadline_calendar_requires_every_check_once(self):
        result = {'passed': len(DEADLINE_CALENDAR_CHECKS), 'cases': list(DEADLINE_CALENDAR_CHECKS)}
        self.assertEqual(verify_deadline_calendar(result), result)
        for bad in ({}, dict(result, passed=True), dict(result, passed=float(result['passed'])),
                    dict(result, passed=result['passed'] - 1), dict(result, cases=result['cases'][:-1]),
                    dict(result, cases=result['cases'] + [result['cases'][0]]),
                    dict(result, cases=[result['cases'][0]] * result['passed']),
                    dict(result, cases=['foreign-' + str(i) for i in range(result['passed'])]),
                    dict(result, cases=[None] + result['cases'][1:])):
            with self.subTest(result=bad), self.assertRaises(ValueError):
                verify_deadline_calendar(bad)
        # Every period/form/error assertion is mandatory, including when the
        # old daily-only receipt still has a plausible positive count.
        for missing in DEADLINE_CALENDAR_CHECKS:
            cases = [name for name in DEADLINE_CALENDAR_CHECKS if name != missing]
            with self.subTest(missing=missing), self.assertRaises(ValueError):
                verify_deadline_calendar({'passed': len(cases), 'cases': cases})



if __name__ == '__main__':
    unittest.main()
