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
	"sort"
	"strconv"
	"strings"
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

	// waitDelay is how long a stopped lloc command may take to exit on its
	// own (it saves and quits on an interrupt) before it is killed.
	waitDelay = 10 * time.Second

	// maxAttempts bounds automatic restarts of a translate run that lloc
	// ended with a fatal error (a dropped connection, a crash): lloc saves
	// after every batch, so a restart continues where it stopped.
	maxAttempts = 3

	// batchChars is the amount of English text aimed for in one request.
	// Long descriptions get smaller batches: a 20-string batch of
	// 1500-character descriptions makes a reply slow enough to time out.
	batchChars    = 4000
	minBatchSize  = 4
	maxBatchSize  = 20
	defaultTone   = "concise in names and interface labels; natural, in the voice of the game, in descriptions and events"
	groupFilePref = "g_"

	// noStringsMsg is how lloc reports a mod with nothing to translate
	noStringsMsg = "no translatable strings found"

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

	// Group is the ID of the mod this one builds on (directly or through
	// other mods), "" for a standalone mod. Mods of a group share
	// terminology and few-shot examples. See AssignGroups.
	Group string

	Strings, Chars int // English strings and their total length
}

// AssignGroups puts every mod that depends on another mod of the list into
// the group of the mod at the root of its dependency chain, so that a
// sub-mod reuses the names and terms of the mod it extends. deps maps a mod
// ID to the names of the mods it requires.
func AssignGroups(mods []Mod, deps map[string][]string) {
	byName := make(map[string]string, len(mods))
	for _, m := range mods {
		byName[strings.ToLower(m.Name)] = m.ID
	}
	parent := func(id string) string {
		for _, name := range deps[id] {
			if p, ok := byName[strings.ToLower(name)]; ok && p != id {
				return p
			}
		}
		return ""
	}
	for i := range mods {
		root, seen := mods[i].ID, map[string]bool{}
		for p := parent(root); p != "" && !seen[p]; p = parent(root) {
			seen[root] = true
			root = p
		}
		if root != mods[i].ID {
			mods[i].Group = root
		}
	}
	// the root itself joins the group it heads
	heads := map[string]bool{}
	for _, m := range mods {
		if m.Group != "" {
			heads[m.Group] = true
		}
	}
	for i := range mods {
		if heads[mods[i].ID] {
			mods[i].Group = mods[i].ID
		}
	}
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
	StyleNotes  string // game-specific translation rules, may be ""
	Mods        []Mod

	// Ctx stops the job: the running lloc command is interrupted (it saves
	// and exits) and nothing further is started. nil = never stopped.
	Ctx context.Context

	Log       func(format string, args ...interface{})
	LogUpdate func(format string, args ...interface{}) // replaces the last log line
	Status    func(status string)

	total, done int // strings to translate in this run / finished so far
	started     time.Time
	active      map[string]bool // mods with a batch in flight
	failed      map[string]int  // per mod: strings lloc gave up on so far
	failedKeys  int             // FAILED lines in the final report of a run
	fatal       bool            // the current attempt hit a fatal error
}

func (j *Job) ctx() context.Context {
	if j.Ctx == nil {
		return context.Background()
	}
	return j.Ctx
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
	prepared, err := j.prepare()
	if err != nil {
		// stopped while reading the mods: earlier runs may still have
		// translations that never made it into the output files
		if _, statErr := os.Stat(filepath.Join(j.Workspace, "l10n", "config.toml")); statErr == nil {
			j.build()
		}
		return err
	}
	if prepared == 0 {
		j.Log("Nothing to translate: no mod has translatable English text.")
		return nil
	}
	// whatever happens from here on, the output files get everything saved,
	// also when an earlier run was stopped before lloc wrote them
	defer j.build()

	j.Status("Counting strings to translate...")
	j.total, j.done = j.countPending(), 0
	if err := j.ctx().Err(); err != nil {
		return err
	}
	if j.total == 0 {
		j.Log("\nEverything is already translated.")
		return nil
	}
	j.Log("\nTranslating %d strings in %d mods with lloc (two models and a judge)...", j.total, prepared)
	j.Log("%s", j.progressLine())
	j.started = time.Now()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		j.active, j.failed, j.failedKeys, j.fatal = map[string]bool{}, map[string]int{}, 0, false
		before := j.done
		err = j.run(j.Workspace, j.report, "translate", "--lang", j.Lang)
		if j.failedKeys > 0 {
			j.note(fmt.Sprintf("  ⚠ %d strings could not be translated reliably and stay in English", j.failedKeys))
		}
		if err == nil || j.ctx().Err() != nil {
			break
		}
		// FAILED strings alone also end with an error exit: a restart would
		// only repeat them. Restart after a fatal error that let some
		// work through, so a dropped connection does not end the whole run.
		if !j.fatal {
			if j.failedKeys > 0 {
				err = nil // only FAILED strings: reported above, not an error
			}
			break
		}
		if attempt == maxAttempts || (attempt > 1 && j.done == before) {
			break
		}
		j.note(fmt.Sprintf("  ⚠ lloc stopped (%v); continuing where it left off (attempt %d/%d)...",
			err, attempt+1, maxAttempts))
	}
	if ctxErr := j.ctx().Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// buildTimeout bounds `lloc build`: it only writes files from what is saved.
