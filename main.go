package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lintax/paradox-localize/internal/config"
	"github.com/lintax/paradox-localize/internal/gui"
	"github.com/lintax/paradox-localize/internal/lloc"
	"github.com/lintax/paradox-localize/internal/localization"
	"github.com/lintax/paradox-localize/internal/mod"
	"github.com/lintax/paradox-localize/internal/translation"
)

func main() {
	version := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *version {
		fmt.Printf("Paradox Mod Localization Extender v%s\n", config.Version)
		return
	}

	installedGames := config.DetectInstalledGames()

	// The lloc backend is offered only where it can actually run.
	llocEnv := lloc.Detect()

	g := gui.NewGUI()
	if llocEnv != nil {
		g.EnableLloc()
	}
	g.ShowSelectionScreen(installedGames, func(sel gui.Selection) {
		processWithGUI(g, sel, llocEnv)
	})
	g.ShowAndRun()

	// the window is closed: do not leave a translation running behind it
	lloc.Stop()
}

// backendConfigFromSelection translates GUI choices into a translation.BackendConfig.
func backendConfigFromSelection(sel gui.Selection) translation.BackendConfig {
	switch sel.BackendType {
	case "openai":
		return translation.BackendConfig{
			Type:    translation.BackendOpenAI,
			BaseURL: strings.TrimSpace(sel.OpenAIURL),
			Model:   strings.TrimSpace(sel.OpenAIModel),
			APIKey:  strings.TrimSpace(sel.OpenAIAPIKey),
		}
	default:
		return translation.BackendConfig{
			Type:    translation.BackendOllama,
			BaseURL: strings.TrimSpace(sel.OllamaURL),
			Model:   strings.TrimSpace(sel.OllamaModel),
		}
	}
}

