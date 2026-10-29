package conf

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

const DefaultNodeRetryCount = 1
const DefaultNodeTimeout = 15

type AuditConfig struct {
	Enabled   bool   `mapstructure:"Enabled" json:"Enabled"`
	Directory string `mapstructure:"Directory" json:"Directory"`
	MemoryMiB int64  `mapstructure:"MemoryMiB" json:"MemoryMiB"`
	DiskMiB   int64  `mapstructure:"DiskMiB" json:"DiskMiB"`
}

type Conf struct {
	Agent       *AgentConfig    `mapstructure:"Agent" json:"Agent"`
	ReadyFile   string          `mapstructure:"ReadyFile" json:"ReadyFile"`
	Gateways    []GatewayConfig `mapstructure:"Gateways" json:"Gateways"`
	Audit       AuditConfig     `mapstructure:"Audit" json:"Audit"`
	LogConfig   LogConfig       `mapstructure:"Log" json:"Log"`
	NodeConfigs []NodeConfig    `mapstructure:"Nodes" json:"Nodes"`
	PprofPort   int             `mapstructure:"PprofPort" json:"PprofPort"`
}

type LogConfig struct {
	Level  string `mapstructure:"Level" json:"Level"`
	Output string `mapstructure:"Output" json:"Output"`
	Access string `mapstructure:"Access" json:"Access"`
}

type NodeConfig struct {
	TrafficDirectory string `mapstructure:"TrafficDirectory" json:"TrafficDirectory,omitempty"`
	ConfigFile       string `mapstructure:"ConfigFile" json:"ConfigFile"`
	APIHost          string `mapstructure:"ApiHost" json:"ApiHost"`
	NodeID           int    `mapstructure:"NodeID" json:"NodeID"`
	Key              string `mapstructure:"ApiKey" json:"ApiKey"`
	Timeout          int    `mapstructure:"Timeout" json:"Timeout"`
	RetryCount       *int   `mapstructure:"RetryCount" json:"RetryCount"`
}

type AgentConfig struct {
	APIHost         string `mapstructure:"ApiHost" json:"ApiHost"`
	HostID          int    `mapstructure:"HostID" json:"HostID"`
	EnrollmentToken string `mapstructure:"EnrollmentToken" json:"EnrollmentToken"`
	Directory       string `mapstructure:"Directory" json:"Directory"`
}

type GatewayConfig struct {
	Port        int    `mapstructure:"Port" json:"Port"`
	Host        string `mapstructure:"Host" json:"Host"`
	Path        string `mapstructure:"Path" json:"Path"`
	BackendPort int    `mapstructure:"BackendPort" json:"BackendPort"`
	Transport   string `mapstructure:"Transport" json:"Transport"`
	Certificate string `mapstructure:"Certificate" json:"Certificate"`
}

func New() *Conf {
	return &Conf{
		Audit: AuditConfig{Directory: "/var/lib/proxynode/audit", MemoryMiB: 64, DiskMiB: 1024},
		LogConfig: LogConfig{
			Level:  "info",
			Output: "",
			Access: "none",
		},
	}
}

func (p *Conf) LoadFromPath(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open config file error: %s", err)
	}
	defer f.Close()
	v := viper.New()
	v.SetConfigFile(filePath)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config file error: %s", err)
	}
	if err := v.Unmarshal(p); err != nil {
		return fmt.Errorf("unmarshal config error: %s", err)
	}
	if p.Audit.Enabled && (p.Audit.Directory == "" || p.Audit.MemoryMiB < 1 || p.Audit.MemoryMiB > 128 || p.Audit.DiskMiB < 1 || p.Audit.DiskMiB > 1024) {
		return fmt.Errorf("audit requires a directory, MemoryMiB 1..128 and DiskMiB 1..1024")
	}
	for i := range p.NodeConfigs {
		if p.NodeConfigs[i].RetryCount == nil {
			p.NodeConfigs[i].RetryCount = intPtr(DefaultNodeRetryCount)
		}
	}
	return nil
}

func intPtr(v int) *int {
	return &v
}
