import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


class GatewayCredentialsTest(unittest.TestCase):
    def test_fill_missing_keys_without_logging_rotating_or_exposing_backups(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder)
            env_file=root/".env"
            env_file.write_text("ALEM_API_KEY='keep-alem'\nWORKSPACE_AI_OPENAI_API_KEY='keep-openai'\n")
            env_file.chmod(0o640)
            docker=root/"docker"
            docker.write_text("#!/usr/bin/env python3\nimport json\nprint(json.dumps(['MEETING_AI_CHATGPT_API_KEY=old-openai','SMART_AGENT_ELEVENLABS_API_KEY=copy-eleven']))\n")
            docker.chmod(0o700)
            environment={**os.environ,"PATH":str(root)+os.pathsep+os.environ["PATH"]}
            script=Path(__file__).with_name("prepare-workspace-ai-credentials.sh")
            first=subprocess.run(["bash",str(script),str(env_file)],env=environment,capture_output=True,text=True)
            self.assertEqual(first.returncode,0,first.stderr)
            self.assertNotIn("copy-eleven",first.stdout+first.stderr)
            self.assertNotIn("old-openai",first.stdout+first.stderr)
            self.assertIn("WORKSPACE_AI_OPENAI_API_KEY='keep-openai'",env_file.read_text())
            self.assertIn("WORKSPACE_AI_ELEVENLABS_API_KEY='copy-eleven'",env_file.read_text())
            self.assertIn("WORKSPACE_AI_JOB_SIGNING_KEY=",env_file.read_text())
            before=env_file.read_bytes()
            subprocess.run(["bash",str(script),str(env_file)],env=environment,capture_output=True,text=True,check=True)
            self.assertEqual(before,env_file.read_bytes())
            backups=list(root.glob(".env.before-workspace-ai-*"))
            self.assertEqual(len(backups),1)
            self.assertEqual(stat.S_IMODE(backups[0].stat().st_mode),0o600)
            self.assertEqual(stat.S_IMODE(env_file.stat().st_mode),0o640)


if __name__=="__main__":
    unittest.main()
