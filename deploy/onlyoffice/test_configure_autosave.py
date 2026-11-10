import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("configure", Path(__file__).with_name("configure-autosave.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ConfigureTests(unittest.TestCase):
    def test_preserves_existing_configuration_and_permissions(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "local.json"
            original = {"services": {"CoAuthoring": {"secret": {"session": {"string": "test-only"}}, "sql": {"dbName": "existing"}}}, "other": [1, 2]}
            path.write_text(json.dumps(original))
            path.chmod(0o640)
            module.configure(path)
            result = json.loads(path.read_text())
            settings = result["services"]["CoAuthoring"]
            self.assertEqual(settings["sql"], original["services"]["CoAuthoring"]["sql"])
            self.assertEqual(settings["secret"], original["services"]["CoAuthoring"]["secret"])
            self.assertEqual(result["other"], [1, 2])
            self.assertTrue(settings["autoAssembly"]["enable"])
            self.assertEqual(settings["callbackBackoffOptions"]["retries"], 8)
            self.assertEqual(path.stat().st_mode & 0o777, 0o640)
            module.configure(path)
            self.assertEqual(json.loads(path.read_text()), result)

    def test_invalid_config_is_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "local.json"
            for invalid in ['broken', '{"services":null}']:
                path.write_text(invalid)
                with self.assertRaises((ValueError, AttributeError)):
                    module.configure(path)
                self.assertEqual(path.read_text(), invalid)


if __name__ == "__main__":
    unittest.main()
