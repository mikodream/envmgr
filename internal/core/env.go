package core

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Env is the overlay a project adds to a process: directories to put in
// front of PATH, variables to set, and variables to drop.
//
// EnvMgr never writes a global variable. Everything here reaches a process
// only when EnvMgr starts it, or when the user's shell asks EnvMgr for it.
type Env struct {
	Path  []string
	Vars  map[string]string
	Unset []string
	// Pairs is what the overlay resolves to, for the banner the shell hook
	// prints when it enters a project.
	Pairs []Pair
}

// Pair is one SDK an overlay switched to.
type Pair struct {
	Kind    Kind
	Version string
}

// Empty reports whether the overlay changes nothing.
func (e Env) Empty() bool {
	return len(e.Path) == 0 && len(e.Vars) == 0 && len(e.Unset) == 0
}

// BuildEnv turns a project's resolutions into an overlay.
func BuildEnv(res []Resolution) Env {
	env := Env{}
	seen := map[string]bool{}
	for _, r := range res {
		if r.Installed == nil {
			continue
		}
		env.Pairs = append(env.Pairs, Pair{Kind: r.Kind, Version: r.Installed.Version})
		for _, dir := range r.Installed.BinDirs {
			key := normalizePathKey(dir)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			env.Path = append(env.Path, dir)
		}
		for k, v := range r.Installed.Env {
			if env.Vars == nil {
				env.Vars = map[string]string{}
			}
			env.Vars[k] = v
		}
	}
	return env
}

// normalizePathKey compares paths the way the OS does.
func normalizePathKey(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// envKey returns the name environment variables are compared by. Windows
// treats names without regard to case; Unix does not.
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// pathVarName is the PATH variable; Windows also spells it Path.
func pathVarName() string {
	if runtime.GOOS == "windows" {
		return "Path"
	}
	return "PATH"
}

// Apply overlays e on top of an environment list such as os.Environ().
// Directories that e puts first are removed from the old PATH, so a shim
// belonging to another manager cannot win over the version the project
// pinned.
func Apply(base []string, e Env) []string {
	if e.Empty() {
		return base
	}
	vars := map[string]string{}  // envKey -> value
	names := map[string]string{} // envKey -> name as it was written
	var order []string           // envKey order of first appearance
	put := func(name, value string) {
		key := envKey(name)
		if _, ok := vars[key]; !ok {
			order = append(order, key)
		}
		if _, ok := names[key]; !ok {
			names[key] = name
		}
		vars[key] = value
	}
	for _, entry := range base {
		i := strings.IndexByte(entry, '=')
		if i < 0 {
			continue
		}
		put(entry[:i], entry[i+1:])
	}

	for _, k := range e.Unset {
		delete(vars, envKey(k))
	}
	for k, v := range e.Vars {
		put(k, v)
	}

	if len(e.Path) > 0 {
		pathName := pathVarName()
		var existing string
		if v, ok := vars[envKey("PATH")]; ok {
			existing = v
			pathName = names[envKey("PATH")]
		}
		drop := map[string]bool{}
		for _, dir := range e.Path {
			drop[normalizePathKey(dir)] = true
		}
		merged := make([]string, 0, len(e.Path)+8)
		for _, dir := range e.Path {
			if dir != "" {
				merged = append(merged, dir)
			}
		}
		for _, dir := range splitPath(existing) {
			if dir == "" || drop[normalizePathKey(dir)] {
				continue
			}
			merged = append(merged, dir)
		}
		put(pathName, strings.Join(merged, PathListSep()))
	}

	out := make([]string, 0, len(order))
	for _, key := range order {
		value, ok := vars[key]
		if !ok {
			continue // unset
		}
		out = append(out, names[key]+"="+value)
	}
	return out
}

// splitPath splits a PATH value.
func splitPath(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, PathListSep())
}

// Environ is the environment a project's processes run with, starting from
// this process's environment.
func Environ(e Env) []string {
	return Apply(os.Environ(), e)
}

// Lookup returns the value of one overlay variable.
func (e Env) Lookup(key string) (string, bool) {
	v, ok := e.Vars[key]
	return v, ok
}

// Describe renders the overlay for display, one entry per line, with PATH
// shown as the directories it adds in front.
func (e Env) Describe() []string {
	var out []string
	if len(e.Path) > 0 {
		out = append(out, "PATH += "+strings.Join(e.Path, string(PathListSep())))
	}
	keys := make([]string, 0, len(e.Vars))
	for k := range e.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+e.Vars[k])
	}
	if len(e.Unset) > 0 {
		out = append(out, "unset "+strings.Join(e.Unset, " "))
	}
	return out
}
