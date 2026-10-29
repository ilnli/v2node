import json
import os
import pty
import select
import time
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class ManagerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.config = self.base / 'config.json'
        self.original = {'Log': {'Level': 'debug'}, 'Audit': {'Enabled': True, 'MemoryMiB': 64},
                         'Custom': {'keep': [1, 2]}, 'Nodes': [
                             {'ApiHost': 'https://panel.example', 'NodeID': 101,
                              'ApiKey': 'original-secret', 'Timeout': 37}]}
        self.config.write_text(json.dumps(self.original))
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        self.network_log = self.base / 'network'
        for name in ('curl', 'wget', 'systemctl', 'rc-service'):
            p = self.bin / name
            p.write_text('#!/bin/sh\necho called >> "$CALL_LOG"\nexit 99\n')
            p.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.bin) + ':' + os.environ['PATH'],
                        CALL_LOG=str(self.network_log))

    def run_manager(self, command, *args):
        return subprocess.run(['bash', str(ROOT / 'script/proxynode.sh'), command,
                               '--config', str(self.config), *args], env=self.env,
                              capture_output=True, text=True, stdin=subprocess.DEVNULL)

    def test_managed_host_local_node_edits_are_refused(self):
        self.config.write_text(json.dumps({'Agent': {'HostID': 7, 'ApiHost': 'https://panel.example',
                                                  'EnrollmentToken': 'pn_enroll_private'}, 'Nodes': []}))
        before = self.config.read_bytes()
        for action, flags in [('add', ['102', '--api-host', 'https://panel.example', '--api-key', 'key']),
                              ('del', ['102'])]:
            result = self.run_manager(action, *flags)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('托管', result.stderr)
            self.assertEqual(self.config.read_bytes(), before)
        listing = self.run_manager('list')
        self.assertEqual(listing.returncode, 0, listing.stderr)
        self.assertIn('面板', listing.stdout)
        self.assertNotIn('pn_enroll_private', listing.stdout)
        self.assertFalse(Path(str(self.config) + '.bak').exists())

    def test_batch_add_list_remove_preserves_config_and_backup(self):
        before = self.config.read_bytes()
        key = 'secret"with\\characters'
        result = self.run_manager('add-node', '--api-host', 'https://panel.example/',
                                  '--node-ids', '102,103', '--api-key', key)
        self.assertEqual(result.returncode, 0, result.stderr)
        data = json.loads(self.config.read_text())
        self.assertEqual([n['NodeID'] for n in data['Nodes']], [101, 102, 103])
        self.assertEqual(data['Nodes'][0], self.original['Nodes'][0])
        self.assertEqual(data['Audit'], self.original['Audit'])
        self.assertEqual(data['Custom'], self.original['Custom'])
        self.assertEqual(data['Log'], self.original['Log'])
        self.assertEqual(data['Nodes'][1]['ApiKey'], key)
        self.assertEqual(Path(str(self.config) + '.bak').read_bytes(), before)
        for path in (self.config, Path(str(self.config) + '.bak')):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        listing = self.run_manager('list-nodes')
        self.assertEqual(listing.returncode, 0, listing.stderr)
        self.assertIn('102', listing.stdout)
        self.assertIn('https://panel.example', listing.stdout)
        self.assertNotIn('original-secret', listing.stdout)
        self.assertNotIn('secret', listing.stdout)
        removed = self.run_manager('remove-node', '--api-host', 'https://panel.example', '--node-id', '102')
        self.assertEqual(removed.returncode, 0, removed.stderr)
        self.assertEqual([n['NodeID'] for n in json.loads(self.config.read_text())['Nodes']], [101, 103])
        self.assertFalse(self.network_log.exists(), 'configuration commands must not contact network/services')

    def test_short_commands_and_positional_ids(self):
        result = self.run_manager('add', '102,103', '--api-host', 'https://panel.example', '--api-key', 'key')
        self.assertEqual(result.returncode, 0, result.stderr)
        listing = self.run_manager('list')
        self.assertEqual(listing.returncode, 0, listing.stderr)
        self.assertIn('103', listing.stdout)
        result = self.run_manager('del', '102')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([n['NodeID'] for n in json.loads(self.config.read_text())['Nodes']], [101, 103])

    def test_menu_lists_nodes_and_exits_in_terminal(self):
        master, slave = pty.openpty()
        process = subprocess.Popen(['bash', str(ROOT / 'script/proxynode.sh'), 'menu',
                                    '--config', str(self.config)], stdin=slave, stdout=slave,
                                   stderr=slave, env=self.env)
        os.close(slave)
        output = b''
        try:
            os.write(master, b'1\n0\n')
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        part = os.read(master, 8192)
                    except OSError:
                        break
                    if not part:
                        break
                    output += part
                elif process.poll() is not None:
                    break
            self.assertEqual(process.wait(timeout=1), 0, output.decode(errors='replace'))
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)
        text = output.decode()
        self.assertIn('pnode', text)
        self.assertIn('101', text)
        self.assertIn('https://panel.example', text)
        self.assertNotIn('original-secret', text)
        self.assertFalse(self.network_log.exists())

    def test_duplicate_and_bad_batch_are_atomic(self):
        before = self.config.read_bytes()
        for ids in ('101,102', '102,102', '0', '102,nope'):
            result = self.run_manager('add-node', '--api-host', 'https://panel.example/',
                                      '--node-ids', ids, '--api-key', 'new-secret')
            self.assertNotEqual(result.returncode, 0, ids)
            self.assertEqual(self.config.read_bytes(), before)
        self.assertFalse(Path(str(self.config) + '.bak').exists())

    def test_ambiguous_remove_requires_host_and_missing_id_preserves_file(self):
        data = self.original
        data['Nodes'].append({'ApiHost': 'https://second.example', 'NodeID': 101, 'ApiKey': 'second'})
        self.config.write_text(json.dumps(data))
        before = self.config.read_bytes()
        for ids in ('101', '999'):
            result = self.run_manager('remove-node', '--node-id', ids)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(self.config.read_bytes(), before)
        result = self.run_manager('remove-node', '--api-host', 'https://second.example', '--node-id', '101')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.config.read_text())['Nodes'], self.original['Nodes'][:1])

    def test_invalid_config_or_missing_key_is_not_overwritten(self):
        for content in ('{broken', '{"Nodes":{}}'):
            self.config.write_text(content)
            result = self.run_manager('add-node', '--api-host', 'https://panel.example',
                                      '--node-id', '102', '--api-key', 'key')
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(self.config.read_text(), content)
        self.config.write_text(json.dumps(self.original))
        result = self.run_manager('add-node', '--api-host', 'https://panel.example', '--node-id', '102')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Traceback', result.stderr)

    def test_concurrent_adds_keep_both_nodes(self):
        processes = [subprocess.Popen([
            'bash', str(ROOT / 'script/proxynode.sh'), 'add-node', '--config', str(self.config),
            '--api-host', 'https://panel.example', '--node-id', str(node_id), '--api-key', 'key',
        ], env=self.env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True) for node_id in (102, 103)]
        for process in processes:
            _, error = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 0, error)
        self.assertEqual(sorted(n['NodeID'] for n in json.loads(self.config.read_text())['Nodes']),
                         [101, 102, 103])

    def test_repeated_node_id_flags_and_url_validation(self):
        result = self.run_manager('add-node', '--api-host', 'https://panel.example',
                                  '--node-id', '102', '--node-id', '103', '--api-key', 'key')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(json.loads(self.config.read_text())['Nodes']), 3)
        before = self.config.read_bytes()
        for host in ('not-a-url', 'https://user:password@panel.example', 'https://panel.example?token=secret'):
            result = self.run_manager('add-node', '--api-host', host, '--node-id', '104', '--api-key', 'key')
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(self.config.read_bytes(), before)


if __name__ == '__main__':
    unittest.main()
