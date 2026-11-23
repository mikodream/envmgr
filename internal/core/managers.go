package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ManagerInfo describes one of the version managers EnvMgr works with.
type ManagerInfo struct {
	ID    string
	Name  string
	Desc  string
	Kinds []Kind
	Site  string
	// Install is how EnvMgr can fetch it, nil when it has no one-click
	// install (the tool is a package of another ecosystem, or a system
	// package).
	Install *ManagerInstall
	// Docs is a one-line hint shown when Install is nil.
	Hint string
}

// ManagerInstall says where to get a manager from its official release.
type ManagerInstall struct {
	// URL is the archive to unpack, resolved per OS.
	URL string
	// Dir is the folder under <data>/managers.
	Dir string
	// Launcher is the program or script that appears once unpacked.
	Launcher string
}

// ManagerState is the result of looking for a manager on this machine.
type ManagerState struct {
	ManagerInfo
	Installed bool
	Managed   bool // installed by EnvMgr itself
	// Path is what EnvMgr would run.
	Path string
	// Home is the manager's root, e.g. ~/.nvm, whenever it is known.
	Home string
}

// Managers is every manager EnvMgr knows, in display order.
func Managers() []ManagerInfo {
	zipFor := func(os string, name string) string {
		return "https://github.com/Schniz/fnm/releases/latest/download/" + name
	}
	list := []ManagerInfo{
		{
			ID: "fnm", Name: "fnm", Desc: "快速的 Node 版本管理器，Rust 编写，单文件",
			Kinds: []Kind{Node}, Site: "https://github.com/Schniz/fnm",
			Install: &ManagerInstall{URL: zipFor(runtime.GOOS, fnmAsset()), Dir: "fnm", Launcher: fnmLauncher()},
		},
		{
			ID: "nvm", Name: "nvm", Desc: "最常用的 Node 版本管理器（nvm-sh）",
			Kinds: []Kind{Node}, Site: "https://github.com/nvm-sh/nvm",
			Install: &ManagerInstall{URL: "https://codeload.github.com/nvm-sh/nvm/tar.gz/refs/heads/master", Dir: "nvm", Launcher: "nvm.sh"},
		}, {
			ID: "nvm-windows", Name: "nvm-windows", Desc: "Windows 上的 nvm（nvm-windows）",
			Kinds: []Kind{Node}, Site: "https://github.com/coreybutler/nvm-windows",
			Install: &ManagerInstall{URL: "https://github.com/coreybutler/nvm-windows/releases/latest/download/nvm-noinstall.zip", Dir: "nvm-windows", Launcher: "nvm.exe"},
		},
		{
			ID: "n", Name: "n", Desc: "极简的 Node 版本管理器（npm 包）",
			Kinds: []Kind{Node}, Site: "https://github.com/tj/n",
			Hint: "n 是一个 npm 包：npm install -g n",
		},
		{
			ID: "pyenv", Name: "pyenv", Desc: "Python 版本管理器",
			Kinds: []Kind{Python}, Site: "https://github.com/pyenv/pyenv",
			Install: &ManagerInstall{URL: "https://codeload.github.com/pyenv/pyenv/tar.gz/refs/heads/master", Dir: "pyenv", Launcher: "bin/pyenv"},
		},
		{
			ID: "jenv", Name: "jenv", Desc: "JDK 版本管理器",
			Kinds: []Kind{Java}, Site: "https://github.com/jenv/jenv",
			Install: &ManagerInstall{URL: "https://codeload.github.com/jenv/jenv/tar.gz/refs/heads/master", Dir: "jenv", Launcher: "bin/jenv"},
		},
		{
			ID: "rbenv", Name: "rbenv", Desc: "Ruby 版本管理器",
			Kinds: []Kind{Ruby}, Site: "https://github.com/rbenv/rbenv",
			Install: &ManagerInstall{URL: "https://codeload.github.com/rbenv/rbenv/tar.gz/refs/heads/master", Dir: "rbenv", Launcher: "bin/rbenv"},
		},
		{
			ID: "volta", Name: "Volta", Desc: "Node 工具链管理器，按项目固定版本",
			Kinds: []Kind{Node}, Site: "https://volta.sh",
			Hint: "官方安装：curl https://get.volta.sh | bash",
		},
		{
			ID: "asdf", Name: "asdf", Desc: "多语言版本管理器，通用 .tool-versions",
			Kinds: []Kind{Node, Python, Java, Go, Rust, Ruby}, Site: "https://asdf-vm.com",
			Hint: "官方安装：见 asdf 文档（git clone + shim）",
		},
		{
			ID: "mise", Name: "mise", Desc: "asdf 的现代替代品，快速、兼容 .tool-versions",
			Kinds: []Kind{Node, Python, Java, Go, Rust, Ruby}, Site: "https://mise.jdx.dev",
			Hint: "官方安装：curl https://mise.run | sh",
		},
	}
	// Each nvm belongs to one family of systems: nvm-sh is a set of shell
	// functions, nvm-windows is a program. Offering the wrong one would
	// install something that cannot run.
	for i := range list {
		if runtime.GOOS == "windows" && list[i].ID == "nvm" {
			list[i].Install = nil
			list[i].Hint = "Windows 请用 nvm-windows（下面可一键安装）"
		}
		if runtime.GOOS != "windows" && list[i].ID == "nvm-windows" {
			list[i].Install = nil
			list[i].Hint = "nvm-windows 只用于 Windows"
		}
	}
	return list
}

