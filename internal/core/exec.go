package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LookPath finds a program the way the overlay environment would: the
// project's own SDK directories come before the machine's PATH.
//
// This matters because exec.Command resolves the program against the parent
// process's PATH. When the whole point is to run a different version of a
// tool, that lookup finds the wrong binary — the pinned one is only in the
// child's environment. So callers resolve here and pass the absolute path.
func LookPath(e Env, name string) (string, error) {
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return name, nil
	}
	dirs := make([]string, 0, len(e.Path)+8)
	dirs = append(dirs, e.Path...)
	dirs = append(dirs, filepath.SplitList(os.Getenv(pathVarName()))...)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, ext := range extsFor(name) {
			candidate := filepath.Join(dir, name+ext)
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate, nil
			}
		}
	}
	return exec.LookPath(name)
}

// extsFor lists the suffixes to try when resolving name. On Windows a bare
// "node" has to become node.exe, and "npm" npm.cmd, or the lookup finds
// nothing.
func extsFor(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{""}
	}
	if filepath.Ext(name) != "" {
		return []string{""}
	}
	out := []string{""}
	for _, ext := range filepath.SplitList(os.Getenv("PATHEXT")) {
		if ext != "" {
			out = append(out, strings.ToLower(ext))
		}
	}
	if len(out) == 1 {
		out = append(out, ".exe", ".cmd", ".bat")
	}
	return out
}
