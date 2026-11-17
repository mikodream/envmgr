// Command envmgr is a project environment manager: it pins the SDK versions a
// project uses and hands them to a terminal, without touching any global
// variable.
//
// Running it with no arguments opens the window. Every other use is a
// subcommand of the same program, sharing the same core:
//
//	envmgr                          open the window
//	envmgr env [--shell bash]       print the export commands for this directory
//	envmgr run -- go test ./...     run a command with the project's SDKs
//	envmgr use node@20.11.1         pin a version in this directory
//	envmgr list [node]              list installed versions (--remote too)
//	envmgr install node@20.11.1     download and install a version
//	envmgr doctor                   check what is installed and what is missing
//	envmgr hook bash                print the opt-in shell hook
//	envmgr add <dir>                add a project to the window's list
//	envmgr scan <dir>               find project directories under a path
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"envmgr/internal/app"
	"envmgr/internal/cli"
	"envmgr/internal/core"
)

func main() {
	// A subcommand means command-line use; no arguments means the window.
	if len(os.Args) > 1 && cli.HasCommand(os.Args[1]) {
		os.Exit(cli.Run(os.Args[1:]))
	}
	if len(os.Args) > 1 {
		if os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "help" {
			cli.PrintUsage(os.Stdout)
			return
		}
		// macOS passes -psn_… when launched from Finder.
		if !strings.HasPrefix(os.Args[1], "-psn") && !strings.HasPrefix(os.Args[1], "-") {
			fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", os.Args[1])
			cli.PrintUsage(os.Stderr)
			os.Exit(2)
		}
	}
	runWindow()
}

// runWindow opens the native window.
func runWindow() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	mygo.App.SetName("EnvMgr")
	mygo.App.SetVersion("1.0.0")
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)

	svc, err := core.NewService()
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	state := app.New(svc)

	mygo.App.OnSecondInstance(func(args []string, dir string) {
		if wins := mygo.Windows(); len(wins) > 0 {
			wins[0].Show()
			wins[0].Focus()
		}
	})
	mygo.App.OnWindowAllClosed(func() { mygo.App.Quit() })
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleQuit},
		}},
		// The Edit menu is what makes Cmd/Ctrl+C and +V work in text fields.
		{Role: mygo.RoleEditMenu},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "添加项目…", Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) { state.AddProject() }},
			{Label: "重新扫描", Accelerator: "CmdOrCtrl+R", Click: func(*mygo.MenuItem, *mygo.Window) { state.Rescan() }},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleWindowMenu},
	}))

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "EnvMgr · 项目环境管理",
			Width:     1180,
			Height:    780,
			MinWidth:  880,
			MinHeight: 560,
			StateKey:  "main",
			Content:   ui.View(state.View),
		})
		state.SetWindow(win)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
