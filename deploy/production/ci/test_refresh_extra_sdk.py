import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('refresh_sdk_tested', Path(__file__).with_name('refresh_extra_sdk.py'))
SDK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SDK)


class RefreshSDKTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.folder = Path(temporary.name)
        self.runner = self.folder / 'runner'
        self.runner.mkdir()
        for name in ['bin', 'externals']:
            (self.runner / name).mkdir()
            (self.runner / name / 'original').write_text(name)
        (self.runner / '.ci-sdk-version').write_text('2.337.0\n')
        (self.runner / '.service').write_text('actions.runner.west6ide-mail.test.service')
        self.lease = self.folder / 'job.lock'
        self.lease.touch()

    def install(self, target):
        for name in ['bin', 'externals']:
            (target / name).mkdir()
            (target / name / 'new').write_text(name)

    def refresh(self, execute):
        with patch.object(SDK, 'LEASE', self.lease), \
             patch.object(SDK, 'configured_runners', return_value={'mail': str(self.runner)}), \
             patch.object(SDK, 'sdk_version', return_value='2.338.0'), \
             patch.object(SDK, 'busy', return_value=False), \
             patch.object(SDK, 'install_sdk', side_effect=self.install), \
             patch.object(SDK.subprocess, 'run', side_effect=execute):
            return SDK.refresh()

    def test_idle_runner_refreshes_and_preserves_configuration(self):
        result = self.refresh(lambda *args, **kwargs: None)
        self.assertEqual(result['updated'], ['mail'])
        self.assertEqual((self.runner / '.ci-sdk-version').read_text(), '2.338.0\n')
        for name in ['bin', 'externals']:
            self.assertEqual((self.runner / name / 'new').read_text(), name)
        self.assertEqual((self.runner / '.service').read_text(), 'actions.runner.west6ide-mail.test.service')

    def test_activation_failure_restores_both_trees_and_version(self):
        failed = False

        def execute(command, **kwargs):
            nonlocal failed
            if command[1] == 'start' and not failed:
                failed = True
                raise subprocess.CalledProcessError(1, command)

        with self.assertRaises(subprocess.CalledProcessError):
            self.refresh(execute)
        self.assertEqual((self.runner / '.ci-sdk-version').read_text(), '2.337.0\n')
        for name in ['bin', 'externals']:
            self.assertEqual((self.runner / name / 'original').read_text(), name)
        self.assertFalse(list(self.runner.glob('.sdk-*')))


if __name__ == '__main__':
    unittest.main()
