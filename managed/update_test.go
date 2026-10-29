package managed

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func updateArchive(t *testing.T, unsafe string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"proxynode", "proxynode-manager", "install.sh", "supervise.sh", "config.example.json", "geoip.dat", "geosite.dat"} {
		if name == unsafe {
			continue
		}
		data := []byte("synthetic " + name)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if unsafe != "" {
		if err := tw.WriteHeader(&tar.Header{Name: unsafe, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func installedUpdateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := filepath.Join(root, "releases", "old")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "proxynode"), []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "identity.json"), []byte("preserve identity"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func releaseServer(t *testing.T, archive []byte, corrupt bool) *httptest.Server {
	t.Helper()
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if corrupt {
		digest = strings.Repeat("0", 64)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/proxynode-v1.2.3/SHA256SUMS":
			fmt.Fprintf(w, "%s  proxynode-linux-amd64.tar.gz\n", digest)
		case "/download/proxynode-v1.2.3/proxynode-linux-amd64.tar.gz":
			_, _ = w.Write(archive)
		default:
			t.Errorf("unexpected release URL %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
func TestStageUpdateAndAtomicBundleActivation(t *testing.T) {
	root := installedUpdateRoot(t)
	server := releaseServer(t, updateArchive(t, ""), false)
	update, err := stageUpdate(context.Background(), root, "proxynode-v1.2.3", server.Client(), server.URL, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "current", "proxynode"))
	if string(before) != "old binary" {
		t.Fatal("stage activated before health")
	}
	if update.Binary != filepath.Join(update.Directory, "proxynode") {
		t.Fatal("wrong candidate binary")
	}
	if err := update.Activate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"proxynode", "geoip.dat", "geosite.dat", "install.sh", "supervise.sh"} {
		got, err := os.ReadFile(filepath.Join(root, "current", name))
		if err != nil || string(got) != "synthetic "+name {
			t.Fatalf("bundle %s = %q, %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "pending-upgrade.json")); err != nil {
		t.Fatal("missing startup rollback marker", err)
	}
	identity, _ := os.ReadFile(filepath.Join(root, "identity.json"))
	if string(identity) != "preserve identity" {
		t.Fatal("identity changed")
	}
	if err := update.Activate(); err == nil {
		t.Fatal("overlapping activation accepted")
	}
}
func TestUpdateRejectsChecksumArchiveAndTagBeforeActivation(t *testing.T) {
	for _, tc := range []struct {
		name, unsafe string
		corrupt      bool
	}{{"checksum", "", true}, {"symlink", "proxynode", false}, {"traversal", "../outside", false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := installedUpdateRoot(t)
			server := releaseServer(t, updateArchive(t, tc.unsafe), tc.corrupt)
			if _, err := stageUpdate(context.Background(), root, "proxynode-v1.2.3", server.Client(), server.URL, "amd64"); err == nil {
				t.Fatal("unsafe release accepted")
			}
			before, _ := os.ReadFile(filepath.Join(root, "current", "proxynode"))
			if string(before) != "old binary" {
				t.Fatal("working install changed")
			}
		})
	}
	root := installedUpdateRoot(t)
	if _, err := stageUpdate(context.Background(), root, "../../arbitrary", http.DefaultClient, "http://127.0.0.1:1", "amd64"); err == nil {
		t.Fatal("invalid release tag accepted")
	}
}
