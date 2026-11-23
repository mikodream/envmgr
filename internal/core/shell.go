package core

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Shell is one of the command interpreters EnvMgr can write code for.
type Shell string

const (
	ShellPowerShell Shell = "pwsh"
	ShellCmd        Shell = "cmd"
	ShellBash       Shell = "bash"
	ShellZsh        Shell = "zsh"
	ShellFish       Shell = "fish"
	ShellSh         Shell = "sh"
)

// Shells is every shell the app offers.
var Shells = []Shell{ShellPowerShell, ShellCmd, ShellBash, ShellZsh, ShellFish, ShellSh}

// ShellName is how the shell is shown and typed.
func (s Shell) ShellName() string {
	switch s {
	case ShellPowerShell:
		return "PowerShell"
	case ShellCmd:
		return "cmd"
	case ShellZsh:
		return "zsh"
	case ShellFish:
		return "fish"
	case ShellSh:
		return "sh"
	}
	return "bash"
}

// ParseShell maps a name to a shell, guessing from $SHELL when empty.
func ParseShell(name string) (Shell, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "pwsh", "powershell", "ps", "ps1":
		return ShellPowerShell, true
	case "cmd", "bat", "cmd.exe":
		return ShellCmd, true
	case "bash":
		return ShellBash, true
	case "zsh":
		return ShellZsh, true
	case "fish":
		return ShellFish, true
	case "sh":
		return ShellSh, true
	case "":
		return CurrentShell(), true
	}
	return "", false
}

// CurrentShell guesses the shell the user runs.
func CurrentShell() Shell {
	switch runtime.GOOS {
	case "windows":
		return ShellPowerShell
	}
	base := filepath.Base(os.Getenv("SHELL"))
	if s, ok := ParseShell(base); ok && base != "" {
		return s
	}
	return ShellBash
}

// Export renders the overlay as commands the shell evaluates.
//
// This is what makes EnvMgr useful without a global change: the shell asks
// EnvMgr for a directory's environment, evaluates the answer, and the
// variables live in that shell only.
//
// An incomplete project exports nothing here: callers that promise a fully
// working environment (run, the terminal button, the activation script) must
// not silently fall back to the system SDK.
func (e Env) Export(sh Shell) string {
	if e.Validate() != nil {
		return ""
	}
	return e.exportResolved(sh)
}

// ExportResolved renders the SDKs that did resolve and ignores requests that
// are still missing. The shell hook uses it so one absent version cannot erase
// the whole project; the caller is responsible for reporting e.Issues.
func (e Env) ExportResolved(sh Shell) string {
	return e.exportResolved(sh)
}

func (e Env) exportResolved(sh Shell) string {
	if e.Empty() {
		return ""
	}
	var b strings.Builder
	// One comment line per resolved SDK. Comments are valid in every shell
	// EnvMgr writes for, so the script still evaluates to the same thing, and
	// a shell that wants to show a banner can pick these lines out.
	for _, p := range e.Pairs {
		fmt.Fprintf(&b, "%s envmgr: %s %s\n", commentPrefix(sh), p.Kind, p.Version)
	}
	switch sh {
	case ShellPowerShell:
		for _, dir := range e.Path {
			fmt.Fprintf(&b, "$env:PATH = %s + [IO.Path]::PathSeparator + $env:PATH\n", psQuote(dir))
		}
		for _, k := range sortedKeys(e.Vars) {
			fmt.Fprintf(&b, "$env:%s = %s\n", k, psQuote(e.Vars[k]))
		}
		for _, k := range e.Unset {
			fmt.Fprintf(&b, "Remove-Item Env:%s -ErrorAction SilentlyContinue\n", k)
		}
	case ShellCmd:
		if len(e.Path) > 0 {
			fmt.Fprintf(&b, "SET \"PATH=%s;%%PATH%%\"\r\n", strings.Join(e.Path, ";"))
		}
		for _, k := range sortedKeys(e.Vars) {
			fmt.Fprintf(&b, "SET \"%s=%s\"\r\n", k, e.Vars[k])
		}
		for _, k := range e.Unset {
			fmt.Fprintf(&b, "SET \"%s=\"\r\n", k)
		}
	case ShellFish:
		for i := len(e.Path) - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "set -gx PATH %s $PATH\n", fishQuote(e.Path[i]))
		}
		for _, k := range sortedKeys(e.Vars) {
			fmt.Fprintf(&b, "set -gx %s %s\n", k, fishQuote(e.Vars[k]))
		}
		for _, k := range e.Unset {
			fmt.Fprintf(&b, "set -e %s\n", k)
		}
	default: // bash, zsh, sh
		// One assignment keeps the directory order intact.
		if len(e.Path) > 0 {
			parts := make([]string, 0, len(e.Path)+1)
			for _, dir := range e.Path {
				parts = append(parts, shQuote(dir))
			}
			parts = append(parts, `"$PATH"`)
			fmt.Fprintf(&b, "export PATH=%s\n", strings.Join(parts, ":"))
		}
		for _, k := range sortedKeys(e.Vars) {
			fmt.Fprintf(&b, "export %s=%s\n", k, shQuote(e.Vars[k]))
		}
		for _, k := range e.Unset {
			fmt.Fprintf(&b, "unset %s\n", k)
		}
	}
	return b.String()
}

