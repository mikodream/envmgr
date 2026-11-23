package core

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Project is a directory whose SDK versions EnvMgr pins.
type Project struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Path  string          `json:"path"`
	Pins  map[Kind]string `json:"pins,omitempty"`
	Note  string          `json:"note,omitempty"`
	Tags  []string        `json:"tags,omitempty"`
	Added time.Time       `json:"added"`
	// LastOpened orders the list and drives "recent".
	LastOpened time.Time `json:"lastOpened"`
}

// ConfigFileName is EnvMgr's own file inside a project.
const ConfigFileName = ".envmgr.json"

// NewProject builds a project for a directory. Pins already written into the
// directory's .envmgr.json are picked up, so a project EnvMgr has never seen
// still resolves the way its own config says.
func NewProject(path string) Project {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	now := time.Now()
	p := Project{
		ID:         projectID(path),
		Name:       filepath.Base(path),
		Path:       path,
		Added:      now,
		LastOpened: now,
	}
	if pins, err := ReadProjectPins(path); err == nil && len(pins) > 0 {
		p.Pins = pins
	}
	return p
}

// ReadProjectPins reads the pins a project carries in its own .envmgr.json.
func ReadProjectPins(dir string) (map[Kind]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		return nil, err
	}
	var file *struct {
		Pins map[string]string `json:"pins"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, err
	}
	if file == nil {
		return nil, errors.New("项目配置必须是 JSON 对象")
	}
	out := make(map[Kind]string, len(file.Pins))
	for name, expr := range file.Pins {
		if kind, ok := ParseKind(name); ok && expr != "" {
			out[kind] = expr
		}
	}
	return out, nil
}

// projectID is a stable short id from the path.
func projectID(path string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(path))))
	return hex.EncodeToString(h[:8])
}

// Pin returns the pinned expression for a kind.
func (p Project) Pin(kind Kind) string { return p.Pins[kind] }

// declarationFiles are the project files that mean "this directory pins SDK
// versions". NearestProjectDir climbs until it finds one, so a command run in
// src/ still sees the project root's .nvmrc, the way nvm and pyenv do.
var declarationFiles = []string{
	ConfigFileName,
	".nvmrc",
	".node-version",
	".python-version",
	".java-version",
	".sdkmanrc",
	".go-version",
	"go.mod",
	"rust-toolchain",
	"rust-toolchain.toml",
	".ruby-version",
	".tool-versions",
}

// HasVersionDeclaration reports whether dir itself carries a version
// declaration, without parsing it. A malformed .envmgr.json still counts:
// Resolve reports the error instead of quietly walking past the project.
func HasVersionDeclaration(dir string) bool {
	for _, name := range declarationFiles {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// NearestProjectDir walks up from dir to the closest directory that declares
// versions. A monorepo's subdirectories inherit its environment, and a nested
// package with its own .nvmrc wins over the parent.
func NearestProjectDir(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	for {
		if HasVersionDeclaration(abs) {
			return abs, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", false
		}
		abs = parent
	}
}

// Marker is a version request found in one of a project's files.
type Marker struct {
	Kind Kind
	Expr string
	File string
}

// DetectMarkers reads the files other tools already use to pin versions, so
// a project that works with nvm, pyenv or jenv works here without changes.
func DetectMarkers(dir string) []Marker {
	var out []Marker
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", false
		}
		return string(b), true
	}

	if body, ok := read(".nvmrc"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Node, v, ".nvmrc"})
		}
	}
	if body, ok := read(".node-version"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Node, v, ".node-version"})
		}
	}
	if body, ok := read(".python-version"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Python, v, ".python-version"})
		}
	}
	if body, ok := read(".java-version"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Java, v, ".java-version"})
		}
	}
	if body, ok := read(".ruby-version"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Ruby, strings.TrimPrefix(v, "ruby-"), ".ruby-version"})
		}
	}
	if body, ok := read(".go-version"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Go, v, ".go-version"})
		}
	}
	if body, ok := read("rust-toolchain"); ok {
		if v := firstVersionLine(body); v != "" {
			out = append(out, Marker{Rust, v, "rust-toolchain"})
		}
	}
	if body, ok := read("rust-toolchain.toml"); ok {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "channel") {
				if _, v, ok := strings.Cut(line, "="); ok {
					v = strings.Trim(strings.TrimSpace(v), `"'`)
					if v != "" {
						out = append(out, Marker{Rust, v, "rust-toolchain.toml"})
					}
				}
				break
			}
		}
	}
	if body, ok := read("go.mod"); ok {
		if v := goModVersion(body); v != "" {
			out = append(out, Marker{Go, v, "go.mod"})
		}
	}
	if body, ok := read(".sdkmanrc"); ok {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			key, val, ok := strings.Cut(line, "=")
			if !ok || strings.HasPrefix(line, "#") {
				continue
			}
			switch strings.TrimSpace(key) {
			case "java":
				out = append(out, Marker{Java, versionPrefix(strings.TrimSpace(val)), ".sdkmanrc"})
			case "ruby":
				out = append(out, Marker{Ruby, versionPrefix(strings.TrimSpace(val)), ".sdkmanrc"})
			}
		}
	}
	if body, ok := read(".tool-versions"); ok {
		for _, line := range strings.Split(body, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			var kind Kind
			switch fields[0] {
			case "nodejs", "node":
				kind = Node
			case "python":
				kind = Python
			case "java", "temurin", "corretto":
				kind = Java
			case "golang", "go":
				kind = Go
			case "rust":
				kind = Rust
			case "ruby":
				kind = Ruby
			default:
				continue
			}
			out = append(out, Marker{kind, fields[1], ".tool-versions"})
		}
	}
	if body, ok := read(ConfigFileName); ok {
		var cfg struct {
			Pins map[string]string `json:"pins"`
		}
		if json.Unmarshal([]byte(body), &cfg) == nil {
			for k, v := range cfg.Pins {
				if kind, ok := ParseKind(k); ok && v != "" {
					out = append(out, Marker{kind, v, ConfigFileName})
				}
			}
		}
	}
	return out
}