const buildTimeout = 2 * time.Minute

// build writes lloc's output files from everything translated so far.
// `translate` does it only at the end of a complete run, so after a stop or
// a failure the files would lack this run's work. It runs even when the job
// was stopped (that is when it matters most), under its own short timeout.
func (j *Job) build() {
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	saved := j.Ctx
	j.Ctx = ctx
	defer func() { j.Ctx = saved }()
	j.Status("Saving translations...")
	if err := j.run(j.Workspace, nil, "build", "--lang", j.Lang); err != nil {
		j.Log("  ⚠ Could not write lloc's output files: %v", firstLine(err))
	}
}

// prepare brings the workspace up to date and returns the number of mods
// ready to translate.
//
// Order matters: as soon as l10n/config.toml exists, every lloc command run
// on a module loads the whole workspace, so whatever the modules name (group
// files) must be on disk before any module is read. Group files of earlier
// runs are pruned only after every module config has been rewritten.
func (j *Job) prepare() (int, error) {
	if err := os.MkdirAll(filepath.Join(j.Workspace, "l10n"), 0755); err != nil {
		return 0, err
	}
	j.writeGroups(false)
	hasVanilla := j.prepareVanilla()
	if err := j.ctx().Err(); err != nil {
		return 0, err
	}
	if err := j.writeWorkspaceConfig(hasVanilla); err != nil {
		return 0, err
	}
	prepared := j.prepareMods()
	if err := j.ctx().Err(); err != nil {
		return 0, err
	}
	j.writeGroups(true)
	return prepared, nil
}

// writeWorkspaceConfig writes l10n/config.toml. The profile and the
// concurrent ensemble are pinned: progress is read from the log lines of
// exactly this mode.
func (j *Job) writeWorkspaceConfig(hasVanilla bool) error {
	members := `"` + modsDir + `/*"`
	if hasVanilla {
		members = `"` + vanillaModule + `", ` + members
	}
	config := "members = [" + members + "]\n\n" +
		"[app]\n" +
		"tone = " + tomlString(defaultTone) + "\n\n" +
		"[ollama]\n" +
		"profile = \"cloud\"\n" +
		"prompts_dir = " + tomlString(filepath.Join(j.Env.Dir, "prompts", "paradox")) + "\n\n" +
		"[validation]\n" +
		"ensemble_concurrency = \"batch\"\n"
	writeIfChanged(filepath.Join(j.Workspace, "l10n", "config.toml"), config)
	return nil
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
	config := "reference = true\n\n" + j.appSection(j.gameDescription()+"\nThe base game itself.", 0) + outputSection(j.GameDir)
	changed := writeIfChanged(filepath.Join(dir, "l10n.toml"), config)

	if _, err := os.Stat(filepath.Join(dir, "strings.toml")); err != nil || changed {
		j.Status("Reading the game's official localization...")
		j.Log("Reading the game's official localization (once per game)...")
		if err := j.run(dir, nil, "import", "--bootstrap-strings", "--overwrite"); err != nil {
			if j.ctx().Err() != nil {
				return false // stopped: keep what is there for the next run
			}
			j.Log("  ⚠ Could not read the game's localization: %v", err)
			os.RemoveAll(dir) // only a copy of the game's files: safe to rebuild
			return false
		}
		os.Remove(filepath.Join(dir, stampName))
	}
	if readStamp(dir) != j.Lang {
		// the stamp is written only on success, so a failed import is retried
		if err := j.run(dir, nil, "import", "--lang", j.Lang); err != nil {
			if j.ctx().Err() != nil {
				return false
			}
			j.Log("  ⚠ Could not import the official %s localization (does the game have it?): %v",
				j.LangDisplay, firstLine(err))
		} else {
			writeStamp(dir, j.Lang)
		}
	}
	return true
}

