package node

import (
	"context"
	"errors"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/wyx2685/v2node/conf"
	"github.com/wyx2685/v2node/core"
)

type Node struct {
	controllers []*Controller
	NodeInfos   []*panel.NodeInfo
}

func New(nodes []conf.NodeConfig) (*Node, error) {
	n := &Node{
		controllers: make([]*Controller, len(nodes)),
		NodeInfos:   make([]*panel.NodeInfo, len(nodes)),
	}
	for i, node := range nodes {
		p, err := panel.New(&node)
		if err != nil {
			return nil, err
		}
		var info *panel.NodeInfo
		if node.ConfigFile != "" {
			var data []byte
			data, err = os.ReadFile(node.ConfigFile)
			if err == nil {
				info, err = panel.ParseNodeInfo(data, node.NodeID, node.APIHost)
			}
		} else {
			info, err = p.GetNodeInfo(context.Background())
		}
		if err != nil {
			return nil, err
		}
		n.controllers[i] = NewController(p, &node, info)
		n.NodeInfos[i] = info
	}
	return n, nil
}

func (n *Node) Start(nodes []conf.NodeConfig, core *core.V2Core) error {
	for i, node := range nodes {
		err := n.controllers[i].Start(core)
		if err != nil {
			return fmt.Errorf("start node controller [%s-%d] error: %s",
				node.APIHost,
				node.NodeID,
				err)
		}
	}
	return nil
}

func (n *Node) Close() error {
	var err error
	for _, c := range n.controllers {
		if closeErr := c.Close(); closeErr != nil {
			log.Errorf("close controller failed: %v", closeErr)
			err = errors.Join(err, closeErr)
		}
	}
	n.controllers = nil
	return err
}