// processWithGUI handles the main processing logic with GUI output.
func processWithGUI(g *gui.GUI, sel gui.Selection, llocEnv *lloc.Env) {
	debug := true

	g.SetStatus("Initializing...")

	gameName, gameExists := config.Games[sel.Game]
	if !gameExists {
		g.Log("ERROR: Unknown game ID: %s", sel.Game)
		return
	}

	enableTranslation := sel.Translate

	// "All languages" mode: copy English into every locale, no LLM.
	var languagesToProcess []string
	if sel.Language == "all" {
		for id := range config.Languages {
			languagesToProcess = append(languagesToProcess, id)
		}
		sort.Strings(languagesToProcess)
		enableTranslation = false
	} else {
		if _, ok := config.LanguageCodes[sel.Language]; !ok {
			g.Log("ERROR: Unknown language ID: %s", sel.Language)
			return
		}
		languagesToProcess = []string{sel.Language}
	}

	var targetLangName, targetParadoxCode string
	if sel.Language != "all" {
		targetLangName = config.Languages[sel.Language]
		targetParadoxCode = config.LanguageCodes[sel.Language]
	}

	workshopDir, err := config.GetWorkshopDir(config.GameIDs[sel.Game])
	if err != nil {
		g.Log("ERROR: Failed to determine workshop directory: %v", err)
		return
	}
	if _, err := os.Stat(workshopDir); os.IsNotExist(err) {
		g.Log("ERROR: Workshop directory not found: %s", workshopDir)
		g.Log("Please ensure %s is installed via Steam and has mods subscribed.", gameName)
		return
	}

	g.Log("Configuration Summary")
	g.Log("  Game:              %s", gameName)
	if sel.Language == "all" {
		g.Log("  Target Language:   All Languages (%d languages)", len(languagesToProcess))
	} else {
		g.Log("  Target Language:   %s (%s)", targetLangName, targetParadoxCode)
	}
	g.Log("  Translation:       %s", formatBool(enableTranslation))
	g.Log("  Cleanup Old Files: %s", formatBool(sel.Cleanup))
	g.Log("  Workshop:          %s", workshopDir)
	g.Log("")

	if sel.Cleanup {
		g.SetStatus("Cleaning up old files...")
		g.Log("\nDeleting old consolidated translation files...")
		totalCount := 0
		for _, langID := range languagesToProcess {
			langCode := config.LanguageCodes[langID]
			count, err := localization.CleanupConsolidatedFiles(workshopDir, langCode)
			if err != nil {
				g.Log("ERROR: Cleanup failed for %s: %v", langID, err)
				return
			}
			totalCount += count
		}
		g.Log("✅ Deleted %d consolidated files", totalCount)
		g.Log("")
	}

	if enableTranslation && sel.BackendType == "lloc" && llocEnv != nil {
		processWithLloc(g, sel, llocEnv, workshopDir)
		return
	}

	if sel.ClearCache && enableTranslation && sel.Language != "all" {
		g.Log("Clearing translation cache...")
		if err := translation.ClearCache("en", sel.Language, sel.Game); err != nil {
			g.Log("Warning: Failed to clear cache: %v", err)
		} else {
			g.Log("✅ Translation cache cleared")
		}
	}

	var translator *translation.Translator
	if enableTranslation && sel.Language != "all" {
		g.SetStatus("Initializing translation backend...")
		g.Log("Initializing translator...")
		cfg := backendConfigFromSelection(sel)
		translator, err = translation.NewTranslator(cfg, "en", sel.Language, sel.Game, debug, g.Log)
		if err != nil {
			g.Log("ERROR: Failed to initialize translator: %v", err)
			return
		}
		defer translator.Close()
		g.Log("Translator initialized (backend: %s)", translator.BackendName())
		g.Log("")
	}

	g.SetStatus("Scanning mods...")
	g.Log("Processing mods...")
	mods, err := os.ReadDir(workshopDir)
	if err != nil {
		g.Log("ERROR: Failed to read workshop directory: %v", err)
		return
	}

	var modDirs []os.DirEntry
	for _, m := range mods {
		if m.IsDir() {
			modDirs = append(modDirs, m)
		}
	}
	totalMods := len(modDirs)
	languageStats := make(map[string]int)
	g.Log("Found %d mods to check\n", totalMods)

	for idx, modEntry := range modDirs {
		modPath := filepath.Join(workshopDir, modEntry.Name())
		modName := mod.Name(modPath, modEntry.Name())

		if translator != nil {
			gameContext := config.GameContexts[sel.Game]
			translator.SetContext(gameContext, modName, mod.Tags(modPath))
		}

		g.SetStatus(fmt.Sprintf("Processing mod %d/%d: %s", idx+1, totalMods, modName))

		if debug {
			g.Log("\n[%d/%d] %s (ID: %s)", idx+1, totalMods, modName, modEntry.Name())
		}

		englishKeys, _, _, locRoot := localization.ScanModLocalization(modPath, "english", debug)

		if len(englishKeys) == 0 {
			if debug {
				g.Log("  ⊘ No English localization found")
			}
			continue
		}
		if locRoot == "" {
			if debug {
				g.Log("  ⊘ No localization directory found")
			}
			continue
		}

		for _, currentLangID := range languagesToProcess {
			currentTargetParadoxCode := config.LanguageCodes[currentLangID]
			currentTargetLangName := config.Languages[currentLangID]

			_, modOwnerKeys, existingConsolidated, _ := localization.ScanModLocalization(modPath, currentTargetParadoxCode, false)

			untranslatedKeys := make(map[string]string, len(englishKeys))
			retranslatedCount := 0
			for key, value := range englishKeys {
				if modOwnerValue, exists := modOwnerKeys[key]; exists {
					if sel.RetranslateEnglish && enableTranslation && modOwnerValue == value {
						retranslatedCount++
					} else {
						continue
					}
				}
				if _, exists := existingConsolidated[key]; exists {
					continue
				}
				untranslatedKeys[key] = value
			}
			if retranslatedCount > 0 && debug {
				g.Log("  ℹ Found %d mod entries identical to English — will re-translate", retranslatedCount)
			}

			if len(untranslatedKeys) == 0 {
				if debug && sel.Language != "all" {
					if len(existingConsolidated) > 0 {
						g.Log("  ✓ %s: All keys already translated (mod owner: %d, existing: %d)",
							currentTargetLangName, len(modOwnerKeys), len(existingConsolidated))
					} else {
						g.Log("  ✓ %s: All keys already translated by mod owner", currentTargetLangName)
					}
				}
				continue
			}

			newTranslations := make(map[string]string, len(untranslatedKeys))

			if translator != nil {
				totalKeys := len(untranslatedKeys)
				g.Log("  🔄 Translating %d new keys [0/%d]...", totalKeys, totalKeys)

				keys := make([]string, 0, totalKeys)
				texts := make([]string, 0, totalKeys)
				for key, englishText := range untranslatedKeys {
					keys = append(keys, key)
					texts = append(texts, englishText)
				}

				results, err := translator.TranslateBatch(texts, func(done, total int, eta string) {
					if eta != "" {
						g.SetStatus(fmt.Sprintf("Translating [%d/%d] ETA: %s - mod %d/%d: %s", done, total, eta, idx+1, totalMods, modName))
						g.LogUpdate("  🔄 Translating %d new keys [%d/%d] ETA: %s", totalKeys, done, total, eta)
					} else {
						g.SetStatus(fmt.Sprintf("Translating [%d/%d] - mod %d/%d: %s", done, total, idx+1, totalMods, modName))
						g.LogUpdate("  🔄 Translating %d new keys [%d/%d]...", totalKeys, done, total)
					}
				})
				if err != nil {
					g.Log("  ⚠ Batch translation failed: %v, falling back to English", err)
					for key, englishText := range untranslatedKeys {
						newTranslations[key] = englishText
					}
				} else {
					for i, key := range keys {
						newTranslations[key] = results[i]
					}
				}
			} else {
				if sel.Language == "all" {
					if debug {
						g.Log("  ✓ %s: Generating %d new entries", currentTargetLangName, len(untranslatedKeys))
					}
				} else if !debug {
					g.Log("[%d/%d] %s - Processing %d new keys...", idx+1, totalMods, modName, len(untranslatedKeys))
				}
				for key, englishText := range untranslatedKeys {
					newTranslations[key] = englishText
				}
			}

			allTranslations := make(map[string]string, len(existingConsolidated)+len(newTranslations))
			for key, value := range existingConsolidated {
				allTranslations[key] = value
			}
			for key, value := range newTranslations {
				allTranslations[key] = value
			}

			err := localization.WriteConsolidatedFile(locRoot, currentTargetParadoxCode, modEntry.Name(), allTranslations)
			if err != nil {
				g.Log("  ✗ Failed to write consolidated file for %s: %v", currentTargetLangName, err)
				continue
			}

			languageStats[currentLangID]++

			if debug && sel.Language != "all" {
				g.Log("  ✅ Written %d translations (%d existing + %d new)",
					len(allTranslations), len(existingConsolidated), len(newTranslations))
			} else if sel.Language != "all" {
				if len(newTranslations) > 0 {
					g.Log("[%d/%d] %s - ✅ Complete (%d new, %d total)",
						idx+1, totalMods, modName, len(newTranslations), len(allTranslations))
				} else {
					g.Log("[%d/%d] %s - ✅ Complete (up to date)", idx+1, totalMods, modName)
				}
			}
		}

		if sel.Language == "all" && !debug {
			g.Log("[%d/%d] %s - ✅ Generated files for %d languages",
				idx+1, totalMods, modName, len(languagesToProcess))
		}
	}

	g.SetStatus("✅ Completed!")

	if sel.Language == "all" {
		var statsText strings.Builder
		statsText.WriteString("Processed mods per language:\n")
		for _, langID := range languagesToProcess {
			langName := config.Languages[langID]
			count := languageStats[langID]
			statsText.WriteString(fmt.Sprintf("  %s: %d mods\n", langName, count))
		}

		g.Log("\n✅ All Languages Completed!")
		g.ShowLanguageStats(statsText.String())
	} else {
		g.Log("\n✅ Completed! Processed %d mods", languageStats[sel.Language])
	}

	if translator != nil {
		stats := translator.GetStats()
		g.Log("\nTranslation Statistics:")
		g.Log("  Actual translations:    %d", stats.ActualTranslations)
		g.Log("  Cache hits:             %d", stats.CacheHits)
		g.Log("  Skipped (syntax-only):  %d", stats.Skipped)
		g.Log("  Cache hit rate:         %.1f%%", stats.CacheHitRate())
	}

	g.Log("\nDone! You can now launch the game and enable the target language.")
}