// sortedKeys lists the variables in a stable order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// psQuote quotes a string for PowerShell.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// shQuote quotes a string for POSIX shells.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishQuote quotes a string for fish.
func fishQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// HookScript is the opt-in snippet a shell runs on start so that cd-ing into
// a project changes its SDKs by itself.
//
// EnvMgr never writes this into a shell's startup file on its own: the app
// shows the snippet and the file it belongs in, and the user decides.
// HookScript is the shell integration with EnvMgr's own path filled in.
func HookScript(sh Shell) string {
	return strings.ReplaceAll(hookScriptTemplate(sh), "@ENVMGR@", hookExe())
}

// hookScriptTemplate is the shell integration. It re-applies the overlay on
// every directory change, and puts the environment back the way it was when
// leaving a project, so the global setup stays authoritative outside projects.
func hookScriptTemplate(sh Shell) string {
	switch sh {
	case ShellPowerShell:
		return `# >>> envmgr >>>
# EnvMgr 钩子：进入带版本声明的目录时应用覆盖层，离开时还原。
# 卸载：envmgr hook --uninstall；或删掉本段。
if (-not $env:ENVMGR_BASE_PATH) {
    $env:ENVMGR_BASE_PATH = $env:PATH
    $Global:ENVMGR_ORIGINAL = @{}
}
function global:__envmgr_hook {
    $dir = (Get-Location).Path
    if ($env:ENVMGR_ACTIVE -eq $dir) { return }
    $env:ENVMGR_ACTIVE = $dir
    # 先回到基线：PATH 复原，上次改过的变量复原
    $env:PATH = $env:ENVMGR_BASE_PATH
    foreach ($k in @($Global:ENVMGR_ORIGINAL.Keys)) {
        $v = $Global:ENVMGR_ORIGINAL[$k]
        if ($null -eq $v) { Remove-Item "Env:$k" -ErrorAction SilentlyContinue }
        else { Set-Item "Env:$k" $v }
    }
    $Global:ENVMGR_ORIGINAL = @{}
    $out = & '@ENVMGR@' env --shell pwsh --quiet
    if (-not $out) { return }
    # 记下即将被覆盖的变量原值
    foreach ($line in $out) {
        if ($line -match '^\$env:([A-Za-z_][A-Za-z0-9_]*)\s*=') {
            $n = $Matches[1]
            if ($n -ne 'PATH' -and -not $Global:ENVMGR_ORIGINAL.ContainsKey($n)) {
                $Global:ENVMGR_ORIGINAL[$n] = [Environment]::GetEnvironmentVariable($n)
            }
        }
    }
    Invoke-Expression ($out -join "` + "`" + `n")
    __envmgr_banner $out
}
# 进入项目时打印彩色提示：每种 SDK 用它自己的颜色。
function global:__envmgr_banner($lines) {
    if ($env:ENVMGR_NO_BANNER) { return }
    $pairs = @()
    foreach ($line in $lines) {
        if ($line -match '^#\s+envmgr:\s+(\S+)\s+(\S+)$') {
            $pairs += ,@($Matches[1], $Matches[2])
        }
    }
    if ($pairs.Count -eq 0) { return }
    $colors = @{ node = 'Green'; python = 'Cyan'; java = 'Yellow'; go = 'Blue'; rust = 'Red'; ruby = 'Magenta' }
    Write-Host ''
    Write-Host '  ◆ EnvMgr' -NoNewline -ForegroundColor DarkCyan
    Write-Host ('  ' + (Split-Path (Get-Location).Path -Leaf)) -NoNewline -ForegroundColor DarkGray
    Write-Host ''
    Write-Host '    ' -NoNewline
    for ($i = 0; $i -lt $pairs.Count; $i++) {
        if ($i -gt 0) { Write-Host ' · ' -NoNewline -ForegroundColor DarkGray }
        $c = $colors[$pairs[$i][0]]
        if (-not $c) { $c = 'Gray' }
        Write-Host ($pairs[$i][0] + ' ') -NoNewline -ForegroundColor DarkGray
        Write-Host $pairs[$i][1] -NoNewline -ForegroundColor $c
    }
    Write-Host ''
}
__envmgr_hook
$Global:__envmgr_prompt = $function:prompt
function global:prompt { __envmgr_hook; & $Global:__envmgr_prompt }
# <<< envmgr <<<
`
	case ShellCmd:
		return `@echo off
REM >>> envmgr >>>
REM cmd 没有可靠的目录钩子，请在项目里用 envmgr run 或 envmgr env 进入环境。
REM <<< envmgr >>>
`
	case ShellFish:
		return `# >>> envmgr >>>
# EnvMgr 钩子：进入带版本声明的目录时应用覆盖层，离开时还原。
if not set -q ENVMGR_BASE_PATH
    set -gx ENVMGR_BASE_PATH $PATH
end
function __envmgr_hook --on-variable PWD
    if test "$ENVMGR_ACTIVE" = "$PWD"
        return
    end
    set -gx ENVMGR_ACTIVE $PWD
    set -gx PATH $ENVMGR_BASE_PATH
    for name in $ENVMGR_TOUCHED
        set -e $name
    end
    set -g ENVMGR_TOUCHED
    set -l out ('@ENVMGR@' env --shell fish --quiet)
    if test -z "$out"
        return
    end
    for line in $out
        set -l parts (string match -r '^set -gx ([A-Za-z_][A-Za-z0-9_]*) ' -- $line)
        if test (count $parts) -ge 2
            set -g ENVMGR_TOUCHED $ENVMGR_TOUCHED $parts[2]
        end
    end
    eval $out
    __envmgr_banner $out
end
# 进入项目时打印彩色提示：每种 SDK 用它自己的颜色。
function __envmgr_banner
    if set -q ENVMGR_NO_BANNER
        return
    end
    set -l items (string match -r '^#\s+envmgr:\s+.+$' -- $argv | string replace -r '^#\s+envmgr:\s+' '')
    if test (count $items) -eq 0
        return
    end
    echo ''
    set_color cyan
    printf '  ◆ EnvMgr'
    set_color brblack
    printf '  %s\n    ' (basename $PWD)
    set -l first 1
    for item in $items
        if test -z "$first"
            set_color brblack
            printf ' · '
        end
        set first ''
        set -l kind (string split -m1 ' ' -- $item)[1]
        set -l ver (string split -m1 ' ' -- $item)[2]
        switch $kind
            case node
                set_color green
            case python
                set_color cyan
            case java
                set_color yellow
            case go
                set_color blue
            case rust
                set_color red
            case ruby
                set_color magenta
            case '*'
                set_color white
        end
        set_color brblack
        printf '%s ' $kind
        switch $kind
            case node
                set_color green
            case python
                set_color cyan
            case java
                set_color yellow
            case go
                set_color blue
            case rust
                set_color red
            case ruby
                set_color magenta
            case '*'
                set_color white
        end
        printf '%s' $ver
    end
    set_color normal
    echo ''
end
__envmgr_hook
# <<< envmgr <<<
`
	default:
		return `# >>> envmgr >>>
# EnvMgr 钩子：进入带版本声明的目录时应用覆盖层，离开时还原。
# 卸载：envmgr hook --uninstall；或删掉本段。
if [ -z "$ENVMGR_BASE_PATH" ]; then
    export ENVMGR_BASE_PATH="$PATH"
    ENVMGR_TOUCHED=""
fi
__envmgr_hook() {
    [ "$ENVMGR_ACTIVE" = "$PWD" ] && return
    export ENVMGR_ACTIVE="$PWD"
    # 先回到基线：PATH 复原，上次改过的变量复原
    PATH="$ENVMGR_BASE_PATH"
    export PATH
    for __n in $ENVMGR_TOUCHED; do
        eval "__v=\${ENVMGR_ORIG_$__n-__ENVMGR_UNSET__}"
        if [ "$__v" = "__ENVMGR_UNSET__" ]; then
            unset "$__n"
        else
            eval "export $__n=\"\$__v\""
        fi
        unset "ENVMGR_ORIG_$__n"
    done
    ENVMGR_TOUCHED=""
    __out="$('@ENVMGR@' env --shell ` + string(sh) + ` --quiet)"
    if [ -z "$__out" ]; then
        unset __out
        return
    fi
    for __n in $(printf '%s\n' "$__out" | sed -n 's/^export \([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p'); do
        [ "$__n" = "PATH" ] && continue
        eval "__cur=\${$__n-__ENVMGR_UNSET__}"
        eval "ENVMGR_ORIG_$__n=\"\$__cur\""
        ENVMGR_TOUCHED="$ENVMGR_TOUCHED $__n"
    done
    eval "$__out"
    __envmgr_banner "$__out"
    unset __out __n __v __cur
}
# 进入项目时打印彩色提示：每种 SDK 用它自己的颜色。
__envmgr_banner() {
    [ -n "$ENVMGR_NO_BANNER" ] && return
    __items=$(printf '%s\n' "$1" | sed -n 's/^#[[:space:]]*envmgr:[[:space:]]*//p')
    [ -z "$__items" ] && return
    printf '\n  \033[36m◆ EnvMgr\033[0m  \033[90m%s\033[0m\n    ' "$(basename "$PWD")"
    __first=1
    __oldifs=$IFS
    IFS='
'
    for __item in $__items; do
        IFS=$__oldifs
        [ -z "$__first" ] && printf '\033[90m · \033[0m'
        __first=""
        __kind=${__item%% *}
        __ver=${__item#* }
        case "$__kind" in
            node)   __c='\033[32m' ;;
            python) __c='\033[36m' ;;
            java)   __c='\033[33m' ;;
            go)     __c='\033[34m' ;;
            rust)   __c='\033[31m' ;;
            ruby)   __c='\033[35m' ;;
            *)      __c='\033[37m' ;;
        esac
        printf '\033[90m%s \033[0m%b%s\033[0m' "$__kind" "$__c" "$__ver"
        IFS='
'
    done
    IFS=$__oldifs
    printf '\n'
    unset __items __item __kind __ver __c __first
}
# bash/zsh: run after every prompt.
if [ -n "$ZSH_VERSION" ]; then
    autoload -U add-zsh-hook 2>/dev/null && add-zsh-hook chpwd __envmgr_hook
    precmd_functions+=(__envmgr_hook)
else
    PROMPT_COMMAND="__envmgr_hook;${PROMPT_COMMAND}"
fi
__envmgr_hook
# <<< envmgr <<<
`
	}
}

