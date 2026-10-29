package managed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wyx2685/v2node/conf"
)

func readIdentity(path string) (Identity, error) {
	var identity Identity
	data, err := os.ReadFile(path)
	if err != nil {
		return identity, err
	}
	err = json.Unmarshal(data, &identity)
	return identity, err
}

func saveJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writePrivate(path, data)
}

type runtimeState struct {
	directory            string
	binary               string
	identity             Identity
	worker               *Worker
	applied              *Manifest
	prepared             *Prepared
	nextCertificateCheck time.Time
}

func (s *runtimeState) stop() {
	if s.worker != nil {
		s.worker.Stop()
		s.worker = nil
	}
}

func (s *runtimeState) start(ctx context.Context, p *Prepared) (*Worker, error) {
	if len(p.Nodes) == 0 {
		return nil, nil
	}
	startup, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return StartWorker(startup, s.binary, p.ConfigPath, p.ReadyPath)
}

func (s *runtimeState) apply(ctx context.Context, m Manifest, force bool) error {
	alive := s.prepared != nil && (len(s.prepared.Nodes) == 0 || s.worker.Alive())
	unchanged := s.applied != nil && s.applied.Revision == m.Revision
	if !force && unchanged && alive && time.Now().Before(s.nextCertificateCheck) {
		return nil
	}
	defer func() { pruneConfigurations(s.directory, s.applied, &m) }()
	prepared, err := Prepare(m, s.identity, s.directory)
	if err != nil {
		return err
	}
	s.nextCertificateCheck = time.Now().Add(time.Hour)
	if !force && unchanged && alive && !prepared.CertificateChanged {
		s.prepared = prepared
		return nil
	}
	previous := s.prepared
	s.stop()
	worker, err := s.start(ctx, prepared)
	if err != nil {
		if previous != nil {
			old, restoreErr := s.start(ctx, previous)
			s.worker = old
			if restoreErr != nil {
				return fmt.Errorf("apply failed (%v); restore failed (%v)", err, restoreErr)
			}
		}
		return err
	}
	s.worker = worker
	s.prepared = prepared
	s.applied = &m
	if err := saveJSON(filepath.Join(s.directory, "last-good.json"), m); err != nil {
		return fmt.Errorf("persist applied configuration: %w", err)
	}
	return nil
}

