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
		"[mods/123:ru] A+B 2/3: +18 judged, failed 2",
		"server throttling (429): concurrency limit -> 44",
		"retrying after Ollama request timed out after 600s (attempt 2/3)",
		"[mods/123:ru] A+B 3/3: +20 judged, failed 2",
		"[mods/123:ru] FAILED key_a: wrong script",
		"[mods/123:ru] FAILED key_b: wrong script",
	} {
		j.report(line)
	}
	want := []string{
		"  server throttling (429): concurrency limit -> 44",
		"  retrying after Ollama request timed out after 600s (attempt 2/3)",
		"  ✓ Big Mod translated",
		"  Progress: 60/60 strings (100%)",
	}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Errorf("log = %q, want %q", log, want)
	}
	if j.failedKeys != 2 || j.fatal {
		t.Errorf("failedKeys = %d, fatal = %v; want 2, false", j.failedKeys, j.fatal)
	}
	if len(j.active) != 0 {
		t.Errorf("finished mod still counted as in progress: %v", j.active)
	}
}

// One model: "failed" in a batch line counts that batch only.
func TestReportProgressOneModel(t *testing.T) {
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
		"[mods/123:ru] batch 1/3: translating 20 strings...",
		"[mods/123:ru] batch 1/3: +18 in 12.3s, failed 2",
		"[mods/123:ru] batch 2/3: translating 20 strings...",
		"[mods/123:ru] batch 2/3: still working, 30s...",
		"[mods/123:ru] batch 2/3: +20 in 41.0s",
		"[mods/123:ru] batch 3/3: translating 20 strings...",
		"[mods/123:ru] batch 3/3: +19 in 9.8s, failed 1",
		"[mods/123:ru] FAILED key_a: placeholder $X$ missing",
	} {
		j.report(line)
	}
	if j.done != 60 {
		t.Errorf("done = %d, want 60", j.done)
	}
	want := []string{"  ✓ Big Mod translated", "  Progress: 60/60 strings (100%)"}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Errorf("log = %q, want %q", log, want)
	}
	if len(j.active) != 0 || j.failedKeys != 1 {
		t.Errorf("active = %v, failedKeys = %d", j.active, j.failedKeys)
	}
}

// lloc runs the batches of a mod in parallel: the last one may finish first.
func TestReportBatchesOutOfOrder(t *testing.T) {
	var log []string
	j := &Job{Mods: []Mod{{ID: "1", Name: "One"}}, total: 30,
		Log:       func(format string, args ...interface{}) { log = append(log, fmt.Sprintf(format, args...)) },
		LogUpdate: func(format string, args ...interface{}) { log[len(log)-1] = fmt.Sprintf(format, args...) },
		Status:    func(string) {}}
	j.Log("%s", j.progressLine())
	for _, line := range []string{
		"[mods/1:ru] batch 1/3: translating 10 strings...",
		"[mods/1:ru] batch 2/3: translating 10 strings...",
		"[mods/1:ru] batch 3/3: translating 10 strings...",
		"[mods/1:ru] batch 3/3: +10 in 5.0s",
		"[mods/1:ru] batch 1/3: +10 in 6.0s",
	} {
		j.report(line)
	}
	if !j.active["1"] || strings.Contains(strings.Join(log, "|"), "translated") {
		t.Fatalf("mod finished too early: active = %v, log = %q", j.active, log)
	}
	j.report("[mods/1:ru] batch 2/3: +10 in 7.0s")
	if j.active["1"] || !strings.Contains(strings.Join(log, "|"), "✓ One translated") {
		t.Errorf("mod not finished: active = %v, log = %q", j.active, log)
	}
}

func TestStatusLineCountsActiveMods(t *testing.T) {
	var status string
	j := &Job{Mods: []Mod{{ID: "1", Name: "One"}, {ID: "2", Name: "Two"}}, total: 40,
		Log: func(string, ...interface{}) {}, LogUpdate: func(string, ...interface{}) {},
		Status: func(s string) { status = s }}
	j.report("[mods/1:ru] A+B 1/2: translating 20 strings (2 models)...")
	if want := "Translating: 0/40 strings — One"; status != want {
		t.Errorf("status = %q, want %q", status, want)
	}
	j.report("[mods/2:ru] A+B 1/1: translating 20 strings (2 models)...")
	if want := "Translating: 0/40 strings — 2 mods in progress"; status != want {
		t.Errorf("status = %q, want %q", status, want)
	}
	j.report("[mods/2:ru] A+B 1/1: still working, 60s...")
	if want := "Translating: 0/40 strings — 2 mods in progress (waiting for the models, 60s)"; status != want {
		t.Errorf("status = %q, want %q", status, want)
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
