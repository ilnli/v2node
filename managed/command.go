package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrRestartSupervisor requests an orderly handoff to the installed guardian.
var ErrRestartSupervisor = errors.New("activate managed release")

type commandResult struct {
	ID      string `json:"id"`
	Action  string `json:"action,omitempty"`
	Version string `json:"version,omitempty"`
	State   string `json:"state"`
	Error   string `json:"error"`
}

func readCommand(directory string) (commandResult, error) {
	var result commandResult
	data, err := os.ReadFile(filepath.Join(directory, "command-result.json"))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(data, &result)
	return result, err
}
func persistCommand(directory string, result commandResult) error {
	return saveJSON(filepath.Join(directory, "command-result.json"), result)
}
func (r commandResult) report() map[string]any {
	return map[string]any{"id": r.ID, "state": r.State, "error": r.Error}
}
func (r commandResult) needsExecution(command *Command) bool {
	return command != nil && command.ID != r.ID
}
func (r *commandResult) recover(version string) {
	if r.State != "running" {
		return
	}
	if r.Action == "update" {
		root := os.Getenv("PROXYNODE_INSTALL_ROOT")
		var outcome struct {
			Version string `json:"version"`
			State   string `json:"state"`
			Error   string `json:"error"`
		}
		data, err := os.ReadFile(filepath.Join(root, "upgrade-result.json"))
		if err == nil && json.Unmarshal(data, &outcome) == nil && outcome.Version == r.Version {
			if outcome.State == "failed" {
				r.State, r.Error = "failed", outcome.Error
				return
			}
			if outcome.State == "succeeded" && version == r.Version {
				r.State, r.Error = "succeeded", ""
				return
			}
		}
		if _, err := os.Stat(filepath.Join(root, "pending-upgrade.json")); err == nil {
			return
		}
	}
	r.State, r.Error = "failed", "command interrupted before confirmed completion; submit a new command to retry"
}

func (s *runtimeState) upgrade(ctx context.Context, command *Command) error {
	root := os.Getenv("PROXYNODE_INSTALL_ROOT")
	if root == "" || os.Getenv("PROXYNODE_HEALTH_FILE") == "" {
		return errors.New("remote updates require the installed managed supervisor")
	}
	candidate, err := StageUpdate(ctx, root, command.Version)
	if err != nil {
		return err
	}
	return s.activateUpdate(ctx, candidate)
}

func (s *runtimeState) activateUpdate(ctx context.Context, candidate *Update) error {
	defer candidate.Discard()
	// Test the actual candidate and its geodata with the currently applied listeners.
	// Stop first so the candidate can bind the same ports and own the shared audit queue.
	previousBinary := s.binary
	s.stop()
	s.binary = candidate.Binary
	startup, cancel := context.WithTimeout(ctx, 45*time.Second)
	worker, err := StartWorker(startup, candidate.Binary, s.prepared.ConfigPath, s.prepared.ReadyPath)
	cancel()
	if worker != nil {
		worker.Stop()
	}
	s.binary = previousBinary
	if err == nil {
		err = candidate.Activate()
	}
	if err != nil {
		restored, restoreErr := s.start(ctx, s.prepared)
		s.worker = restored
		if restoreErr != nil {
			return fmt.Errorf("update failed (%v); restore failed (%v)", err, restoreErr)
		}
		return err
	}
	return ErrRestartSupervisor
}
