package app

import (
	"testing"

	"envmgr/internal/core"
)

// TestGitStateVerdict pins the order of the checks: a missing program outranks
// everything, then "not a repository", then a detached head, then the commit and
// dirtiness states.
func TestGitStateVerdict(t *testing.T) {
	cases := []struct {
		name string
		info core.GitInfo
		want string
		tone tone
	}{
		{
			name: "no git program",
			info: core.GitInfo{},
			want: "未安装", tone: toneMuted,
		},
		{
			// git is installed but this directory is not a repository.
			name: "not a repository",
			info: core.GitInfo{Available: true},
			want: "不是仓库", tone: toneWarn,
		},
		{
			name: "detached head",
			info: core.GitInfo{Available: true, Repo: true, Detached: true, Branch: ""},
			want: "游离 HEAD", tone: toneWarn,
		},
		{
			// Fresh after git init: on a branch, but nothing committed.
			name: "no commits yet",
			info: core.GitInfo{Available: true, Repo: true, Branch: "main"},
			want: "还没有提交", tone: toneMuted,
		},
		{
			name: "clean",
			info: core.GitInfo{Available: true, Repo: true, Branch: "main", Commits: true},
			want: "干净", tone: toneGood,
		},
		{
			name: "dirty",
			info: core.GitInfo{Available: true, Repo: true, Branch: "main", Commits: true,
				Modified: 2, Untracked: 1},
			want: "3 项改动", tone: toneAccent,
		},
	}
	for _, c := range cases {
		got, tn := gitState(c.info)
		if got != c.want || tn != c.tone {
			t.Errorf("%s: got %q/%d, want %q/%d", c.name, got, tn, c.want, c.tone)
		}
	}
}

// TestChangeSummaryWordsEachKind keeps the status line honest about which kind
// of change it is counting.
func TestChangeSummaryWordsEachKind(t *testing.T) {
	cases := []struct {
		info core.GitInfo
		want string
	}{
		{core.GitInfo{}, "没有未提交的改动"},
		{core.GitInfo{Staged: 1}, "已暂存 1"},
		{core.GitInfo{Modified: 2}, "已修改 2"},
		{core.GitInfo{Untracked: 3}, "未跟踪 3"},
		{core.GitInfo{Staged: 1, Modified: 1, Untracked: 2}, "已暂存 1 · 已修改 1 · 未跟踪 2"},
	}
	for _, c := range cases {
		if got := changeSummary(c.info); got != c.want {
			t.Errorf("changeSummary(%+v) = %q, want %q", c.info, got, c.want)
		}
	}
}

// TestSamePathIgnoresCaseAndSeparators: git reports forward slashes and may
// report different case on Windows, so comparing the project path with the
// repository root has to be forgiving or the "nested in a bigger repository"
// line would appear for ordinary projects.
func TestSamePathIgnoresCaseAndSeparators(t *testing.T) {
	same := [][2]string{
		{`C:\Users\Dev\app`, `C:/Users/Dev/app`},
		{`C:\Users\Dev\app\`, `C:\Users\Dev\app`},
		{`c:\users\dev\app`, `C:\Users\Dev\App`},
	}
	for _, pair := range same {
		if !samePath(pair[0], pair[1]) {
			t.Errorf("samePath(%q, %q) = false, want true", pair[0], pair[1])
		}
	}
	differ := [][2]string{
		{`C:\Users\Dev\app`, `C:\Users\Dev\app2`},
		{`/home/dev/app`, `/home/dev/other`},
	}
	for _, pair := range differ {
		if samePath(pair[0], pair[1]) {
			t.Errorf("samePath(%q, %q) = true, want false", pair[0], pair[1])
		}
	}
}
