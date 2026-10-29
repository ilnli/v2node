package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type tailLog struct {
	mu   sync.Mutex
	data []byte
}

func (l *tailLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data = append(l.data, data...)
	if len(l.data) > 8192 {
		l.data = append([]byte(nil), l.data[len(l.data)-8192:]...)
	}
	return len(data), nil
}
func (l *tailLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return string(l.data) }

type Worker struct {
	cmd  *exec.Cmd
	done chan struct{}
	log  tailLog
	stop sync.Once
}

func StartWorker(ctx context.Context, binary, config, ready string) (*Worker, error) {
	if err := os.Remove(ready); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	w := &Worker{cmd: exec.Command(binary, "server", "--config", config, "--watch=false"), done: make(chan struct{})}
	if _, err := os.Stat(filepath.Join(filepath.Dir(binary), "geoip.dat")); err == nil {
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "XRAY_LOCATION_ASSET=") {
				w.cmd.Env = append(w.cmd.Env, value)
			}
		}
		w.cmd.Env = append(w.cmd.Env, "XRAY_LOCATION_ASSET="+filepath.Dir(binary))
	}
	w.cmd.Stdout = &w.log
	w.cmd.Stderr = &w.log
	// If the supervising agent is killed, do not leave an unmanaged listener behind.
	w.cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err := w.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start proxy worker: %w", err)
	}
	go func() { _ = w.cmd.Wait(); close(w.done) }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.Stop()
			return nil, errors.New("proxy worker startup timed out")
		case <-w.done:
			return nil, fmt.Errorf("proxy worker failed to start: %s", w.log.String())
		case <-ticker.C:
			data, err := os.ReadFile(ready)
			if err != nil {
				continue
			}
			var status struct {
				PID int `json:"pid"`
			}
			if json.Unmarshal(data, &status) == nil && status.PID == w.cmd.Process.Pid && w.Alive() {
				return w, nil
			}
		}
	}
}

func (w *Worker) Alive() bool {
	if w == nil {
		return false
	}
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	w.stop.Do(func() {
		if !w.Alive() {
			return
		}
		_ = w.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-w.done:
		case <-time.After(120 * time.Second):
			_ = w.cmd.Process.Kill()
			<-w.done
		}
	})
}

func WriteReady(path string) error {
	data, _ := json.Marshal(map[string]any{"pid": os.Getpid()})
	return writePrivate(path, data)
}
