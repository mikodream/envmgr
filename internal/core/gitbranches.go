package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GitBranch describes a local branch or a cached remote tracking ref. Remote is
// empty for local branches; Name excludes refs/heads/ or refs/remotes/<remote>/.
type GitBranch struct {
	Name, Ref, Remote, Upstream string
	Current                     bool
	Ahead, Behind               int
}

const gitBranchesFormat = "--format=%(refname)%09%(HEAD)%09%(upstream)%09%(upstream:track)%09%(symref)"

func readGitBranches(run gitRunner, dir string, info GitInfo) []GitBranch {
	out, err := run(dir, "for-each-ref", gitBranchesFormat, "refs/heads/", "refs/remotes/")
	if err != nil {
		return nil
	}
	var branches []GitBranch
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) != 5 {
			continue
		}
		b := GitBranch{Ref: fields[0], Upstream: fields[2]}
		switch {
		case strings.HasPrefix(b.Ref, "refs/heads/"):
			b.Name = strings.TrimPrefix(b.Ref, "refs/heads/")
			b.Current = fields[1] == "*" || (!info.Detached && b.Name == info.Branch)
			for _, part := range strings.Split(strings.Trim(fields[3], "[]"), ",") {
				words := strings.Fields(part)
				if len(words) != 2 {
					continue
				}
				n, err := strconv.Atoi(words[1])
				if err != nil || n < 0 {
					continue
				}
				if words[0] == "ahead" {
					b.Ahead = n
				}
				if words[0] == "behind" {
					b.Behind = n
				}
			}
		case strings.HasPrefix(b.Ref, "refs/remotes/"):
			// Remote HEAD aliases are navigation aids, not branches.
			if fields[4] != "" {
				continue
			}
			relative := strings.TrimPrefix(b.Ref, "refs/remotes/")
			for _, remote := range info.Remotes {
				if strings.HasPrefix(relative, remote.Name+"/") && len(remote.Name) > len(b.Remote) {
					b.Remote = remote.Name
				}
			}
			if b.Remote == "" {
				continue
			}
			b.Name = strings.TrimPrefix(relative, b.Remote+"/")
		default:
			continue
		}
		branches = append(branches, b)
	}
	return branches
}

func readGitPushRemote(run gitRunner, dir string, info GitInfo) string {
	if info.Branch != "" && !info.Detached {
		if name := firstLine(mustRun(run, dir, "config", "--get", "branch."+info.Branch+".pushRemote")); name != "" {
			return name
		}
	}
	if name := firstLine(mustRun(run, dir, "config", "--get", "remote.pushDefault")); name != "" {
		return name
	}
	for _, remote := range info.Remotes {
		if remote.Name == "origin" {
			return remote.Name
		}
	}
	if len(info.Remotes) > 0 {
		return info.Remotes[0].Name
	}
	return ""
}

// runGitOperation preserves Git's diagnostic output for user initiated writes.
func runGitOperation(dir string, args ...string) (string, error) {
	return runGitOperationContext(context.Background(), dir, args...)
}

