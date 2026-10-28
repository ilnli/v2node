#!/usr/bin/env python3
"""Real Mihomo -> real proxynode VLESS transfers. Python standard library only."""

import argparse
import base64
import contextlib
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import socket
import socketserver
import ssl
import struct
import subprocess
import tempfile
import threading
import time
import urllib.parse

VERSION = "v1.19.33"
UUID = "c7599b31-fc0b-4b36-9626-f7ce2adeb767"
HOST = "127.0.0.1"
TIMEOUT = 15


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def file_sha256(path):
    with open(path, "rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


def free_port():
    with socket.socket() as sock:
        sock.bind((HOST, 0))
        return sock.getsockname()[1]


def receive(sock, size):
    data = bytearray()
    while len(data) < size:
        part = sock.recv(size - len(data))
        if not part:
            raise EOFError(f"connection closed after {len(data)}/{size} bytes")
        data.extend(part)
    return bytes(data)


def socks_address(port):
    return b"\x01" + socket.inet_aton(HOST) + struct.pack("!H", port)


def socks_connect(port, target, command=1):
    sock = socket.create_connection((HOST, port), TIMEOUT)
    try:
        sock.sendall(b"\x05\x01\x00")
        if receive(sock, 2) != b"\x05\x00":
            raise AssertionError("SOCKS authentication failed")
        sock.sendall(bytes([5, command, 0]) + socks_address(target))
        header = receive(sock, 4)
        if header[:2] != b"\x05\x00":
            raise AssertionError(f"SOCKS request failed: {header.hex()}")
        if header[3] == 1:
            address = socket.inet_ntoa(receive(sock, 4))
        elif header[3] == 4:
            address = socket.inet_ntop(socket.AF_INET6, receive(sock, 16))
        elif header[3] == 3:
            address = receive(sock, receive(sock, 1)[0]).decode()
        else:
            raise AssertionError("bad SOCKS reply address")
        bound_port = struct.unpack("!H", receive(sock, 2))[0]
        return sock, (address, bound_port)
    except BaseException:
        sock.close()
        raise


class QuietHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def reply(self, data):
        body = json.dumps(data).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class PanelHandler(QuietHandler):
    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if urllib.parse.parse_qs(parsed.query).get("token") != ["interop-test-token"]:
            self.send_error(403)
        elif parsed.path == "/api/v2/server/config":
            self.reply(self.server.node)
        elif parsed.path.endswith("/user"):
            self.reply({"users": [{"id": 1, "uuid": UUID, "speed_limit": 0, "device_limit": 0}]})
        elif parsed.path.endswith("/alivelist"):
            self.reply({"alive": {}})
        elif parsed.path == "/ready":
            self.reply({"ready": True})
        else:
            self.send_error(404)

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.reply({"success": True})


class EchoHandler(QuietHandler):
    def do_POST(self):
        data = self.rfile.read(int(self.headers["Content-Length"]))
        self.send_response(200)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


class UDPEcho(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(data, self.client_address)


class TLSHTTPServer(http.server.ThreadingHTTPServer):
    def process_request_thread(self, request, client_address):
        # Handshake in the worker: a REALITY probe must not block the accept loop.
        request.settimeout(TIMEOUT)
        try:
            request = self.context.wrap_socket(request, server_side=True)
        except (ssl.SSLError, OSError):
            request.close()
            return
        super().process_request_thread(request, client_address)


@contextlib.contextmanager
def serve(server):
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


@contextlib.contextmanager
def process(args, directory, log_name):
    with (directory / log_name).open("wb") as log:
        proc = subprocess.Popen(args, cwd=directory, stdout=log, stderr=subprocess.STDOUT)
        try:
            yield proc
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


def ready(proc, port):
    deadline = time.monotonic() + TIMEOUT
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"process exited with status {proc.returncode}")
        try:
            with socket.create_connection((HOST, port), 0.2):
                return
        except OSError:
            time.sleep(0.05)
    raise TimeoutError(f"listener {port} did not start")


def ready_mihomo(proc, socks_port, panel_port):
    # Listener creation precedes tunnel.OnRunning in Mihomo. A separate DIRECT
    # route to the panel readiness endpoint probes that transition; test traffic
    # uses different target ports and always matches tested-proxynode.
    deadline = time.monotonic() + TIMEOUT
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"Mihomo exited with status {proc.returncode}")
        try:
            sock, _ = socks_connect(socks_port, panel_port)
            with sock:
                sock.settimeout(0.5)
                sock.sendall(b"GET /ready?token=interop-test-token HTTP/1.0\r\n\r\n")
                response = http.client.HTTPResponse(sock)
                response.begin()
                if response.status == 200 and json.loads(response.read()) == {"ready": True}:
                    return
        except (OSError, EOFError, http.client.HTTPException):
            pass
        time.sleep(0.05)
    raise TimeoutError("Mihomo tunnel did not finish applying configuration")


