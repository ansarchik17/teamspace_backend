import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import extra_runners


class ExtraRunnerRegistryTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.path = Path(temporary.name) / 'runners.json'

    def load(self):
        return extra_runners.configured_runners(self.path, trusted_uid=os.getuid())

    def test_absent_registry_keeps_original_runners(self):
        self.assertEqual(self.load(), {})

    def test_empty_registry_is_valid(self):
        self.path.write_text('{}')
        self.assertEqual(self.load(), {})

    def test_unknown_repository_rejected(self):
        self.path.write_text(json.dumps({'another-owner': '/opt/anything'}))
        with self.assertRaises(RuntimeError):
            self.load()

    def test_writable_registry_rejected(self):
        self.path.write_text('{}')
        self.path.chmod(0o666)
        with self.assertRaises(RuntimeError):
            self.load()

    def test_symlink_registry_rejected(self):
        other = self.path.with_name('other')
        other.write_text('{}')
        self.path.symlink_to(other)
        with self.assertRaises(RuntimeError):
            self.load()

    def test_path_escape_rejected_before_registration_read(self):
        self.path.write_text(json.dumps({'workspace-mail': '/opt/../../root'}))
        with self.assertRaises(RuntimeError):
            self.load()

    def test_mismatched_github_registration_rejected(self):
        self.path.write_text(json.dumps({'workspace-mail': '/opt/actions-runner-ci-workspace-mail'}))
        with patch.object(Path, 'read_text', side_effect=[self.path.read_text(), '{"gitHubUrl":"https://github.com/another/repo"}']):
            with self.assertRaises(RuntimeError):
                self.load()


if __name__ == '__main__':
    unittest.main()
