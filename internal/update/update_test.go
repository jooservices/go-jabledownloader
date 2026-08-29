package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"", "v1.0.0", true},
		{"dev", "v1.0.0", true},
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.1", "v1.0.0", false},
		{"v1.0.0", "v1.0.0", false},
		{"v1.2.0", "v1.10.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.0.0-beta", "v1.0.0", true},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.current, tc.latest); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want [3]int
	}{
		{"v1.2.3", [3]int{1, 2, 3}},
		{"1.2", [3]int{1, 2, 0}},
		{"v1.10.0-rc.1", [3]int{1, 10, 0}},
		{"", [3]int{0, 0, 0}},
	}
	for _, tc := range cases {
		if got := parseVersion(tc.in); got != tc.want {
			t.Errorf("parseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAssetFor(t *testing.T) {
	rel := Release{Assets: []Asset{
		{Name: "jabledownloader_v1.0.0_linux_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_windows_amd64.tar.gz"},
		{Name: "jabledownloader_v1.0.0_linux_arm64.tar.gz"},
	}}
	a := rel.AssetFor()
	if a == nil {
		t.Fatal("expected an asset for this platform")
	}
	if a.Name != "jabledownloader_v1.0.0_linux_arm64.tar.gz" && a.Name != "jabledownloader_v1.0.0_linux_amd64.tar.gz" {
		t.Fatalf("unexpected asset: %q", a.Name)
	}
}