// fnmAsset is fnm's release archive for this OS.
func fnmAsset() string {
	switch runtime.GOOS {
	case "windows":
		return "fnm-windows.zip"
	case "darwin":
		return "fnm-macos.zip"
	default:
		return "fnm-linux.zip"
	}
}

// fnmLauncher is the program inside fnm's archive.
func fnmLauncher() string {
	return ExecName("fnm")
}

// managerDataDir is where EnvMgr installs managers.
func managerDataDir(cfg Config) (string, error) {
	root, err := DataDirRoot(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "managers"), nil
}

// DetectManagers looks for every manager on this machine.
func DetectManagers(cfg Config) []ManagerState {
	base, _ := managerDataDir(cfg)
	states := make([]ManagerState, 0, len(Managers()))
	for _, m := range Managers() {
		st := ManagerState{ManagerInfo: m}
		if m.Install != nil {
			launcher := filepath.Join(base, m.Install.Dir, filepath.FromSlash(m.Install.Launcher))
			if fileExists(launcher) {
				st.Installed = true
				st.Managed = true
				st.Path = launcher
				st.Home = filepath.Join(base, m.Install.Dir)
			}
		}
		if !st.Installed {
			if p, home := findManager(m.ID); p != "" {
				st.Installed = true
				st.Path = p
				st.Home = home
			}
		}
		states = append(states, st)
	}
	return states
}

// findManager looks for a manager the user installed the usual way.
func findManager(id string) (path, home string) {
	switch id {
	case "fnm":
		if p, err := exec.LookPath(ExecName("fnm")); err == nil {
			return p, fnmDataDir()
		}
	case "nvm":
		home = homeJoin(os.Getenv("NVM_DIR"), ".nvm")
		if fileExists(filepath.Join(home, "nvm.sh")) {
			return filepath.Join(home, "nvm.sh"), home
		}
	case "nvm-windows":
		home = homeJoin(os.Getenv("NVM_HOME"), filepath.Join("AppData", "Roaming", "nvm"))
		if p := filepath.Join(home, "nvm.exe"); fileExists(p) {
			return p, home
		}
	case "n", "volta", "asdf", "mise":
		name := id
		if id == "n" {
			name = "n"
		}
		if p, err := exec.LookPath(ExecName(name)); err == nil {
			return p, filepath.Dir(filepath.Dir(p))
		}
	case "pyenv":
		home = homeJoin(os.Getenv("PYENV_ROOT"), ".pyenv")
		if p := filepath.Join(home, "bin", ExecName("pyenv")); fileExists(p) {
			return p, home
		}
		if p, err := exec.LookPath("pyenv"); err == nil {
			return p, home
		}
	case "jenv":
		home = homeJoin(os.Getenv("JENV_ROOT"), ".jenv")
		if p := filepath.Join(home, "bin", ExecName("jenv")); fileExists(p) {
			return p, home
		}
		if p, err := exec.LookPath(ExecName("jenv")); err == nil {
			return p, home
		}
	case "rbenv":
		home = homeJoin(os.Getenv("RBENV_ROOT"), ".rbenv")
		if p := filepath.Join(home, "bin", ExecName("rbenv")); fileExists(p) {
			return p, home
		}
		if p, err := exec.LookPath(ExecName("rbenv")); err == nil {
			return p, home
		}
	}
	return "", ""
}