// prepareMods writes one lloc module per mod, refreshing its strings when the
// mod's files changed, and drops modules of mods that are gone.
//
// A mod lloc cannot read is left out of this run by removing its module
// config only: its translations stay on disk for when the mod is fixed.
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
		if j.ctx().Err() != nil {
			return prepared
		}
		dir := filepath.Join(root, m.ID)
		configPath := filepath.Join(dir, "l10n.toml")
		config := j.modConfig(m)
		changed := writeIfChanged(configPath, config)

		stamp := stampVersion + " " + j.Lang + " " + sourceSignature(m.Path, j.LangName)
		_, stringsErr := os.Stat(filepath.Join(dir, "strings.toml"))
		if changed || stringsErr != nil || readStamp(dir) != stamp {
			j.Status(fmt.Sprintf("Reading mod %d/%d: %s", i+1, len(j.Mods), m.Name))
			if err := j.run(dir, nil, "import", "--bootstrap-strings", "--overwrite"); err != nil {
				if j.ctx().Err() != nil {
					return prepared
				}
				if strings.Contains(err.Error(), noStringsMsg) {
					// name lists, or text that is only script references
					j.Log("  · %s: nothing to translate (only names or script references)", m.Name)
				} else {
					// its earlier translations stay for when it can be read again
					j.Log("  ⚠ %s: skipped, lloc could not read it (%s)", m.Name, firstLine(err))
				}
				os.Remove(configPath)
				os.Remove(filepath.Join(dir, stampName))
				continue
			}
			if err := j.run(dir, nil, "import", "--lang", j.Lang); err != nil {
				if j.ctx().Err() != nil {
					return prepared
				}
				j.Log("  ⚠ %s: could not import the mod's own translation: %v", m.Name, firstLine(err))
			} else {
				writeStamp(dir, stamp)
			}
		}
		prepared++
	}
	return prepared
}

// gameDescription is the part of every module description that is about the
// game rather than the mod.
func (j *Job) gameDescription() string {
	d := j.GameName + ": " + j.GameContext + "."
	if j.StyleNotes != "" {
		d += "\nGame-specific rules: " + j.StyleNotes
	}
	return d + "\nTerminology must match the game's official " + j.LangDisplay + " localization."
}

// modConfig is the l10n.toml of one mod's module.
func (j *Job) modConfig(m Mod) string {
	description := j.gameDescription() + "\nMod: \"" + m.Name + "\""
	if len(m.Tags) > 0 {
		description += " (about: " + strings.Join(m.Tags, ", ") + ")"
	}
	description += "."
	config := ""
	if m.Group != "" {
		config = "group = " + tomlString(groupFilePref+m.Group) + "\n\n"
	}
	return config + j.appSection(description, batchSize(m)) + outputSection(m.Path)
}

// batchSize sizes a mod's requests by the length of its strings.
func batchSize(m Mod) int {
	if m.Strings == 0 || m.Chars == 0 {
		return 0 // unknown: lloc's default
	}
	avg := m.Chars / m.Strings
	if avg < 1 {
		avg = 1
	}
	size := batchChars / avg
	if size < minBatchSize {
		size = minBatchSize
	}
	if size > maxBatchSize {
		size = maxBatchSize
	}
	return size
}