// hookExe is the EnvMgr executable the hook calls. It is an absolute path
// because the directory EnvMgr was unpacked into need not be on PATH.
func hookExe() string {
	p, err := os.Executable()
	if err != nil || p == "" {
		return "envmgr"
	}
	return p
}

// hookMarkers delimit the block EnvMgr owns in a startup file, so installing
// twice replaces it instead of stacking copies, and uninstalling removes
// exactly what was added.
const (
	hookBegin = "# >>> envmgr >>>"
	hookEnd   = "# <<< envmgr <<<"
)

// HookFiles lists every startup file the shell may load. Windows PowerShell
// 5.1 and PowerShell 7 keep separate profiles, and a machine usually has both,
// so a hook that only knew one of them would silently never load.
func HookFiles(sh Shell) []string {
	if sh == ShellPowerShell && runtime.GOOS == "windows" {
		home := homeDir()
		return []string{
			filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
			filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		}
	}
	return []string{HookFile(sh)}
}

// InstallHook adds the hook to the shell's startup file, keeping one backup
// and never touching anything outside its own marked block. When the shell
// has several startup files, every one that already exists is updated, plus
// the primary one for shells that have never been started.
func InstallHook(sh Shell) (string, error) {
	files := HookFiles(sh)
	// Prefer the files that exist; fall back to the primary so a first install
	// still has somewhere to go.
	var existing []string
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			existing = append(existing, f)
		}
	}
	if len(existing) == 0 {
		existing = files[:1]
	}
	for _, file := range existing {
		if err := installHookFile(sh, file); err != nil {
			return file, err
		}
	}
	return strings.Join(existing, ", "), nil
}