// homeJoin resolves an environment value or a directory under ~.
func homeJoin(value, fallback string) string {
	if value != "" {
		return value
	}
	home := homeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, filepath.FromSlash(fallback))
}

// fileExists reports whether p is an existing file.
func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// InstallManager fetches a manager from its official release into EnvMgr's
// own directory, so it can be used without touching a global PATH.
func InstallManager(ctx context.Context, cfg Config, id string, progress Progress) (ManagerState, error) {
	var target ManagerInfo
	for _, m := range Managers() {
		if m.ID == id {
			target = m
			break
		}
	}
	if target.ID == "" {
		return ManagerState{}, fmt.Errorf("未知的版本管理器：%s", id)
	}
	if target.Install == nil {
		return ManagerState{}, fmt.Errorf("%s 没有一键安装：%s", target.Name, target.Hint)
	}
	base, err := managerDataDir(cfg)
	if err != nil {
		return ManagerState{}, err
	}
	dir := filepath.Join(base, target.Install.Dir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return ManagerState{}, err
	}
	cache, err := CacheDir(cfg)
	if err != nil {
		return ManagerState{}, err
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return ManagerState{}, err
	}
	archive := filepath.Join(cache, target.Install.Dir+"-"+filepath.Base(target.Install.URL))
	unlock, err := lockInstallPath(ctx, archive)
	if err != nil {
		return ManagerState{}, err
	}
	defer unlock()
	reportInstallStage(ctx, "下载")
	if err := Download(ctx, target.Install.URL, archive, progress); err != nil {
		return ManagerState{}, err
	}
	workDir, err := os.MkdirTemp(base, ".manager-*")
	if err != nil {
		return ManagerState{}, err
	}
	defer os.RemoveAll(workDir) // Unique temporary directory created above.
	reportInstallStage(ctx, "解包")
	if err := ExtractContext(ctx, archive, workDir); err != nil {
		return ManagerState{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagerState{}, err
	}
	reportInstallStage(ctx, "安装")
	if err := Flatten(workDir); err != nil {
		return ManagerState{}, err
	}
	switch runtime.GOOS {
	case "windows":
	default:
		// GitHub archives keep the executable bit, but a zip through
		// another path may not: make the launcher runnable.
		launcher := filepath.Join(workDir, filepath.FromSlash(target.Install.Launcher))
		if fileExists(launcher) {
			_ = os.Chmod(launcher, 0o755)
		}
	}
	if !fileExists(filepath.Join(workDir, filepath.FromSlash(target.Install.Launcher))) {
		return ManagerState{}, fmt.Errorf("%s 安装包缺少启动文件", target.Name)
	}
	if err := ctx.Err(); err != nil {
		return ManagerState{}, err
	}
	if err := publishInstallDirectory(workDir, dir); err != nil {
		return ManagerState{}, err
	}
	reportInstallStage(ctx, "验证")
	for _, st := range DetectManagers(cfg) {
		if st.ID == id && st.Installed {
			return st, nil
		}
	}
	return ManagerState{}, fmt.Errorf("%s 已解压到 %s，但未找到启动文件", target.Name, dir)
}

// ManagerEnv is the environment a manager needs when EnvMgr runs it itself.
func ManagerEnv(st ManagerState) []string {
	env := os.Environ()
	if st.Home == "" {
		return env
	}
	switch st.ID {
	case "nvm":
		env = append(env, "NVM_DIR="+st.Home)
	case "nvm-windows":
		env = append(env, "NVM_HOME="+st.Home)
	case "pyenv":
		env = append(env, "PYENV_ROOT="+st.Home)
	case "jenv":
		env = append(env, "JENV_ROOT="+st.Home)
	case "rbenv":
		env = append(env, "RBENV_ROOT="+st.Home)
	case "fnm":
		env = append(env, fmt.Sprintf("FNM_DIR=%s", strings.TrimRight(st.Home, `/\`)))
	}
	return env
}
