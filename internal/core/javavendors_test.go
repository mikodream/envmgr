package core

import (
	"strings"
	"testing"
)

// TestNormalizeJavaVersion checks that the same release reported by different
// vendors lands on one version string, which is what lets the page show a
// version × vendor matrix instead of duplicate rows.
func TestNormalizeJavaVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"21.0.9", "21.0.9"},
		{"21.0.13", "21.0.13"},
		{"21.0.13.0.11", "21.0.13"}, // Corretto's four-part version
		{"21.0.13.0.2", "21.0.13"},  // Microsoft's
		{"21.0.13+11", "21.0.13"},   // Liberica's build suffix
		{"27", "27.0.0"},            // a vendor that only names the major
		{"27.0", "27.0.0"},
		{" 25.0.5 ", "25.0.5"},
		// Java 8, spelled four ways by four vendors, all one release.
		{"8.0.472", "8.0.472"},    // Temurin and Zulu
		{"8.512.09.1", "8.0.512"}, // Corretto: the update is the second field
		{"8u504+1", "8.0.504"},    // Liberica
		{"8u504", "8.0.504"},      // Liberica without a build suffix
		{"1.8.0_412", "8.0.412"},  // the legacy spelling
	}
	for _, c := range cases {
		if got := normalizeJavaVersion(c.in); got != c.want {
			t.Errorf("normalizeJavaVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNormalizeJava8LinesUp is the point of the spellings above: one release
// reported by three vendors has to become one row, not three.
func TestNormalizeJava8LinesUp(t *testing.T) {
	same := []string{"8.0.472", "8.472.09.1", "8u472+7"}
	first := normalizeJavaVersion(same[0])
	if first != "8.0.472" {
		t.Fatalf("baseline normalised to %q", first)
	}
	for _, v := range same[1:] {
		if got := normalizeJavaVersion(v); got != first {
			t.Errorf("%q normalised to %q, want %q", v, got, first)
		}
	}
}

// TestNumericVersionKeepsThreeParts pins the shape: a major-only input must
// still come back as major.minor.patch so it sorts with the others.
func TestNumericVersionKeepsThreeParts(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{[]int{27}, "27.0.0"},
		{[]int{21, 0, 13, 0, 11}, "21.0.13"},
		{[]int{8, 0, 472}, "8.0.472"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := numericVersion(c.in); got != c.want {
			t.Errorf("numericVersion(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRemoteInstallName lets two vendors' builds of one version coexist: the
// default vendor keeps the plain directory, the others are suffixed.
func TestRemoteInstallName(t *testing.T) {
	plain := Remote{Kind: Java, Version: "21.0.9", Vendor: DefaultVendor}
	if got := plain.InstallName(); got != "21.0.9" {
		t.Errorf("default vendor install name = %q, want the plain version", got)
	}
	other := Remote{Kind: Java, Version: "21.0.9", Vendor: "zulu"}
	if got := other.InstallName(); got != "21.0.9-zulu" {
		t.Errorf("zulu install name = %q, want 21.0.9-zulu", got)
	}
	single := Remote{Kind: Node, Version: "24.13.0"}
	if got := single.InstallName(); got != "24.13.0" {
		t.Errorf("vendor-less install name = %q", got)
	}
	if plain.InstallName() == other.InstallName() {
		t.Fatal("two vendors would unpack into the same directory")
	}
}

// TestVendorURLParsing covers the vendors that only reveal their version
// through the redirect target.
func TestVendorURLParsing(t *testing.T) {
	corretto := "https://corretto.aws/downloads/resources/21.0.13.0.11/amazon-corretto-21.0.13.0.11-windows-x64-jdk.zip"
	if got := correttoVersionFromURL(corretto); got != "21.0.13.0.11" {
		t.Errorf("corretto version = %q", got)
	}
	ms := "https://download.visualstudio.microsoft.com/download/pr/x/y/microsoft-jdk-21.0.13.0.2-windows-x64.zip"
	if got := microsoftVersionFromURL(ms); got != "21.0.13.0.2" {
		t.Errorf("microsoft version = %q", got)
	}
	if got := correttoVersionFromURL("https://example.com/nope.zip"); got != "" {
		t.Errorf("corretto version of a foreign URL = %q, want empty", got)
	}
	if got := microsoftVersionFromURL("https://example.com/nope.zip"); got != "" {
		t.Errorf("microsoft version of a foreign URL = %q, want empty", got)
	}
}

// TestZuluFilterKeepsOnlyThePlainJDK makes sure the CRaC and JavaFX flavours do
// not turn into duplicate entries of the same version.
func TestZuluFilterKeepsOnlyThePlainJDK(t *testing.T) {
	keep := []string{
		"zulu21.54.109-ca-jdk21.0.13.0.3-win_x64.zip",
		"zulu8.90.0.15-ca-jdk8.0.472-win_x64.zip",
	}
	drop := []string{
		"zulu21.54.109-ca-crac-jdk21.0.13.0.3-win_x64.zip",
		"zulu21.54.109-ca-fx-jdk21.0.13.0.3-win_x64.zip",
		"zulu21.54.109-ca-jre21.0.13.0.3-win_x64.zip",
		"zulu21.54.109-ca-jdk21.0.13-src.zip",
	}
	for _, name := range keep {
		if !isPlainZuluJDK(name) {
			t.Errorf("%q should be kept", name)
		}
	}
	for _, name := range drop {
		if isPlainZuluJDK(name) {
			t.Errorf("%q should be dropped", name)
		}
	}
}

// TestLibericaArchiveChoice picks the archive the installer can unpack: not an
// installer package, not the full or lite bundle, and the right arch.
func TestLibericaArchiveChoice(t *testing.T) {
	good := libericaRelease{
		Version: "21.0.13+11", DownloadURL: "https://x/bellsoft-jdk21.0.13+11-windows-amd64.zip",
		PackageType: "zip", BundleType: "jdk", Architecture: "x86", Bitness: 64, OS: "windows",
	}
	if !libericaArchiveFor(good, "windows", "x86") {
		t.Fatal("plain zip JDK should be accepted")
	}
	msi := good
	msi.PackageType = "msi"
	if libericaArchiveFor(msi, "windows", "x86") {
		t.Error("an installer package cannot be unpacked")
	}
	full := good
	full.BundleType = "jdk-full"
	if libericaArchiveFor(full, "windows", "x86") {
		t.Error("the full bundle is not the plain JDK")
	}
	wrongArch := good
	wrongArch.Bitness = 32
	if libericaArchiveFor(wrongArch, "windows", "x86") {
		t.Error("a 32-bit build is not for this machine")
	}
}

// TestResolveRelative resolves the Location headers the vendor redirects use.
func TestResolveRelative(t *testing.T) {
	cases := []struct{ base, loc, want string }{
		{"https://a.example/x/y", "https://b.example/z", "https://b.example/z"},
		{"https://a.example/x/y", "/z/w", "https://a.example/z/w"},
		{"https://a.example/x/y", "z", "https://a.example/x/z"},
	}
	for _, c := range cases {
		if got := resolveRelative(c.base, c.loc); got != c.want {
			t.Errorf("resolveRelative(%q, %q) = %q, want %q", c.base, c.loc, got, c.want)
		}
	}
}

// TestJavaVendorRegistry keeps the source list honest: every vendor needs an
// id, a readable name and a homepage, and the default vendor must be in it.
func TestJavaVendorRegistry(t *testing.T) {
	vendors := JavaVendors()
	if len(vendors) < 4 {
		t.Fatalf("%d vendors, want the sources the page promises", len(vendors))
	}
	seen := map[string]bool{}
	for _, v := range vendors {
		if v.ID == "" || v.Name == "" || v.Home == "" || v.All == nil {
			t.Errorf("incomplete vendor: %+v", v)
		}
		if seen[v.ID] {
			t.Errorf("duplicate vendor id %q", v.ID)
		}
		seen[v.ID] = true
		if got := JavaVendorName(v.ID); got != v.Name {
			t.Errorf("JavaVendorName(%q) = %q, want %q", v.ID, got, v.Name)
		}
	}
	if !seen[DefaultVendor] {
		t.Errorf("default vendor %q is not in the source list", DefaultVendor)
	}
	names := JavaVendorNames()
	if len(names) != len(vendors) {
		t.Errorf("names = %d, vendors = %d", len(names), len(vendors))
	}
}

// TestJavaMajorsIncludesOldLTS guards the bug this work started from: Java 8,
// 11 and 17 were missing because the list was cut to the eight newest majors.
func TestJavaMajorsIncludesOldLTS(t *testing.T) {
	// The LTS table must not depend on the network: check it directly.
	for _, want := range []int{8, 11, 17, 21, 25} {
		found := false
		for _, m := range javaLTSMajors {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Errorf("javaLTSMajors is missing %d", want)
		}
		if !isJavaLTS(want) {
			t.Errorf("%d should be marked LTS", want)
		}
	}
	if isJavaLTS(22) {
		t.Error("22 is not an LTS release")
	}
}

// TestUniqueStrings keeps the failure summary readable.
func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"a", "b", "a", "c", "b"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("uniqueStrings = %v", got)
	}
}
