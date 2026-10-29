package managed

import (
	"fmt"
	"os"
	"path/filepath"
)

// Retain only the running and pending configurations, including their private PEMs.
// Traffic/audit queues and ACME account state are not configuration generations.
func pruneConfigurations(directory string, manifests ...*Manifest) {
	generations := map[string]bool{}
	certificates := map[string]map[string]bool{}
	for _, manifest := range manifests {
		if manifest == nil {
			continue
		}
		generations[safeID(manifest.Revision)] = true
		for _, n := range manifest.Nodes {
			if n.Certificate == nil {
				continue
			}
			id := fmt.Sprint(n.Certificate.ID)
			if certificates[id] == nil {
				certificates[id] = map[string]bool{}
			}
			certificates[id][safeID(n.Certificate.Revision)] = true
		}
	}
	pruneDirectories(filepath.Join(directory, "generations"), generations)
	root := filepath.Join(directory, "certificates")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() {
			pruneDirectories(filepath.Join(root, entry.Name()), certificates[entry.Name()])
		}
	}
}
func pruneDirectories(directory string, keep map[string]bool) {
	entries, _ := os.ReadDir(directory)
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) == 32 && !keep[entry.Name()] {
			_ = os.RemoveAll(filepath.Join(directory, entry.Name()))
		}
	}
}
