import json
import unittest

from helpdesk_browser import REPORT_STATUS_CHECKS, SUMMARY_CHECKS, result_document, verify_committed, verify_report_status, verify_summary


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
        result = {'summary_read_only_unchanged': True, 'initial_summary_tickets': 30,
                  'committed_tickets': [{'id': i + 1, 'audit': []} for i in range(30)],
                  'committed_links': [], 'report_status_read_only_unchanged': True, 'initial_status_reports': 30,
                  'committed_reports': [{'id': i + 1, 'ticket': i + 1, 'audit': []} for i in range(30)]}
        verify_committed(result)
        for bad in ({}, dict(result, summary_read_only_unchanged=False),
                    dict(result, initial_summary_tickets=29),
                    dict(result, committed_tickets=result['committed_tickets'][:-1]),
                    dict(result, committed_links=[{'ticket': 1, 'label': 1}]),
                    dict(result, committed_tickets=[{'id': 1, 'audit': []}] * 30),
                    dict(result, committed_tickets=[{'id': i + 1, 'audit': ['change']} for i in range(30)]),
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


if __name__ == '__main__':
    unittest.main()
