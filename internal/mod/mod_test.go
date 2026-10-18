package mod

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDescriptorLists(t *testing.T) {
	dir := t.TempDir()
	descriptor := "name=\"Sub Mod\"\n" +
		"tags={ \"Gameplay\" \"Diplomacy\" }\n" +
		"dependencies = {\n\t\"Base Mod\"\n\t\"Other Mod\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "descriptor.mod"), []byte(descriptor), 0644); err != nil {
		t.Fatal(err)
	}
	if got, want := Tags(dir), []string{"Gameplay", "Diplomacy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tags = %q, want %q", got, want)
	}
	if got, want := Dependencies(dir), []string{"Base Mod", "Other Mod"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dependencies = %q, want %q", got, want)
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"[3.12] Gigastructural Engineering & More (4.0)": "Gigastructural Engineering & More",
		"Ethics & Civics Classic - v2.1.3":               "Ethics & Civics Classic",
		"Better Politics Mod (BPM)":                      "Better Politics Mod (BPM)",
		"[4.0]":                                          "[4.0]",
		"More Bureaucrats":                               "More Bureaucrats",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThemeTags(t *testing.T) {
	got := ThemeTags([]string{"Gameplay", "Fixes", "Diplomacy", "Military"})
	if want := []string{"Diplomacy", "Military"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ThemeTags = %q, want %q", got, want)
	}
}