def transfer(socks_port, target_port, tls=False):
    sock, _ = socks_connect(socks_port, target_port)
    with sock:
        if tls:
            ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            ctx.check_hostname = False
            ctx.verify_mode = ssl.CERT_NONE
            ctx.minimum_version = ssl.TLSVersion.TLSv1_3
            sock = ctx.wrap_socket(sock, server_hostname=HOST)
        with sock:
            # Large incompressible TLS transfer exercises Vision's TLS filtering/direct-copy path.
            body = os.urandom(256 * 1024)
            sock.sendall((f"POST /echo HTTP/1.1\r\nHost: {HOST}\r\n"
                          f"Content-Length: {len(body)}\r\nConnection: close\r\n\r\n").encode())
            sock.sendall(body)
            response = http.client.HTTPResponse(sock)
            response.begin()
            actual = response.read()
            if response.status != 200 or actual != body:
                raise AssertionError(f"echo mismatch: HTTP {response.status}, {len(actual)} bytes")
            return len(body)


def udp_transfer(socks_port, target_port):
    control, relay = socks_connect(socks_port, 0, command=3)
    with control, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.settimeout(TIMEOUT)
        total = 0
        # Reuse one association; varied sizes catch packet framing/boundary corruption.
        for size in (1, 257, 1200, 4096):
            body = os.urandom(size)
            sock.sendto(b"\x00\x00\x00" + socks_address(target_port) + body, relay)
            actual = sock.recv(65535)
            if actual != b"\x00\x00\x00" + socks_address(target_port) + body:
                raise AssertionError(f"UDP echo mismatch for {size} bytes")
            total += size
        return total


def credentials(directory):
    key, cert = directory / "tls.key", directory / "tls.crt"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                    "-keyout", str(key), "-out", str(cert), "-days", "1", "-subj", "/CN=localhost",
                    "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1"],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    private = subprocess.check_output(["openssl", "genpkey", "-algorithm", "X25519", "-outform", "DER"])
    public = subprocess.check_output(["openssl", "pkey", "-inform", "DER", "-pubout", "-outform", "DER"],
                                     input=private)
    encode = lambda data: base64.urlsafe_b64encode(data[-32:]).decode().rstrip("=")
    der = ssl.PEM_cert_to_DER_cert(cert.read_text())
    return {"cert": str(cert), "key": str(key), "private": encode(private), "public": encode(public),
            "fingerprint": hashlib.sha256(der).hexdigest()}


def cases():
    matrix = []
    def add(network, security, flow="", mode="", encryption="", variant=""):
        name = "-".join(filter(None, (network, security, "vision" if flow else "", mode, encryption, variant)))
        matrix.append(dict(name=name, network=network, security=security, flow=flow,
                           mode=mode, encryption=encryption, variant=variant))
    for security in ("plain", "tls", "reality"):
        add("tcp", security)
    for security in ("tls", "reality"):
        add("tcp", security, flow="xtls-rprx-vision")
    for network in ("ws", "httpupgrade", "grpc"):
        for security in ("plain", "tls"):
            add(network, security)
    add("grpc", "reality")
    for mode in ("packet-up", "stream-up", "stream-one", "auto"):
        for security in ("tls", "reality"):
            add("xhttp", security, mode=mode)
    for mode in ("packet-up", "auto"):
        add("xhttp", "plain", mode=mode)
    for encryption in ("native-1rtt", "xorpub-0rtt", "random-1rtt"):
        add("tcp", "plain", encryption=encryption)
    add("tcp", "tls", flow="xtls-rprx-vision", encryption="native-1rtt")
    add("tcp", "tls", variant="pinned-cert")
    add("ws", "tls", variant="early-data")
    add("httpupgrade", "tls", variant="fast-open")
    add("xhttp", "tls", mode="packet-up", variant="h1")
    add("xhttp", "tls", mode="packet-up", variant="h3")
    return matrix


