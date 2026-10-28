package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/transport/internet/tls"
)

func TestPanelTLSALPNReachesInbound(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		want           []string
	}{
		{"xhttp_h3", `{"alpn":["h3"]}`, []string{"h3"}},
		{"default", `{}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var settings panel.TlsSettings
			if err := json.Unmarshal([]byte(tc.settings), &settings); err != nil {
				t.Fatal(err)
			}
			// Config construction reads PEM bytes; the integration suite performs real TLS.
			cert := filepath.Join(t.TempDir(), "cert.pem")
			if err := os.WriteFile(cert, []byte("test certificate bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			node := &panel.NodeInfo{Type: "vless", Security: panel.Tls, Common: &panel.CommonNode{
				ListenIP: "127.0.0.1", ServerPort: 443, Network: "xhttp", NetworkSettings: json.RawMessage(`{"path":"/test"}`),
				TlsSettings: settings, CertInfo: &panel.CertInfo{CertMode: "file", CertFile: cert, KeyFile: cert},
			}}
			config, err := buildInbound(node, "test")
			if err != nil {
				t.Fatal(err)
			}
			receiver, err := config.ReceiverSettings.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			security, err := receiver.(*proxyman.ReceiverConfig).StreamSettings.SecuritySettings[0].GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			if got := security.(*tls.Config).NextProtocol; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ALPN = %v, want %v", got, tc.want)
			}
		})
	}
}
