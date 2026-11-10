import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock

SPEC = importlib.util.spec_from_file_location('minio_binary', Path(__file__).with_name('minio-test-binary.py'))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class MinioBinaryCacheTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.inputs = {'commit': MODULE.COMMIT, 'GOVERSION': 'go1.26.0', 'GOOS': 'linux', 'GOARCH': 'amd64'}
        self.build = Mock(side_effect=lambda path: path.write_bytes(b'pinned-test-binary'))

    def resolve(self, inputs=None):
        return MODULE.resolve(self.root, inputs or self.inputs, self.build)

    def test_warm_cache_does_not_compile(self):
        first = self.resolve()
        self.assertEqual(first, self.resolve())
        self.build.assert_called_once()

    def test_modified_binary_is_rebuilt(self):
        binary = self.resolve()
        binary.write_bytes(b'corrupted')
        self.assertEqual(self.resolve().read_bytes(), b'pinned-test-binary')
        self.assertEqual(self.build.call_count, 2)

    def test_toolchain_change_invalidates(self):
        first = self.resolve()
        self.assertNotEqual(first, self.resolve({**self.inputs, 'GOVERSION': 'go1.27.0'}))
        self.assertEqual(self.build.call_count, 2)

    def test_commit_change_invalidates(self):
        first = self.resolve()
        self.assertNotEqual(first, self.resolve({**self.inputs, 'commit': 'a' * 40}))

    def test_missing_or_invalid_manifest_is_rebuilt(self):
        binary = self.resolve()
        binary.with_name('manifest.json').write_text('invalid json')
        self.resolve()
        self.assertEqual(self.build.call_count, 2)

    def test_failed_build_never_becomes_a_cache_hit(self):
        self.build.side_effect = RuntimeError('compile failed')
        with self.assertRaises(RuntimeError):
            self.resolve()
        self.assertFalse(list(self.root.glob('*/minio')))
        self.assertFalse(list(self.root.glob('*/manifest.json')))

    def test_non_executable_binary_is_rebuilt(self):
        self.resolve().chmod(0o644)
        self.resolve()
        self.assertEqual(self.build.call_count, 2)

    def test_symlink_is_not_accepted_as_a_hit(self):
        binary = self.resolve()
        original = binary.with_name('other')
        binary.replace(original)
        binary.symlink_to(original)
        self.assertFalse(MODULE.valid(binary, binary.with_name('manifest.json'), self.inputs))
        self.resolve()
        self.assertFalse(binary.is_symlink())


if __name__ == '__main__':
    unittest.main()
