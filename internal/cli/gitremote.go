package cli

import (
	"fmt"
	"os"
	"strings"

	"envmgr/internal/core"
)

// cmdGitRemote receives an already resolved directory. Remote names and URLs
// remain positional arguments, even when a project has the same name.
func cmdGitRemote(svc *core.Service, args []string, dir string) int {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list", "ls":
		if len(args) != 0 {
			break
		}
		info := svc.Git(dir)
		if !info.Available || !info.Repo {
			fmt.Fprintln(os.Stderr, "目录需要是 Git 仓库，且已安装 Git:", dir)
			return 1
		}
		if len(info.Remotes) == 0 {
			fmt.Println("还没有远端")
		} else {
			printGitRemotes(info.Remotes)
		}
		return 0
	case "add", "set":
		if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
			break
		}
		if err := svc.SetGitRemote(dir, args[0], args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已设置远端 %s → %s\n", args[0], args[1])
		return 0
	case "rm", "remove", "delete":
		if len(args) != 1 || strings.HasPrefix(args[0], "-") {
			break
		}
		if err := svc.RemoveGitRemote(dir, args[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("已删除远端 %s\n", args[0])
		return 0
	}
	fmt.Fprintln(os.Stderr, "用法: envmgr git remote list [--dir 项目或目录]")
	fmt.Fprintln(os.Stderr, "      envmgr git remote add 名称 URL [--dir 项目或目录]")
	fmt.Fprintln(os.Stderr, "      envmgr git remote rm 名称 [--dir 项目或目录]")
	return 2
}

func printGitRemotes(remotes []core.GitRemote) {
	for _, remote := range remotes {
		fmt.Printf("远端  %s  %s\n", remote.Name, remote.URL)
		if key, ok := core.NormalizeRemote(remote.URL); ok {
			fmt.Println("地址  " + key)
		}
		if remote.PushURL != "" && remote.PushURL != remote.URL {
			fmt.Printf("推送  %s  %s\n", remote.Name, remote.PushURL)
		}
	}
}
