package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditLimitsAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"Audit":{"Enabled":true}}`, true},
		{`{"Audit":{"Enabled":true,"MemoryMiB":256}}`, false},
		{`{"Audit":{"Enabled":true,"DiskMiB":1025}}`, false},
		{`{"Audit":{"Enabled":true,"MemoryMiB":-1}}`, false},
		{`{}`, true},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		c := New()
		err := c.LoadFromPath(path)
		if (err == nil) != tc.valid {
			t.Fatalf("config %s: %v", tc.body, err)
		}
	}
}
