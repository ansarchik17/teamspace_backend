import datetime as dt
import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("retention", Path(__file__).with_name("retention.py"))
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)
REPO = "ghcr.io/west6ide/workspace-backend"
NOW = dt.datetime(2026, 10, 11, tzinfo=dt.timezone.utc)


def image(number, hours=100, tags=None):
    return {"Id": "sha256:" + f"{number:064x}", "Created": (NOW - dt.timedelta(hours=hours)).isoformat(),
            "RepoTags": tags if tags is not None else [REPO + ":" + f"{number:040x}"], "Config": {"Labels": {}}}


class RetentionTests(unittest.TestCase):
    def test_sha_prefixed_versions_expire_but_keep_latest_and_pinned_rollback(self):
        images = [image(i, 100 + i, [REPO + ':sha-' + f'{i:040x}']) for i in range(9)]
        pinned = {images[-1]['Id']}
        candidates, protected = retention.select(images, pinned, {REPO}, 5, 24, NOW)
        self.assertEqual({im['Id'] for im in candidates}, {im['Id'] for im in images[5:8]})
        self.assertTrue(pinned.issubset(protected))

    def test_sha_prefix_does_not_make_named_or_incomplete_tags_disposable(self):
        images = [image(i, 100 + i) for i in range(8)]
        images[6]['RepoTags'] = [REPO + ':sha-latest']
        images[7]['RepoTags'] = [REPO + ':sha-1234567']
        candidates, _ = retention.select(images, set(), {REPO}, 5, 24, NOW)
        self.assertEqual([im['Id'] for im in candidates], [images[5]['Id']])

    def test_nanosecond_docker_creation_time(self):
        self.assertEqual(retention.created({'Created': '2026-10-05T06:27:58.641897475Z'}),
                         dt.datetime(2026, 10, 5, 6, 27, 58, 641897, tzinfo=dt.timezone.utc))

    def test_nested_project_rollback_files_are_included(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            release = root / "workspace" / "sd" / "releases"
            release.mkdir(parents=True)
            file = release / "images.env.previous"
            file.write_text("BACKEND_IMAGE=" + REPO + ":" + "a" * 40 + "\n")
            files, refs = retention.pins(root)
            self.assertEqual(files, [file])
            self.assertEqual(refs, {REPO + ":" + "a" * 40})

    def test_missing_historical_refs_do_not_block_but_existing_backup_images_stay_protected(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            active = root / 'workspace' / 'releases'
            archived = root / 'workspace' / 'backups' / 'old' / 'releases'
            active.mkdir(parents=True)
            archived.mkdir(parents=True)
            images = [image(i) for i in range(3)]
            (active / 'images.env').write_text('BACKEND_IMAGE=' + images[0]['RepoTags'][0])
            (active / 'images.env.previous').write_text('BACKEND_IMAGE=' + images[1]['RepoTags'][0])
            (archived / 'images.env').write_text('BACKEND_IMAGE=' + images[2]['RepoTags'][0])
            (archived / 'images.env.previous').write_text('BACKEND_IMAGE=' + REPO + ':' + 'f' * 40)
            with patch.object(retention, 'docker', return_value='ids'), patch.object(retention, 'inspected', side_effect=[images, []]):
                _, _, protected, _, _, missing = retention.snapshot(root)
            self.assertFalse(missing)
            self.assertEqual(protected, {im['Id'] for im in images})

    def test_missing_live_rollback_remains_a_blocker_even_with_backup_manifests(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            active = root / 'growy' / 'releases'
            active.mkdir(parents=True)
            im = image(0)
            (active / 'images.env').write_text('FRONTEND_IMAGE=' + im['RepoTags'][0])
            missing_ref = REPO + ':' + 'e' * 40
            (active / 'images.env.previous').write_text('FRONTEND_IMAGE=' + missing_ref)
            with patch.object(retention, 'docker', return_value='ids'), patch.object(retention, 'inspected', side_effect=[[im], []]):
                self.assertEqual(retention.snapshot(root)[-1], [missing_ref])

    def test_cleanup_obeys_ci_lock(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            state = root / "state"
            state.mkdir()
            ci = root / "workspace-ci"
            ci.mkdir()
            lock = ci / "job.lock"
            lock.touch()
            with lock.open("r+") as handle:
                retention.fcntl.flock(handle, retention.fcntl.LOCK_EX | retention.fcntl.LOCK_NB)
                with self.assertRaises(BlockingIOError):
                    with retention.locks(root, state):
                        self.fail("Cleanup entered while CI was busy")

    def test_keep_latest_and_pinned_old_releases_and_stopped_containers(self):
        images = [image(i, 100 + i) for i in range(12)]
        pinned = {images[-1]["Id"], images[-2]["Id"]}
        candidates, protected = retention.select(images, pinned, {REPO}, 5, 24, NOW)
        self.assertEqual({im["Id"] for im in candidates}, {im["Id"] for im in images[5:10]})
        self.assertTrue(pinned.issubset(protected))

    def test_grace_unknown_repositories_named_tags_and_keep_label(self):
        images = [image(i) for i in range(20)]
        images[6]["Created"] = (NOW - dt.timedelta(hours=12)).isoformat()
        images[7]["RepoTags"].append(REPO + ":rollback")
        images[8]["RepoTags"] = ["postgres:15"]
        images[9]["RepoTags"] = []
        images[10]["Config"]["Labels"]["retention.keep"] = "true"
        candidates, _ = retention.select(images, set(), {REPO}, 5, 24, NOW)
        removed = {im["Id"] for im in candidates}
        for index in range(6, 11):
            self.assertNotIn(images[index]["Id"], removed)

    def test_aliases_count_as_one_version(self):
        images = [image(i, 100 + i) for i in range(6)]
        images[0]["RepoTags"].append(REPO + ":" + "a" * 40)
        candidates, _ = retention.select(images, set(), {REPO}, 5, 24, NOW)
        self.assertEqual([im["Id"] for im in candidates], [images[5]["Id"]])

    def test_release_parser_accepts_sha_rollback_and_rejects_unresolved_vars(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            release = root / "workspace" / "releases"
            release.mkdir(parents=True)
            file = release / "images.env.previous"
            file.write_text('BACKEND_IMAGE="sha256:' + "a" * 64 + '"\n')
            _, refs = retention.pins(root)
            self.assertEqual(refs, {"sha256:" + "a" * 64})
            file.write_text('BACKEND_IMAGE=${UNRESOLVED}\n')
            with self.assertRaises(RuntimeError):
                retention.pins(root)

    def test_snapshot_protects_stopped_container_and_env_candidate(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            release = root / "workspace" / "releases"
            release.mkdir(parents=True)
            images = [image(i) for i in range(3)]
            (release / "images.env").write_text("BACKEND_IMAGE=" + images[0]["RepoTags"][0] + "\n")
            (root / "workspace" / ".env").write_text("BACKEND_IMAGE=" + images[2]["RepoTags"][0] + "\n")
            stopped = [{"Id": "stopped", "Image": images[1]["Id"], "State": {"Running": False}}]
            with patch.object(retention, "docker", return_value="ids") as docker, patch.object(retention, "inspected", side_effect=[images, stopped]):
                _, _, protected, _, _, missing = retention.snapshot(root)
            docker.assert_any_call("image", "ls", "--all", "--quiet", "--no-trunc")
            self.assertEqual(protected, {im["Id"] for im in images})
            self.assertFalse(missing)

    def test_missing_rollback_aborts_before_any_mutation(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            state = root / "state"
            config = {"root": str(root), "state_dir": str(state), "expected_host": "test-host",
                      "repositories": [REPO], "keep_latest": 5, "min_age_hours": 24, "min_free_gib": 0}
            snapshot = ([image(i) for i in range(10)], [], set(), [], set(), ["missing-rollback"])
            with patch.object(retention.socket, "gethostname", return_value="test-host"), patch.object(retention.os, "geteuid", return_value=0), patch.object(retention, "snapshot", return_value=snapshot), patch.object(retention, "docker") as docker:
                self.assertEqual(retention.run(types.SimpleNamespace(apply=True), config), 1)
                docker.assert_not_called()
            self.assertEqual(json.loads((state / "last-run.json").read_text())["status"], "blocked_missing_release_images")

    def test_wrong_host_never_calls_docker(self):
        with patch.object(retention.socket, "gethostname", return_value="other"), patch.object(retention, "docker") as docker:
            with self.assertRaises(RuntimeError):
                retention.run(types.SimpleNamespace(apply=True), {"root": "/opt", "state_dir": "/tmp/test", "expected_host": "correct"})
            docker.assert_not_called()

    def test_apply_rechecks_then_preserves_protected_images(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            images = [image(i, 100 + i) for i in range(6)]
            containers = [{"Id": "running", "Image": images[0]["Id"]}]
            config = {"root": str(root), "state_dir": str(root / "state"), "expected_host": "test-host",
                      "repositories": [REPO], "keep_latest": 5, "min_age_hours": 24, "min_free_gib": 0}
            snapshot = (images, containers, {images[0]["Id"]}, [], set(), [])
            after = (images[:5], containers, {images[0]["Id"]}, [], set(), [])
            with patch.object(retention.socket, "gethostname", return_value="test-host"), patch.object(retention.os, "geteuid", return_value=0), patch.object(retention, "snapshot", side_effect=[snapshot, snapshot, after]), patch.object(retention, "docker") as docker:
                self.assertEqual(retention.run(types.SimpleNamespace(apply=True), config), 0)
                self.assertEqual(docker.call_args_list[0].args, ("image", "rm", "--no-prune", images[5]["RepoTags"][0]))
                self.assertEqual(docker.call_count, 1)
            report = json.loads((root / "state" / "last-run.json").read_text())
            self.assertEqual(report["status"], "ok")


if __name__ == "__main__":
    unittest.main()