func installHookFile(sh Shell, file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(old) > 0 {
		if err := os.WriteFile(file+".envmgr.bak", old, 0o644); err != nil {
			return err
		}
	}
	text, bom := splitBOM(string(old))
	stripped := stripHookBlock(text)
	if stripped != "" && !strings.HasSuffix(stripped, "\n") {
		stripped += "\n"
	}
	return writeProfile(file, stripped+HookScript(sh), sh, bom)
}

// commentPrefix is the comment marker of a shell.
func commentPrefix(sh Shell) string {
	switch sh {
	case ShellCmd:
		return "REM"
	}
	return "#"
}

// BannerPrefix marks the comment lines Export adds, so a shell can find them.
const BannerPrefix = "envmgr: "

// splitBOM reports whether the file starts with a UTF-8 BOM and returns the
// text without it.
func splitBOM(s string) (string, bool) {
	if strings.HasPrefix(s, "\ufeff") {
		return strings.TrimPrefix(s, "\ufeff"), true
	}
	return s, false
}

// writeProfile writes a startup file back.
//
// Windows PowerShell 5.1 decodes .ps1 files as ANSI unless they carry a UTF-8
// BOM, so the Chinese comments in the hook turn into mojibake and the block
// fails to parse. A BOM keeps both 5.1 and 7 reading the same bytes the same
// way.
func writeProfile(file, content string, sh Shell, hadBOM bool) error {
	if sh == ShellPowerShell && runtime.GOOS == "windows" {
		return os.WriteFile(file, []byte("\ufeff"+content), 0o644)
	}
	if hadBOM {
		content = "\ufeff" + content
	}
	return os.WriteFile(file, []byte(content), 0o644)
}

