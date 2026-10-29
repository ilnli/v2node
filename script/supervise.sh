#!/bin/sh
# Stable service guardian: only launch verified installed bundles, never shell commands from the panel.
exec python3 - "$@" <<'PY'
import argparse
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

parser = argparse.ArgumentParser(description='Proxynode service supervisor')
parser.add_argument('--root', default='/var/lib/proxynode')
parser.add_argument('--config', default='/etc/proxynode/config.json')
parser.add_argument('--startup-timeout', type=float, default=90)
parser.add_argument('--restart-delay', type=float, default=5)
args = parser.parse_args()
root, config = Path(args.root).resolve(), Path(args.config).resolve()
health = root / 'agent-health.json'
pending_file = root / 'pending-upgrade.json'
stop = False

def signal_stop(_signal, _frame):
    global stop
    stop = True

for event in (signal.SIGTERM, signal.SIGINT): signal.signal(event, signal_stop)

def read_json(path):
    try:
        with path.open() as source: return json.loads(source.read(65536))
    except (FileNotFoundError, ValueError): return None

def save_json(path, value):
    temp = path.with_name('.' + path.name + '.' + str(os.getpid()))
    with os.fdopen(os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as target:
        json.dump(value, target)
        target.flush(); os.fsync(target.fileno())
    os.replace(temp, path)

def release_path(path):
    value = Path(path).resolve()
    if value.parent != (root / 'releases').resolve() or not (value / 'proxynode').is_file():
        raise ValueError('Invalid installed release path')
    return value

def switch_release(path):
    temporary = root / ('.current-guardian-' + str(os.getpid()))
    temporary.symlink_to(release_path(path))
    os.replace(temporary, root / 'current')

def finish(pending, state, error=''):
    save_json(root / 'upgrade-result.json', {'version': pending['version'], 'state': state,
                                           'error': error, 'finished_at': int(time.time())})
    pending_file.unlink(missing_ok=True)

def terminate(process):
    if process.poll() is None:
        try: os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError: pass
        # Worker.Stop permits 120 seconds for traffic/audit flushes. The service timeout is 140.
        try: process.wait(timeout=125)
        except subprocess.TimeoutExpired:
            try: os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError: pass
            process.wait()
    # Abnormal parent exits can leave descendants alive; never start rollback listeners over them.
    try: os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError: pass

def pause(seconds):
    until = time.monotonic() + seconds
    while not stop and time.monotonic() < until: time.sleep(min(0.1, max(0, until-time.monotonic())))

while not stop:
    pending = read_json(pending_file)
    release = release_path(root / 'current')
    if pending:
        previous = release_path(pending['previous'])
        candidate = release_path(pending['candidate'])
        if release != candidate:
            # The process died between persisting rollback state and publishing the bundle.
            switch_release(previous)
            finish(pending, 'failed', 'Upgrade activation was interrupted; previous release restored')
            pending = None; release = previous
    health.unlink(missing_ok=True)
    environment = dict(os.environ, PROXYNODE_INSTALL_ROOT=str(root), PROXYNODE_HEALTH_FILE=str(health), PROXYNODE_SUPERVISOR_PID=str(os.getpid()),
                       XRAY_LOCATION_ASSET=str(release))
    try:
        process = subprocess.Popen([str(release / 'proxynode'), 'server', '--config', str(config), '--watch=false'],
                                   cwd=str(config.parent), env=environment, start_new_session=True)
    except OSError:
        if pending:
            switch_release(previous); finish(pending, 'failed', 'Candidate agent could not start; previous release restored')
        else: print('Proxynode agent could not start; retrying', file=sys.stderr, flush=True)
        pause(args.restart_delay)
        continue
    deadline = time.monotonic() + args.startup_timeout
    failure = ''
    while not stop:
        if process.poll() is not None:
            if pending: failure = 'Candidate agent exited before startup confirmation; previous release restored'
            break
        if pending:
            ready = read_json(health)
            if ready and ready.get('pid') == process.pid and ready.get('version') == pending['version']:
                finish(pending, 'succeeded'); pending = None
            elif time.monotonic() >= deadline:
                failure = 'Candidate agent startup timeout; previous release restored'
                break
        pause(0.1)
    terminate(process)
    if stop: break
    if failure:
        switch_release(previous); finish(pending, 'failed', failure)
        print(failure, file=sys.stderr, flush=True)
    # Exit 75 explicitly hands off to the newly activated bundle without restart delay.
    if process.returncode != 75: pause(args.restart_delay)
PY
