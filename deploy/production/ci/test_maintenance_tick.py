import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

from maintenance_tick import tick


class MaintenanceCadenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.state = Path(self.temp.name) / 'sweep.json'
        self.recover = Mock(return_value={'status':'reclaimed'})
        self.sweep = Mock(return_value='ok')

    def test_first_success_records_cache_cadence(self):
        self.assertEqual(tick(self.state, 10000, self.recover, self.sweep)['cache'], 'ok')
        self.assertEqual(json.loads(self.state.read_text()), {'last_success':10000})
        self.recover.assert_called_once()
        self.sweep.assert_called_once()

    def test_memory_recovers_each_tick_but_cache_is_hourly(self):
        tick(self.state, 10000, self.recover, self.sweep)
        self.assertEqual(tick(self.state, 10060, self.recover, self.sweep)['cache'], 'not_due')
        self.assertEqual(self.recover.call_count, 2)
        self.sweep.assert_called_once()
        self.assertEqual(tick(self.state, 13600, self.recover, self.sweep)['cache'], 'ok')
        self.assertEqual(self.sweep.call_count, 2)

    def test_busy_cache_sweep_retries_without_touching_success_timestamp(self):
        self.sweep.return_value = 'skipped_busy'
        self.assertEqual(tick(self.state, 10000, self.recover, self.sweep)['cache'], 'skipped_busy')
        self.assertFalse(self.state.exists())
        tick(self.state, 10060, self.recover, self.sweep)
        self.assertEqual(self.sweep.call_count, 2)

    def test_failed_cache_sweep_keeps_existing_timestamp(self):
        self.state.write_text('{"last_success": 5000}')
        self.sweep.side_effect = RuntimeError('failed')
        with self.assertRaises(RuntimeError):
            tick(self.state, 10000, self.recover, self.sweep)
        self.assertEqual(json.loads(self.state.read_text()), {'last_success':5000})

    def test_invalid_timestamp_keeps_memory_recovery_and_blocks_cache_changes(self):
        for value in ('invalid', '{"last_success": "nan"}', '{"last_success": 999999}'):
            self.state.write_text(value)
            self.assertEqual(tick(self.state, 10000, self.recover, self.sweep)['cache'], 'skipped_invalid_timestamp')
        self.assertEqual(self.recover.call_count, 3)
        self.sweep.assert_not_called()


if __name__ == '__main__':
    unittest.main()
