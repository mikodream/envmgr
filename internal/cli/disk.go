package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"envmgr/internal/core"
)

// cmdDisk reports what the installed SDKs occupy and reclaims the parts that
// no version needs.
//
// A version manager that never shows its footprint is a manager users are
// afraid to fill up: every install feels permanent. Naming the cache and the
// leftovers makes the space recoverable.
func cmdDisk(svc *core.Service, args []string) int {
	clean := hasFlag(args, "clean")
	asJSON := hasFlag(args, "json")

	rep, err := svc.DiskReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "统计失败: %v\n", err)
		return 1
	}
	if asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Println(string(b))
	} else {
		printDiskReport(rep)
	}

	if !clean {
		return 0
	}
	res, err := svc.CleanDisk(core.CleanOptions{Cache: true, Residue: true})
	fmt.Println()
	fmt.Println(core.DescribeClean(res))
	if err != nil {
		fmt.Fprintf(os.Stderr, "部分内容未能删除: %v\n", err)
		return 1
	}
	if res.Freed() > 0 {
		fmt.Printf("共释放 %s\n", core.HumanSize(res.Freed()))
	}
	return 0
}

func printDiskReport(rep core.DiskReport) {
	counts := map[core.Kind]int{}
	known := map[core.Kind]int{}
	sizes := map[core.Kind]int64{}
	for _, e := range rep.Entries {
		counts[e.Kind]++
		sizes[e.Kind] += e.Size
		if e.Known {
			known[e.Kind]++
		}
	}
	versions := len(rep.Entries)
	excluded := 0
	for _, e := range rep.Entries {
		if !e.Known {
			excluded++
		}
	}
	scope := fmt.Sprintf("%d 个版本，EnvMgr 管理 %s，其他管理器 %s",
		versions, core.HumanSize(rep.Managed), core.HumanSize(rep.External))
	if excluded > 0 {
		scope += fmt.Sprintf("；%d 个位于系统共享目录，未计入", excluded)
	}
	fmt.Printf("已装 SDK  %s（%s）\n", core.HumanSize(rep.Total), scope)
	for _, kind := range core.Kinds {
		if counts[kind] == 0 {
			continue
		}
		subtotal := core.HumanSize(sizes[kind])
		if known[kind] == 0 {
			// Every install of this kind sits in a shared system directory.
			subtotal = "—"
		}
		fmt.Printf("  %-8s %10s  %d 个\n", core.Info(kind).Name, subtotal, counts[kind])
		for _, e := range rep.Entries {
			if e.Kind != kind {
				continue
			}
			size := core.HumanSize(e.Size)
			note := e.Note
			if !e.Known {
				size = "—"
				if note == "" {
					note = "无法读取"
				}
			}
			manager := e.Manager
			if manager == "" {
				manager = "未知"
			}
			fmt.Printf("    %-14s %-8s %9s  %s%s\n", e.Version, manager, size, shortHome(e.Path), suffix("  "+note))
		}
	}

	fmt.Println()
	if rep.CacheFiles == 0 {
		fmt.Printf("下载缓存  空（%s）\n", shortHome(rep.CacheDir))
	} else {
		oldest := ""
		if !rep.CacheOldest.IsZero() {
			oldest = "，最早 " + rep.CacheOldest.Format("2006-01-02")
		}
		fmt.Printf("下载缓存  %s（%d 个归档%s）\n  %s\n",
			core.HumanSize(rep.CacheSize), rep.CacheFiles, oldest, shortHome(rep.CacheDir))
	}

	if len(rep.Residue) > 0 {
		fmt.Printf("残留      %s（%d 个目录）\n", core.HumanSize(rep.ResidueSize), len(rep.Residue))
		for _, r := range rep.Residue {
			fmt.Printf("  %-9s %s  %s\n", core.HumanSize(r.Size), shortHome(r.Path), r.Label)
		}
	}

	if total := rep.Reclaimable(); total > 0 {
		fmt.Printf("\n可释放 %s：envmgr disk --clean\n", core.HumanSize(total))
		fmt.Println("只删下载归档与残留目录，已安装的版本一个都不会动。")
	}
}

// suffix renders an optional trailing note, including its leading separator.
func suffix(note string) string {
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return note
}

// shortHome abbreviates the home directory so paths fit a terminal.
func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}
