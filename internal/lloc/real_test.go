package lloc

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lintax/paradox-localize/internal/localization"
)

// TestRealPrepare runs the workspace preparation against a real lloc
// checkout (import and status only: no network). It needs LLOC_DIR:
//
//	LLOC_DIR=/path/to/lloc go test ./internal/lloc -run RealPrepare -v
func TestRealPrepare(t *testing.T) {
	dir := os.Getenv("LLOC_DIR")
	if dir == "" || !isCheckout(dir) {
		t.Skip("set LLOC_DIR to an lloc checkout")
	}
	python := findPython()
	if python == "" {
		t.Skip("Python 3.11+ not found")
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Skip(err)
	}
	mk := func(name, body string) string {
		d := t.TempDir()
		p := filepath.Join(d, "localisation", "english", name+"_l_english.yml")
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("\xef\xbb\xbfl_english:\n"+body), 0644)
		return d
	}
	base := mk("base", " base_tech:0 \"Hyper Relay\"\n base_tech_desc:0 \"A network of relays that lets fleets jump.\"\n")
	sub := mk("sub", " sub_building:0 \"Relay Hub\"\n")
	other := mk("other", " other_law:0 \"Free Trade\"\n")

	ws := t.TempDir()
	job := func(deps map[string][]string) *Job {
		mods := []Mod{
			{ID: "100", Name: "Base", Path: base, Strings: 2, Chars: 60},
			{ID: "200", Name: "Sub", Path: sub, Strings: 1, Chars: 9},
			{ID: "300", Name: "Other", Path: other, Strings: 1, Chars: 10},
		}
		AssignGroups(mods, deps)
		return &Job{Env: &Env{Python: python, Dir: dir}, Workspace: ws, GameName: "Stellaris",
			GameContext: "space", Lang: "ru", LangName: "russian", LangDisplay: "Russian", Mods: mods,
			Log: t.Logf, LogUpdate: t.Logf, Status: func(string) {}}
	}
	check := func(label string, j *Job) {
		t.Helper()
		n, err := j.prepare()
		if err != nil || n != 3 {
			t.Fatalf("%s: prepare = %d, %v; want 3 mods", label, n, err)
		}
		if got := j.countPending(); got != 4 {
			t.Errorf("%s: pending = %d, want 4", label, got)
		}
	}

	check("first run", job(map[string][]string{"200": {"Base"}}))
	// the workspace config now exists: modules load the whole workspace
	check("second run", job(map[string][]string{"200": {"Base"}}))
	// groups change: Sub now extends Other; the old group file goes away
	check("regrouped", job(map[string][]string{"200": {"Other"}}))
	if _, err := os.Stat(filepath.Join(ws, "l10n", groupFilePref+"100.toml")); err == nil {
		t.Error("the file of a group that no longer exists was kept")
	}
	if _, err := os.Stat(filepath.Join(ws, "l10n", groupFilePref+"300.toml")); err != nil {
		t.Errorf("group file missing: %v", err)
	}

	// a translation saved by lloc reaches the output file under the same key
	// our scanner reads from the mod, also without a full translate run
	j := job(map[string][]string{"200": {"Other"}})
	if err := j.run(filepath.Join(ws, modsDir, "100"), nil, "set", "--lang", "ru", "base_tech", "Гиперреле"); err != nil {
		t.Fatalf("lloc set: %v", err)
	}
	j.build()
	out, err := localization.ParseLocalizationFile(j.OutputFile("100"))
	if err != nil {
		t.Fatalf("output file: %v", err)
	}
	english, _, _, _ := localization.ScanModLocalization(base, "russian", false)
	for key, text := range out {
		if _, ok := english[key]; !ok {
			t.Errorf("output key %q is not a key of the mod's English file (%v)", key, english)
		}
		if key == "base_tech" && text != "Гиперреле" {
			t.Errorf("base_tech = %q", text)
		}
	}
	if len(out) != 1 {
		t.Errorf("output = %v, want exactly the one saved translation", out)
	}
}
