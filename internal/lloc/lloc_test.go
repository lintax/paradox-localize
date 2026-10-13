package lloc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTomlString(t *testing.T) {
	cases := map[string]string{
		`plain`:               `"plain"`,
		`Mod "Name"`:          `"Mod \"Name\""`,
		`C:\Steam\mods`:       `"C:\\Steam\\mods"`,
		"line\nbreak\ttabbed": `"line\nbreak\u0009tabbed"`,
	}
	for in, want := range cases {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestReportProgress(t *testing.T) {
	var log []string
	j := &Job{
		Mods:  []Mod{{ID: "123", Name: "Big Mod"}},
		total: 60,
		Log:   func(format string, args ...interface{}) { log = append(log, fmt.Sprintf(format, args...)) },
		LogUpdate: func(format string, args ...interface{}) {
			log[len(log)-1] = fmt.Sprintf(format, args...)
		},
		Status: func(string) {},
	}
	j.Log("%s", j.progressLine())
	for _, line := range []string{
		"[mods/123:ru] A+B 1/3: translating 20 strings (2 models)...",
		"[mods/123:ru] ARBITRATED key: chose \"x\" over 2 candidate(s) (ensemble c1)",
		"[mods/123:ru] C 1/3: agree 19, c1 1, c2 0 in 0.8s",
		"[mods/123:ru] A+B 1/3: +20 judged",
		"[mods/123:ru] A+B 2/3: +20 judged",
		"server throttling (429): concurrency limit -> 44",
		"[mods/123:ru] A+B 3/3: +20 judged",
	} {
		j.report(line)
	}
	want := []string{
		"  server throttling (429): concurrency limit -> 44",
		"  ✓ Big Mod translated",
		"  Progress: 60/60 strings (100%)",
	}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Errorf("log = %q, want %q", log, want)
	}
}

func TestSourceSignature(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(dir, "localisation", name)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("english/a_l_english.yml", "l_english:\n")
	before := sourceSignature(dir, "russian")

	// our own output and other languages do not count
	write("russian/0_1_consolidated_l_russian.yml", "l_russian:\n")
	write("german/a_l_german.yml", "l_german:\n")
	if got := sourceSignature(dir, "russian"); got != before {
		t.Errorf("signature changed by ignored files: %s -> %s", before, got)
	}

	write("russian/a_l_russian.yml", "l_russian:\n")
	if got := sourceSignature(dir, "russian"); got == before {
		t.Error("signature did not change when the mod gained a translation")
	}
}

func TestIsCheckout(t *testing.T) {
	dir := t.TempDir()
	if isCheckout(dir) {
		t.Error("empty directory accepted as an lloc checkout")
	}
	for _, rel := range []string{"lloc.py", "lloc/adapters/paradox_yml.py", "prompts/paradox/translate.txt"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, nil, 0644)
	}
	if !isCheckout(dir) {
		t.Error("complete checkout rejected")
	}
}

// TestJobIntegration runs a real translation. It calls paid cloud models, so
// it only runs on request:
//
//	LLOC_TEST_MODS=/path/to/mod1:/path/to/mod2 LLOC_TEST_GAME_DIR=/path/to/Stellaris go test ./internal/lloc -run Integration -v
func TestJobIntegration(t *testing.T) {
	paths := os.Getenv("LLOC_TEST_MODS")
	if paths == "" {
		t.Skip("LLOC_TEST_MODS not set")
	}
	env := Detect()
	if env == nil {
		t.Fatal("lloc is not available (python 3.11+, lloc checkout, signed-in Ollama)")
	}
	workspace := os.Getenv("LLOC_TEST_WORKSPACE")
	if workspace == "" {
		workspace = t.TempDir()
	}
	job := &Job{
		Env: env, Workspace: workspace,
		GameName: "Stellaris", GameContext: "a sci-fi grand strategy game set in space",
		GameDir: os.Getenv("LLOC_TEST_GAME_DIR"),
		Lang:    "ru", LangName: "russian", LangDisplay: "Russian",
		Log:       func(format string, args ...interface{}) { t.Logf(format, args...) },
		LogUpdate: func(format string, args ...interface{}) { t.Logf(format, args...) },
		Status:    func(string) {},
	}
	for _, p := range filepath.SplitList(paths) {
		job.Mods = append(job.Mods, Mod{ID: filepath.Base(p), Name: "Mod " + filepath.Base(p), Path: p})
	}
	if err := job.Run(); err != nil {
		t.Fatal(err)
	}
	for _, m := range job.Mods {
		data, err := os.ReadFile(job.OutputFile(m.ID))
		if _, statErr := os.Stat(filepath.Join(workspace, modsDir, m.ID)); statErr != nil {
			t.Logf("%s: nothing translatable, no module", m.ID)
			continue
		}
		if err != nil {
			t.Errorf("%s: no output: %v", m.ID, err)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		t.Log(fmt.Sprintf("%s: %d translated lines, e.g. %s", m.ID, len(lines)-1, lines[len(lines)-1]))
	}
}
