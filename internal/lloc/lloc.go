// Package lloc drives the external lloc localization tool
// (https://github.com/lintax/lloc) as an optional translation backend.
//
// lloc translates with two cloud models and a judge, validates Paradox script
// syntax and takes its terminology from the game's own official localization.
// It needs Python 3.11+, a checkout of lloc and an Ollama daemon signed in to
// Ollama Cloud; Detect reports whether all three are present.
package lloc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GameInstallDirs maps game IDs to their folder under steamapps/common. The
// whole folder is scanned for localization files, so the layout inside it
// (localisation/, game/localization/, ...) does not matter.
var GameInstallDirs = map[string]string{
	"eu5":       "Europa Universalis V",
	"ck3":       "Crusader Kings III",
	"vic3":      "Victoria 3",
	"hoi4":      "Hearts of Iron IV",
	"stellaris": "Stellaris",
	"imperator": "ImperatorRome",
}

const (
	vanillaModule   = "vanilla"
	modsDir         = "mods"
	outName         = "lloc"
	stampName       = ".stamp"
	consolidatedPat = "*consolidated_l_*"

	// stampVersion is bumped when lloc starts reading mods differently, so
	// that every mod's strings are re-read once.
	stampVersion = "2"
)

// Env is a usable lloc installation.
type Env struct {
	Python string // interpreter command
	Dir    string // lloc checkout (contains lloc.py)
}

// Detect looks for everything the lloc backend needs. It returns nil when any
// part is missing: the backend is then simply not offered.
func Detect() *Env {
	dir := findCheckout()
	if dir == "" {
		return nil
	}
	python := findPython()
	if python == "" || !ollamaSignedIn() {
		return nil
	}
	return &Env{Python: python, Dir: dir}
}