// firstVersionLine returns the first line of a marker file that holds a
// version, skipping comments and blanks.
func firstVersionLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		return versionPrefix(line)
	}
	return ""
}

// versionPrefix trims a leading v and any trailing comment.
func versionPrefix(line string) string {
	if i := strings.IndexAny(line, " #\t"); i > 0 {
		line = line[:i]
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "v"))
}

// goModVersion reads the toolchain or go line of a go.mod.
func goModVersion(body string) string {
	var goLine, toolchain string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "toolchain ") {
			toolchain = strings.TrimSpace(strings.TrimPrefix(line, "toolchain "))
		}
		if strings.HasPrefix(line, "go ") && goLine == "" {
			goLine = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	if toolchain != "" {
		return strings.TrimPrefix(toolchain, "go")
	}
	return goLine
}

// Resolution is what one project asks of one SDK, and what satisfies it.
type Resolution struct {
	// Err reports an unreadable or invalid project configuration.
	Err    error
	Kind   Kind
	Expr   string
	Source string
	// Installed is the version that satisfies the request, nil when none is
	// installed.
	Installed *Installed
	// Missing reports that the request names a version that is not here.
	Missing bool
}

// Resolve works out the environment of a project: explicit pins first, then
// the marker files of the tools it already uses.
func Resolve(p Project, installed []Installed, followMarkers bool) []Resolution {
	return ResolveWithDefaults(p, installed, followMarkers, nil)
}

// ResolveWithDefaults is Resolve plus the global defaults, which fill in every
// kind the directory says nothing about. A default whose version is no longer
// installed is skipped rather than reported missing: a global default is meant
// to be silently there, not to block every directory on the machine.
func ResolveWithDefaults(p Project, installed []Installed, followMarkers bool, defaults map[string]string) []Resolution {
	pins, err := ReadProjectPins(p.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return []Resolution{{Source: ConfigFileName, Err: fmt.Errorf("读取 %s: %w", ConfigFileName, err)}}
	}
	wanted := map[Kind]Resolution{}

	if followMarkers {
		for _, m := range DetectMarkers(p.Path) {
			if m.File == ConfigFileName {
				continue
			}
			if _, ok := wanted[m.Kind]; ok {
				continue // the first marker for a kind wins
			}
			wanted[m.Kind] = Resolution{Kind: m.Kind, Expr: m.Expr, Source: m.File}
		}
	}
	// The project file is the only source of explicit pins.
	for kind, expr := range pins {
		if expr == "" {
			continue
		}
		wanted[kind] = Resolution{Kind: kind, Expr: expr, Source: "pin"}
	}
	applyDefaults(wanted, installed, defaults)
	return resolveWanted(wanted, installed)
}