def configs(case, creds, reality_port, inbound_port, socks_port):
    network, security = case["network"], case["security"]
    node = {"protocol": "vless", "listen_ip": HOST, "server_port": inbound_port,
            "network": network, "network_settings": {}, "tls": {"plain": 0, "tls": 1, "reality": 2}[security],
            "flow": case["flow"], "base_config": {"push_interval": 3600, "pull_interval": 3600}}
    # Current Xray blocks private destinations by default. Explicitly allow only the
    # loopback echo fixtures via the real panel configuration route mechanism.
    node["routes"] = [{"id": 1, "action": "default_out", "match": [], "action_value": json.dumps({
        "tag": "interop-loopback", "protocol": "freedom", "settings": {
            "finalRules": [{"action": "allow", "ip": ["127.0.0.1/32"]}]}})}]
    proxy = {"name": "tested-proxynode", "type": "vless", "server": HOST, "port": inbound_port,
             "uuid": UUID, "network": network, "udp": True, "packet-encoding": "xudp",
             "tls": security != "plain", "flow": case["flow"]}
    if security == "tls":
        node["tls_settings"] = {"cert_mode": "file", "cert_file": creds["cert"], "key_file": creds["key"]}
        proxy.update({"servername": "localhost", "skip-cert-verify": True})
    elif security == "reality":
        node["tls_settings"] = {"dest": HOST, "server_port": str(reality_port), "server_name": "localhost",
                                "private_key": creds["private"], "short_id": "0123456789abcdef"}
        proxy.update({"servername": "localhost", "client-fingerprint": "chrome",
                      "reality-opts": {"public-key": creds["public"], "short-id": "0123456789abcdef",
                                       "support-x25519mlkem768": True}})
    if network in ("ws", "httpupgrade"):
        node["network_settings"] = {"path": "/interop"}
        proxy["network"] = "ws"
        proxy["ws-opts"] = {"path": "/interop"}
        if network == "httpupgrade":
            proxy["ws-opts"]["v2ray-http-upgrade"] = True
    elif network == "grpc":
        node["network_settings"] = {"serviceName": "interop"}
        proxy["grpc-opts"] = {"grpc-service-name": "interop"}
    elif network == "xhttp":
        node["network_settings"] = {"path": "/interop", "mode": case["mode"]}
        proxy["xhttp-opts"] = {"path": "/interop", "mode": case["mode"]}
        if security == "tls":
            proxy["alpn"] = ["h2"]
    if case["encryption"]:
        mode, rtt = case["encryption"].split("-")
        node["encryption"] = "mlkem768x25519plus"
        node["encryption_settings"] = {"mode": mode, "ticket": "600s", "private_key": creds["private"]}
        proxy["encryption"] = f"mlkem768x25519plus.{mode}.{rtt}.{creds['public']}"
    if case["variant"] == "pinned-cert":
        proxy["skip-cert-verify"] = False
        proxy["fingerprint"] = creds["fingerprint"]
    elif case["variant"] == "early-data":
        proxy["ws-opts"].update({"max-early-data": 2048, "early-data-header-name": "Sec-WebSocket-Protocol"})
    elif case["variant"] == "fast-open":
        proxy["ws-opts"]["v2ray-http-upgrade-fast-open"] = True
    elif case["variant"] in ("h1", "h3"):
        alpn = "http/1.1" if case["variant"] == "h1" else "h3"
        proxy["alpn"] = [alpn]
        node["tls_settings"]["alpn"] = [alpn]
    mihomo = {"mixed-port": socks_port, "bind-address": HOST, "allow-lan": False,
              "mode": "rule", "log-level": "debug", "ipv6": False,
              "dns": {"enable": False}, "proxies": [proxy], "rules": ["MATCH,tested-proxynode"]}
    return node, mihomo