// UninstallHook removes the marked block from every startup file of the shell,
// leaving the rest of each file alone.
func UninstallHook(sh Shell) (bool, error) {
	removedAny := false
	for _, file := range HookFiles(sh) {
		old, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removedAny, err
		}
		text, bom := splitBOM(string(old))
		stripped := stripHookBlock(text)
		if stripped == text {
			continue
		}
		if err := writeProfile(file, stripped, sh, bom); err != nil {
			return removedAny, err
		}
		removedAny = true
	}
	return removedAny, nil
}

// HookInstalled reports whether any of the shell's startup files carries the
// block. Every one of them has to be checked: Windows ships PowerShell 5.1 and
// PowerShell 7 side by side, and either may be the terminal the user opens.
func HookInstalled(sh Shell) bool {
	for _, file := range HookFiles(sh) {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), hookBegin) {
			return true
		}
	}
	return false
}

// HookInstalledIn reports whether one specific startup file carries the block.
func HookInstalledIn(file string) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), hookBegin)
}

// HookMissing lists the existing startup files that still lack the block, so
// the UI can point out a half-installed state.
func HookMissing(sh Shell) []string {
	var out []string
	for _, file := range HookFiles(sh) {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if !strings.Contains(string(b), hookBegin) {
			out = append(out, file)
		}
	}
	return out
}

