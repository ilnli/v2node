#!/usr/bin/env python3
"""Real agent/worker lifecycle against a synthetic panel; never uses production services."""

import argparse
import copy
import http.server
import json
import socket
import subprocess
import tempfile
import threading
import time
from pathlib import Path


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_for(predicate, description, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.05)
    raise AssertionError(description)


def listening(port):
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.2):
            return True
    except OSError:
        return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--proxynode", required=True, type=Path)
    args = parser.parse_args()
    binary = args.proxynode.resolve()
    lock = threading.Lock()
    desired = {"host_id": 1, "revision": "empty", "poll_interval": 1, "enabled": True,
               "nodes": [], "audit": {"enabled": False, "memory_mib": 64, "disk_mib": 1024}}
    reports = []
    identity = {"key": "", "revoked": False, "unavailable": False}

    class Panel(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def reply(self, body, status=200):
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def authorized(self):
            return self.headers.get("Authorization") == "Bearer " + identity["key"]

        def do_GET(self):
            if not self.authorized():
                return self.reply({"message": "wrong credential"}, 403)
            if self.path == "/api/v2/agent/desired":
                with lock:
                    if identity["unavailable"]:
                        return self.reply({"message": "temporary outage"}, 503)
                    if identity["revoked"]:
                        return self.reply({"message": "revoked"}, 403)
                    return self.reply({"data": copy.deepcopy(desired)})
            if self.path.startswith("/api/v1/server/UniProxy/user?"):
                return self.reply({"users": []})
            if self.path.startswith("/api/v1/server/UniProxy/alivelist?"):
                return self.reply({"alive": {}})
            self.reply({"message": "unknown route"}, 404)

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            if self.path == "/api/v2/agent/enroll":
                if body.get("enrollment_token") != "pn_enroll_test" or body.get("host_id") != 1:
                    return self.reply({"message": "invalid enrollment"}, 403)
                identity["key"] = body["agent_key"]
                return self.reply({"data": {"host_id": 1}})
            if not self.authorized():
                return self.reply({"message": "wrong credential"}, 403)
            if self.path == "/api/v2/agent/report":
                with lock:
                    reports.append(body)
            self.reply({"data": True})

    panel = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Panel)
    thread = threading.Thread(target=panel.serve_forever, daemon=True)
    thread.start()
    port = free_port()

    def observed(revision, state):
        with lock:
            return any(item.get("revision") == revision and item.get("state") == state for item in reports)

    with tempfile.TemporaryDirectory(prefix="managed-node-") as tmp:
        directory = Path(tmp)
        config = directory / "config.json"
        config.write_text(json.dumps({"Agent": {"ApiHost": f"http://127.0.0.1:{panel.server_port}",
            "HostID": 1, "EnrollmentToken": "pn_enroll_test", "Directory": str(directory / "state")}}))
        with (directory / "agent.log").open("w+") as log:
            process = subprocess.Popen([str(binary), "server", "--config", str(config)], stdout=log, stderr=log)
            try:
                wait_for(lambda: observed("empty", "idle"), "empty host failed to enroll/report")
                node = {"node_id": 7, "enabled": True, "config": {"protocol": "vless", "tls": 0,
                    "listen_ip": "127.0.0.1", "server_port": port, "network": "tcp",
                    "base_config": {"pull_interval": 1, "push_interval": 1}}}
                with lock:
                    desired.update(revision="created", nodes=[copy.deepcopy(node)])
                wait_for(lambda: observed("created", "running"), "created node never became ready")
                assert listening(port), "node report was ready without an actual listener"
                ready = next((directory / "state" / "generations").glob("*/ready.json"))
                first_pid = json.loads(ready.read_text())["pid"]
                with lock:
                    desired["command"] = {"id": "restart-once", "action": "restart"}
                def acknowledged():
                    with lock:
                        return sum(r.get("command", {}).get("id") == "restart-once" and
                                   r["command"].get("state") == "succeeded" for r in reports)
                wait_for(lambda: acknowledged() >= 1, "restart not acknowledged")
                next_pid = json.loads(ready.read_text())["pid"]
                assert first_pid != next_pid, "restart did not replace the worker"
                # Simulate a lost acknowledgement by repeatedly returning the same command.
                wait_for(lambda: acknowledged() >= 3, "command result was not retransmitted")
                assert json.loads(ready.read_text())["pid"] == next_pid, "repeated command restarted twice"
                with lock:
                    desired.pop("command")
                with lock:
                    desired["nodes"][0]["config"].update(network="unsupported", network_settings={"test": True})
                    desired["revision"] = "invalid"
                wait_for(lambda: observed("invalid", "error"), "bad update did not report failure")
                assert listening(port), "invalid update destroyed the last good listener"
                with lock:
                    assert any(r.get("revision") == "invalid" and r.get("applied_revision") == "created" for r in reports)
                    desired.update(revision="removed", nodes=[])
                wait_for(lambda: observed("removed", "idle"), "removed node remained assigned")
                wait_for(lambda: not listening(port), "removed listener was not stopped")
                with lock:
                    desired.update(revision="restored", nodes=[node])
                wait_for(lambda: observed("restored", "running"), "node recreation failed")
                # A cold restart can recover a desired-config outage; user APIs remain available.
                process.terminate()
                process.wait(timeout=15)
                with lock:
                    identity["unavailable"] = True
                process = subprocess.Popen([str(binary), "server", "--config", str(config)], stdout=log, stderr=log)
                wait_for(lambda: listening(port), "cold restart did not restore last good configuration")
                with lock:
                    identity["unavailable"] = False
                    identity["revoked"] = True
                wait_for(lambda: not listening(port), "revoked host retained its listener")
                assert process.poll() is None, "management process exited after revocation"
                # The listener closes before worker shutdown and cache removal finish.
                wait_for(lambda: not (directory / "state" / "last-good.json").exists(),
                         "revoked configuration remains recoverable")
                print("PASS real managed enrollment, create, invalid-update preservation, restart idempotency, remove, recreate, desired-endpoint outage recovery, revoke")
            except BaseException:
                log.seek(0)
                print(log.read())
                with lock:
                    print(json.dumps(reports[-5:], indent=2))
                raise
            finally:
                process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                panel.shutdown()
                panel.server_close()


if __name__ == "__main__":
    main()
