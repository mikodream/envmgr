package core

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ReadyEvent describes the current SDK and installation stage.
type ReadyEvent struct {
	Kind        Kind
	Version     string
	Stage       string
	Done, Total int64
}
type ReadyProgress func(ReadyEvent)
type VerifiedSDK struct {
	Kind    Kind
	Version string
	Path    string
	Output  string
}
type ReadyResult struct {
	Installed []Installed
	Verified  []VerifiedSDK
}

type installStageKey struct{}

// WithInstallStage attaches stage reporting without changing provider interfaces.
func WithInstallStage(ctx context.Context, report func(string)) context.Context {
	return context.WithValue(ctx, installStageKey{}, report)
}
func reportInstallStage(ctx context.Context, stage string) {
	if report, ok := ctx.Value(installStageKey{}).(func(string)); ok && report != nil {
		report(stage)
	}
}

type readyOperations struct {
	resolve func(Project) []Resolution
	list    func(context.Context, Kind) ([]Remote, error)
	install func(context.Context, Remote, Progress) (Installed, error)
	verify  func(context.Context, Project, Resolution, Env) (VerifiedSDK, error)
}

// ReadyProject preflights all missing SDKs, installs matching versions, and
// executes each selected SDK to verify the environment actually works.
func (s *Service) ReadyProject(ctx context.Context, p Project, progress ReadyProgress) (ReadyResult, error) {
	return readyProject(ctx, p, progress, readyOperations{
		resolve: s.Resolve,
		list:    func(ctx context.Context, kind Kind) ([]Remote, error) { return s.ListRemote(ctx, kind, false) },
		install: s.Install,
		verify:  verifyProjectSDK,
	})
}

func readyProject(ctx context.Context, p Project, progress ReadyProgress, ops readyOperations) (ReadyResult, error) {
	result := ReadyResult{}
	emit := func(e ReadyEvent) {
		if progress != nil {
			progress(e)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	resolutions := ops.resolve(p)
	if len(resolutions) == 0 {
		return result, fmt.Errorf("项目没有 SDK 版本声明")
	}
	var plan []Remote
	// Complete the plan before changing disk, so an unsupported or unavailable
	// SDK does not leave an avoidable half-installed project.
	for _, r := range resolutions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if r.Err != nil {
			return result, r.Err
		}
		if !r.Missing {
			continue
		}
		if InstallerFor(r.Kind) == nil {
			return result, fmt.Errorf("%s %s 需要手动安装并导入", Info(r.Kind).Name, r.Expr)
		}
		emit(ReadyEvent{Kind: r.Kind, Version: r.Expr, Stage: "检查版本", Total: -1})
		list, err := ops.list(ctx, r.Kind)
		if err != nil {
			return result, fmt.Errorf("获取 %s 版本失败：%w", r.Kind, err)
		}
		remote, err := matchingRemote(list, r.Kind, r.Expr)
		if err != nil {
			return result, err
		}
		plan = append(plan, remote)
	}
	for _, remote := range plan {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		stage := "下载"
		installCtx := WithInstallStage(ctx, func(next string) {
			stage = next
			emit(ReadyEvent{Kind: remote.Kind, Version: remote.Version, Stage: stage, Total: -1})
		})
		inst, err := ops.install(installCtx, remote, func(done, total int64) {
			emit(ReadyEvent{Kind: remote.Kind, Version: remote.Version, Stage: stage, Done: done, Total: total})
		})
		if err != nil {
			return result, fmt.Errorf("安装 %s %s 失败：%w", remote.Kind, remote.Version, err)
		}
		result.Installed = append(result.Installed, inst)
	}
	resolutions = ops.resolve(p)
	env := BuildEnv(resolutions)
	if err := env.Validate(); err != nil {
		return result, err
	}
	for _, r := range resolutions {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if r.Installed == nil {
			continue
		}
		emit(ReadyEvent{Kind: r.Kind, Version: r.Installed.Version, Stage: "验证", Total: -1})
		verified, err := ops.verify(ctx, p, r, env)
		if err != nil {
			return result, err
		}
		result.Verified = append(result.Verified, verified)
	}
	return result, nil
}

func matchingRemote(list []Remote, kind Kind, expr string) (Remote, error) {
	var candidates []Remote
	for _, r := range list {
		if r.Kind != kind {
			continue
		}
		if strings.EqualFold(expr, "lts") {
			if !r.LTS {
				continue
			}
		} else if !Matches(ParseVersion(r.Version), expr) {
			continue
		}
		candidates = append(candidates, r)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if cmp := ParseVersion(a.Version).Compare(ParseVersion(b.Version)); cmp != 0 {
			return cmp > 0
		}
		return (a.Vendor == "" || a.Vendor == DefaultVendor) && !(b.Vendor == "" || b.Vendor == DefaultVendor)
	})
	if len(candidates) == 0 {
		return Remote{}, fmt.Errorf("没有可安装的 %s %s 版本", Info(kind).Name, expr)
	}
	return candidates[0], nil
}

func sdkVersionOutput(kind Kind, output string) string {
	if kind == Go {
		for _, field := range strings.Fields(output) {
			if strings.HasPrefix(field, "go") && len(field) > 2 && field[2] >= '0' && field[2] <= '9' {
				return strings.TrimPrefix(field, "go")
			}
		}
	}
	if kind == Java {
		for _, field := range strings.Fields(output) {
			raw := strings.Trim(field, `"`)
			if raw != "" && raw[0] >= '0' && raw[0] <= '9' && VersionFromName(raw) != "" {
				return normalizeJavaVersion(raw)
			}
		}
	}
	return VersionFromOutput(output)
}

func verifyProjectSDK(ctx context.Context, p Project, r Resolution, env Env) (VerifiedSDK, error) {
	inst := r.Installed
	exe := inst.ExePath()
	if exe == "" {
		return VerifiedSDK{}, fmt.Errorf("%s %s 的可执行文件已丢失", r.Kind, inst.Version)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	arg := "--version"
	if r.Kind == Java {
		arg = "-version"
	} else if r.Kind == Go {
		arg = "version"
	}
	cmd := exec.CommandContext(ctx, exe, arg)
	cmd.Dir = p.Path
	// Prevent Go's automatic toolchain download from masking the executable being checked.
	if r.Kind == Go {
		vars := make(map[string]string, len(env.Vars)+1)
		for k, v := range env.Vars {
			vars[k] = v
		}
		vars["GOTOOLCHAIN"] = "local"
		env.Vars = vars
	}
	cmd.Env = Environ(env)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return VerifiedSDK{}, fmt.Errorf("验证 %s %s 失败：%w；%s", r.Kind, inst.Version, err, lastLines(string(out), 4))
	}
	actual := sdkVersionOutput(r.Kind, string(out))
	if actual == "" || !Matches(ParseVersion(actual), inst.Version) {
		return VerifiedSDK{}, fmt.Errorf("%s 版本验证不一致：预期 %s，实际 %s", r.Kind, inst.Version, strings.TrimSpace(string(out)))
	}
	return VerifiedSDK{Kind: r.Kind, Version: actual, Path: exe, Output: strings.TrimSpace(string(out))}, nil
}
