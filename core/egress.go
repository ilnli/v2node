package core

import (
	"encoding/json"
	"fmt"

	panel "github.com/wyx2685/v2node/api/v2board"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

func blockedIPs(info *panel.NodeInfo) coreConf.StringList {
	var matches coreConf.StringList
	for _, rule := range info.Common.Routes {
		if rule.Action == "block_ip" {
			matches = append(matches, rule.Match...)
		}
	}
	return matches
}

// Each guarded direct outbound belongs to one inbound. Never mutate a shared
// outbound, or an unrestricted node could inherit another node's destination bans.
func guardDirectOutbound(outbound *coreConf.OutboundDetourConfig, info *panel.NodeInfo, ips coreConf.StringList) error {
	if len(ips) == 0 || (outbound.Protocol != "freedom" && outbound.Protocol != "direct") {
		return nil
	}
	settings := &coreConf.FreedomConfig{}
	if outbound.Settings != nil {
		if err := json.Unmarshal(*outbound.Settings, settings); err != nil {
			return fmt.Errorf("node %s direct outbound: %w", info.Tag, err)
		}
	}
	settings.FinalRules = append([]*coreConf.FreedomFinalRuleConfig{{Action: "block", IP: &ips}}, settings.FinalRules...)
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	raw := json.RawMessage(data)
	outbound.Settings = &raw
	outbound.Tag = "deny-ip/" + info.Tag + "/" + outbound.Tag
	return nil
}
