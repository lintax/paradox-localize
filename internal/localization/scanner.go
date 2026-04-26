package localization

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/lintax/paradox-localize/internal/config"
)

// ScanModLocalization scans a mod directory for localization files in a single pass
// Returns:
// - englishKeys: map of all English keys and their values
// - modOwnerKeys: set of keys already translated by mod owner (excludes our consolidated file)
// - existingConsolidated: map of keys already in our consolidated file from previous run
// - localizationRoot: path to localization directory (if found)
func ScanModLocalization(modPath string, targetLangCode string, debug bool) (
	englishKeys map[string]string,
	modOwnerKeys map[string]string,
	existingConsolidated map[string]string,
	localizationRoot string,
) {
	englishKeys = make(map[string]string)
	modOwnerKeys = make(map[string]string)
	existingConsolidated = make(map[string]string)
	localizationRoot = ""

	// Walk the entire mod directory tree
	filepath.Walk(modPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Check if this is a localization directory
		if info.IsDir() {
			dirName := strings.ToLower(info.Name())
			if dirName == config.LocalizationDirName || dirName == config.LocalisationDirName {
				if localizationRoot == "" {
					localizationRoot = path
				}
			}
			return nil
		}

		// Process .yml files
		if !strings.HasSuffix(path, ".yml") {
			return nil
		}

		fileName := info.Name()

		// English localization files
		if strings.HasSuffix(fileName, "_l_"+config.SourceLanguage+".yml") {
			keys, err := ParseLocalizationFile(path)
			if err != nil {
				if debug {
					log.Printf("  Warning: Failed to parse English file %s: %v\n", fileName, err)
				}
				return nil
			}

			// Merge English keys
			for key, value := range keys {
				englishKeys[key] = value
			}
		}

		// Target language files
		if strings.HasSuffix(fileName, "_l_"+targetLangCode+".yml") {
			keys, err := ParseLocalizationFile(path)
			if err != nil {
				if debug {
					log.Printf("  Warning: Failed to parse target language file %s: %v\n", fileName, err)
				}
				return nil
			}

			// Check if this is our consolidated file from a previous run
			// Match all variants:
			// - Current: <modid>_consolidated_l_russian.yml
			// - Old: consolidated_l_russian.yml
			// - Old: _consolidated_l_russian.yml
			isConsolidated := strings.HasPrefix(fileName, config.ConsolidatedFilePrefix) ||
				strings.HasPrefix(fileName, "_"+config.ConsolidatedFilePrefix) ||
				strings.Contains(fileName, "_"+config.ConsolidatedFilePrefix+"_l_")

			if isConsolidated {
				// Store existing translations to reuse
				for key, value := range keys {
					existingConsolidated[key] = value
				}
				if debug {
					log.Printf("  Found existing consolidated file with %d keys\n", len(keys))
				}
			} else {
				// This is mod owner's translation file
				for key, value := range keys {
					modOwnerKeys[key] = value
				}
			}
		}

		return nil
	})

	return englishKeys, modOwnerKeys, existingConsolidated, localizationRoot
}

// CleanupConsolidatedFiles deletes all consolidated translation files
// Returns the number of files deleted
func CleanupConsolidatedFiles(workshopDir string, targetLangCode string) (int, error) {
	count := 0
	suffix := "_l_" + targetLangCode + ".yml"

	err := filepath.Walk(workshopDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() {
			fileName := info.Name()
			// Match all variants:
			// - Current: <modid>_consolidated_l_russian.yml
			// - Old: consolidated_l_russian.yml
			// - Old: _consolidated_l_russian.yml
			isConsolidated := strings.HasSuffix(fileName, suffix) && (strings.HasPrefix(fileName, config.ConsolidatedFilePrefix) ||
				strings.HasPrefix(fileName, "_"+config.ConsolidatedFilePrefix) ||
				strings.Contains(fileName, "_"+config.ConsolidatedFilePrefix+"_l_"))

			if isConsolidated {
				if err := os.Remove(path); err == nil {
					count++
				}
			}
		}

		return nil
	})

	return count, err
}
