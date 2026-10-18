package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Games maps game IDs to display names
var Games = map[string]string{
	"eu5":       "Europa Universalis 5",
	"ck3":       "Crusader Kings 3",
	"vic3":      "Victoria 3",
	"hoi4":      "Hearts of Iron 4",
	"stellaris": "Stellaris",
	"imperator": "Imperator: Rome",
}

// GameIDs maps game IDs to Steam workshop content IDs
var GameIDs = map[string]string{
	"eu5":       "3450310",
	"ck3":       "1158310",
	"vic3":      "529340",
	"hoi4":      "394360",
	"stellaris": "281990",
	"imperator": "859580",
}

// Languages maps language IDs to display names
var Languages = map[string]string{
	"russian":    "Russian",
	"german":     "German",
	"french":     "French",
	"spanish":    "Spanish",
	"chinese":    "Chinese (Simplified)",
	"japanese":   "Japanese",
	"korean":     "Korean",
	"polish":     "Polish",
	"portuguese": "Portuguese",
	"italian":    "Italian",
	"dutch":      "Dutch",
	"turkish":    "Turkish",
	"arabic":     "Arabic",
}

// LanguageCodes maps language IDs to Paradox language codes
var LanguageCodes = map[string]string{
	"russian":    "russian",
	"german":     "german",
	"french":     "french",
	"spanish":    "spanish",
	"chinese":    "simp_chinese",
	"japanese":   "japanese",
	"korean":     "korean",
	"polish":     "polish",
	"portuguese": "braz_por",
	"italian":    "italian",
	"dutch":      "dutch",
	"turkish":    "turkish",
	"arabic":     "arabic",
}

// ISO639Codes maps language IDs to ISO 639-1 codes (used for cache file naming)
var ISO639Codes = map[string]string{
	"russian":    "ru",
	"german":     "de",
	"french":     "fr",
	"spanish":    "es",
	"chinese":    "zh",
	"japanese":   "ja",
	"korean":     "ko",
	"polish":     "pl",
	"portuguese": "pt",
	"italian":    "it",
	"dutch":      "nl",
	"turkish":    "tr",
	"arabic":     "ar",
}

// GameContexts provides genre/setting descriptions for each game to improve LLM translation quality
var GameContexts = map[string]string{
	"eu5":       "a historical grand strategy game spanning the late Middle Ages and the early modern period (1337-1837), featuring diplomacy, warfare, trade, estates, and colonization",
	"ck3":       "a historical grand strategy RPG set in the medieval period (867-1453), featuring dynasty management, feudal politics, warfare, and religious conflicts",
	"vic3":      "a historical grand strategy game set in the Victorian era (1836-1936), featuring industrialization, politics, economics, and diplomacy",
	"hoi4":      "a historical grand strategy wargame set during World War II (1936-1948), featuring military operations, production, and wartime diplomacy",
	"stellaris": "a sci-fi grand strategy game set in space, featuring space exploration, alien civilizations, interstellar warfare, and galactic politics",
	"imperator": "a historical grand strategy game set in the classical antiquity (304-27 BC), featuring Roman and Hellenistic civilizations, warfare, and governance",
}

// GameStyleNotes are per-game localization rules passed to translators that
// take free-form context (lloc). They cover what differs between the games:
// how names are handled and whose voice event options are written in.
var GameStyleNotes = map[string]string{
	"eu5": "Real countries, rulers, dynasties and places use their established historical names in the target language. " +
		"Event options are short decisions of the player's country.",
	"ck3": "Real historical people, dynasties, titles and places use their established names in the target language; " +
		"feudal and religious titles follow the official translation. " +
		"Event options are short lines spoken by the player's character, in first person.",
	"vic3": "Real countries, people and places use their established names in the target language; " +
		"political, economic and industrial terms follow the official translation. " +
		"Event options are short decisions of the player's country.",
	"hoi4": "Real people, countries, military units, equipment and places use their established names in the target language; " +
		"military terminology follows the official translation. " +
		"Event options are short replies of the player's country.",
	"stellaris": "Invented species, ship, planet and star names are transliterated, not translated, unless the examples show otherwise. " +
		"Event options are short replies of the player's empire.",
	"imperator": "Ancient people, peoples and places use their established classical forms in the target language. " +
		"Event options are short decisions of the player's country or its ruler.",
}

const Version = "1.0.0"

const (
	SourceLanguage         = "english"
	ConsolidatedFilePrefix = "consolidated" // Plain name, no underscore prefix
	LocalizationDirName    = "localization"
	LocalisationDirName    = "localisation" // British spelling
)

// GetWorkshopDir returns the platform-specific Steam workshop directory for a game
func GetWorkshopDir(gameID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	var workshopPath string
	switch runtime.GOOS {
	case "windows":
		steamBase := os.Getenv("PROGRAMFILES(X86)")
		if steamBase == "" {
			steamBase = "C:\\Program Files (x86)"
		}
		workshopPath = filepath.Join(steamBase, "Steam", "steamapps", "workshop", "content", gameID)

		if _, err := os.Stat(workshopPath); os.IsNotExist(err) {
			workshopPath = filepath.Join(home, "Program Files (x86)", "Steam", "steamapps", "workshop", "content", gameID)
		}
	case "darwin":
		workshopPath = filepath.Join(home, "Library", "Application Support", "Steam", "steamapps", "workshop", "content", gameID)
	default:
		workshopPath = filepath.Join(home, ".local", "share", "Steam", "steamapps", "workshop", "content", gameID)
	}

	return workshopPath, nil
}

// InstalledGame holds info about a detected game
type InstalledGame struct {
	ID   string
	Name string
}

// DetectInstalledGames checks which games have workshop directories present
func DetectInstalledGames() []InstalledGame {
	var installed []InstalledGame
	for id, name := range Games {
		steamID := GameIDs[id]
		workshopDir, err := GetWorkshopDir(steamID)
		if err != nil {
			continue
		}
		if info, err := os.Stat(workshopDir); err == nil && info.IsDir() {
			installed = append(installed, InstalledGame{ID: id, Name: name})
		}
	}
	sort.Slice(installed, func(i, j int) bool {
		return installed[i].Name < installed[j].Name
	})
	return installed
}
