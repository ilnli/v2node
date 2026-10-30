import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.prefix = self.base / 'root'
        self.fixture = self.base / 'releases'
        self.fixture.mkdir()
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        self.scratch = self.base / 'scratch'
        self.scratch.mkdir()
        self.log = self.base / 'requests'
        self.env = dict(os.environ, PATH=str(self.bin) + ':' + os.environ['PATH'],
                        FIXTURES=str(self.fixture), REQUEST_LOG=str(self.log), TMPDIR=str(self.scratch))
        self.command('uname', '#!/bin/bash\nif [[ $1 == -s ]]; then echo Linux; else echo x86_64; fi\n')
        self.command('curl', '''#!/usr/bin/env python3
import os, pathlib, shutil, sys
args = sys.argv[1:]
out = args[args.index('--output') + 1]
url = args[-1]
with open(os.environ['REQUEST_LOG'], 'a') as f: f.write(url + '\\n')
if not url.startswith('https://github.com/ilnli/v2node/releases/'): sys.exit(22)
shutil.copyfile(pathlib.Path(os.environ['FIXTURES']) / url.rsplit('/', 1)[-1], out)
''')
        self.package('amd64')
        self.package('arm64')
        self.checksums()

    def command(self, name, content):
        p = self.bin / name
        p.write_text(content)
        p.chmod(0o755)

    def package(self, arch):
        name = f'proxynode-linux-{arch}.tar.gz'
        files = {'proxynode': '#!/bin/sh\necho test-proxynode\n',
                 'proxynode-manager': '#!/bin/sh\nexit 0\n',
                 'install.sh': '#!/bin/sh\nexit 0\n',
                 'supervise.sh': (ROOT / 'script/supervise.sh').read_text(),
                 'config.example.json': json.dumps({'Audit': {'Enabled': True}, 'Nodes': [{'ApiHost': 'https://panel.example.com', 'NodeID': 1, 'ApiKey': 'replace-with-node-token'}]}),
                 'geoip.dat': 'synthetic geoip', 'geosite.dat': 'synthetic geosite'}
        with tarfile.open(self.fixture / name, 'w:gz') as archive:
            for filename, value in files.items():
                data = value.encode()
                info = tarfile.TarInfo(filename)
                info.size, info.mode = len(data), 0o755 if filename == 'proxynode' else 0o644
                archive.addfile(info, io.BytesIO(data))

    def checksums(self):
        (self.fixture / 'SHA256SUMS').write_text(''.join(
            hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n'
            for p in sorted(self.fixture.glob('*.tar.gz'))))

    def run_install(self, *args):
        return subprocess.run(['bash', str(ROOT / 'script/install.sh'), '--prefix', str(self.prefix),
                               '--no-service', *args], env=self.env, text=True, capture_output=True)

    def test_own_release_and_audit_disabled_by_default(self):
        result = self.run_install('--api-host', 'https://panel.example', '--node-id', '3',
                                  '--api-key', 'key"with\\characters')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.prefix / 'usr/local/bin/proxynode').is_file())
        config = json.loads((self.prefix / 'etc/proxynode/config.json').read_text())
        self.assertFalse(config['Audit']['Enabled'])
        self.assertEqual(config['Nodes'][0]['ApiKey'], 'key"with\\characters')
        self.assertIn('https://github.com/ilnli/v2node/releases/latest/download/proxynode-linux-amd64.tar.gz', self.log.read_text())

    def test_install_without_credentials_has_no_placeholder_node(self):
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads((self.prefix / 'etc/proxynode/config.json').read_text())
        self.assertEqual(config['Nodes'], [])
        self.assertIn('pnode add', result.stdout)

    def test_multiple_ids_and_no_upstream_reporting(self):
        result = self.run_install('--api-host', 'https://panel.example', '--node-ids', '101,102',
                                  '--node-id', '103', '--api-key', 'key')
        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads((self.prefix / 'etc/proxynode/config.json').read_text())
        self.assertEqual([n['NodeID'] for n in config['Nodes']], [101, 102, 103])
        self.assertEqual(self.log.read_text().splitlines(), [
            'https://github.com/ilnli/v2node/releases/latest/download/proxynode-linux-amd64.tar.gz',
            'https://github.com/ilnli/v2node/releases/latest/download/SHA256SUMS',
        ])

    def test_short_entry_is_installed_and_preserved_on_update(self):
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        entry = self.prefix / 'usr/local/bin/pnode'
        self.assertTrue(entry.is_symlink())
        self.assertEqual(os.readlink(entry), 'proxynode-manager')
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(entry.is_symlink())

    def test_existing_unrelated_short_command_is_not_overwritten(self):
        entry = self.prefix / 'usr/local/bin/pnode'
        entry.parent.mkdir(parents=True)
        entry.write_text('unrelated program')
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(entry.read_text(), 'unrelated program')
        self.assertFalse(self.log.exists(), 'detect conflict before downloading or mutating installation')

    def test_update_preserves_config_and_audit_cache(self):
        config = self.prefix / 'etc/proxynode/config.json'
        config.parent.mkdir(parents=True)
        config.write_text('{"private":"keep", "Audit":{"Enabled":true}}')
        cache = self.prefix / 'var/lib/proxynode/audit/old.batch'
        cache.parent.mkdir(parents=True)
        cache.write_text('pending')
        before = config.read_bytes()
        result = self.run_install('--version', 'proxynode-v0.1.0')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(config.read_bytes(), before)
        self.assertEqual(cache.read_text(), 'pending')
        self.assertIn('/releases/download/proxynode-v0.1.0/', self.log.read_text())

    def test_checksum_failure_does_not_replace_binary(self):
        binary = self.prefix / 'usr/local/bin/proxynode'
        binary.parent.mkdir(parents=True)
        binary.write_text('old binary')
        (self.fixture / 'proxynode-linux-amd64.tar.gz').write_bytes(b'corrupt')
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(binary.read_text(), 'old binary')
        self.assertFalse((self.prefix / 'etc/proxynode/config.json').exists())
        self.assertEqual(list(self.scratch.iterdir()), [], result.stderr)

    def test_failed_download_cleans_temporary_files(self):
        self.command('curl', '''#!/bin/bash
while [[ $1 != --output ]]; do shift; done
printf partial > "$2"
exit 23
''')
        result = self.run_install()
        self.assertEqual(result.returncode, 23, result.stderr)
        self.assertNotIn('unbound variable', result.stderr)
        self.assertEqual(list(self.scratch.iterdir()), [])

    def test_hangup_cleans_temporary_files(self):
        self.command('curl', '''#!/bin/bash
while [[ $1 != --output ]]; do shift; done
printf partial > "$2"
kill -HUP "$PPID"
''')
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(self.scratch.iterdir()), [], result.stderr)

    def test_failed_bundle_copy_removes_only_unpublished_bundle(self):
        first = self.run_install()
        self.assertEqual(first.returncode, 0, first.stderr)
        state = self.prefix / 'var/lib/proxynode'
        current = (state / 'current').resolve()
        config = self.prefix / 'etc/proxynode/config.json'
        before = config.read_bytes()
        self.command('install', '''#!/bin/bash
target=${!#}
if [[ $target == */releases/*/proxynode ]]; then
    printf partial > "$target"
    echo 'No space left on device' >&2
    exit 28
fi
exec /usr/bin/install "$@"
''')
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((state / 'current').resolve(), current)
        self.assertEqual(config.read_bytes(), before)
        self.assertEqual(list((state / 'releases').iterdir()), [current])
        self.assertEqual(list(self.scratch.iterdir()), [])
        (self.bin / 'install').unlink()
        retried = self.run_install()
        self.assertEqual(retried.returncode, 0, retried.stderr)
        self.assertEqual(config.read_bytes(), before)

    def test_cleanup_preserves_guardian_candidate(self):
        state = self.prefix / 'var/lib/proxynode'
        candidate = state / 'releases/candidate'
        candidate.mkdir(parents=True)
        (candidate / 'proxynode').write_text('candidate')
        (state / 'pending-upgrade.json').write_text(json.dumps({'candidate': str(candidate)}))
        result = subprocess.run([
            'bash', '-c',
            'source "$1"; PREFIX="$2"; set_paths; INSTALL_BUNDLE="$3"; cleanup_install',
            'test', str(ROOT / 'script/install.sh'), str(self.prefix), str(candidate),
        ], env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((candidate / 'proxynode').is_file())

    def test_arm64_and_unknown_architecture(self):
        self.command('uname', '#!/bin/bash\nif [[ $1 == -s ]]; then echo Linux; else echo aarch64; fi\n')
        result = self.run_install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('proxynode-linux-arm64.tar.gz', self.log.read_text())
        self.command('uname', '#!/bin/bash\nif [[ $1 == -s ]]; then echo Linux; else echo mystery; fi\n')
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)

    def test_legacy_config_requires_explicit_migration(self):
        old = self.prefix / 'etc/v2node/config.json'
        old.parent.mkdir(parents=True)
        old.write_text('{"Nodes":[{"NodeID":9}]}')
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)
        result = self.run_install('--migrate-v2node')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.prefix / 'etc/proxynode/config.json').read_bytes(), old.read_bytes())


    def test_managed_enrollment_has_private_agent_config_and_guardian_bundle(self):
        token = 'pn_enroll_' + 'a' * 40
        result = self.run_install('--api-host', 'https://panel.example', '--host-id', '7',
                                  '--enrollment-token', token)
        self.assertEqual(result.returncode, 0, result.stderr)
        path = self.prefix / 'etc/proxynode/config.json'
        config = json.loads(path.read_text())
        self.assertEqual(config['Nodes'], [])
        self.assertEqual(config['Agent']['HostID'], 7)
        self.assertEqual(config['Agent']['EnrollmentToken'], token)
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        current = self.prefix / 'var/lib/proxynode/current'
        self.assertTrue(current.is_symlink())
        self.assertTrue((current / 'supervise.sh').is_file())
        self.assertEqual((self.prefix / 'usr/local/bin/proxynode').resolve(), current.resolve() / 'proxynode')
        self.assertNotIn(token, result.stdout + result.stderr)

    def test_managed_conversion_requires_explicit_choice_and_keeps_backup(self):
        path = self.prefix / 'etc/proxynode/config.json'
        path.parent.mkdir(parents=True)
        original = '{"Nodes":[{"NodeID":9,"ApiKey":"old-secret"}]}'
        path.write_text(original)
        flags = ['--api-host', 'https://panel.example', '--host-id', '7', '--enrollment-token', 'pn_enroll_' + 'b' * 40]
        result = self.run_install(*flags)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(path.read_text(), original)
        result = self.run_install('--migrate-managed', *flags)
        self.assertEqual(result.returncode, 0, result.stderr)
        backups = list(path.parent.glob('config.json.legacy-*'))
        self.assertEqual(len(backups), 1)
        self.assertEqual(backups[0].read_text(), original)
        self.assertEqual(json.loads(path.read_text())['Agent']['HostID'], 7)

    def test_combined_old_v2node_and_managed_conversion_keeps_agent_config(self):
        old = self.prefix / 'etc/v2node/config.json'
        old.parent.mkdir(parents=True)
        original = '{"Nodes":[{"NodeID":9,"ApiKey":"legacy-secret"}]}'
        old.write_text(original)
        result = self.run_install('--migrate-v2node', '--migrate-managed', '--api-host',
                                  'https://panel.example', '--host-id', '8', '--enrollment-token',
                                  'pn_enroll_' + 'c' * 40)
        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads((self.prefix / 'etc/proxynode/config.json').read_text())
        self.assertEqual(config['Agent']['HostID'], 8)
        self.assertEqual(config['Nodes'], [])
        self.assertEqual(old.read_text(), original)
        backup = next((self.prefix / 'etc/proxynode').glob('config.json.legacy-*'))
        self.assertEqual(backup.read_text(), original)

    def test_archive_link_is_rejected_before_existing_install_changes(self):
        archive = self.fixture / 'proxynode-linux-amd64.tar.gz'
        with tarfile.open(archive, 'w:gz') as out:
            info = tarfile.TarInfo('proxynode')
            info.type = tarfile.SYMTYPE
            info.linkname = '/etc/passwd'
            out.addfile(info)
        self.checksums()
        result = self.run_install()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.prefix / 'usr/local/bin/proxynode').exists())

    def test_managed_installer_update_arms_startup_rollback(self):
        state = self.prefix / 'var/lib/proxynode'
        old, candidate = state / 'releases/old', state / 'releases/new'
        old.mkdir(parents=True); candidate.mkdir()
        (old / 'proxynode').write_text('old binary')
        binary = candidate / 'proxynode'
        binary.write_text('#!/bin/sh\necho "proxynode proxynode-v1.2.3 (fixture)"\n')
        binary.chmod(0o755)
        (state / 'current').symlink_to(old)
        config = self.prefix / 'etc/proxynode/config.json'
        config.parent.mkdir(parents=True)
        config.write_text('{"Agent":{"HostID":7},"Nodes":[]}')
        (state / 'upgrade-result.json').write_text('{"state":"failed"}')
        result = subprocess.run(['bash', '-c',
            'source "$1"; PREFIX="$2"; set_paths; NO_SERVICE=false; publish_bundle "$3"',
            'test', str(ROOT / 'script/install.sh'), str(self.prefix), str(candidate)],
            env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        pending = json.loads((state / 'pending-upgrade.json').read_text())
        self.assertEqual(pending['previous'], str(old))
        self.assertEqual(pending['version'], 'proxynode-v1.2.3')
        self.assertEqual((state / 'current').resolve(), candidate)
        self.assertFalse((state / 'upgrade-result.json').exists())

    def test_migration_stops_old_service_before_starting_new(self):
        service_log = self.base / 'services'
        self.env['SERVICE_LOG'] = str(service_log)
        self.command('systemctl', '#!/bin/bash\nprintf "%s\\n" "$*" >> "$SERVICE_LOG"\n')
        for migrate in ('false', 'true'):
            service_log.write_text('')
            result = subprocess.run([
                'bash', '-c',
                'source "$1"; PREFIX="$2"; set_paths; MIGRATE="$3"; NEEDS_CONFIG=false; install_service systemd',
                'test', str(ROOT / 'script/install.sh'), str(self.prefix), migrate,
            ], env=self.env, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            commands = service_log.read_text().splitlines()
            self.assertIn('restart proxynode', commands)
            if migrate == 'true':
                self.assertLess(commands.index('disable --now v2node'), commands.index('restart proxynode'))
            else:
                self.assertFalse(any('v2node' in command for command in commands))

if __name__ == '__main__':
    unittest.main()