// findCheckout honours LLOC_DIR, then looks for a lloc folder next to the
// binary or the working directory and up to two levels above them.
func findCheckout() string {
	var candidates []string
	if dir := os.Getenv("LLOC_DIR"); dir != "" {
		candidates = append(candidates, dir)
	}
	var bases []string
	if exe, err := os.Executable(); err == nil {
		bases = append(bases, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		bases = append(bases, cwd)
	}
	for _, base := range bases {
		for _, up := range []string{"", "..", filepath.Join("..", "..")} {
			candidates = append(candidates, filepath.Join(base, up, "lloc"))
		}
	}
	for _, dir := range candidates {
		if isCheckout(dir) {
			if abs, err := filepath.Abs(dir); err == nil {
				return abs
			}
		}
	}
	return ""
}

// isCheckout requires the parts this integration relies on, so an lloc
// version without Paradox support does not count.
func isCheckout(dir string) bool {
	for _, rel := range []string{
		"lloc.py",
		filepath.Join("lloc", "adapters", "paradox_yml.py"),
		filepath.Join("prompts", "paradox", "translate.txt"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			return false
		}
	}
	return true
}

func findPython() string {
	for _, name := range []string{"python3", "python"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := exec.CommandContext(ctx, name, "-c",
			"import sys; sys.exit(sys.version_info < (3, 11))").Run()
		cancel()
		if err == nil {
			return name
		}
	}
	return ""
}

// ollamaSignedIn asks the local daemon who is signed in; cloud models are
// only reachable through a signed-in daemon.
func ollamaSignedIn() bool {
	url := os.Getenv("OLLAMA_HOST")
	if url == "" {
		url = "http://localhost:11434"
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(strings.TrimRight(url, "/")+"/api/me", "application/json", nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// Mod is one workshop mod to translate.
type Mod struct {
	ID   string
	Name string
	Path string
	Tags []string
}

// Job translates the mods of one game into one language.
type Job struct {
	Env         *Env
	Workspace   string // lloc workspace of the game (created on demand)
	GameName    string
	GameContext string
	GameDir     string // game install folder; "" or missing = no official examples
	Lang        string // BCP-47 code, e.g. "ru"
	LangName    string // Paradox language name, e.g. "russian"
	LangDisplay string // e.g. "Russian"
	Mods        []Mod

	Log       func(format string, args ...interface{})
	LogUpdate func(format string, args ...interface{}) // replaces the last log line
	Status    func(status string)

	total, done int // strings to translate in this run / finished so far
	started     time.Time
}

// OutputFile is where lloc leaves its translations of a mod.
func (j *Job) OutputFile(modID string) string {
	return filepath.Join(j.Workspace, modsDir, modID, "out", j.LangName,
		outName+"_l_"+j.LangName+".yml")
}

// Run prepares the workspace and translates everything that is missing or
// outdated. It can be interrupted and rerun: lloc saves after every batch.
func (j *Job) Run() error {
	if err := os.MkdirAll(filepath.Join(j.Workspace, "l10n"), 0755); err != nil {
		return err
	}
	hasVanilla := j.prepareVanilla()
	prepared := j.prepareMods()
	if prepared == 0 {
		j.Log("Nothing to translate: no mod has translatable English text.")
		return nil
	}

	members := `"` + modsDir + `/*"`
	if hasVanilla {
		members = `"` + vanillaModule + `", ` + members
	}
	config := "members = [" + members + "]\n\n" +
		"[app]\n" +
		"tone = " + tomlString("matches the official "+j.LangDisplay+" localization of the game") + "\n\n" +
		"[ollama]\n" +
		"prompts_dir = " + tomlString(filepath.Join(j.Env.Dir, "prompts", "paradox")) + "\n"
	if err := os.WriteFile(filepath.Join(j.Workspace, "l10n", "config.toml"), []byte(config), 0644); err != nil {
		return err
	}

	j.Status("Counting strings to translate...")
	j.total, j.done = j.countPending(), 0
	if j.total == 0 {
		j.Log("\nEverything is already translated.")
	} else {
		j.Log("\nTranslating %d strings in %d mods with lloc (two models and a judge)...", j.total, prepared)
		j.Log("%s", j.progressLine())
	}
	j.started = time.Now()
	return j.run(j.Workspace, j.report, "translate", "--lang", j.Lang)
}

// countPending sums the missing and outdated strings over all mods, from the
// table `lloc status` prints per module: "<lang> ok missing stale orphan".
func (j *Job) countPending() int {
	pending := 0
	// status exits non-zero whenever there is work; only its output matters
	j.run(j.Workspace, func(line string) {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == j.Lang {
			missing, err1 := strconv.Atoi(fields[2])
			stale, err2 := strconv.Atoi(fields[3])
			if err1 == nil && err2 == nil {
				pending += missing + stale
			}
		}
	}, "status", "--lang", j.Lang)
	return pending
}

func (j *Job) progressLine() string {
	if j.total == 0 {
		return "  Progress: —"
	}
	line := fmt.Sprintf("  Progress: %d/%d strings (%d%%)", j.done, j.total, j.done*100/j.total)
	if j.done > 0 && j.done < j.total {
		remaining := time.Duration(float64(time.Since(j.started)) / float64(j.done) * float64(j.total-j.done))
		line += ", " + formatDuration(remaining) + " left"
	}
	return line
}

func formatDuration(d time.Duration) string {
	minutes := int(d.Round(time.Minute).Minutes())
	switch {
	case minutes < 1:
		return "less than a minute"
	case minutes < 60:
		return fmt.Sprintf("about %d min", minutes)
	default:
		return fmt.Sprintf("about %dh %02dm", minutes/60, minutes%60)
	}
}

// prepareVanilla imports the game's own localization as the reference corpus.
// Returns false when the game folder is unknown — translation then runs
// without official examples.
func (j *Job) prepareVanilla() bool {
	dir := filepath.Join(j.Workspace, vanillaModule)
	if j.GameDir == "" {
		os.RemoveAll(dir)
		return false
	}
	if _, err := os.Stat(j.GameDir); err != nil {
		j.Log("Game folder not found (%s): translating without official examples.", j.GameDir)
		os.RemoveAll(dir)
		return false
	}
	config := "reference = true\n\n" + j.appSection(j.GameName+", the base game.") + outputSection(j.GameDir)
	changed := writeIfChanged(filepath.Join(dir, "l10n.toml"), config)

	if _, err := os.Stat(filepath.Join(dir, "strings.toml")); err != nil || changed {
		j.Status("Reading the game's official localization...")
		j.Log("Reading the game's official localization (once per game)...")
		if err := j.run(dir, nil, "import", "--bootstrap-strings", "--overwrite"); err != nil {
			j.Log("  ⚠ Could not read the game's localization: %v", err)
			os.RemoveAll(dir)
			return false
		}
		os.Remove(filepath.Join(dir, stampName))
	}
	if readStamp(dir) != j.Lang {
		if err := j.run(dir, nil, "import", "--lang", j.Lang); err != nil {
			j.Log("  ⚠ Could not import the official %s localization: %v", j.LangDisplay, err)
		}
		writeStamp(dir, j.Lang)
	}
	return true
}

// prepareMods writes one lloc module per mod, refreshing its strings when the
// mod's files changed, and drops modules of mods that are gone.
func (j *Job) prepareMods() int {
	root := filepath.Join(j.Workspace, modsDir)
	os.MkdirAll(root, 0755)

	wanted := make(map[string]bool, len(j.Mods))
	for _, m := range j.Mods {
		wanted[m.ID] = true
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if !wanted[e.Name()] {
				os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}

	prepared := 0
	for i, m := range j.Mods {
		dir := filepath.Join(root, m.ID)
		description := j.GameName + ": " + j.GameContext + ". Mod \"" + m.Name + "\""
		if len(m.Tags) > 0 {
			description += " (tags: " + strings.Join(m.Tags, ", ") + ")"
		}
		config := j.appSection(description+".") + outputSection(m.Path)
		changed := writeIfChanged(filepath.Join(dir, "l10n.toml"), config)

		stamp := stampVersion + " " + j.Lang + " " + sourceSignature(m.Path, j.LangName)
		_, stringsErr := os.Stat(filepath.Join(dir, "strings.toml"))
		if changed || stringsErr != nil || readStamp(dir) != stamp {
			j.Status(fmt.Sprintf("Reading mod %d/%d: %s", i+1, len(j.Mods), m.Name))
			if err := j.run(dir, nil, "import", "--bootstrap-strings", "--overwrite"); err != nil {
				// typically a mod whose English text is all script references
				os.RemoveAll(dir)
				continue
			}
			if err := j.run(dir, nil, "import", "--lang", j.Lang); err != nil {
				j.Log("  ⚠ %s: could not import the mod's own translation: %v", m.Name, err)
			}
			writeStamp(dir, stamp)
		}
		prepared++
	}
	return prepared
}

// appSection carries the languages in every module, not in the workspace
// config: modules are read on their own before the workspace is complete.
func (j *Job) appSection(description string) string {
	return "[app]\n" +
		"description = " + tomlString(description) + "\n" +
		"base_language = \"en\"\n" +
		"languages = [" + tomlString(j.Lang) + "]\n\n"
}

func outputSection(sourceDir string) string {
	return "[output]\nadapter = \"paradox-yml\"\n\n[output.paradox-yml]\n" +
		"source_dir = " + tomlString(sourceDir) + "\n" +
		"out_name = " + tomlString(outName) + "\n" +
		"exclude = [" + tomlString(consolidatedPat) + "]\n"
}

// sourceSignature changes whenever a localization file lloc reads from the
// mod is added, removed or modified.
func sourceSignature(modPath, langName string) string {
	count := 0
	var newest time.Time
	var size int64
	filepath.Walk(modPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := strings.ToLower(info.Name())
		if matched, _ := filepath.Match(consolidatedPat, name); matched {
			return nil
		}
		if strings.HasSuffix(name, "_l_english.yml") || strings.HasSuffix(name, "_l_"+langName+".yml") {
			count++
			size += info.Size()
			if info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
		return nil
	})
	return fmt.Sprintf("%d %d %d", count, size, newest.UnixNano())
}

func readStamp(dir string) string {
	data, _ := os.ReadFile(filepath.Join(dir, stampName))
	return string(data)
}

func writeStamp(dir, stamp string) {
	os.WriteFile(filepath.Join(dir, stampName), []byte(stamp), 0644)
}

// writeIfChanged reports whether the file had to be (re)written.
func writeIfChanged(path, content string) bool {
	if old, err := os.ReadFile(path); err == nil && string(old) == content {
		return false
	}
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(content), 0644)
	return true
}

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// run executes one lloc command on a workspace or module directory, passing
// its output to onLine. Without onLine the output is kept only to explain a
// failure.
func (j *Job) run(dir string, onLine func(line string), args ...string) error {
	cmd := exec.Command(j.Env.Python, append([]string{filepath.Join(j.Env.Dir, "lloc.py"), "--dir", dir}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTHONIOENCODING=utf-8")
	hideWindow(cmd)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	setRunning(cmd)
	defer setRunning(nil)

	var tail []string
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		tail = append(tail, line)
		if len(tail) > 15 {
			tail = tail[1:]
		}
		if onLine != nil {
			onLine(line)
		}
	}
	if err := cmd.Wait(); err != nil {
		if onLine != nil {
			return fmt.Errorf("lloc %s: %w", args[0], err)
		}
		return fmt.Errorf("lloc %s: %w\n%s", args[0], err, strings.Join(tail, "\n"))
	}
	return nil
}

var running struct {
	sync.Mutex
	cmd *exec.Cmd
}

func setRunning(cmd *exec.Cmd) {
	running.Lock()
	running.cmd = cmd
	running.Unlock()
}

// Stop ends the lloc command in progress, if any. Call it when the
// application exits: a child process would otherwise keep translating in the
// background. lloc saves after every batch, so nothing is lost.
func Stop() {
	running.Lock()
	defer running.Unlock()
	if running.cmd == nil || running.cmd.Process == nil {
		return
	}
	// lloc finishes cleanly on an interrupt; Windows has no such signal
	if runtime.GOOS == "windows" || running.cmd.Process.Signal(os.Interrupt) != nil {
		running.cmd.Process.Kill()
	}
}

// "[mods/<id>:ru] A+B 3/21: +20 judged": batch 3 of 21 of a mod is finished
var batchDoneRe = regexp.MustCompile(`^\[` + modsDir + `/([^:\]]+)[^\]]*\] A\+B (\d+)/(\d+): \+(\d+) judged`)

// report turns the output of `lloc translate` into one progress line that is
// updated in place, a line per finished mod, and any problems lloc reports.
// Per-batch and per-string detail is left out: a full run has thousands.
func (j *Job) report(line string) {
	if m := batchDoneRe.FindStringSubmatch(line); m != nil {
		count, _ := strconv.Atoi(m[4])
		j.done += count
		if j.done > j.total {
			j.total = j.done
		}
		if m[2] == m[3] {
			j.note("  ✓ " + j.modName(m[1]) + " translated")
		} else {
			j.LogUpdate("%s", j.progressLine())
		}
		j.Status(fmt.Sprintf("Translating: %d/%d strings — %s", j.done, j.total, j.modName(m[1])))
		return
	}
	switch {
	case strings.HasPrefix(line, "error"), strings.HasPrefix(line, "server throttling"),
		strings.HasPrefix(line, "interrupted"), strings.HasPrefix(line, "Traceback"),
		strings.Contains(line, "looks weak"), strings.Contains(line, "FAILED"):
		j.note("  " + line)
	}
}

// note logs a line above the progress line.
func (j *Job) note(line string) {
	j.LogUpdate("%s", line)
	j.Log("%s", j.progressLine())
}

func (j *Job) modName(id string) string {
	for _, m := range j.Mods {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}
