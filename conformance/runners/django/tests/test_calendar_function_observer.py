from datetime import date, datetime, time, timedelta, timezone
import importlib.util
from pathlib import Path
import unittest


path = Path(__file__).resolve().parents[1] / 'calendar_extraction_reference/observe.py'
spec = importlib.util.spec_from_file_location('calendar_function_observer', path)
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)


class CalendarFunctionObserverTests(unittest.TestCase):
    def test_serialization_retains_trunc_aggregate_datetime_domains(self):
        # Pinned PostgreSQL returns an aware timestamp for MIN(TruncWeek(Date))
        # and a naive timestamp for MAX(TruncDay(DateTime, tzinfo=UTC)).
        value = {'first': datetime(1, 1, 1, tzinfo=timezone.utc),
                 'last': datetime(9999, 12, 31), 'count': 10}
        self.assertEqual(observer.canonical(value), {
            'first': '0001-01-01T00:00:00.000000Z',
            'last': '9999-12-31T00:00:00.000000', 'count': 10,
        })
        self.assertEqual(observer.canonical([datetime(2000, 2, 29, 12, 34, 56, 123456), None]),
                         ['2000-02-29T12:34:56.123456', None])
        self.assertFalse(observer.native(value['last'])['aware'])
        self.assertTrue(observer.native(value['first'])['aware'])

    def test_other_temporal_domains_keep_their_existing_contract(self):
        self.assertEqual(observer.canonical(datetime(2021, 1, 1, 1, 2, 3, 456789,
                                                     tzinfo=timezone(timedelta(hours=9)))),
                         '2020-12-31T16:02:03.456789Z')
        self.assertEqual(observer.canonical(date(2000, 2, 29)), '2000-02-29')
        self.assertEqual(observer.canonical(time(12, 34, 56, 123456)), '12:34:56.123456')
        with self.assertRaisesRegex(AssertionError, 'converted Time acquired an offset'):
            observer.canonical(time(12, tzinfo=timezone.utc))


if __name__ == '__main__':
    unittest.main()