// writeGroups writes l10n/<group>.toml for every group of mods; lloc requires
// the file of a group a module names, and puts its context into the prompt.
// With prune it also removes the files of groups that no longer exist.
func (j *Job) writeGroups(prune bool) {
	dir := filepath.Join(j.Workspace, "l10n")
	names := map[string]string{}
	for _, m := range j.Mods {
		if m.Group == m.ID {
			names[m.ID] = m.Name
		}
	}
	if entries, err := os.ReadDir(dir); prune && err == nil {
		for _, e := range entries {
			id := strings.TrimSuffix(strings.TrimPrefix(e.Name(), groupFilePref), ".toml")
			if strings.HasPrefix(e.Name(), groupFilePref) && names[id] == "" {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		context := "These mods extend \"" + names[id] + "\": names and terms they share with it " +
			"must be translated exactly as in it."
		writeIfChanged(filepath.Join(dir, groupFilePref+id+".toml"), "context = "+tomlString(context)+"\n")
	}
}

// appSection carries the languages in every module, not in the workspace
// config: modules are read on their own before the workspace is complete.
func (j *Job) appSection(description string, batch int) string {
	section := "[app]\n" +
		"description = " + tomlString(description) + "\n" +
		"base_language = \"en\"\n" +
		"languages = [" + tomlString(j.Lang) + "]\n\n"
	if batch > 0 {
		section += "[ollama]\nbatch_size = " + strconv.Itoa(batch) + "\n\n"
	}
	return section
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
// failure. A stopped job interrupts the command and starts no new one.
func (j *Job) run(dir string, onLine func(line string), args ...string) error {
	ctx := j.ctx()
	cmd := exec.CommandContext(ctx, j.Env.Python,
		append([]string{filepath.Join(j.Env.Dir, "lloc.py"), "--dir", dir}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTHONIOENCODING=utf-8")
	hideWindow(cmd)
	interruptOnCancel(cmd)
	cmd.WaitDelay = waitDelay
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err // also when the job is already stopped
	}

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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if onLine != nil {
			return fmt.Errorf("lloc %s: %w", args[0], err)
		}
		return fmt.Errorf("lloc %s: %w\n%s", args[0], err, strings.Join(tail, "\n"))
	}
	return nil
}

// firstLine shortens a multi-line error (one with lloc's output attached)
// for a log line: the last non-empty line of the output says the most.
func firstLine(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	if len(lines) == 1 {
		return lines[0]
	}
	for i := len(lines) - 1; i > 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return lines[0]
}

var (
	// "[mods/<id>:ru] A+B 3/21: translating 20 strings (2 models)..."
	batchStartRe = regexp.MustCompile(`^\[` + modsDir + `/([^:\]]+)[^\]]*\] A\+B (\d+)/(\d+): translating`)
	// "[mods/<id>:ru] A+B 3/21: +20 judged, failed 2": batch 3 of 21 is
	// finished; "failed" counts the mod's strings given up on so far
	batchDoneRe = regexp.MustCompile(`^\[` + modsDir + `/([^:\]]+)[^\]]*\] A\+B (\d+)/(\d+): \+(\d+) judged(?:, failed (\d+))?`)
	// "[mods/<id>:ru] A+B 3/21: still working, 90s..."
	heartbeatRe = regexp.MustCompile(`still working, (\d+)s`)
)

// report turns the output of `lloc translate` into one progress line that is
// updated in place, a line per finished mod, and any problems lloc reports.
// Per-batch and per-string detail is left out: a full run has thousands.
func (j *Job) report(line string) {
	if j.active == nil {
		j.active, j.failed = map[string]bool{}, map[string]int{}
	}
	if m := batchStartRe.FindStringSubmatch(line); m != nil {
		j.active[m[1]] = true
		j.Status(j.statusLine(""))
		return
	}
	if m := batchDoneRe.FindStringSubmatch(line); m != nil {
		count, _ := strconv.Atoi(m[4])
		failed, _ := strconv.Atoi(m[5])
		// strings lloc gave up on are done as well: they stay in English
		count += failed - j.failed[m[1]]
		j.failed[m[1]] = failed
		j.done += count
		if j.done > j.total {
			j.total = j.done
		}
		if m[2] == m[3] {
			delete(j.active, m[1])
			j.note("  ✓ " + j.modName(m[1]) + " translated")
		} else {
			j.LogUpdate("%s", j.progressLine())
		}
		j.Status(j.statusLine(""))
		return
	}
	if m := heartbeatRe.FindStringSubmatch(line); m != nil {
		j.Status(j.statusLine("waiting for the models, " + m[1] + "s"))
		return
	}
	switch {
	case strings.Contains(line, "] FAILED "):
		j.failedKeys++ // summed up after the run: there can be hundreds
	case strings.Contains(line, "FATAL"), strings.HasPrefix(line, "Traceback"):
		j.fatal = true
		j.note("  " + line)
	case strings.HasPrefix(line, "error"), strings.HasPrefix(line, "server throttling"),
		strings.HasPrefix(line, "retrying"), strings.HasPrefix(line, "interrupted"),
		strings.Contains(line, "looks weak"):
		j.note("  " + line)
	}
}

func (j *Job) statusLine(extra string) string {
	s := fmt.Sprintf("Translating: %d/%d strings", j.done, j.total)
	switch n := len(j.active); {
	case n == 1:
		for id := range j.active {
			s += " — " + j.modName(id)
		}
	case n > 1:
		s += fmt.Sprintf(" — %d mods in progress", n)
	}
	if extra != "" {
		s += " (" + extra + ")"
	}
	return s
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
