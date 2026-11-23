package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeDiscoveryFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestJenvSymlinkVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix manager symlinks")
	}
	root := t.TempDir()
	versions := filepath.Join(root, "jenv", "versions")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"11.0.29", "21.0.9"} {
		sdk := filepath.Join(root, "jdks", version, "Contents", "Home")
		writeDiscoveryFile(t, filepath.Join(sdk, "release"), "JAVA_VERSION=\""+version+"\"\n")
		writeDiscoveryFile(t, filepath.Join(sdk, "bin", "java"), "placeholder")
		for _, alias := range []string{version[:2], version[:2] + ".0", version, "openjdk64-" + version} {
			if err := os.Symlink(sdk, filepath.Join(versions, alias)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(versions, "broken")); err != nil {
		t.Fatal(err)
	}
	got := dedupeInstalls(versionDirs(Java, "jenv", versions))
	if len(got) != 2 {
		t.Fatalf("installs = %+v, want two actual JDKs", got)
	}
	for i, version := range []string{"21.0.9", "11.0.29"} {
		if got[i].Version != version || got[i].Manager != "jenv" {
			t.Errorf("install = %+v, want %s from jenv", got[i], version)
		}
		if got[i].Env["JAVA_HOME"] != got[i].Path || got[i].ExePath() == "" {
			t.Errorf("unusable install: %+v", got[i])
		}
		if filepath.Base(got[i].Path) != "Home" {
			t.Errorf("alias path retained: %s", got[i].Path)
		}
	}
}

func TestProbeVersionPrefersSDKMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "openjdk64-21.0")
	writeDiscoveryFile(t, filepath.Join(root, "release"), "JAVA_VERSION=\"21.0.9\"\r\n")
	if got := ProbeVersion(Java, root); got != "21.0.9" {
		t.Fatalf("version = %q", got)
	}
	writeDiscoveryFile(t, filepath.Join(root, "release"), "JAVA_VERSION=\"1.8.0_412\"\n")
	if got := ProbeVersion(Java, root); got != "8.0.412" {
		t.Fatalf("Java 8 version = %q", got)
	}
}

func TestProbeNodeVersionPrefersExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := filepath.Join(t.TempDir(), "v20.0.0")
	writeDiscoveryFile(t, filepath.Join(root, "bin", "node"), "#!/bin/sh\nprintf 'v22.1.0\\n'\n")
	if got := ProbeVersion(Node, root); got != "22.1.0" {
		t.Fatalf("version = %q", got)
	}
}

func TestProbeJava8UsesLegacyVersionFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := filepath.Join(t.TempDir(), "jdk")
	writeDiscoveryFile(t, filepath.Join(root, "bin", "java"), "#!/bin/sh\n[ \"$1\" = '-version' ] || exit 1\nprintf 'java version \"1.8.0_412\"\\n' >&2\n")
	if got := ProbeVersion(Java, root); got != "8.0.412" {
		t.Fatalf("version = %q", got)
	}
}

func TestVersionDirsFindsMacJDKBundles(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "jdk-21.jdk", "Contents", "Home")
	writeDiscoveryFile(t, filepath.Join(sdk, "release"), "JAVA_VERSION=\"21.0.9\"\n")
	writeDiscoveryFile(t, filepath.Join(sdk, "bin", ExecName("java")), "placeholder")
	got := versionDirs(Java, "system", root)
	sdk, _ = filepath.EvalSymlinks(sdk)
	if len(got) != 1 || got[0].Version != "21.0.9" || got[0].Path != sdk {
		t.Fatalf("installs = %+v", got)
	}
}

func TestPathInstallSkipsManagerShims(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	shims := filepath.Join(root, "shims")
	writeDiscoveryFile(t, filepath.Join(shims, "java"), "#!/bin/sh\nprintf 'openjdk 21.0.9\\n'\n")
	t.Setenv("PATH", shims)
	if inst, ok := pathInstall(Java); ok {
		t.Fatalf("shim reported as SDK: %+v", inst)
	}
}