func runGitOperationContext(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	// Bound waiting for output pipes if a transport child survives cancellation.
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	text := trimNewlines(string(out))
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return text, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

// FetchGitRemote explicitly refreshes and prunes cached refs for one existing
// named remote. Background reads and remote selection never invoke this action.
// The caller can cancel it; a 30-second deadline bounds interactive transports.
func FetchGitRemote(ctx context.Context, dir, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := runGitOperationContext(ctx, dir, "remote")
	if err != nil {
		return err
	}
	found := false
	for _, remote := range strings.Split(out, "\n") {
		if strings.TrimSuffix(remote, "\r") == name && name != "" {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("Git remote %q does not exist", name)
	}
	_, err = runGitOperationContext(ctx, dir, "fetch", "--prune", "--", name)
	return err
}

// SwitchGitBranch accepts only an existing full local or cached remote ref.
// Remote refs create a same-named local tracking branch; an existing local name
// is an explicit conflict. Git preserves working changes or reports its error.
func SwitchGitBranch(dir, ref string) error {
	if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/remotes/") {
		return fmt.Errorf("branch must be an existing full refs/heads/ or refs/remotes/ ref: %q", ref)
	}
	if _, err := runGitOperation(dir, "show-ref", "--verify", "--", ref); err != nil {
		return err
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		name := strings.TrimPrefix(ref, "refs/heads/")
		if _, err := runGitOperation(dir, "check-ref-format", "--branch", name); err != nil {
			return err
		}
		_, err := runGitOperation(dir, "switch", "--no-guess", name)
		return err
	}
	info := LoadGit(dir)
	for _, branch := range info.Branches {
		if branch.Ref != ref || branch.Remote == "" {
			continue
		}
		if _, err := runGitOperation(dir, "check-ref-format", "--branch", branch.Name); err != nil {
			return err
		}
		if _, err := runGitOperation(dir, "show-ref", "--verify", "--quiet", "--", "refs/heads/"+branch.Name); err == nil {
			return fmt.Errorf("local branch %q already exists; switch to refs/heads/%s explicitly", branch.Name, branch.Name)
		}
		_, err := runGitOperation(dir, "switch", "--track", "-c", branch.Name, ref)
		return err
	}
	return fmt.Errorf("cached remote branch %q has no named remote or is a symbolic ref", ref)
}

// CreateGitBranch creates a branch from the current checkout without forcing it.
func CreateGitBranch(dir, name string) error {
	if _, err := runGitOperation(dir, "check-ref-format", "--branch", name); err != nil {
		return err
	}
	// check-ref-format expands @{-N}; require a literal branch name instead.
	if strings.Contains(name, "@{") {
		return fmt.Errorf("branch name must be literal: %q", name)
	}
	_, err := runGitOperation(dir, "switch", "-c", name)
	return err
}

// DeleteGitBranch deletes only a merged local branch. Git rejects current,
// worktree-checked-out and unmerged branches; no force option is used.
func DeleteGitBranch(dir, name string) error {
	if _, err := runGitOperation(dir, "check-ref-format", "--branch", name); err != nil {
		return err
	}
	if strings.Contains(name, "@{") {
		return fmt.Errorf("branch name must be literal: %q", name)
	}
	_, err := runGitOperation(dir, "branch", "-d", "--", name)
	return err
}

// clearGitRemoteSelection removes only local push selections naming a removed
// remote. Global defaults and selections of other remotes remain untouched.
func clearGitRemoteSelection(dir, removedName string) error {
	out, err := runGitOperation(dir, "config", "--local", "--null", "--get-regexp", `^(remote\.pushdefault|branch\..*\.pushremote)$`)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil // No matching local configuration.
		}
		return err
	}
	keys := make(map[string]bool)
	for _, record := range strings.Split(out, "\x00") {
		key, value, ok := strings.Cut(record, "\n")
		if ok && value == removedName {
			keys[key] = true
		}
	}
	for key := range keys {
		if _, err := runGitOperation(dir, "config", "--local", "--fixed-value", "--unset-all", key, removedName); err != nil {
			return err
		}
	}
	return nil
}

// SetGitDefaultRemote writes repository-local remote.pushDefault and updates an
// existing active-branch pushRemote override so the selection takes effect.
// Other branches and upstream/fetch settings are preserved.
func SetGitDefaultRemote(dir, name string) error {
	out, err := runGitOperation(dir, "remote")
	if err != nil {
		return err
	}
	found := false
	for _, remote := range strings.Split(out, "\n") {
		if strings.TrimSuffix(remote, "\r") == name && name != "" {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("Git remote %q does not exist", name)
	}
	branch, detached := readBranch(runGit, dir)
	overrideKey := ""
	if branch != "" && !detached {
		key := "branch." + branch + ".pushRemote"
		override := firstLine(mustRun(runGit, dir, "config", "--get", key))
		if override != "" && override != name {
			overrideKey = key
		}
	}
	if _, err = runGitOperation(dir, "config", "--local", "remote.pushDefault", name); err != nil {
		return err
	}
	if overrideKey != "" {
		if _, err = runGitOperation(dir, "config", "--local", overrideKey, name); err != nil {
			return fmt.Errorf("remote.pushDefault was selected, but updating the active branch override failed: %w", err)
		}
	}
	return nil
}
