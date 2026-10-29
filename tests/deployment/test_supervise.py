"""Run the real supervisor with synthetic agents; never touch host services."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[2]


class SupervisorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = self.root / 'config.json'
        self.config.write_text('{"Agent":{"HostID":1}}')
        self.previous = self.bundle('previous', 'proxynode-v1.0.0')
        self.candidate = self.bundle('candidate', 'proxynode-v1.1.0')
        (self.root / 'current').symlink_to(self.candidate)
        self.pending = {'previous': str(self.previous), 'candidate': str(self.candidate), 'version': 'proxynode-v1.1.0'}
        (self.root / 'pending-upgrade.json').write_text(json.dumps(self.pending))

    def bundle(self, name, version):
        directory = self.root / 'releases' / name
        directory.mkdir(parents=True)
        binary = directory / 'proxynode'
        binary.write_text('#!/usr/bin/env python3\nimport json,os,time\n'
                          f'open(os.environ["PROXYNODE_HEALTH_FILE"],"w").write(json.dumps({{"pid":os.getpid(),"version":"{version}"}}))\n'
                          'while True: time.sleep(0.1)\n')
        binary.chmod(0o755)
        return directory

    def supervise(self):
        proc = subprocess.Popen(['bash', str(ROOT / 'script/supervise.sh'), '--root', str(self.root),
                                 '--config', str(self.config), '--startup-timeout', '1', '--restart-delay', '0.1'],
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(lambda: self.stop(proc))
        return proc

    @staticmethod
    def stop(proc):
        if proc.poll() is None:
            proc.terminate()
            try: proc.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill(); proc.communicate()

    def result(self, proc):
        path = self.root / 'upgrade-result.json'
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if path.exists(): return json.loads(path.read_text())
            if proc.poll() is not None: self.fail(proc.communicate()[1])
            time.sleep(0.05)
        self.fail('supervisor did not finish startup check')

    def test_healthy_startup_commits_the_bundle(self):
        proc = self.supervise()
        self.assertEqual(self.result(proc)['state'], 'succeeded')
        self.assertEqual((self.root / 'current').resolve(), self.candidate)
        self.assertFalse((self.root / 'pending-upgrade.json').exists())
        self.assertEqual(self.config.read_text(), '{"Agent":{"HostID":1}}')

    def test_failed_startup_restores_previous_bundle_and_restarts(self):
        (self.candidate / 'proxynode').write_text('#!/bin/sh\nexit 23\n')
        proc = self.supervise()
        self.assertEqual(self.result(proc)['state'], 'failed')
        self.assertEqual((self.root / 'current').resolve(), self.previous)
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            try:
                if json.loads((self.root / 'agent-health.json').read_text())['version'] == 'proxynode-v1.0.0': break
            except (FileNotFoundError, json.JSONDecodeError): pass
            time.sleep(0.05)
        else: self.fail('previous agent was not restarted')
        self.assertIsNone(proc.poll())

    def test_failed_agent_does_not_leave_its_worker_running_after_rollback(self):
        heartbeat = self.root / 'orphan-heartbeat'
        child = self.root / 'child.py'
        child.write_text('import pathlib,signal,time\nsignal.signal(signal.SIGTERM,signal.SIG_IGN)\n'
                         f'p=pathlib.Path({str(heartbeat)!r})\nwhile True: p.write_text(str(time.time()));time.sleep(0.05)\n')
        (self.candidate / 'proxynode').write_text('#!/usr/bin/env python3\nimport subprocess,sys,time\n'
            f'subprocess.Popen([sys.executable,{str(child)!r}],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\ntime.sleep(0.2)\nsys.exit(23)\n')
        proc = self.supervise()
        self.assertEqual(self.result(proc)['state'], 'failed')
        first = heartbeat.read_text()
        time.sleep(0.3)
        self.assertEqual(heartbeat.read_text(), first, 'failed candidate left a running proxy worker')

    def test_stale_or_wrong_version_health_cannot_confirm_upgrade(self):
        (self.candidate / 'proxynode').write_text('#!/usr/bin/env python3\nimport json,os,time\nopen(os.environ["PROXYNODE_HEALTH_FILE"],"w").write(json.dumps({"pid":os.getpid(),"version":"wrong"}))\ntime.sleep(20)\n')
        proc = self.supervise()
        result = self.result(proc)
        self.assertEqual(result['state'], 'failed')
        self.assertIn('timeout', result['error'])
        self.assertEqual((self.root / 'current').resolve(), self.previous)


if __name__ == '__main__': unittest.main()
