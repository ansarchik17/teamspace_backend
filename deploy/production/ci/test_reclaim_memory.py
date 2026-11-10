import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import reclaim_memory as reclaim

GROUP = '/system.slice/actions.runner.west6ide-workspace-backend.test.service'


def proc(pid, ppid, comm, args=(), group=GROUP, state='S'):
    return reclaim.Process(pid, ppid, comm, group, args, state)


class WorkerClassificationTests(unittest.TestCase):
    def test_initial_hook_and_detached_watcher_are_waiting(self):
        ps = {10: proc(10, 1, 'Runner.Worker'), 11: proc(11, 10, 'bash'),
              12: proc(12, 11, 'python3', ('python3', reclaim.HOOK)),
              13: proc(13, 12, 'python3', ('python3', reclaim.HOOK, '--watch', '10'))}
        self.assertEqual(reclaim.worker_states(ps), {10: 'waiting'})

    def test_executing_step_is_active_even_if_another_hook_exists(self):
        ps = {10: proc(10, 1, 'Runner.Worker'),
              12: proc(12, 10, 'python3', ('python3', reclaim.HOOK)),
              14: proc(14, 10, 'node')}
        self.assertEqual(reclaim.worker_states(ps), {10: 'active'})

    def test_worker_without_initial_hook_is_active(self):
        self.assertEqual(reclaim.worker_states({10: proc(10, 1, 'Runner.Worker')}), {10: 'active'})

    def test_unrelated_hook_cannot_hide_active_worker(self):
        ps = {10: proc(10, 1, 'Runner.Worker'),
              12: proc(12, 1, 'python3', ('python3', reclaim.HOOK))}
        self.assertEqual(reclaim.worker_states(ps), {10: 'active'})

    def test_wrong_hook_path_and_wrong_control_group_are_rejected(self):
        for args, group in [(('python3', '/tmp/job-lock.py'), GROUP),
                            (('python3', reclaim.HOOK), '/another.service')]:
            ps = {10: proc(10, 1, 'Runner.Worker'), 12: proc(12, 10, 'python3', args, group)}
            self.assertEqual(reclaim.worker_states(ps), {10: 'active'})

    def test_zombies_do_not_keep_worker_active_or_hide_a_step(self):
        self.assertEqual(reclaim.worker_states({10: proc(10, 1, 'Runner.Worker', state='Z')}), {})
        ps = {10: proc(10, 1, 'Runner.Worker'),
              12: proc(12, 10, 'python3', ('python3', reclaim.HOOK), state='Z')}
        self.assertEqual(reclaim.worker_states(ps), {10: 'active'})

    def test_unknown_runner_worker_fails_closed(self):
        self.assertEqual(reclaim.worker_states({10: proc(10, 1, 'Runner.Worker', group='/unknown')}), {10: 'active'})


class ReclaimSafetyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.group = Path(self.temp.name)
        self.values = {'memory.high': str(1024 * reclaim.MIB) + '\n',
                       'memory.current': str(70 * reclaim.MIB) + '\n',
                       'memory.swap.current': '0\n', 'memory.max': str(1536 * reclaim.MIB) + '\n',
                       'memory.swap.max': str(256 * reclaim.MIB) + '\n',
                       'memory.events': 'low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n'}
        for key, value in self.values.items():
            (self.group / key).write_text(value)

    def test_plan_does_not_write_any_control_file(self):
        result = reclaim.reclaim_group(self.group, 16 * reclaim.MIB)
        self.assertEqual(result['status'], 'planned')
        self.assertEqual({p.name:p.read_text() for p in self.group.iterdir()}, self.values)

    def test_success_restores_soft_limit_and_preserves_hard_caps(self):
        def kernel_reclaim():
            self.assertEqual((self.group / 'memory.high').read_text(), str(16 * reclaim.MIB))
            (self.group / 'memory.current').write_text(str(16 * reclaim.MIB))
            (self.group / 'memory.swap.current').write_text(str(36 * reclaim.MIB))
        result = reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True, settle=kernel_reclaim)
        self.assertTrue(result['soft_limit_restored'])
        for key in ('memory.high', 'memory.max', 'memory.swap.max'):
            self.assertEqual((self.group / key).read_text(), self.values[key])

    def test_exception_and_interrupt_restore_soft_limit(self):
        for error in (OSError('probe failure'), InterruptedError('signal')):
            with self.assertRaises(type(error)):
                reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True,
                                      settle=lambda: (_ for _ in ()).throw(error))
            self.assertEqual((self.group / 'memory.high').read_text(), self.values['memory.high'])

    def test_inadequate_swap_budget_is_not_modified(self):
        (self.group / 'memory.swap.current').write_text(str(230 * reclaim.MIB))
        self.assertEqual(reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True)['status'],
                         'skipped_swap_budget')
        self.assertEqual((self.group / 'memory.high').read_text(), self.values['memory.high'])

    def test_large_cache_is_reclaimed_in_bounded_passes(self):
        (self.group / 'memory.current').write_text(str(450 * reclaim.MIB))
        (self.group / 'memory.swap.current').write_text(str(8 * reclaim.MIB))
        def first_pass():
            self.assertEqual((self.group / 'memory.high').read_text(), str(234 * reclaim.MIB))
            (self.group / 'memory.current').write_text(str(234 * reclaim.MIB))
            (self.group / 'memory.swap.current').write_text(str(40 * reclaim.MIB))
        result = reclaim.reclaim_group(self.group, 64 * reclaim.MIB, apply=True, settle=first_pass)
        self.assertEqual(result['target_bytes'], 234 * reclaim.MIB)
        self.assertEqual((self.group / 'memory.high').read_text(), self.values['memory.high'])
        second = reclaim.reclaim_group(self.group, 64 * reclaim.MIB)
        self.assertEqual(second['target_bytes'], 64 * reclaim.MIB)

    def test_bounded_pass_never_relaxes_a_tighter_existing_limit(self):
        (self.group / 'memory.current').write_text(str(450 * reclaim.MIB))
        (self.group / 'memory.high').write_text(str(128 * reclaim.MIB))
        self.assertEqual(reclaim.reclaim_group(self.group, 64 * reclaim.MIB, apply=True)['status'],
                         'skipped_already_tight_soft_limit')
        self.assertEqual((self.group / 'memory.high').read_text(), str(128 * reclaim.MIB))

    def test_tiny_remaining_swap_budget_is_not_used(self):
        (self.group / 'memory.swap.current').write_text(str(220 * reclaim.MIB))
        self.assertEqual(reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True)['status'],
                         'skipped_swap_budget')

    def test_unrecognized_hard_caps_are_not_modified(self):
        (self.group / 'memory.max').write_text('max')
        with self.assertRaises(ValueError):
            reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True)
        self.assertEqual((self.group / 'memory.high').read_text(), self.values['memory.high'])

    def test_existing_tighter_soft_limit_is_never_relaxed(self):
        (self.group / 'memory.high').write_text(str(8 * reclaim.MIB))
        self.assertEqual(reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True)['status'],
                         'skipped_already_tight_soft_limit')
        self.assertEqual((self.group / 'memory.high').read_text(), str(8 * reclaim.MIB))

    def test_worker_admission_race_is_rechecked_before_write(self):
        result = reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True, still_safe=lambda: False)
        self.assertEqual(result['status'], 'skipped_process_changed')
        self.assertEqual((self.group / 'memory.high').read_text(), self.values['memory.high'])

    def test_unexpected_oom_is_reported_after_restoring_soft_limit(self):
        def oom():
            (self.group / 'memory.events').write_text('oom 1\noom_kill 0\n')
        with self.assertRaisesRegex(AssertionError, 'OOM'):
            reclaim.reclaim_group(self.group, 16 * reclaim.MIB, apply=True, settle=oom)
        self.assertEqual((self.group / 'memory.high').read_text(), self.values['memory.high'])

    def test_active_pipeline_prevents_all_reclamation(self):
        with patch.object(reclaim, 'host_resources', return_value=(2800 * reclaim.MIB, 3000 * reclaim.MIB, 0)), \
             patch.object(reclaim, 'managed_groups', return_value={'backend':self.group}), \
             patch.object(reclaim, 'process_snapshot', return_value={10:proc(10, 1, 'Runner.Worker')}), \
             patch.object(reclaim, 'reclaim_group') as action:
            self.assertEqual(reclaim.recover(True)['status'], 'skipped_active_worker')
            action.assert_not_called()

    def test_sufficient_memory_does_not_inspect_or_modify_runners(self):
        with patch.object(reclaim, 'host_resources', return_value=(4000 * reclaim.MIB, 3000 * reclaim.MIB, 0)), \
             patch.object(reclaim, 'managed_groups') as groups:
            self.assertEqual(reclaim.recover(True)['status'], 'skipped_sufficient_memory')
            groups.assert_not_called()

    def test_host_pressure_or_low_swap_prevents_reclamation(self):
        for resources in [(2800 * reclaim.MIB, 500 * reclaim.MIB, 0),
                          (2800 * reclaim.MIB, 3000 * reclaim.MIB, 5)]:
            with patch.object(reclaim, 'host_resources', return_value=resources), \
                 patch.object(reclaim, 'managed_groups') as groups:
                self.assertEqual(reclaim.recover(True)['status'], 'skipped_host_pressure_or_swap')
                groups.assert_not_called()


class IdleBuilderSafetyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'job.lock').touch()
        self.name = 'buildx_buildkit_ci-workspace-odo-front0'
        self.data = {'Id': 'a' * 64, 'Name': '/' + self.name,
                     'State': {'Running': True}, 'Config': {'Image': 'moby/buildkit:buildx-stable-1'},
                     'HostConfig': {'PortBindings': {}},
                     'Mounts': [{'Type': 'volume', 'Name': self.name + '_state', 'Destination': '/var/lib/buildkit'}]}

    def exercise(self, *, active=False, apply=True, second_active=False):
        import json
        snapshots = [{10: proc(10, 1, 'Runner.Worker')}] if active else [{}, {10:proc(10, 1, 'Runner.Worker')}] if second_active else [{}]
        def snapshot():
            return snapshots.pop(0) if len(snapshots) > 1 else snapshots[0]
        def output(command, **kwargs):
            if command[1] == 'ps':
                return self.name + '\nproduction-database\n'
            self.assertEqual(command[1:], ['inspect', self.name])
            return json.dumps([self.data])
        with patch.object(reclaim, 'ROOT', self.root), \
             patch.object(reclaim, 'process_snapshot', side_effect=snapshot), \
             patch.object(reclaim, 'host_resources', return_value=(2800 * reclaim.MIB, 3000 * reclaim.MIB, 0)), \
             patch.object(reclaim.subprocess, 'check_output', side_effect=output), \
             patch.object(reclaim.subprocess, 'run') as run:
            result = reclaim.recover_builders(apply)
            return result, run.call_args_list

    def test_only_known_idle_builder_is_stopped_by_immutable_id(self):
        result, calls = self.exercise()
        self.assertEqual(result, {self.name:'stopped_cache_retained'})
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].args[0], ['docker', 'stop', '--time', '10', 'a' * 64])

    def test_dry_run_keeps_all_containers_running(self):
        result, calls = self.exercise(apply=False)
        self.assertEqual(result[self.name], 'planned_stop')
        self.assertEqual(calls, [])

    def test_active_worker_blocks_builder_changes(self):
        result, calls = self.exercise(active=True)
        self.assertEqual(result['status'], 'skipped_active_worker')
        self.assertEqual(calls, [])

    def test_worker_state_is_rechecked_immediately_before_stop(self):
        result, calls = self.exercise(second_active=True)
        self.assertEqual(result[self.name], 'skipped_worker_admitted')
        self.assertEqual(calls, [])

    def test_unknown_image_volume_bind_or_ports_are_never_touched(self):
        import copy
        original = copy.deepcopy(self.data)
        variants = [dict(original, Config={'Image': 'postgres:17'}),
                    dict(original, Mounts=[{'Type':'bind', 'Source':'/production'}]),
                    dict(original, Mounts=[{'Type':'volume','Name':'production','Destination':'/var/lib/buildkit'}]),
                    dict(original, HostConfig={'PortBindings': {'80/tcp':[{'HostPort':'8080'}]}})]
        for self.data in variants:
            result, calls = self.exercise()
            self.assertEqual(result[self.name], 'skipped_unknown_configuration')
            self.assertEqual(calls, [])

    def test_held_lease_prevents_builder_changes(self):
        import fcntl
        with (self.root / 'job.lock').open('r+') as lease:
            fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result, calls = self.exercise()
        self.assertEqual(result['status'], 'skipped_busy_slot')
        self.assertEqual(calls, [])


if __name__ == '__main__':
    unittest.main()
