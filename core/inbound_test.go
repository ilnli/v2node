package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	panel "github.com/wyx2685/v2node/api/v2board"
	"github.com/xtls/xray-core/app/proxyman"
	vinbound "github.com/xtls/xray-core/proxy/vless/inbound"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/tls"
)

func TestRealityDestinationAndUnsetMinimumVersion(t *testing.T) {
	for _, tc := range []struct{ dest, want string }{
		{"", "camouflage.example:8443"}, {"127.0.0.1", "127.0.0.1:8443"},
		{"127.0.0.1:9443", "127.0.0.1:9443"}, {"2001:db8::1", "[2001:db8::1]:8443"},
		{"[2001:db8::1]:9443", "[2001:db8::1]:9443"},
	} {
		t.Run(tc.dest, func(t *testing.T) {
			node := &panel.NodeInfo{Type: "vless", Security: panel.Reality, Common: &panel.CommonNode{ListenIP: "127.0.0.1", ServerPort: 443, Network: "tcp", TlsSettings: panel.TlsSettings{
				Dest: tc.dest, ServerName: "camouflage.example", ServerPort: "8443", PrivateKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE", ShortId: "0123",
			}}}
			built, err := buildInbound(node, "reality-target")
			if err != nil {
				t.Fatal(err)
			}
			r, _ := built.ReceiverSettings.GetInstance()
			s, _ := r.(*proxyman.ReceiverConfig).StreamSettings.SecuritySettings[0].GetInstance()
			got := s.(*reality.Config)
			if got.Dest != tc.want || len(got.MinClientVer) != 0 {
				t.Fatalf("dest=%s min=%v", got.Dest, got.MinClientVer)
			}
		})
	}
}

func TestVLESSEncryptionMultiplePrivateKeysReachCore(t *testing.T) {
	keys := []string{"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI"}
	node := &panel.NodeInfo{Type: "vless", Common: &panel.CommonNode{ListenIP: "127.0.0.1", ServerPort: 443, Network: "tcp", Encryption: "mlkem768x25519plus", EncryptionSettings: panel.EncSettings{Mode: "native", Ticket: "0s", PrivateKey: "ignored", PrivateKeys: keys}}}
	built, err := buildInbound(node, "encryption-keys")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := built.ProxySettings.GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.(*vinbound.Config).Decryption; got != keys[0]+"."+keys[1] {
		t.Fatalf("decryption keys not preserved: %q", got)
	}
}

func TestPanelRealitySecuritySettingsReachInbound(t *testing.T) {
	for _, name := range []string{"snake", "camel"} {
		t.Run(name, func(t *testing.T) {
			settings := map[string]any{
				"server_name": "camouflage.example", "server_names": []string{"first.example", "second.example"},
				"dest": "127.0.0.1:8443", "server_port": "443",
				"private_key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE", "short_ids": []string{"0123", "4567"},
			}
			minKey, seedKey, upKey, downKey := "min_client_ver", "mldsa65_seed", "limit_fallback_upload", "limit_fallback_download"
			if name == "camel" {
				minKey, seedKey, upKey, downKey = "minClientVer", "mldsa65Seed", "limitFallbackUpload", "limitFallbackDownload"
			}
			settings[minKey] = "26.3.27"
			settings[seedKey] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAI"
			settings[upKey] = map[string]uint64{"afterBytes": 4096, "bytesPerSec": 1024, "burstBytesPerSec": 2048}
			settings[downKey] = map[string]uint64{"afterBytes": 8192, "bytesPerSec": 2048, "burstBytesPerSec": 4096}
			raw, _ := json.Marshal(settings)
			var tlsSettings panel.TlsSettings
			if err := json.Unmarshal(raw, &tlsSettings); err != nil {
				t.Fatal(err)
			}
			node := &panel.NodeInfo{Type: "vless", Security: panel.Reality, Common: &panel.CommonNode{
				ListenIP: "127.0.0.1", ServerPort: 443, Network: "tcp", TlsSettings: tlsSettings,
			}}
			built, err := buildInbound(node, "security-test")
			if err != nil {
				t.Fatal(err)
			}
			receiver, _ := built.ReceiverSettings.GetInstance()
			security, _ := receiver.(*proxyman.ReceiverConfig).StreamSettings.SecuritySettings[0].GetInstance()
			got := security.(*reality.Config)
			if got.Dest != "127.0.0.1:8443" || !reflect.DeepEqual(got.MinClientVer, []byte{26, 3, 27}) ||
				len(got.Mldsa65Seed) != 32 || got.Mldsa65Seed[31] != 2 ||
				got.LimitFallbackUpload.GetBytesPerSec() != 1024 || got.LimitFallbackDownload.GetAfterBytes() != 8192 ||
				!reflect.DeepEqual(got.ServerNames, []string{"first.example", "second.example"}) || len(got.ShortIds) != 2 {
				t.Fatalf("security settings not preserved: dest=%s min=%v seedLen=%d upload=%v download=%v names=%v ids=%d", got.Dest, got.MinClientVer, len(got.Mldsa65Seed), got.LimitFallbackUpload, got.LimitFallbackDownload, got.ServerNames, len(got.ShortIds))
			}
		})
	}
}

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