// stripHookBlock removes the EnvMgr block from a startup file's text.
func stripHookBlock(text string) string {
	var out []string
	inside := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == hookBegin {
			inside = true
			continue
		}
		if inside {
			if trimmed == hookEnd {
				inside = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// HookFile is the startup file the snippet belongs in.
func HookFile(sh Shell) string {
	home := homeDir()
	switch sh {
	case ShellPowerShell:
		if runtime.GOOS == "windows" {
			return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
		}
		return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
	case ShellZsh:
		return filepath.Join(home, ".zshrc")
	case ShellFish:
		return filepath.Join(home, ".config", "fish", "config.fish")
	case ShellBash:
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, ".bash_profile")
		}
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

// Terminal describes a terminal program EnvMgr can open with a project's
// environment ready.
type Terminal struct {
	Name string
	Path string
}

// Terminals lists the terminal programs available here, best first.
func Terminals() []Terminal {
	var out []Terminal
	add := func(name string, candidates ...string) {
		for _, c := range candidates {
			if p, err := exec.LookPath(c); err == nil {
				out = append(out, Terminal{Name: name, Path: p})
				return
			}
		}
	}
	switch runtime.GOOS {
	case "windows":
		add("Windows Terminal", "wt.exe")
		add("PowerShell", "pwsh.exe", "powershell.exe")
		add("cmd", "cmd.exe")
	case "darwin":
		add("Terminal", "/usr/bin/open") // opens Terminal.app with a script
		add("iTerm", "/Applications/iTerm.app/Contents/MacOS/iTerm2")
		add("kitty", "kitty")
		add("alacritty", "alacritty")
	default:
		add("Terminal", "x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "kitty", "alacritty", "xterm")
	}
	return out
}

// activateScript writes a startup file that applies the overlay and returns
// its path.
//
// Passing an environment to a terminal process only works when that process is
// the one starting the shell. Windows Terminal and gnome-terminal are
// single-instance: they hand the request to an already-running process, which
// starts the shell from its own environment and drops ours. Having the shell
// source the overlay itself works either way.
func activateScript(e Env, sh Shell) (string, error) {
	ext := ".sh"
	switch sh {
	case ShellPowerShell:
		ext = ".ps1"
	case ShellCmd:
		ext = ".cmd"
	}
	body := e.Export(sh) + bannerScript(e.Pairs, sh)
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.Join(e.Path, "\x00")))
	// The banner names versions, so it has to be part of the key: two projects
	// can share a PATH layout and still switch different SDK versions.
	for _, p := range e.Pairs {
		_, _ = h.Write([]byte("\x02" + string(p.Kind) + "=" + p.Version))
	}
	_, _ = h.Write([]byte("\x01" + string(sh)))
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "activate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("activate-%08x%s", h.Sum32(), ext))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// windowsShell picks the shell to open inside a terminal, with the arguments
// that make it apply the overlay before showing a prompt.
func windowsShell(e Env) (string, []string) {
	if prog, err := LookPath(e, "pwsh"); err == nil {
		script, serr := activateScript(e, ShellPowerShell)
		if serr == nil {
			return prog, []string{"-NoLogo", "-NoExit", "-File", quoteIfNeeded(script)}
		}
	}
	script, err := activateScript(e, ShellCmd)
	if err != nil {
		return "cmd.exe", []string{"/K"}
	}
	return "cmd.exe", []string{"/K", quoteIfNeeded(script)}
}

// quoteIfNeeded wraps a path in quotes when it contains a space, for the
// terminals that join their arguments back into one command line.
func quoteIfNeeded(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

// bannerScript prints the same colored line the shell hook shows, for the
// terminals EnvMgr opens itself. It is self-contained so an activation script
// works without the hook installed.
func bannerScript(pairs []Pair, sh Shell) string {
	if len(pairs) == 0 {
		return ""
	}
	var b strings.Builder
	switch sh {
	case ShellPowerShell:
		b.WriteString("\n# EnvMgr 提示：本终端已切到项目锁定的版本\n")
		b.WriteString("Write-Host ''\n")
		b.WriteString("Write-Host '  ◆ EnvMgr' -NoNewline -ForegroundColor DarkCyan\n")
		b.WriteString("Write-Host ''\n")
		b.WriteString("Write-Host '    ' -NoNewline\n")
		for i, p := range pairs {
			if i > 0 {
				b.WriteString("Write-Host ' · ' -NoNewline -ForegroundColor DarkGray\n")
			}
			fmt.Fprintf(&b, "Write-Host %s -NoNewline -ForegroundColor DarkGray\n", psQuote(string(p.Kind)+" "))
			fmt.Fprintf(&b, "Write-Host %s -ForegroundColor %s\n", psQuote(p.Version), psColor(p.Kind))
		}
	case ShellCmd:
		fmt.Fprintf(&b, "echo. \necho   EnvMgr: ")
		for i, p := range pairs {
			if i > 0 {
				b.WriteString(" ^| ")
			}
			fmt.Fprintf(&b, "%s %s", p.Kind, p.Version)
		}
		b.WriteString("\necho.\n")
	default:
		b.WriteString("\n# EnvMgr 提示：本终端已切到项目锁定的版本\n")
		b.WriteString(`printf '\n  \033[36m◆ EnvMgr\033[0m\n    '` + "\n")
		for i, p := range pairs {
			if i > 0 {
				b.WriteString(`printf '\033[90m · \033[0m'` + "\n")
			}
			fmt.Fprintf(&b, `printf '\033[90m%s \033[0m%s%s\033[0m'`+"\n",
				p.Kind, ansiColor(p.Kind), p.Version)
		}
		b.WriteString("printf '\\n'\n")
	}
	return b.String()
}

// psColor maps an SDK to a PowerShell console color.
func psColor(k Kind) string {
	switch k {
	case Node:
		return "Green"
	case Python:
		return "Cyan"
	case Java:
		return "Yellow"
	case Go:
		return "Blue"
	case Rust:
		return "Red"
	case Ruby:
		return "Magenta"
	}
	return "Gray"
}

// ansiColor is the escape sequence that matches psColor.
func ansiColor(k Kind) string {
	switch k {
	case Node:
		return `\033[32m`
	case Python:
		return `\033[36m`
	case Java:
		return `\033[33m`
	case Go:
		return `\033[34m`
	case Rust:
		return `\033[31m`
	case Ruby:
		return `\033[35m`
	}
	return `\033[37m`
}

// posixShellCommand is what a single-instance terminal server should run: a
// shell that sources the overlay, then becomes interactive.
func posixShellCommand(e Env, shell string) (string, bool) {
	sh, ok := ParseShell(filepath.Base(shell))
	if !ok {
		sh = ShellBash
	}
	script, err := activateScript(e, sh)
	if err != nil {
		return "", false
	}
	switch sh {
	case ShellFish:
		return shell + ` -c "source ` + script + `; exec ` + shell + `"`, true
	}
	return shell + ` -c ". ` + script + `; exec ` + shell + `"`, true
}

// LaunchTerminal opens a terminal in dir with the overlay applied.
//
// The child shell is started with the project's environment already in
// place, which is why no global variable ever needs to change.
func LaunchTerminal(dir string, e Env, term Terminal) error {
	if err := e.Validate(); err != nil {
		return err
	}
	env := Environ(e)
	env = append(env, "ENVMGR_ACTIVE="+dir)
	switch runtime.GOOS {
	case "windows":
		name := strings.ToLower(filepath.Base(term.Path))
		switch {
		case strings.HasPrefix(name, "wt"):
			// Windows Terminal hands this to the running instance, so the
			// shell itself has to apply the overlay.
			prog, argv := windowsShell(e)
			args := append([]string{"-d", dir, "--", prog}, argv...)
			cmd := exec.Command(term.Path, args...)
			cmd.Dir = dir
			cmd.Env = env
			return cmd.Start()
		case strings.HasPrefix(name, "pwsh"), strings.HasPrefix(name, "powershell"):
			args := []string{"-NoLogo", "-NoExit"}
			if script, err := activateScript(e, ShellPowerShell); err == nil {
				args = append(args, "-File", script)
			}
			cmd := exec.Command(term.Path, args...)
			cmd.Dir = dir
			cmd.Env = env
			return cmd.Start()
		default:
			if script, err := activateScript(e, ShellCmd); err == nil {
				cmd := exec.Command(term.Path, "/K", script)
				cmd.Dir = dir
				cmd.Env = env
				return cmd.Start()
			}
			cmd := exec.Command(term.Path, "/K", "cd /d "+dir)
			cmd.Dir = dir
			cmd.Env = env
			return cmd.Start()
		}
	case "darwin":
		// Terminal.app needs a script; write one and open it.
		return launchMacTerminal(dir, e)
	default:
		name := filepath.Base(term.Path)
		shell := shellFor(dir)
		// A terminal server spawns the shell from its own environment, so give
		// it a command that applies the overlay instead.
		sourced, ok := posixShellCommand(e, shell)
		var args []string
		switch {
		case strings.Contains(name, "gnome-terminal"):
			if ok {
				args = []string{"--working-directory=" + dir, "--", "sh", "-c", sourced}
			} else {
				args = []string{"--working-directory=" + dir, "--", shell}
			}
		case strings.Contains(name, "konsole"):
			if ok {
				args = []string{"--workdir", dir, "-e", "sh", "-c", sourced}
			} else {
				args = []string{"--workdir", dir, "-e", shell}
			}
		case strings.Contains(name, "xfce4-terminal"):
			if ok {
				args = []string{"--working-directory=" + dir, "-x", "sh", "-c", sourced}
			} else {
				args = []string{"--working-directory=" + dir, "-x", shell}
			}
		case strings.Contains(name, "alacritty"):
			if ok {
				args = []string{"--working-directory", dir, "-e", "sh", "-c", sourced}
			} else {
				args = []string{"--working-directory", dir, "-e", shell}
			}
		case strings.Contains(name, "kitty"):
			if ok {
				args = []string{"--directory", dir, "sh", "-c", sourced}
			} else {
				args = []string{"--directory", dir, shell}
			}
		default:
			if ok {
				args = []string{"-e", "sh", "-c", sourced}
			} else {
				args = []string{"-e", shell}
			}
		}
		cmd := exec.Command(term.Path, args...)
		cmd.Dir = dir
		cmd.Env = env
		return cmd.Start()
	}
}

// shellFor picks the interactive shell to open.
func shellFor(dir string) string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	return "/bin/sh"
}

// launchMacTerminal opens Terminal.app on a generated script, because that
// is the only supported way to give it an environment.
func launchMacTerminal(dir string, e Env) error {
	script := filepath.Join(os.TempDir(), "envmgr-terminal.sh")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("cd " + shQuote(dir) + "\n")
	for _, k := range sortedKeys(e.Vars) {
		b.WriteString("export " + k + "=" + shQuote(e.Vars[k]) + "\n")
	}
	if len(e.Path) > 0 {
		parts := []string{}
		for _, p := range e.Path {
			parts = append(parts, shQuote(p))
		}
		b.WriteString("export PATH=" + strings.Join(parts, ":") + `:"$PATH"` + "\n")
	}
	b.WriteString("export ENVMGR_ACTIVE=" + shQuote(dir) + "\n")
	b.WriteString("exec ${SHELL:-/bin/sh} -l\n")
	if err := os.WriteFile(script, []byte(b.String()), 0o755); err != nil {
		return err
	}
	return exec.Command("/usr/bin/open", "-a", "Terminal", script).Start()
}

// RunCommand runs a program with the overlay applied, in dir, wired to this
// process's standard streams. It returns the program's exit code.
func RunCommand(dir string, e Env, args []string) (int, error) {
	if err := e.Validate(); err != nil {
		return 1, err
	}
	if len(args) == 0 {
		return 1, errors.New("没有要运行的程序")
	}
	prog, err := LookPath(e, args[0])
	if err != nil {
		return 1, err
	}
	cmd := exec.Command(prog, args[1:]...)
	cmd.Dir = dir
	cmd.Env = Environ(e)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
