#!/usr/bin/env python3
"""Real PostgreSQL/FastAPI MCP -> managed agent -> Mihomo TCP/TLS/UDP transfers.

Run using proxyboard/backend's locked uv environment; both repositories must be
present. Only loopback databases/endpoints and synthetic users/certificates are used.
"""

import argparse
import asyncio
import contextlib
import copy
import http.server
import json
import os
import socket
import socketserver
import ssl
import subprocess
import tempfile
import threading
import time
import traceback
import uuid
from pathlib import Path

import fakeredis
import httpx
import uvicorn
import yaml
from alembic import command
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client
from sqlalchemy import create_engine
from sqlalchemy.engine import make_url
from sqlalchemy.orm import Session

from proxyboard.auth import hash_password
from proxyboard.cli import migration_config
from proxyboard.core import Settings
from proxyboard.db import Database
from proxyboard.main import create_app
from proxyboard.schema import metadata

import mihomo as probes

DOMAIN = 'node.example.test'
PASSWORD = 'synthetic-managed-admin-password'


@contextlib.contextmanager
def database(configured):
    url = make_url(configured)
    if url.host not in ('localhost', '127.0.0.1') or url.get_backend_name() != 'postgresql':
        raise ValueError('A disposable loopback PostgreSQL URL is required')
    schema = 'pb_managed_interop_' + uuid.uuid4().hex
    owner = create_engine(url)
    with owner.begin() as connection:
        connection.exec_driver_sql(f'CREATE SCHEMA "{schema}"')
    isolated = url.update_query_dict({'options': f'-csearch_path={schema}'})
    engine = create_engine(isolated)
    try:
        metadata.create_all(engine)
        with engine.begin() as connection:
            command.upgrade(migration_config(connection), 'head')
        yield engine, isolated.render_as_string(hide_password=False)
    finally:
        engine.dispose()
        with owner.begin() as connection:
            connection.exec_driver_sql(f'DROP SCHEMA "{schema}" CASCADE')
        owner.dispose()


@contextlib.contextmanager
def panel_server(settings):
    app = create_app(settings, redis_client=fakeredis.FakeRedis(decode_responses=True))
    listener = socket.socket()
    listener.bind(('127.0.0.1', 0))
    listener.listen()
    address = f'http://127.0.0.1:{listener.getsockname()[1]}'
    server = uvicorn.Server(uvicorn.Config(app, log_level='warning', access_log=False))
    thread = threading.Thread(target=lambda: server.run(sockets=[listener]), daemon=True)
    thread.start()
    try:
        wait_for(lambda: server.started, 'FastAPI server startup', timeout=15)
        yield app, address
    finally:
        server.should_exit = True
        thread.join(timeout=15)
        listener.close()
        if thread.is_alive():
            raise RuntimeError('FastAPI server did not stop')


def wait_for(predicate, description, timeout=60):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = predicate()
        if last:
            return last
        time.sleep(0.2)
    raise TimeoutError(f'{description}; last={last!r}')


class Panel:
    def __init__(self, address, settings):
        self.address = address
        self.frontend_key = settings.frontend_proxy_secret
        self.client = httpx.Client(base_url=address, timeout=15, headers={
            'X-ProxyBoard-Frontend-Key': self.frontend_key})
        auth = self.request('/api/v1/passport/auth/login', {
            'email': 'managed-admin@example.com', 'password': PASSWORD})
        self.client.headers['Authorization'] = auth['auth_data']
        self.key = self.request('/api/v1/admin/mcp/rotate', {})['key']
        self.tools = []

    def request(self, path, data=None):
        response = self.client.get(path) if data is None else self.client.post(path, json=data)
        response.raise_for_status()
        return response.json()['data']

    def call(self, name, arguments):
        async def invoke():
            async with httpx.AsyncClient(headers={
                'X-ProxyBoard-Frontend-Key': self.frontend_key,
                'Authorization': 'Bearer ' + self.key,
            }) as client, streamable_http_client(self.address + '/api/v1/mcp', http_client=client) as streams:
                async with ClientSession(streams[0], streams[1]) as session:
                    await session.initialize()
                    result = await session.call_tool(name, arguments)
                    if result.isError:
                        raise AssertionError(f'{name}: {result.structuredContent}')
                    return result.structuredContent['body']['data']
        result = asyncio.run(invoke())
        self.tools.append(name)
        return result

    def host(self, id):
        return next(item for item in self.request('/api/v1/admin/hosts/fetch') if item['id'] == id)


