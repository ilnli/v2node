package managed

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releaseBase = "https://github.com/ilnli/v2node/releases"
const maxArchiveBytes = 256 << 20
const maxExpandedBytes = 512 << 20

var releaseTag = regexp.MustCompile(`^proxynode-v[0-9]+\.[0-9]+\.[0-9]+$`)
var bundleFiles = map[string]bool{
	"proxynode": true, "proxynode-manager": true, "install.sh": true, "supervise.sh": true,
	"config.example.json": false, "geoip.dat": false, "geosite.dat": false,
	"LICENSE": false, "NOTICE.md": false, "README.md": false, "GEODATA_SOURCE.txt": false,
}

// Update is a verified but inactive release. Validate its worker before calling Activate.
type Update struct {
	Directory, Binary, Version string
	root                       string
}

// StageUpdate only downloads tagged releases from the project's own repository.
func StageUpdate(ctx context.Context, installRoot, tag string) (*Update, error) {
	supervisorPID, _ := strconv.Atoi(os.Getenv("PROXYNODE_SUPERVISOR_PID"))
	if supervisorPID != os.Getppid() || os.Getenv("PROXYNODE_INSTALL_ROOT") != installRoot {
		return nil, errors.New("remote updates require the installed supervisor; run the managed installer first")
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.Scheme != "https" || (req.URL.Hostname() != "github.com" && !strings.HasSuffix(req.URL.Hostname(), ".githubusercontent.com")) {
			return errors.New("unexpected release redirect")
		}
		return nil
	}}
	return stageUpdate(ctx, installRoot, tag, client, releaseBase, runtime.GOARCH)
}

func stageUpdate(ctx context.Context, root, tag string, client *http.Client, base, arch string) (_ *Update, resultErr error) {
	if !releaseTag.MatchString(tag) {
		return nil, errors.New("update requires a proxynode-vX.Y.Z release tag")
	}
	if arch != "amd64" && arch != "arm64" {
		return nil, errors.New("unsupported update architecture")
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("install the managed supervisor before enabling remote updates")
	}
	if _, err := currentRelease(root); err != nil {
		return nil, errors.New("install the managed supervisor before enabling remote updates")
	}
	dir, err := os.MkdirTemp(filepath.Join(root, "releases"), tag+"-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	archiveName := "proxynode-linux-" + arch + ".tar.gz"
	url := strings.TrimRight(base, "/") + "/download/" + tag + "/"
	sums, err := fetchBounded(ctx, client, url+"SHA256SUMS", 64<<10)
	if err != nil {
		return nil, err
	}
	expected := ""
	scan := bufio.NewScanner(strings.NewReader(string(sums)))
	for scan.Scan() {
		parts := strings.Fields(scan.Text())
		if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == archiveName {
			decoded, e := hex.DecodeString(parts[0])
			if e != nil || len(decoded) != sha256.Size || expected != "" {
				return nil, errors.New("invalid release checksum manifest")
			}
			expected = strings.ToLower(parts[0])
		}
	}
	if scan.Err() != nil || expected == "" {
		return nil, errors.New("release archive checksum is missing")
	}
	archive, err := os.CreateTemp(dir, ".download-")
	if err != nil {
		return nil, err
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	response, err := releaseRequest(ctx, client, url+archiveName)
	if err != nil {
		archive.Close()
		return nil, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, maxArchiveBytes+1))
	response.Body.Close()
	closeErr := archive.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if size > maxArchiveBytes {
		return nil, errors.New("release archive exceeds size limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return nil, errors.New("release archive SHA-256 mismatch")
	}
	if err := extractBundle(archivePath, dir); err != nil {
		return nil, err
	}
	return &Update{Directory: dir, Binary: filepath.Join(dir, "proxynode"), Version: tag, root: root}, nil
}

func releaseRequest(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("release download failed: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("release download returned HTTP %d", response.StatusCode)
	}
	return response, nil
}
func fetchBounded(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	response, err := releaseRequest(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	value, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > limit {
		return nil, errors.New("release metadata exceeds size limit")
	}
	return value, nil
}
func extractBundle(path, dir string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return errors.New("invalid release archive")
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	seen := map[string]bool{}
	var size int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid release archive")
		}
		executable, allowed := bundleFiles[header.Name]
		if !allowed || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maxArchiveBytes {
			return errors.New("release archive contains an invalid file")
		}
		size += header.Size
		if size > maxExpandedBytes {
			return errors.New("expanded release exceeds size limit")
		}
		seen[header.Name] = true
		mode := os.FileMode(0644)
		if executable {
			mode = 0755
		}
		output, err := os.OpenFile(filepath.Join(dir, header.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, reader)
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, name := range []string{"proxynode", "proxynode-manager", "install.sh", "supervise.sh", "config.example.json", "geoip.dat", "geosite.dat"} {
		if !seen[name] {
			return fmt.Errorf("release archive is missing %s", name)
		}
	}
	return nil
}
func currentRelease(root string) (string, error) {
	current, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if err != nil {
		return "", err
	}
	releases, err := filepath.EvalSymlinks(filepath.Join(root, "releases"))
	if err != nil {
		return "", err
	}
	if filepath.Dir(current) != releases {
		return "", errors.New("invalid installed release path")
	}
	binary, err := os.Stat(filepath.Join(current, "proxynode"))
	if err != nil {
		return "", err
	}
	if !binary.Mode().IsRegular() {
		return "", errors.New("invalid installed binary")
	}
	return current, nil
}

// Activate switches the entire bundle together. The supervisor owns startup confirmation and rollback.
func (u *Update) Activate() error {
	previous, err := currentRelease(u.root)
	if err != nil {
		return err
	}
	if previous == u.Directory {
		return errors.New("release is already active")
	}
	marker := filepath.Join(u.root, "pending-upgrade.json")
	if _, err := os.Lstat(marker); err == nil {
		return errors.New("another upgrade is awaiting startup confirmation")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(u.root, "upgrade-result.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.CreateTemp(u.root, ".pending-upgrade-")
	if err != nil {
		return err
	}
	pendingPath := temporary.Name()
	temporary.Close()
	defer os.Remove(pendingPath)
	if err := saveJSON(pendingPath, map[string]any{"previous": previous, "candidate": u.Directory, "version": u.Version, "started_at": time.Now().Unix()}); err != nil {
		return err
	}
	// Link creates the complete marker exclusively; a crash can never leave a partial JSON marker.
	if err := os.Link(pendingPath, marker); err != nil {
		return fmt.Errorf("another upgrade is awaiting startup confirmation: %w", err)
	}
	pointer := filepath.Join(u.root, ".current-next")
	if err := os.Symlink(u.Directory, pointer); err != nil {
		os.Remove(marker)
		return err
	}
	defer os.Remove(pointer)
	if err := os.Rename(pointer, filepath.Join(u.root, "current")); err != nil {
		os.Remove(marker)
		return err
	}
	if directory, err := os.Open(u.root); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

// Discard removes a staged candidate only while it is inactive.
func (u *Update) Discard() {
	current, err := currentRelease(u.root)
	if err == nil && current != u.Directory {
		_ = os.RemoveAll(u.Directory)
	}
}

// WriteUpdateHealth is called only after the new agent has applied a valid desired configuration.
func WriteUpdateHealth(version string) error {
	path := os.Getenv("PROXYNODE_HEALTH_FILE")
	if path == "" {
		return nil
	}
	return saveJSON(path, map[string]any{"pid": os.Getpid(), "version": version})
}
