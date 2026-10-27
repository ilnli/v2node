package core

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/common/audit"
)

func (v *V2Core) startAudit(infos []*panel.NodeInfo) error {
	if !v.Config.Audit.Enabled {
		return nil
	}
	if len(infos) != len(v.Config.NodeConfigs) {
		return fmt.Errorf("audit node configuration mismatch")
	}
	type target struct {
		key      string
		id       int
		protocol string
	}
	targets := make(map[string]target, len(infos))
	senders := make(map[string]audit.Sender, len(infos))
	for i, c := range v.Config.NodeConfigs {
		endpoint, err := audit.NewEndpoint(c.APIHost, c.NodeID, c.Key)
		if err != nil {
			return fmt.Errorf("audit node %d: %w", c.NodeID, err)
		}
		if _, ok := senders[endpoint.Target]; ok {
			return fmt.Errorf("duplicate audit panel/node %d", c.NodeID)
		}
		senders[endpoint.Target] = endpoint.Send
		targets[infos[i].Tag] = target{key: endpoint.Target, id: c.NodeID, protocol: infos[i].Type}
	}
	s, err := audit.NewService(audit.Options{
		Directory:   v.Config.Audit.Directory,
		MemoryBytes: v.Config.Audit.MemoryMiB << 20,
		DiskBytes:   v.Config.Audit.DiskMiB << 20,
	}, senders, func(format string, args ...any) { log.Warnf(format, args...) })
	if err != nil {
		return fmt.Errorf("start audit: %w", err)
	}
	v.audit = s
	// Installed once before node listeners/users are added. The maps stay immutable.
	v.dispatcher.Audit = func(tag, email string, r audit.Record) {
		node, ok := targets[tag]
		if !ok {
			return
		}
		v.users.mapLock.RLock()
		uid := v.users.uidMap[email]
		v.users.mapLock.RUnlock()
		if uid <= 0 {
			return
		}
		r.UserID = uid
		r.NodeID = node.id
		r.Protocol = node.protocol
		s.Record(node.key, r)
	}
	return nil
}
