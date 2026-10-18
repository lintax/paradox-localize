package lloc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeLloc is a stand-in for lloc.py, driven by the FAKE_MODE variable:
//
//	sleep       - every command hangs until interrupted
//	flaky       - translate fails with a FATAL line once, then succeeds
//	bad-import  - import --bootstrap-strings fails
//	bad-config  - translate exits with a config error
const fakeLloc = `
import os, sys, time, signal
args = sys.argv[1:]
d = args[args.index("--dir") + 1]
cmd = args[args.index("--dir") + 2]
mode = os.environ.get("FAKE_MODE", "")
state = os.path.join(os.environ["FAKE_STATE"], "attempts")

if mode == "sleep":
    def stop(*_):
        print("interrupted — progress saved, rerun to continue", flush=True)
        os._exit(130)
    signal.signal(signal.SIGINT, stop)
    open(os.path.join(os.environ["FAKE_STATE"], "started"), "w").close()
    time.sleep(60)
    sys.exit(0)

if cmd == "import":
    if "--bootstrap-strings" in args:
        if mode == "bad-import":
            print("error: cannot parse the base file")
            sys.exit(2)
        if mode == "no-strings":
            print("error: no translatable strings found in the base file")
            sys.exit(2)
        open(os.path.join(d, "strings.toml"), "w").write("")
    sys.exit(0)
if cmd == "status":
    # after the first translate attempt 8 of the 20 strings are done: the 2
    # it gave up on are pending again
    n = int(open(state).read()) if os.path.exists(state) else 0
    print("lang ok missing stale orphan")
    print("ru 8 12 0 0" if n else "ru 0 20 0 0")
    sys.exit(1)
if cmd == "translate":
    n = int(open(state).read()) if os.path.exists(state) else 0
    open(state, "w").write(str(n + 1))
    if mode == "flaky" and n == 0:
        print("[mods/1:ru] batch 1/2: translating 10 strings...", flush=True)
        print("[mods/1:ru] batch 1/2: +8 in 3.2s, failed 2", flush=True)
        print("[mods/1:ru] FATAL connection to Ollama lost", flush=True)
        sys.exit(1)
    if mode == "bad-config":
        print("error: l10n/config.toml: bad key", flush=True)
        sys.exit(2)
    if mode == "flaky":
        print("[mods/1:ru] batch 2/2: translating 10 strings...", flush=True)
        print("[mods/1:ru] batch 2/2: +11 in 2.9s, failed 1", flush=True)
        print("[mods/1:ru] FAILED some_key: wrong script", flush=True)
        sys.exit(1)
    sys.exit(0)
`

func fakeJob(t *testing.T, mode string) (*Job, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	dir, state, ws, modDir := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lloc.py"), []byte(fakeLloc), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_MODE", mode)
	t.Setenv("FAKE_STATE", state)
	var log []string
	j := &Job{
		Env:       &Env{Python: python, Dir: dir},
		Workspace: ws,
		GameName:  "Stellaris", GameContext: "space", Lang: "ru", LangName: "russian", LangDisplay: "Russian",
		Mods: []Mod{{ID: "1", Name: "Mod", Path: modDir, Strings: 20, Chars: 400}},
		Log:  func(f string, a ...interface{}) { log = append(log, fmt.Sprintf(f, a...)) },
		LogUpdate: func(f string, a ...interface{}) {
			if len(log) > 0 {
				log[len(log)-1] = fmt.Sprintf(f, a...)
			}
		},
		Status: func(string) {},
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("log:\n%s", strings.Join(log, "\n"))
		}
	})
	return j, state
}

func TestStopInterruptsRunningCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses SIGINT")
	}
	j, state := fakeJob(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	j.Ctx = ctx

	errc := make(chan error, 1)
	go func() { errc <- j.run(j.Workspace, nil, "translate") }()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(filepath.Join(state, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake lloc did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("run = %v, want context.Canceled", err)
		}
		if time.Since(start) > 5*time.Second {
			t.Errorf("stopping took %s: the interrupt was not delivered", time.Since(start))
		}
	case <-time.After(waitDelay + 5*time.Second):
		t.Fatal("run did not return after cancel")
	}

	// a stopped job starts nothing new
	if err := j.run(j.Workspace, nil, "status"); !errors.Is(err, context.Canceled) {
		t.Errorf("run after stop = %v, want context.Canceled", err)
	}
}