def credentials(root):
    cert, key = root / 'tls.crt', root / 'tls.key'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
        '-keyout', str(key), '-out', str(cert), '-days', '1', '-subj', f'/CN={DOMAIN}',
        '-addext', f'subjectAltName=DNS:{DOMAIN},DNS:localhost,IP:127.0.0.1',
        '-addext', 'basicConstraints=critical,CA:TRUE'], check=True,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return cert, key


def cases():
    return ([{'name': protocol + '-direct', 'protocol': protocol, 'network': 'tcp', 'gateway': False}
             for protocol in ('vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'anytls')]
            + [{'name': 'vless-gateway-' + network, 'protocol': 'vless', 'network': network, 'gateway': True}
               for network in ('ws', 'httpupgrade', 'grpc', 'xhttp')])


@contextlib.contextmanager
def mihomo_process(args, directory, certificate):
    env = os.environ | {'SSL_CERT_FILE': str(certificate)}
    with (directory / 'mihomo.log').open('wb') as log:
        process = subprocess.Popen(args, cwd=directory, env=env, stdout=log, stderr=subprocess.STDOUT)
        try:
            yield process
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


class Readiness(probes.QuietHandler):
    def do_GET(self):
        self.reply({'ready': True})


def run(args):
    root = Path(args.output).resolve()
    root.mkdir(parents=True, exist_ok=False)
    root.chmod(0o700)
    args.proxynode = str(Path(args.proxynode).resolve())
    args.mihomo = str(Path(args.mihomo).resolve())
    version = subprocess.check_output([args.mihomo, '-v'], text=True).strip()
    if f' {probes.VERSION} ' not in version:
        raise ValueError(f'Expected Mihomo {probes.VERSION}, got {version}')
    cert, key = credentials(root)
    matrix = [case for case in cases() if not args.case or case['name'] in args.case]
    if not matrix:
        raise ValueError('No selected test cases')
    results, lifecycle = [], {}
    report = {'mihomo_version': version, 'mihomo_sha256': probes.file_sha256(args.mihomo),
              'proxynode_sha256': probes.file_sha256(args.proxynode), 'results': results, 'lifecycle': lifecycle,
              'not_tested': ['Production DNS/firewalls', 'ACME issuance/renewal (separate Pebble suite)',
                             'Every cipher/transport combination, IPv6, CDN, full VLESS encryption/REALITY matrix']}
    print(f'Artifacts: {root}', flush=True)
    try:
        with database(args.postgres_url) as (engine, url):
            settings = Settings(database_url=url, frontend_proxy_secret='synthetic-frontend-control-secret-32',
                app_key='synthetic-app-key-for-managed-tests-32', testing=True, timezone='UTC')
            with Session(engine) as session:
                db = Database(session)
                group = db.insert('server_group', {'name': 'Synthetic interop users'})
                db.insert('user', {'email': 'managed-admin@example.com', 'password': hash_password(PASSWORD),
                    'uuid': str(uuid.uuid4()), 'token': 'synthetic-admin-token', 'is_admin': 1})
                user = db.insert('user', {'email': 'managed-user@example.com', 'password': hash_password(PASSWORD),
                    'uuid': probes.UUID, 'token': uuid.uuid4().hex, 'group_id': group['id'],
                    'transfer_enable': 10 * 1024**3, 'expired_at': None})
                db.set_config('server_pull_interval', 1)
                db.set_config('server_push_interval', 1)
                session.commit()
            with panel_server(settings) as (_, address), contextlib.ExitStack() as stack:
                panel = Panel(address, settings)
                stack.callback(panel.client.close)
                host = panel.call('admin_hosts_save', {'name': 'Real managed host'})
                profile = panel.call('admin_certificates_save', {'name': 'Local uploaded certificate', 'mode': 'remote',
                    'domain': DOMAIN, 'certificate': cert.read_text(), 'private_key': key.read_text()})
                panel.call('admin_server_route_save', {'remarks': 'Synthetic loopback targets only', 'action': 'default_out',
                    'match': [], 'action_value': json.dumps({'tag': 'interop-loopback', 'protocol': 'freedom',
                        'settings': {'finalRules': [{'action': 'allow', 'ip': ['127.0.0.1/32']}]}})})
                route = panel.call('admin_server_route_fetch', {})[0]
                invitation = panel.call('admin_hosts_enrollment', {'id': host['id']})
                probes.write_json(root / 'agent.json', {'Agent': {'ApiHost': address, 'HostID': host['id'],
                    'EnrollmentToken': invitation['enrollment_token'], 'Directory': str(root / 'state')}})
                agent = stack.enter_context(probes.process([args.proxynode, 'server', '-c', str(root / 'agent.json')],
                                                           root, 'agent.log'))
                wait_for(lambda: (panel.host(host['id']).get('status') or {}).get('state') == 'idle', 'enrolled empty host')
                lifecycle['empty_host_enrolled'] = True
                used = set()
                def port():
                    number = probes.free_port()
                    while number in used:
                        number = probes.free_port()
                    used.add(number)
                    return number
                nodes = []
                for case in matrix:
                    public = port()
                    network = {'path': '/interop'} if case['network'] in ('ws', 'httpupgrade', 'xhttp') else {}
                    if case['network'] == 'ws':
                        network['headers'] = {'Host': DOMAIN}
                    elif case['network'] == 'httpupgrade':
                        network['host'] = DOMAIN
                    elif case['network'] == 'grpc':
                        network = {'serviceName': 'interop'}
                    elif case['network'] == 'xhttp':
                        network.update(mode='packet-up', host=DOMAIN)
                    tls = case['protocol'] != 'shadowsocks'
                    assignment = {'host_id': host['id'], 'enabled': True,
                                  'certificate_id': profile['id'] if tls else None}
                    if case['gateway']:
                        assignment['gateway'] = {'enabled': True, 'port': public, 'host': DOMAIN,
                                                 'path': '/interop', 'backend_port': port()}
                    payload = {'name': case['name'], 'protocol': case['protocol'], 'host': probes.HOST,
                        'port': public, 'server_port': public, 'listen_ip': probes.HOST, 'group_id': [group['id']],
                        'route_id': [route['id']], 'rate': 1, 'show': 1, 'network': case['network'],
                        # Imported SNI must yield to the assigned certificate in worker and subscription views.
                        'network_settings': network, 'tls': int(tls), 'tls_settings': {
                        'server_name': 'stale.example.test', 'server_names': ['stale.example.test'],
                        'serverName': 'stale.example.test', 'serverNames': ['stale.example.test'],
                        'alpn': ['h3'] if case['protocol'] in ('hysteria2', 'tuic') else
                            ['h2'] if case['network'] in ('grpc', 'xhttp') else ['h2', 'http/1.1']},
                        'cipher': 'aes-128-gcm', 'up_mbps': 0, 'down_mbps': 0, 'congestion_control': 'bbr',
                        'managed': assignment, 'include_record': True}
                    nodes.append(panel.call('admin_server_v2node_save', payload))
                expected = {node['id'] for node in nodes}
                def applied():
                    value = panel.host(host['id']).get('status') or {}
                    return value if value.get('state') == 'running' and {
                        item['node_id'] for item in value.get('nodes', [])} == expected else None
                try:
                    status = wait_for(applied, 'all requested nodes applied', timeout=75)
                except Exception:
                    probes.write_json(root / 'failed-host-status.json', panel.host(host['id']))
                    raise
                probes.write_json(root / 'applied-status.json', status)
                lifecycle['all_nodes_reported_running'] = True
                response = panel.client.get('/api/v1/client/subscribe', params={'token': user['token'], 'flag': 'mihomo'})
                response.raise_for_status()
                subscription = yaml.safe_load(response.text)
                proxies = {item['name']: item for item in subscription['proxies']}
                probes.write_json(root / 'subscription-proxies.json', proxies)
                plain = stack.enter_context(probes.serve(http.server.ThreadingHTTPServer((probes.HOST, 0), probes.EchoHandler)))
                tls_echo = probes.TLSHTTPServer((probes.HOST, 0), probes.EchoHandler)
                tls_echo.context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
                tls_echo.context.minimum_version = ssl.TLSVersion.TLSv1_3
                tls_echo.context.load_cert_chain(cert, key)
                stack.enter_context(probes.serve(tls_echo))
                udp = stack.enter_context(probes.serve(socketserver.ThreadingUDPServer((probes.HOST, 0), probes.UDPEcho)))
                readiness = stack.enter_context(probes.serve(http.server.ThreadingHTTPServer((probes.HOST, 0), Readiness)))
                for case in matrix:
                    directory = root / case['name']
                    directory.mkdir()
                    proxy = copy.deepcopy(proxies[case['name']])
                    assert not proxy.get('skip-cert-verify'), 'TLS trust must be verified using the synthetic CA'
                    socks_port = port()
                    config = {'mixed-port': socks_port, 'bind-address': probes.HOST, 'allow-lan': False,
                        'mode': 'rule', 'log-level': 'debug', 'ipv6': False, 'dns': {'enable': False},
                        'proxies': [proxy], 'rules': [f'DST-PORT,{readiness.server_port},DIRECT', f'MATCH,{proxy["name"]}']}
                    probes.write_json(directory / 'mihomo.json', config)
                    result = {**case, 'status': 'FAIL', 'checks': {}}
                    started = time.monotonic()
                    try:
                        with mihomo_process([args.mihomo, '-d', str(directory), '-f', 'mihomo.json'], directory, cert) as client:
                            probes.ready(client, socks_port)
                            probes.ready_mihomo(client, socks_port, readiness.server_port)
                            result['phase'] = 'tcp'
                            result['checks']['tcp_bytes'] = probes.transfer(socks_port, plain.server_port)
                            result['phase'] = 'tls13'
                            result['checks']['tls13_bytes'] = probes.transfer(socks_port, tls_echo.server_port, True)
                            result['phase'] = 'udp'
                            result['checks']['udp_bytes'] = probes.udp_transfer(socks_port, udp.server_address[1])
                        result['status'] = 'PASS'
                        result.pop('phase', None)
                    except Exception as error:
                        result['error'] = f'{type(error).__name__}: {error}'
                    result['seconds'] = round(time.monotonic() - started, 2)
                    results.append(result)
                    probes.write_json(directory / 'result.json', result)
                    print(f"{result['status']:4} {case['name']} {result.get('error', '')}", flush=True)
                # Actual traffic ingestion must reach the panel, not only the echo fixtures.
                def traffic_ingested():
                    with Session(engine) as session:
                        return Database(session).count('traffic_batch') > 0
                wait_for(traffic_ingested, 'traffic submitted to real panel', timeout=15)
                lifecycle['traffic_batches_received'] = True
                for node in nodes:
                    panel.call('admin_server_v2node_drop', {'id': node['id']})
                wait_for(lambda: (panel.host(host['id']).get('status') or {}).get('state') == 'idle', 'node deletion applied')
                lifecycle['all_nodes_removed_agent_alive'] = agent.poll() is None
                report['mcp_tools_called'] = sorted(set(panel.tools))
    except Exception as error:
        report['fatal_error'] = f'{type(error).__name__}: {error}'
        (root / 'harness-error.log').write_text(traceback.format_exc())
        print(report['fatal_error'], flush=True)
    finally:
        probes.write_json(root / 'report.json', report)
    passed = sum(result['status'] == 'PASS' for result in results)
    print(f'{passed}/{len(matrix)} passed; report: {root / "report.json"}', flush=True)
    return 0 if passed == len(matrix) and not report.get('fatal_error') else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--postgres-url', default=os.getenv('PROXYBOARD_TEST_POSTGRES_URL'))
    parser.add_argument('--proxynode', required=True)
    parser.add_argument('--mihomo', required=True)
    parser.add_argument('--output', default=str(Path(tempfile.gettempdir()) / ('panel-managed-' + uuid.uuid4().hex)))
    parser.add_argument('--case', action='append', choices=[case['name'] for case in cases()])
    args = parser.parse_args()
    if not args.postgres_url:
        parser.error('--postgres-url or PROXYBOARD_TEST_POSTGRES_URL is required')
    return run(args)


if __name__ == '__main__':
    raise SystemExit(main())
