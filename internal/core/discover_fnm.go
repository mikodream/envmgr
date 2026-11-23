package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// fnmDataDirs includes the configured location and known platform defaults.
// macOS installations can use either Application Support or the XDG layout.
func fnmDataDirs() []string {
	var dirs []string
	if dir := os.Getenv("FNM_DIR"); dir != "" {
		dirs = append(dirs, join(dir))
	}
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, join(appSupport(), "fnm"))
	}
	dirs = append(dirs, join(xdgData(), "fnm"))
	seen := map[string]bool{}
	var out []string
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

// fnmNodeInstalls unwraps node-versions/vX/installation to the actual SDK
// root, so PATH points to installation/bin (or installation on Windows).
func fnmNodeInstalls() []Installed {
	var out []Installed
	for _, dataDir := range fnmDataDirs() {
		versions := filepath.Join(dataDir, "node-versions")
		entries, err := os.ReadDir(versions)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			versionDir := filepath.Join(versions, entry.Name())
			root := filepath.Join(versionDir, "installation")
			// Retain support for versions installed directly in vX.
			if !dirExists(root) {
				root = versionDir
			}
			version := ProbeVersion(Node, root)
			if version == "" {
				version = VersionFromName(entry.Name())
			}
			if inst, ok := rootInstall(Node, "fnm", root, version); ok {
				out = append(out, inst)
			}
		}
	}
	return dedupeInstalls(out)
}
