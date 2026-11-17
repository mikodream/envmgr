// Package core holds everything EnvMgr does without a window: which SDKs
// exist, which versions are installed, which version a project asks for, the
// environment a project runs with, and how versions are downloaded.
package core

import (
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Kind is a family of SDK, one row in the toolchains page.
type Kind string

const (
	Node   Kind = "node"
	Python Kind = "python"
	Java   Kind = "java"
	Go     Kind = "go"
	Rust   Kind = "rust"
	Ruby   Kind = "ruby"
)

// Kinds is every SDK EnvMgr knows, in the order the app shows them.
var Kinds = []Kind{Node, Python, Java, Go, Rust, Ruby}

// KindInfo describes how one SDK is laid out on disk and in a project.
type KindInfo struct {
	Kind Kind
	// Name is what the UI calls it.
	Name string
	// Desc is one line under the name.
	Desc string
	// HomeVar is the variable that must point at the SDK root, if any.
	HomeVar string
	// Exe is the program that proves the SDK works.
	Exe string
	// Markers are the project files other tools use to pin this SDK.
	Markers []string
	// Color is the SDK's accent, used by the UI.
	Color string
	// Remote reports whether EnvMgr can download versions of it.
	Remote bool
}

var kindInfos = map[Kind]KindInfo{
	Node: {
		Kind: Node, Name: "Node.js", Desc: "JavaScript 运行时 · npm / yarn / pnpm",
		Exe: "node", Markers: []string{".nvmrc", ".node-version"}, Color: "#5FA04E", Remote: true,
	},
	Python: {
		Kind: Python, Name: "Python", Desc: "解释器 · pip / venv",
		Exe: "python3", Markers: []string{".python-version"}, Color: "#3776AB", Remote: true,
	},
	Java: {
		Kind: Java, Name: "Java", Desc: "JDK · javac / maven / gradle",
		HomeVar: "JAVA_HOME", Exe: "java", Markers: []string{".java-version", ".sdkmanrc"}, Color: "#E76F00", Remote: true,
	},
	Go: {
		Kind: Go, Name: "Go", Desc: "工具链 · go build / go test",
		HomeVar: "GOROOT", Exe: "go", Markers: []string{".go-version"}, Color: "#00ADD8", Remote: true,
	},
	Rust: {
		Kind: Rust, Name: "Rust", Desc: "工具链 · cargo / rustc",
		Exe: "cargo", Markers: []string{"rust-toolchain.toml", "rust-toolchain"}, Color: "#CE422B", Remote: true,
	},
	Ruby: {
		Kind: Ruby, Name: "Ruby", Desc: "解释器 · gem / bundler",
		Exe: "ruby", Markers: []string{".ruby-version"}, Color: "#CC342D", Remote: false,
	},
}

// Info returns the description of a kind. Unknown kinds get a bare one.
func Info(k Kind) KindInfo {
	if info, ok := kindInfos[k]; ok {
		return info
	}
	return KindInfo{Kind: k, Name: string(k), Exe: string(k)}
}

// ParseKind maps a user-typed name to a kind.
func ParseKind(s string) (Kind, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "node", "nodejs", "node.js", "npm", "js", "javascript":
		return Node, true
	case "python", "python3", "py":
		return Python, true
	case "java", "jdk", "jre", "jvm":
		return Java, true
	case "go", "golang":
		return Go, true
	case "rust", "cargo", "rustc":
		return Rust, true
	case "ruby", "gem":
		return Ruby, true
	}
	return "", false
}

// BinDirs returns the directories under an SDK root that hold its programs,
// most specific first, for the given OS.
func (info KindInfo) BinDirs(goos string) []string {
	switch info.Kind {
	case Node:
		if goos == "windows" {
			return []string{"", "bin"}
		}
		return []string{"bin"}
	case Python:
		if goos == "windows" {
			return []string{"", "bin", "Scripts"}
		}
		return []string{"bin"}
	case Ruby:
		if goos == "windows" {
			return []string{"bin"}
		}
		return []string{"bin"}
	default:
		return []string{"bin"}
	}
}

// Version is a dotted version such as 20.11.1, 3.12.4 or 17.0.2.
type Version struct {
	Raw   string
	Parts []int
	Pre   string // prerelease suffix, "" for a release
}