// processWithLloc translates every mod of the game through lloc and writes
// the same consolidated files as the built-in backends. Keys lloc leaves out
// (script-only values, strings it could not translate safely) are filled with
// the English text, so the game never shows raw key names.
func processWithLloc(g *gui.GUI, sel gui.Selection, env *lloc.Env, workshopDir string) {
	langCode := config.LanguageCodes[sel.Language]
	langName := config.Languages[sel.Language]
	workspace := filepath.Join(translation.CacheDir(), "lloc", sel.Game)

	if sel.ClearCache {
		g.Log("Clearing lloc translations for this game...")
		if err := os.RemoveAll(workspace); err != nil {
			g.Log("Warning: Failed to clear lloc workspace: %v", err)
		}
	}

	g.SetStatus("Scanning mods...")
	entries, err := os.ReadDir(workshopDir)
	if err != nil {
		g.Log("ERROR: Failed to read workshop directory: %v", err)
		return
	}

	type scanned struct {
		mod       lloc.Mod
		english   map[string]string
		modOwner  map[string]string
		locRoot   string
		dirName   string
		processed bool
	}
	var mods []scanned
	job := &lloc.Job{
		Env:         env,
		Workspace:   workspace,
		GameName:    config.Games[sel.Game],
		GameContext: config.GameContexts[sel.Game],
		Lang:        config.ISO639Codes[sel.Language],
		LangName:    langCode,
		LangDisplay: langName,
		Log:         g.Log,
		LogUpdate:   g.LogUpdate,
		Status:      g.SetStatus,
	}
	if dir, ok := lloc.GameInstallDirs[sel.Game]; ok {
		// <steamapps>/workshop/content/<id>  ->  <steamapps>/common/<game>
		steamapps := filepath.Dir(filepath.Dir(filepath.Dir(workshopDir)))
		job.GameDir = filepath.Join(steamapps, "common", dir)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modPath := filepath.Join(workshopDir, entry.Name())
		english, modOwner, _, locRoot := localization.ScanModLocalization(modPath, langCode, false)
		if len(english) == 0 || locRoot == "" {
			continue
		}
		m := lloc.Mod{ID: entry.Name(), Name: mod.Name(modPath, entry.Name()), Path: modPath, Tags: mod.Tags(modPath)}
		mods = append(mods, scanned{mod: m, english: english, modOwner: modOwner, locRoot: locRoot, dirName: entry.Name()})
		job.Mods = append(job.Mods, m)
	}
	g.Log("Found %d mods with English localization (lloc: %s)\n", len(mods), env.Dir)

	if err := job.Run(); err != nil {
		g.Log("⚠ lloc stopped: %v", err)
		g.Log("Writing what has been translated so far; run again to continue.")
	}

	g.SetStatus("Writing localization files...")
	written, translatedTotal, englishTotal := 0, 0, 0
	for _, s := range mods {
		translated, _ := localization.ParseLocalizationFile(job.OutputFile(s.mod.ID))
		all := make(map[string]string, len(s.english))
		translatedCount, englishCount := 0, 0
		for key, englishText := range s.english {
			if text, ok := translated[key]; ok {
				all[key] = text
				translatedCount++
			} else if _, owned := s.modOwner[key]; !owned {
				all[key] = englishText
				englishCount++
			}
		}
		if len(all) == 0 {
			g.Log("  ✓ %s: already translated by the mod author", s.mod.Name)
			continue
		}
		if err := localization.WriteConsolidatedFile(s.locRoot, langCode, s.dirName, all); err != nil {
			g.Log("  ✗ %s: failed to write consolidated file: %v", s.mod.Name, err)
			continue
		}
		written++
		translatedTotal += translatedCount
		englishTotal += englishCount
		g.Log("  ✅ %s: %d translated, %d kept in English", s.mod.Name, translatedCount, englishCount)
	}

	g.SetStatus("✅ Completed!")
	g.Log("\n✅ Completed! Wrote %d mods: %d strings translated, %d kept in English", written, translatedTotal, englishTotal)
	g.Log("\nDone! You can now launch the game and enable the target language.")
}

func formatBool(b bool) string {
	if b {
		return "Enabled"
	}
	return "Disabled"
}
