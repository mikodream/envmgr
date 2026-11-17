package app

import (
	"testing"

	"envmgr/internal/core"
)

// TestGroupRemoteMergesVendors is the core of the Java page: one row per
// version, with every vendor build of it inside.
func TestGroupRemoteMergesVendors(t *testing.T) {
	list := []core.Remote{
		{Kind: core.Java, Version: "21.0.9", Vendor: "zulu", VendorName: "Azul Zulu Community", Size: 100},
		{Kind: core.Java, Version: "21.0.9", Vendor: "temurin", VendorName: "Eclipse Temurin", Size: 200},
		{Kind: core.Java, Version: "21.0.9", Vendor: "corretto", VendorName: "Amazon Corretto", Size: 200},
		{Kind: core.Java, Version: "17.0.17", Vendor: "temurin", VendorName: "Eclipse Temurin", Size: 150},
	}
	groups := groupRemote(list)
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2 (one per version)", len(groups))
	}
	// Newest first.
	if groups[0].Version != "21.0.9" || groups[1].Version != "17.0.17" {
		t.Fatalf("order = %s, %s", groups[0].Version, groups[1].Version)
	}
	if len(groups[0].Options) != 3 {
		t.Fatalf("21.0.9 has %d vendors, want 3", len(groups[0].Options))
	}
	// The default vendor is first, so a single-source click is predictable.
	if groups[0].Options[0].Vendor != core.DefaultVendor {
		t.Errorf("first option = %q, want the default vendor", groups[0].Options[0].Vendor)
	}
	if !groups[0].MultiVendor() {
		t.Error("21.0.9 should offer a source choice")
	}
	if groups[1].MultiVendor() {
		t.Error("17.0.17 has one source, so no chooser")
	}
}

// TestGroupRemoteSizeText shows the honest size: a range when the builds
// differ, and nothing when no vendor reports one.
func TestGroupRemoteSizeText(t *testing.T) {
	same := groupRemote([]core.Remote{
		{Kind: core.Java, Version: "21.0.9", Vendor: "temurin", Size: 200 << 20},
		{Kind: core.Java, Version: "21.0.9", Vendor: "zulu", Size: 200 << 20},
	})
	if got := same[0].SizeText(); got != "200.0 MB" {
		t.Errorf("equal sizes = %q", got)
	}

	differ := groupRemote([]core.Remote{
		{Kind: core.Java, Version: "21.0.9", Vendor: "temurin", Size: 200 << 20},
		{Kind: core.Java, Version: "21.0.9", Vendor: "zulu", Size: 208 << 20},
	})
	if got := differ[0].SizeText(); got != "200.0 MB–208.0 MB" {
		t.Errorf("differing sizes = %q", got)
	}

	unknown := groupRemote([]core.Remote{{Kind: core.Java, Version: "21.0.9", Vendor: "zulu"}})
	if got := unknown[0].SizeText(); got != "" {
		t.Errorf("unknown size = %q, want empty rather than 0 B", got)
	}
}

// TestGroupsForHonoursLTSFilter keeps the 仅 LTS switch working on grouped rows.
func TestGroupsForHonoursLTSFilter(t *testing.T) {
	a := newTestApp(t)
	a.downloadKind = kindIndexOf(core.Java)
	a.remote = []core.Remote{
		{Kind: core.Java, Version: "27.0.1", Vendor: "corretto", LTS: false},
		{Kind: core.Java, Version: "21.0.9", Vendor: "corretto", LTS: true},
		{Kind: core.Java, Version: "21.0.9", Vendor: "temurin", LTS: true},
	}
	a.filterLTS = true
	groups := a.groupsFor(core.Java)
	if len(groups) != 1 || groups[0].Version != "21.0.9" {
		t.Fatalf("LTS filter kept %d groups: %+v", len(groups), groups)
	}
	if len(groups[0].Options) != 2 {
		t.Errorf("vendors after filtering = %d, want 2", len(groups[0].Options))
	}

	a.filterLTS = false
	if got := len(a.groupsFor(core.Java)); got != 2 {
		t.Errorf("without the filter = %d groups, want 2", got)
	}
}

// TestShortVendor keeps chips short enough to sit in a row.
func TestShortVendor(t *testing.T) {
	cases := map[string]string{
		"Eclipse Temurin":            "Temurin",
		"Amazon Corretto":            "Amazon",
		"Azul Zulu Community":        "Azul",
		"BellSoft Liberica JDK":      "BellSoft",
		"Microsoft Build of OpenJDK": "Microsoft",
		"Something Else Entirely":    "Something Else Entirely",
	}
	for in, want := range cases {
		if got := shortVendor(in); got != want {
			t.Errorf("shortVendor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestVendorPickerOpensForMultiVendor checks the modal only appears when there
// is a real choice to make.
func TestVendorPickerOpensForMultiVendor(t *testing.T) {
	a := newTestApp(t)
	a.downloadKind = kindIndexOf(core.Java)
	a.remote = []core.Remote{
		{Kind: core.Java, Version: "21.0.9", Vendor: "temurin", VendorName: "Eclipse Temurin"},
		{Kind: core.Java, Version: "21.0.9", Vendor: "zulu", VendorName: "Azul Zulu Community"},
		{Kind: core.Java, Version: "17.0.17", Vendor: "temurin", VendorName: "Eclipse Temurin"},
	}
	a.remoteKind = core.Java
	a.remoteLoaded = true

	a.OpenVendorPicker("17.0.17")
	if a.vendorPick != nil {
		t.Error("a single-source version should install directly, not open a chooser")
	}
	a.OpenVendorPicker("21.0.9")
	if a.vendorPick == nil {
		t.Fatal("21.0.9 has two sources, the chooser should open")
	}
	if len(a.vendorPick.Group.Options) != 2 {
		t.Errorf("chooser offers %d builds, want 2", len(a.vendorPick.Group.Options))
	}
}
