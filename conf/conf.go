package conf

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

const DefaultNodeRetryCount = 1
const DefaultNodeTimeout = 15

type AuditConfig struct {
	Enabled   bool   `mapstructure:"Enabled"`
	Directory string `mapstructure:"Directory"`
	MemoryMiB int64  `mapstructure:"MemoryMiB"`
	DiskMiB   int64  `mapstructure:"DiskMiB"`
}

type Conf struct {
	Audit       AuditConfig  `mapstructure:"Audit"`
	LogConfig   LogConfig    `mapstructure:"Log"`
	NodeConfigs []NodeConfig `mapstructure:"Nodes"`
	PprofPort   int          `mapstructure:"PprofPort"`
}

type LogConfig struct {
	Level  string `mapstructure:"Level"`
	Output string `mapstructure:"Output"`
	Access string `mapstructure:"Access"`
}

type NodeConfig struct {
	APIHost    string `mapstructure:"ApiHost"`
	NodeID     int    `mapstructure:"NodeID"`
	Key        string `mapstructure:"ApiKey"`
	Timeout    int    `mapstructure:"Timeout"`
	RetryCount *int   `mapstructure:"RetryCount"`
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