def run_case(case, args, root, creds, plain_port, tls_port, udp_port):
    directory = root / case["name"]
    directory.mkdir()
    inbound_port, socks_port = free_port(), free_port()
    node, mihomo = configs(case, creds, tls_port, inbound_port, socks_port)
    write_json(directory / "panel.json", node)
    panel = http.server.ThreadingHTTPServer((HOST, 0), PanelHandler)
    panel.node = node
    mihomo["rules"].insert(0, f"DST-PORT,{panel.server_port},DIRECT")
    write_json(directory / "mihomo.json", mihomo)
    config = {"Log": {"Level": "debug", "Access": "none"}, "Audit": {"Enabled": False},
              "Nodes": [{"ApiHost": f"http://{HOST}:{panel.server_port}", "NodeID": 1,
                         "ApiKey": "interop-test-token", "Timeout": 5, "RetryCount": 0}]}
    write_json(directory / "proxynode.json", config)
    result = dict(case, status="FAIL", checks={})
    start = time.monotonic()
    try:
        with serve(panel), process([args.proxynode, "server", "--watch=false", "-c", "proxynode.json"],
                                   directory, "proxynode.log") as server:
            if case["variant"] != "h3":
                ready(server, inbound_port)
            else:
                deadline = time.monotonic() + TIMEOUT
                while "Nodes started" not in (directory / "proxynode.log").read_text():
                    if server.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError("proxynode HTTP/3 listener did not initialize")
                    time.sleep(0.05)
            with process([args.mihomo, "-d", str(directory), "-f", "mihomo.json"], directory, "mihomo.log") as client:
                ready(client, socks_port)
                ready_mihomo(client, socks_port, panel.server_port)
                for label, target, tls in (("tcp", plain_port, False), ("tls13", tls_port, True)):
                    result["phase"] = label
                    result["checks"][label] = transfer(socks_port, target, tls)
                if case["encryption"].endswith("0rtt"):
                    result["checks"]["resumed-tls13"] = transfer(socks_port, tls_port, True)
                result["phase"] = "udp-xudp"
                result["checks"]["udp-xudp"] = udp_transfer(socks_port, udp_port)
                if case["flow"]:
                    log = (directory / "proxynode.log").read_text()
                    if "XtlsFilterTls found tls 1.3!" not in log or "command 2" not in log:
                        raise AssertionError("Vision did not recognize TLS 1.3 and switch to direct copy")
                    result["checks"]["vision-direct-copy"] = True
                if server.poll() is not None or client.poll() is not None:
                    raise RuntimeError("proxy exited during transfers")
            if case["variant"] == "pinned-cert":
                mihomo["proxies"][0]["uuid"] = "00000000-0000-4000-8000-000000000001"
                write_json(directory / "mihomo-wrong-uuid.json", mihomo)
                with process([args.mihomo, "-d", str(directory), "-f", "mihomo-wrong-uuid.json"],
                             directory, "mihomo-wrong-uuid.log") as client:
                    ready(client, socks_port)
                    ready_mihomo(client, socks_port, panel.server_port)
                    try:
                        transfer(socks_port, plain_port)
                    except (OSError, EOFError, http.client.HTTPException):
                        result["checks"]["wrong-uuid-rejected"] = True
                    else:
                        raise AssertionError("wrong UUID unexpectedly passed a transfer")
            result.pop("phase", None)
            result["status"] = "PASS"
    except Exception as exc:
        result["error"] = f"{type(exc).__name__}: {exc}"
    result["seconds"] = round(time.monotonic() - start, 2)
    write_json(directory / "result.json", result)
    print(f"{result['status']:4} {case['name']} {result.get('error', '')}", flush=True)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--proxynode", required=True)
    parser.add_argument("--mihomo", required=True)
    parser.add_argument("--output", help="New directory for configs, logs, and report (default: temporary directory)")
    parser.add_argument("--case", action="append", help="Run exact case name (repeatable); defaults to full matrix")
    parser.add_argument("--list", action="store_true")
    args = parser.parse_args()
    matrix = cases()
    if args.list:
        print("\n".join(case["name"] for case in matrix))
        return 0
    if args.case:
        unknown = set(args.case) - {case["name"] for case in matrix}
        if unknown:
            parser.error(f"unknown cases: {sorted(unknown)}")
        matrix = [case for case in matrix if case["name"] in args.case]
    args.proxynode = str(Path(args.proxynode).resolve())
    args.mihomo = str(Path(args.mihomo).resolve())
    version = subprocess.check_output([args.mihomo, "-v"], text=True).strip()
    if f" {VERSION} " not in version:
        parser.error(f"expected Mihomo {VERSION}, found {version}")
    root = Path(args.output).resolve() if args.output else Path(tempfile.mkdtemp(prefix="mihomo-interop-"))
    if args.output:
        root.mkdir(parents=True, exist_ok=False)
    print(f"Artifacts: {root}", flush=True)
    creds = credentials(root)
    plain = http.server.ThreadingHTTPServer((HOST, 0), EchoHandler)
    tls = TLSHTTPServer((HOST, 0), EchoHandler)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.load_cert_chain(creds["cert"], creds["key"])
    # REALITY only needs a TLS handshake from the local camouflage target.
    ctx.set_alpn_protocols(["h2", "http/1.1"])
    tls.context = ctx
    udp = socketserver.ThreadingUDPServer((HOST, 0), UDPEcho)
    with serve(plain), serve(tls), serve(udp):
        results = [run_case(case, args, root, creds, plain.server_port, tls.server_port,
                            udp.server_address[1]) for case in matrix]
    report = {"mihomo_version": version, "mihomo_sha256": file_sha256(args.mihomo),
              "proxynode_sha256": file_sha256(args.proxynode),
              "results": results,
              "unsupported": ["Mihomo v1.19.33 defaults to XUDP; xudp:false does not select raw VLESS UDP.",
                              "Vision is only tested with TCP + TLS/REALITY; transport combinations are not assumed valid."],
              "known_incompatible": ["REALITY without support-x25519mlkem768:true is rejected by this Xray/REALITY version."],
              "not_tested": ["IPv6, CDN/reverse proxies, Mux, REALITY ML-DSA, packetaddr",
                             "XHTTP separate download settings, every encryption key/padding/transport permutation"]}
    write_json(root / "report.json", report)
    failed = sum(result["status"] != "PASS" for result in results)
    print(f"{len(results) - failed}/{len(results)} passed; report: {root / 'report.json'}", flush=True)
    return bool(failed)


if __name__ == "__main__":
    raise SystemExit(main())