func diagnostic(err error, identity Identity, m *Manifest) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	secrets := []string{identity.Key}
	if m != nil {
		for _, n := range m.Nodes {
			if c := n.Certificate; c != nil {
				secrets = append(secrets, c.PrivateKey)
				for _, v := range c.DNSEnv {
					secrets = append(secrets, v)
				}
			}
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	if len(message) > 1800 {
		message = message[len(message)-1800:]
	}
	return message
}

// Run keeps management alive independently from proxy listeners and their configuration.
func Run(ctx context.Context, config *conf.AgentConfig, version string) error {
	if config.Directory == "" {
		config.Directory = "/var/lib/proxynode/managed"
	}
	if !filepath.IsAbs(config.Directory) {
		return errors.New("managed state directory must be absolute")
	}
	identityPath := filepath.Join(config.Directory, "identity.json")
	identity, err := readIdentity(identityPath)
	if os.IsNotExist(err) {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		identity = Identity{APIHost: strings.TrimRight(config.APIHost, "/"), HostID: config.HostID, Key: "pn_host_" + hex.EncodeToString(key)}
		if err = saveJSON(identityPath, identity); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("read managed identity: %w", err)
	}
	if identity.HostID != config.HostID || identity.APIHost != strings.TrimRight(config.APIHost, "/") {
		return errors.New("managed identity belongs to a different panel or host")
	}
	client, err := NewClient(identity.APIHost, identity.HostID, identity.Key)
	if err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	state := &runtimeState{directory: config.Directory, binary: binary, identity: identity}
	defer state.stop()
	var cached Manifest
	if data, err := os.ReadFile(filepath.Join(config.Directory, "last-good.json")); err == nil {
		_ = json.Unmarshal(data, &cached)
	}
	commandState, err := readCommand(config.Directory)
	if err != nil {
		return fmt.Errorf("read command journal: %w", err)
	}
	commandState.recover(version)
	if commandState.ID != "" {
		if err = persistCommand(config.Directory, commandState); err != nil {
			return err
		}
	}
	poll := 15 * time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		if !identity.Enrolled {
			if config.EnrollmentToken == "" {
				return errors.New("new host requires an enrollment token")
			}
			if err = client.Enroll(ctx, config.EnrollmentToken, version); err != nil {
				log.Warnf("Host enrollment pending: %s", diagnostic(err, identity, nil))
				if !pause(ctx, poll) {
					return nil
				}
				continue
			}
			identity.Enrolled = true
			state.identity = identity
			if err = saveJSON(identityPath, identity); err != nil {
				return err
			}
		}
		manifest, fetchErr := client.Desired(ctx)
		status := map[string]any{"agent_version": version, "nodes": []map[string]any{}, "certificates": []map[string]any{}}
		if fetchErr != nil {
			var denied *HTTPError
			if errors.As(fetchErr, &denied) && (denied.Status == 401 || denied.Status == 403 || denied.Status == 404) {
				state.stop()
				state.applied = nil
				state.prepared = nil
				cached = Manifest{}
				_ = os.Remove(filepath.Join(config.Directory, "last-good.json"))
			} else if state.applied == nil && cached.Revision != "" {
				_ = state.apply(ctx, cached, false)
			}
			status["state"] = "disconnected"
			status["error"] = diagnostic(fetchErr, identity, &cached)
		} else {
			if manifest.PollInterval >= 1 && manifest.PollInterval <= 300 {
				poll = time.Duration(manifest.PollInterval) * time.Second
			}
			status["revision"] = manifest.Revision
			execute := commandState.needsExecution(manifest.Command)
			force := execute && (manifest.Command.Action == "retry" || manifest.Command.Action == "restart")
			if execute {
				if manifest.Command.Action == "update" {
					if root := os.Getenv("PROXYNODE_INSTALL_ROOT"); root != "" {
						if err = os.Remove(filepath.Join(root, "upgrade-result.json")); err != nil && !os.IsNotExist(err) {
							return fmt.Errorf("clear previous update result: %w", err)
						}
					}
				}
				commandState = commandResult{ID: manifest.Command.ID, Action: manifest.Command.Action, Version: manifest.Command.Version, State: "running"}
				if err = persistCommand(config.Directory, commandState); err != nil {
					return err
				}
			}
			applyErr := state.apply(ctx, manifest, force)
			if applyErr != nil {
				status["state"] = "error"
				status["error"] = diagnostic(applyErr, identity, &manifest)
			} else {
				status["state"] = "running"
				if len(state.prepared.Nodes) == 0 {
					status["state"] = "idle"
				}
			}
			if applyErr == nil {
				if err = WriteUpdateHealth(version); err != nil {
					log.WithError(err).Warn("Write supervisor health failed")
				}
			}
			if command := manifest.Command; command != nil {
				if execute {
					commandErr := applyErr
					if commandErr == nil && command.Action == "update" {
						status["command"] = commandState.report()
						if reportErr := client.Report(ctx, status); reportErr != nil && ctx.Err() == nil {
							log.Warnf("Host report pending: %s", diagnostic(reportErr, identity, &manifest))
						}
						commandErr = state.upgrade(ctx, command)
						if errors.Is(commandErr, ErrRestartSupervisor) {
							return commandErr
						}
					}
					commandState.State = "succeeded"
					if commandErr != nil {
						commandState.State = "failed"
					}
					commandState.Error = diagnostic(commandErr, identity, &manifest)
				} else {
					commandState.recover(version)
				}
				if err = persistCommand(config.Directory, commandState); err != nil {
					return err
				}
				status["command"] = commandState.report()
			}
		}
		if state.applied != nil {
			status["applied_revision"] = state.applied.Revision
			status["gateway"] = map[string]any{"state": "disabled", "error": ""}
			if state.applied.Enabled {
				for _, n := range state.applied.Nodes {
					if n.Enabled && n.Gateway != nil && n.Gateway.Enabled {
						status["gateway"] = map[string]any{"state": "running", "error": ""}
						break
					}
				}
			}
			// Reports may only include resources still assigned in the desired manifest.
			if fetchErr == nil && state.prepared != nil {
				allowedNodes := map[int]bool{}
				allowedCertificates := map[int]bool{}
				for _, n := range manifest.Nodes {
					allowedNodes[n.ID] = true
					if n.Certificate != nil {
						allowedCertificates[n.Certificate.ID] = true
					}
				}
				nodes := []map[string]any{}
				certs := []map[string]any{}
				for _, n := range state.prepared.Nodes {
					if allowedNodes[n["node_id"].(int)] {
						nodes = append(nodes, n)
					}
				}
				for _, c := range state.prepared.Certificates {
					if allowedCertificates[c["id"].(int)] {
						certs = append(certs, c)
					}
				}
				status["nodes"] = nodes
				status["certificates"] = certs
			}
		}
		if reportErr := client.Report(ctx, status); reportErr != nil && ctx.Err() == nil {
			log.Warnf("Host report pending: %s", diagnostic(reportErr, identity, &manifest))
		}
		if !pause(ctx, poll) {
			return nil
		}
	}
}

func pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