// DefaultResolutions is the environment outside any project: only what the
// user set as global defaults.
func DefaultResolutions(installed []Installed, defaults map[string]string) []Resolution {
	wanted := map[Kind]Resolution{}
	applyDefaults(wanted, installed, defaults)
	return resolveWanted(wanted, installed)
}

// applyDefaults adds every kind the directory did not ask for, as long as the
// default still matches an installed version.
func applyDefaults(wanted map[Kind]Resolution, installed []Installed, defaults map[string]string) {
	for _, kind := range Kinds {
		if _, ok := wanted[kind]; ok {
			continue
		}
		expr := strings.TrimSpace(defaults[string(kind)])
		if expr == "" {
			continue
		}
		if _, found := Best(installed, kind, expr); !found {
			continue
		}
		wanted[kind] = Resolution{Kind: kind, Expr: expr, Source: "默认"}
	}
}

func resolveWanted(wanted map[Kind]Resolution, installed []Installed) []Resolution {
	out := make([]Resolution, 0, len(wanted))
	for _, kind := range Kinds {
		res, ok := wanted[kind]
		if !ok {
			continue
		}
		if inst, found := Best(installed, kind, res.Expr); found {
			res.Installed = &inst
		} else {
			res.Missing = true
		}
		out = append(out, res)
	}
	return out
}

// WriteProjectConfig replaces pins atomically, preserving other JSON fields.
// For concurrent partial edits use UpdateProjectPins instead.
func WriteProjectConfig(dir string, pins map[Kind]string) error {
	return withFileLock(filepath.Join(dir, ConfigFileName), func() error {
		return writeProjectConfigLocked(dir, pins)
	})
}

// UpdateProjectPins atomically merges multiple SDK edits. An empty expression
// deletes that SDK's pin; unrelated pins and configuration fields survive.
func UpdateProjectPins(dir string, updates map[Kind]string) error {
	return withFileLock(filepath.Join(dir, ConfigFileName), func() error {
		pins, err := ReadProjectPins(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if pins == nil {
			pins = map[Kind]string{}
		}
		for kind, expr := range updates {
			if _, ok := ParseKind(string(kind)); !ok {
				return fmt.Errorf("未知 SDK: %s", kind)
			}
			if expr == "" {
				delete(pins, kind)
			} else {
				pins[kind] = expr
			}
		}
		return writeProjectConfigLocked(dir, pins)
	})
}

func SetProjectPin(dir string, kind Kind, version string) error {
	return UpdateProjectPins(dir, map[Kind]string{kind: version})
}

func writeProjectConfigLocked(dir string, pins map[Kind]string) error {
	path := filepath.Join(dir, ConfigFileName)
	file := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &file); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if file == nil {
		return errors.New("项目配置必须是 JSON 对象")
	}
	plain := map[string]string{}
	for k, v := range pins {
		if v != "" {
			plain[string(k)] = v
		}
	}
	file["pins"], err = json.Marshal(plain)
	if err != nil {
		return err
	}
	b, err = json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(b, '\n'), 0o644)
}

// ProjectHealth summarizes a project for the list: how many SDKs resolve.
type ProjectHealth struct {
	Resolved int
	Missing  int
	Total    int
}

// Health computes the summary above.
func Health(res []Resolution) ProjectHealth {
	h := ProjectHealth{Total: len(res)}
	for _, r := range res {
		if r.Missing || r.Err != nil {
			h.Missing++
		} else {
			h.Resolved++
		}
	}
	return h
}
