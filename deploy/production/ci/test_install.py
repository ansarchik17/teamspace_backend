from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import install as installer


class InstallationPreflightTests(unittest.TestCase):
    def test_wrong_host_is_rejected_before_any_changes(self):
        with patch.object(installer.socket, 'gethostname', return_value='other-host'), \
             patch.object(installer, 'ROOT') as root, patch.object(installer.subprocess, 'run') as run:
            with self.assertRaisesRegex(RuntimeError, 'Unexpected server'):
                installer.main()
            root.mkdir.assert_not_called()
            run.assert_not_called()

    def test_active_worker_blocks_installation(self):
        self.check_worker_probe(0, 'A job is running')

    def test_failed_worker_probe_fails_closed(self):
        self.check_worker_probe(2, 'Unable to determine')

    def check_worker_probe(self, code, message):
        with patch.object(installer.socket, 'gethostname', return_value='vds38185.vpsza500.kz'), \
             patch.object(installer, 'ROOT') as root, \
             patch.object(installer.subprocess, 'run', return_value=SimpleNamespace(returncode=code)):
            with self.assertRaisesRegex(RuntimeError, message):
                installer.main()
            root.mkdir.assert_not_called()


if __name__ == '__main__':
    unittest.main()