func TestRunRestartsAfterFatalError(t *testing.T) {
	j, state := fakeJob(t, "flaky")
	if err := j.Run(); err != nil {
		t.Fatalf("Run = %v, want success after a restart (FAILED strings alone are not an error)", err)
	}
	data, _ := os.ReadFile(filepath.Join(state, "attempts"))
	if string(data) != "2" {
		t.Errorf("translate ran %s times, want 2", data)
	}
	if j.done != 20 || j.total != 20 {
		t.Errorf("progress %d/%d, want 20/20 (failed strings count as done)", j.done, j.total)
	}
}

func TestRunReportsOtherErrors(t *testing.T) {
	j, state := fakeJob(t, "bad-config")
	if err := j.Run(); err == nil {
		t.Fatal("Run succeeded although lloc exited with a config error")
	}
	if data, _ := os.ReadFile(filepath.Join(state, "attempts")); string(data) != "1" {
		t.Errorf("translate ran %s times, want 1 (a config error is not worth a restart)", data)
	}
}

func TestFailedImportKeepsTranslations(t *testing.T) {
	j, _ := fakeJob(t, "bad-import")
	locale := filepath.Join(j.Workspace, modsDir, "1", "locales", "ru.json")
	os.MkdirAll(filepath.Dir(locale), 0755)
	os.WriteFile(locale, []byte(`{"strings":{}}`), 0644)

	if n := j.prepareMods(); n != 0 {
		t.Errorf("prepared %d mods, want 0", n)
	}
	if _, err := os.Stat(locale); err != nil {
		t.Errorf("translations of a mod lloc could not read were deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(j.Workspace, modsDir, "1", "l10n.toml")); err == nil {
		t.Error("the skipped mod still has a module config: lloc would pick it up")
	}
}

func TestModWithoutStringsIsNotAWarning(t *testing.T) {
	j, _ := fakeJob(t, "no-strings")
	var logged []string
	j.Log = func(f string, a ...interface{}) { logged = append(logged, fmt.Sprintf(f, a...)) }
	if n := j.prepareMods(); n != 0 {
		t.Errorf("prepared %d mods, want 0", n)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "nothing to translate") {
		t.Errorf("log = %q, want one calm \"nothing to translate\" line", logged)
	}
}

func TestAssignGroups(t *testing.T) {
	mods := []Mod{{ID: "base", Name: "Base Mod"}, {ID: "sub", Name: "Sub"}, {ID: "subsub", Name: "Sub Sub"},
		{ID: "alone", Name: "Alone"}, {ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	AssignGroups(mods, map[string][]string{
		"sub":    {"base mod"},           // names match case-insensitively
		"subsub": {"Sub", "Missing Mod"}, // a chain leads to its root
		"alone":  {"Not Installed"},
		"a":      {"B"}, "b": {"A"}, // a cycle must not hang
	})
	want := map[string]string{"base": "base", "sub": "base", "subsub": "base", "alone": ""}
	for _, m := range mods {
		if w, ok := want[m.ID]; ok && m.Group != w {
			t.Errorf("%s: group %q, want %q", m.ID, m.Group, w)
		}
	}
}

func TestBatchSize(t *testing.T) {
	cases := []struct {
		strings, chars, want int
	}{
		{0, 0, 0},        // unknown: lloc default
		{100, 3000, 20},  // short names: the maximum
		{100, 40000, 10}, // 400-character descriptions
		{100, 200000, 4}, // very long texts: the minimum
	}
	for _, c := range cases {
		if got := batchSize(Mod{Strings: c.strings, Chars: c.chars}); got != c.want {
			t.Errorf("batchSize(%d strings, %d chars) = %d, want %d", c.strings, c.chars, got, c.want)
		}
	}
}

func TestModConfig(t *testing.T) {
	j := &Job{GameName: "Crusader Kings III", GameContext: "a medieval game", Lang: "ru",
		LangDisplay: "Russian", StyleNotes: "Real names use established spelling."}
	config := j.modConfig(Mod{ID: "7", Name: "Sub", Path: "/m", Group: "3", Tags: []string{"Religion"},
		Strings: 10, Chars: 5000})
	for _, want := range []string{
		`group = "g_3"`,
		`Game-specific rules: Real names use established spelling.`,
		`official Russian localization`,
		`Mod: \"Sub\" (about: Religion).`,
		"batch_size = 8",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config lacks %q:\n%s", want, config)
		}
	}
}