// ParseVersion reads "v20.11.1", "20.11", "3.12.4-rc1" and the like.
func ParseVersion(s string) Version {
	v := Version{Raw: strings.TrimSpace(s)}
	v.Raw = strings.TrimPrefix(v.Raw, "v")
	v.Raw = strings.TrimPrefix(v.Raw, "V")
	s = v.Raw
	// Cut a prerelease or build suffix: 1.79.0-beta.1, 20.0.0-rc.1
	for i, r := range s {
		if r == '-' || r == '+' {
			v.Pre = s[i+1:]
			s = s[:i]
			break
		}
	}
	for _, part := range strings.Split(s, ".") {
		// Tolerate a trailing wildcard: 20.x counts as 20.
		if part == "" || part == "x" || part == "X" || part == "*" {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		v.Parts = append(v.Parts, n)
	}
	return v
}

// String formats a version the way users write it.
func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	parts := make([]string, len(v.Parts))
	for i, p := range v.Parts {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}

// Major is the first number, or 0.
func (v Version) Major() int {
	if len(v.Parts) == 0 {
		return 0
	}
	return v.Parts[0]
}

// Minor is the second number, or 0.
func (v Version) Minor() int {
	if len(v.Parts) < 2 {
		return 0
	}
	return v.Parts[1]
}

// Patch is the third number, or 0.
func (v Version) Patch() int {
	if len(v.Parts) < 3 {
		return 0
	}
	return v.Parts[2]
}

// Compare orders two versions: negative when v sorts before o.
func (v Version) Compare(o Version) int {
	n := len(v.Parts)
	if len(o.Parts) > n {
		n = len(o.Parts)
	}
	for i := 0; i < n; i++ {
		var a, b int
		if i < len(v.Parts) {
			a = v.Parts[i]
		}
		if i < len(o.Parts) {
			b = o.Parts[i]
		}
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	// A release sorts after its prereleases.
	switch {
	case v.Pre == "" && o.Pre != "":
		return 1
	case v.Pre != "" && o.Pre == "":
		return -1
	case v.Pre != o.Pre:
		if v.Pre < o.Pre {
			return -1
		}
		return 1
	}
	return 0
}

// Matches reports whether expr selects v. It understands the forms the
// ecosystem's own files use: "20.11.1", "20", "20.x", "lts", "^20.11",
// "~20.11", ">=18 <21", "18 || 20", and "*" or "".
func Matches(v Version, expr string) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" || expr == "latest" {
		return true
	}
	if strings.EqualFold(expr, "lts") || strings.HasPrefix(strings.ToLower(expr), "lts/") {
		// LTS status is a fact of the release feed, not of the number; the
		// caller filters those. Anything released counts here.
		return true
	}
	for _, alt := range strings.Split(expr, "||") {
		if matchesAll(v, strings.TrimSpace(alt)) {
			return true
		}
	}
	return false
}

func matchesAll(v Version, expr string) bool {
	tokens := strings.Fields(strings.ReplaceAll(expr, ",", " "))
	if len(tokens) == 0 {
		return true
	}
	for _, tok := range tokens {
		if !matchesOne(v, tok) {
			return false
		}
	}
	return true
}

func matchesOne(v Version, tok string) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" || tok == "*" || tok == "x" {
		return true
	}
	switch {
	case strings.HasPrefix(tok, ">="):
		return v.Compare(ParseVersion(tok[2:])) >= 0
	case strings.HasPrefix(tok, "<="):
		return v.Compare(ParseVersion(tok[2:])) <= 0
	case strings.HasPrefix(tok, ">"):
		return v.Compare(ParseVersion(tok[1:])) > 0
	case strings.HasPrefix(tok, "<"):
		return v.Compare(ParseVersion(tok[1:])) < 0
	case strings.HasPrefix(tok, "="):
		return v.Compare(ParseVersion(tok[1:])) == 0
	case strings.HasPrefix(tok, "^"):
		base := ParseVersion(tok[1:])
		if v.Compare(base) < 0 {
			return false
		}
		return v.Major() == base.Major()
	case strings.HasPrefix(tok, "~"):
		base := ParseVersion(tok[1:])
		if v.Compare(base) < 0 {
			return false
		}
		return v.Major() == base.Major() && v.Minor() == base.Minor()
	}
	// A bare version is a prefix: "20" and "20.11" select any release under
	// them, which is what .nvmrc and .python-version mean.
	base := ParseVersion(tok)
	if len(base.Parts) == 0 {
		return true
	}
	if len(v.Parts) < len(base.Parts) {
		return false
	}
	for i, p := range base.Parts {
		if v.Parts[i] != p {
			return false
		}
	}
	return true
}

// SortVersionsDesc orders versions newest first.
func SortVersionsDesc(vs []Version) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) > 0 })
}

// PathListSep is the separator between entries of PATH on this OS.
func PathListSep() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

// ExecName adds the platform's executable suffix.
func ExecName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// JoinDir joins an SDK root with one of its bin dirs, tolerating "".
func JoinDir(root, rel string) string {
	if rel == "" {
		return root
	}
	return filepath.Join(root, rel)
}
