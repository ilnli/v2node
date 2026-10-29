package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerReadinessAndStop(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready.json")
	config := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(map[string]any{"ReadyFile": ready})
	os.WriteFile(config, data, 0600)
	program := filepath.Join(dir, "fixture")
	os.WriteFile(program, []byte("#!/usr/bin/env python3\nimport json,os,sys,time\nc=json.load(open(sys.argv[3]))\nwith open(c['ReadyFile'],'w') as f: json.dump({'pid':os.getpid()},f)\nwhile True: time.sleep(1)\n"), 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w, err := StartWorker(ctx, program, config, ready)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Alive() {
		t.Fatal("worker not running after readiness")
	}
	w.Stop()
	if w.Alive() {
		t.Fatal("worker survived stop")
	}
}

func TestWorkerDoesNotAcceptStaleReadiness(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready.json")
	os.WriteFile(ready, []byte(`{"pid":1234}`), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := StartWorker(ctx, "/bin/false", filepath.Join(dir, "config.json"), ready); err == nil {
		t.Fatal("dead worker accepted")
	}
}
