package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"envmgr/internal/core"
)

// cmdReady brings the current project's declared SDKs up: it shows what each
// declaration resolves to, installs the missing ones, and runs every SDK to
// verify the environment actually works. It is the command-line twin of the
// window's "一键就绪" button, so CI and fresh clones get the same path.
func cmdReady(svc *core.Service, args []string) int {
	dir, args := flagValue(args, "dir", "")
	dry := hasFlag(args, "dry-run")
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	path, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	root := path
	if found, ok := core.NearestProjectDir(path); ok {
		root = found
	}
	p := core.NewProject(root)
	res := svc.Resolve(p)
	if len(res) == 0 {
		fmt.Fprintf(os.Stderr, "%s 没有 SDK 版本声明，先用 envmgr use 固定版本\n", root)
		return 1
	}
	if !core.SamePath(root, path) {
		fmt.Printf("项目 %s（从 %s 向上找到）\n", root, path)
	}

	missing := 0
	fmt.Println("版本声明：")
	for _, r := range res {
		if r.Err != nil {
			fmt.Fprintln(os.Stderr, "项目配置错误：", r.Err)
			return 1
		}
		if r.Missing {
			missing++
			fmt.Printf("  ✗ %-8s %-12s 未安装\n", core.Info(r.Kind).Name, r.Expr)
			continue
		}
		fmt.Printf("  ✓ %-8s %-12s %s · %s\n", core.Info(r.Kind).Name, r.Installed.Version, r.Installed.Manager, r.Source)
	}

	if missing == 0 && dry {
		fmt.Println("\n全部就绪，无需安装")
		return 0
	}

	if dry {
		fmt.Println("\n将安装：")
		code := 0
		for _, r := range res {
			if !r.Missing {
				continue
			}
			if core.InstallerFor(r.Kind) == nil {
				fmt.Printf("  ✗ %s %s 需要手动安装并导入\n", core.Info(r.Kind).Name, r.Expr)
				code = 1
				continue
			}
			list, err := svc.ListRemote(context.Background(), r.Kind, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "获取 %s 版本列表失败：%v\n", core.Info(r.Kind).Name, err)
				return 1
			}
			remote, err := core.MatchRemote(list, r.Kind, r.Expr)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Printf("  · %s %s（满足声明 %s）\n", core.Info(remote.Kind).Name, remote.Version, r.Expr)
		}
		if code == 0 {
			fmt.Println("\n去掉 --dry-run 执行安装与验证：envmgr ready")
		}
		return code
	}

	fmt.Println()
	progress, flush := readyProgress()
	result, err := svc.ReadyProject(context.Background(), p, progress)
	flush()
	if err != nil {
		fmt.Fprintln(os.Stderr, "\n"+err.Error())
		return 1
	}
	fmt.Println()
	for _, v := range result.Verified {
		fmt.Printf("✓ %s %s\n", core.Info(v.Kind).Name, v.Version)
	}
	if len(result.Installed) > 0 {
		fmt.Printf("新安装 %d 个版本\n", len(result.Installed))
	}
	fmt.Println("项目环境就绪")
	return 0
}

// readyProgress renders installation stages and download progress on one
// terminal line per SDK, so a clone-and-run script stays readable. The second
// return closes a half-written progress line, for the error paths.
func readyProgress() (core.ReadyProgress, func()) {
	lastKey, lastStage, lastPct := "", "", -1
	inline := false
	flush := func() {
		if inline {
			fmt.Println()
			inline = false
		}
	}
	progress := core.ReadyProgress(func(e core.ReadyEvent) {
		key := string(e.Kind) + "@" + e.Version
		if key != lastKey {
			flush()
			fmt.Printf("  %s %s\n", core.Info(e.Kind).Name, e.Version)
			lastKey, lastStage, lastPct = key, "", -1
		}
		if e.Stage != lastStage {
			flush()
			fmt.Printf("    %s", e.Stage)
			lastStage, lastPct, inline = e.Stage, -1, true
		}
		if e.Total > 0 {
			pct := int(e.Done * 100 / e.Total)
			// A percent every 1% floods a piped log; a rewrite every 5% reads
			// the same on a terminal and keeps non-TTY output sane.
			if lastPct < 0 || pct-lastPct >= 5 {
				fmt.Printf("\r    %s %3d%%", e.Stage, pct)
				lastPct, inline = pct, true
			}
		}
	})
	return progress, flush
}
