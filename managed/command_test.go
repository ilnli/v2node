package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandResultSurvivesLostAcknowledgement(t *testing.T) {
	dir := t.TempDir()
	result := commandResult{ID: "command-1", State: "succeeded"}
	if err := persistCommand(dir, result); err != nil {
		t.Fatal(err)
	}
	restored, err := readCommand(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != result.ID || restored.State != "succeeded" {
		t.Fatalf("lost terminal command result: %+v", restored)
	}
	if restored.needsExecution(&Command{ID: result.ID, Action: "restart"}) {
		t.Fatal("lost acknowledgement would restart worker twice")
	}
	if !restored.needsExecution(&Command{ID: "command-2", Action: "restart"}) {
		t.Fatal("new command suppressed")
	}
}

func TestInterruptedUpdateUsesSupervisorOutcome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROXYNODE_INSTALL_ROOT", root)
	result := commandResult{ID: "update-1", Action: "update", Version: "proxynode-v1.2.3", State: "running"}
	if err := saveJSON(filepath.Join(root, "pending-upgrade.json"), map[string]any{"version": result.Version}); err != nil {
		t.Fatal(err)
	}
	result.recover("proxynode-v1.2.3")
	if result.State != "running" {
		t.Fatal("acknowledged before supervisor confirmed startup")
	}
	if err := saveJSON(filepath.Join(root, "upgrade-result.json"), map[string]any{"version": result.Version, "state": "failed", "error": "candidate startup timed out"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(root, "pending-upgrade.json"))
	result.recover("proxynode-v1.2.2")
	if result.State != "failed" || result.Error == "" {
		t.Fatalf("rollback lost: %+v", result)
	}
	if result.needsExecution(&Command{ID: result.ID, Action: "update"}) {
		t.Fatal("rolled back update would run again")
	}
}

type updateWorkerStart struct {
	PID    int    `json:"pid"`
	Binary string `json:"binary"`
	Assets string `json:"assets"`
}

func updateCommandFixture(t *testing.T, healthyCandidate bool) (*runtimeState, *Update, string) {
	t.Helper()
	root := installedUpdateRoot(t)
	previous, err := currentRelease(root)
	if err != nil {
		t.Fatal(err)
	}
	program := []byte("#!/usr/bin/env python3\n" +
		"import json,os,sys,time\n" +
		"c=json.load(open(sys.argv[3]))\n" +
		"with open(c['Starts'],'a') as f: f.write(json.dumps({'pid':os.getpid(),'binary':os.path.realpath(sys.argv[0]),'assets':os.environ.get('XRAY_LOCATION_ASSET')})+'\\n')\n" +
		"with open(c['ReadyFile'],'w') as f: json.dump({'pid':os.getpid()},f)\n" +
		"while True: time.sleep(1)\n")
	if err := os.WriteFile(filepath.Join(previous, "proxynode"), program, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, "geoip.dat"), []byte("old geodata"), 0600); err != nil {
		t.Fatal(err)
	}
	candidateDirectory := filepath.Join(root, "releases", "candidate")
	if err := os.Mkdir(candidateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if !healthyCandidate {
		program, err = os.ReadFile("/bin/false")
		if err != nil {
			t.Fatal(err)
		}
	}
	candidate := &Update{Directory: candidateDirectory, Binary: filepath.Join(candidateDirectory, "proxynode"), Version: "proxynode-v1.2.3", root: root}
	if err := os.WriteFile(candidate.Binary, program, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDirectory, "geoip.dat"), []byte("candidate geodata"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared := &Prepared{ConfigPath: filepath.Join(root, "config.json"), ReadyPath: filepath.Join(root, "ready.json"), Nodes: []map[string]any{{"node_id": 1}}}
	if err := saveJSON(prepared.ConfigPath, map[string]any{"ReadyFile": prepared.ReadyPath, "Starts": filepath.Join(root, "worker-starts.jsonl")}); err != nil {
		t.Fatal(err)
	}
	state := &runtimeState{directory: root, binary: filepath.Join(previous, "proxynode"), prepared: prepared, applied: &Manifest{Revision: "last-known-good"}}
	t.Cleanup(state.stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state.worker, err = state.start(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	return state, candidate, previous
}

func updateWorkerStarts(t *testing.T, directory string) []updateWorkerStart {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "worker-starts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var starts []updateWorkerStart
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var start updateWorkerStart
		if err := json.Unmarshal([]byte(line), &start); err != nil {
			t.Fatal(err)
		}
		starts = append(starts, start)
	}
	return starts
}

func TestActivateUpdateFailedWorkerRestoresPreviousBundle(t *testing.T) {
	state, candidate, previous := updateCommandFixture(t, false)
	original := state.worker
	prepared := state.prepared
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := state.activateUpdate(ctx, candidate)
	if err == nil || errors.Is(err, ErrRestartSupervisor) {
		t.Fatalf("failed candidate requested activation: %v", err)
	}
	if original.Alive() || state.worker == original || !state.worker.Alive() {
		t.Fatal("previous worker was not stopped and restored as a healthy new process")
	}
	if state.binary != filepath.Join(previous, "proxynode") || state.prepared != prepared || state.applied.Revision != "last-known-good" {
		t.Fatal("candidate failure changed the applied configuration or binary")
	}
	current, err := currentRelease(candidate.root)
	if err != nil || current != previous {
		t.Fatalf("failed candidate changed the bundle pointer: %s, %v", current, err)
	}
	data, err := os.ReadFile(filepath.Join(candidate.root, "current", "geoip.dat"))
	if err != nil || string(data) != "old geodata" {
		t.Fatalf("failed candidate replaced geodata: %q, %v", data, err)
	}
	for _, path := range []string{candidate.Directory, filepath.Join(candidate.root, "pending-upgrade.json"), filepath.Join(candidate.root, ".current-next")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("failed candidate left an activation artifact %s: %v", path, err)
		}
	}
	starts := updateWorkerStarts(t, candidate.root)
	if len(starts) != 2 || starts[0].Binary != state.binary || starts[1].Binary != state.binary || starts[1].PID != state.worker.cmd.Process.Pid || starts[1].Assets != previous {
		t.Fatalf("restored worker did not use the previous bundle: %+v", starts)
	}
	data, err = os.ReadFile(prepared.ReadyPath)
	var ready struct {
		PID int `json:"pid"`
	}
	if err != nil || json.Unmarshal(data, &ready) != nil || ready.PID != state.worker.cmd.Process.Pid {
		t.Fatalf("failed candidate left stale readiness: %s, %v", data, err)
	}
}

func TestActivateUpdateHealthyWorkerSwitchesBundleAndRequestsRestart(t *testing.T) {
	state, candidate, previous := updateCommandFixture(t, true)
	original := state.worker
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := state.activateUpdate(ctx, candidate); !errors.Is(err, ErrRestartSupervisor) {
		t.Fatalf("healthy candidate did not request supervisor handoff: %v", err)
	}
	if original.Alive() || state.worker != nil {
		t.Fatal("a worker remained active during supervisor handoff")
	}
	starts := updateWorkerStarts(t, candidate.root)
	if len(starts) != 2 || starts[1].Binary != candidate.Binary || starts[1].Assets != candidate.Directory {
		t.Fatalf("candidate was not validated with its own binary/geodata: %+v", starts)
	}
	if err := syscall.Kill(starts[1].PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("candidate validation worker survived handoff: %v", err)
	}
	current, err := currentRelease(candidate.root)
	if err != nil || current != candidate.Directory {
		t.Fatalf("healthy candidate did not become current: %s, %v", current, err)
	}
	data, err := os.ReadFile(filepath.Join(candidate.root, "current", "geoip.dat"))
	if err != nil || string(data) != "candidate geodata" {
		t.Fatalf("bundle assets did not switch together: %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(candidate.root, "pending-upgrade.json"))
	var pending struct {
		Previous, Candidate, Version string
		StartedAt                    int64 `json:"started_at"`
	}
	if err != nil || json.Unmarshal(data, &pending) != nil || pending.Previous != previous || pending.Candidate != candidate.Directory || pending.Version != candidate.Version || pending.StartedAt == 0 {
		t.Fatalf("missing or incorrect supervisor rollback marker: %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(previous, "proxynode")); err != nil {
		t.Fatalf("previous release removed before supervisor confirmation: %v", err)
	}
	identity, err := os.ReadFile(filepath.Join(candidate.root, "identity.json"))
	if err != nil || string(identity) != "preserve identity" {
		t.Fatalf("update changed enrollment identity: %q, %v", identity, err)
	}
}
