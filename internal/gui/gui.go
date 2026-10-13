package gui

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/lintax/paradox-localize/internal/config"
)

// Selection bundles every choice the user makes on the configuration screen.
// Empty backend fields mean "use defaults" (resolved by the translation package).
type Selection struct {
	Game               string // game ID, e.g. "ck3"
	Language           string // language ID, e.g. "russian", or "all"
	Translate          bool
	Cleanup            bool
	ClearCache         bool
	RetranslateEnglish bool

	BackendType  string // "ollama", "openai" or "lloc"
	OllamaURL    string
	OllamaModel  string
	OpenAIURL    string
	OpenAIModel  string
	OpenAIAPIKey string
}

// GUI represents the main GUI application.
type GUI struct {
	app          fyne.App
	window       fyne.Window
	logOutput    *widget.Label
	statusLabel  *widget.Label
	statsButton  *widget.Button
	logScroll    *container.Scroll
	logContainer *fyne.Container
	logMutex     sync.Mutex
	logText      string

	sel           Selection
	onComplete    func(Selection)
	llocAvailable bool
}

// EnableLloc offers the lloc backend on the selection screen. Call it before
// ShowSelectionScreen, and only when lloc is actually usable.
func (g *GUI) EnableLloc() {
	g.llocAvailable = true
}

// NewGUI creates a new GUI instance.
func NewGUI() *GUI {
	a := app.NewWithID("com.lintax.paradox-localize")
	w := a.NewWindow(fmt.Sprintf("Paradox Localize v%s", config.Version))
	return &GUI{app: a, window: w}
}

// Preference keys.
const (
	prefKeyGame               = "selected_game"
	prefKeyLanguage           = "selected_language"
	prefKeyTranslate          = "enable_translate"
	prefKeyCleanup            = "enable_cleanup"
	prefKeyRetranslateEnglish = "retranslate_english"

	prefKeyBackendType = "backend_type"
	prefKeyOllamaURL   = "ollama_url"
	prefKeyOllamaModel = "ollama_model"
	prefKeyOpenAIURL   = "openai_url"
	prefKeyOpenAIModel = "openai_model"
	prefKeyOpenAIKey   = "openai_api_key"

	backendOllama = "ollama"
	backendOpenAI = "openai"
	backendLloc   = "lloc"

	backendOllamaLabel = "Local Ollama"
	backendOpenAILabel = "Cloud LLM (OpenAI-compatible)"
	backendLlocLabel   = "lloc (Ollama Cloud)"
)

// ShowSelectionScreen displays the initial configuration screen.
func (g *GUI) ShowSelectionScreen(installedGames []config.InstalledGame, onComplete func(Selection)) {
	g.onComplete = onComplete
	prefs := g.app.Preferences()

	// --- Game selection ---
	gameLabel := widget.NewLabel("Select Game:")
	displayByGameID := make(map[string]string)
	gameIDByDisplay := make(map[string]string)
	var gameRadio *widget.RadioGroup
	if len(installedGames) > 0 {
		options := make([]string, 0, len(installedGames))
		for _, game := range installedGames {
			options = append(options, game.Name)
			displayByGameID[game.ID] = game.Name
			gameIDByDisplay[game.Name] = game.ID
		}
		gameRadio = widget.NewRadioGroup(options, func(selected string) {
			g.sel.Game = gameIDByDisplay[selected]
		})

		savedGame := prefs.StringWithFallback(prefKeyGame, "")
		if display, ok := displayByGameID[savedGame]; ok {
			gameRadio.SetSelected(display)
		} else if len(installedGames) == 1 {
			gameRadio.SetSelected(installedGames[0].Name)
		}
	}

	// --- Language selection ---
	const allLangDisplay = "All Languages (copy English into every missing locale)"
	displayByLangID := map[string]string{"all": allLangDisplay}
	langIDByDisplay := map[string]string{allLangDisplay: "all"}

	langDisplays := make([]string, 0, len(config.Languages))
	for id, name := range config.Languages {
		display := name
		displayByLangID[id] = display
		langIDByDisplay[display] = id
		langDisplays = append(langDisplays, display)
	}
	sort.Strings(langDisplays)
	langOptions := append([]string{allLangDisplay}, langDisplays...)

	langLabel := widget.NewLabel("Select Language:")
	langSelect := widget.NewSelect(langOptions, func(selected string) {
		g.sel.Language = langIDByDisplay[selected]
	})

	if savedLang := prefs.StringWithFallback(prefKeyLanguage, ""); savedLang != "" {
		if display, ok := displayByLangID[savedLang]; ok {
			langSelect.SetSelected(display)
		}
	}

	// --- Generic options ---
	g.sel.Cleanup = prefs.BoolWithFallback(prefKeyCleanup, false)
	cleanupCheck := widget.NewCheck("Delete old consolidated files before processing", func(checked bool) {
		g.sel.Cleanup = checked
	})
	cleanupCheck.Checked = g.sel.Cleanup

	// --- Translation section ---
	g.sel.RetranslateEnglish = prefs.BoolWithFallback(prefKeyRetranslateEnglish, false)
	g.sel.Translate = prefs.BoolWithFallback(prefKeyTranslate, false)

	clearCacheCheck := widget.NewCheck("Clear translation cache (re-translate from scratch)", func(checked bool) {
		g.sel.ClearCache = checked
	})

	retranslateCheck := widget.NewCheck("Re-translate mod entries identical to English", func(checked bool) {
		g.sel.RetranslateEnglish = checked
	})
	retranslateCheck.Checked = g.sel.RetranslateEnglish

	// Backend selector
	g.sel.BackendType = prefs.StringWithFallback(prefKeyBackendType, backendOllama)
	if g.sel.BackendType == backendLloc && !g.llocAvailable {
		g.sel.BackendType = backendOllama
	}
	g.sel.OllamaURL = prefs.StringWithFallback(prefKeyOllamaURL, "")
	g.sel.OllamaModel = prefs.StringWithFallback(prefKeyOllamaModel, "")
	g.sel.OpenAIURL = prefs.StringWithFallback(prefKeyOpenAIURL, "")
	g.sel.OpenAIModel = prefs.StringWithFallback(prefKeyOpenAIModel, "")
	g.sel.OpenAIAPIKey = prefs.StringWithFallback(prefKeyOpenAIKey, "")

	ollamaURLEntry := widget.NewEntry()
	ollamaURLEntry.SetPlaceHolder("http://localhost:11434 (leave blank for default)")
	ollamaURLEntry.SetText(g.sel.OllamaURL)
	ollamaURLEntry.OnChanged = func(s string) { g.sel.OllamaURL = s }

	ollamaModelEntry := widget.NewEntry()
	ollamaModelEntry.SetPlaceHolder("qwen2.5:1.5b (leave blank for default)")
	ollamaModelEntry.SetText(g.sel.OllamaModel)
	ollamaModelEntry.OnChanged = func(s string) { g.sel.OllamaModel = s }

	ollamaFields := container.NewVBox(
		widget.NewLabel("Ollama server URL:"),
		ollamaURLEntry,
		widget.NewLabel("Ollama model:"),
		ollamaModelEntry,
	)

	openaiURLEntry := widget.NewEntry()
	openaiURLEntry.SetPlaceHolder("https://api.openai.com/v1 (or other compatible endpoint)")
	openaiURLEntry.SetText(g.sel.OpenAIURL)
	openaiURLEntry.OnChanged = func(s string) { g.sel.OpenAIURL = s }

	openaiModelEntry := widget.NewEntry()
	openaiModelEntry.SetPlaceHolder("gpt-4o-mini")
	openaiModelEntry.SetText(g.sel.OpenAIModel)
	openaiModelEntry.OnChanged = func(s string) { g.sel.OpenAIModel = s }

	openaiKeyEntry := widget.NewPasswordEntry()
	openaiKeyEntry.SetPlaceHolder("sk-...")
	openaiKeyEntry.SetText(g.sel.OpenAIAPIKey)
	openaiKeyEntry.OnChanged = func(s string) { g.sel.OpenAIAPIKey = s }

	openaiKeyNote := widget.NewLabel("Note: API key is stored in plain text in the local app preferences file.")
	openaiKeyNote.TextStyle = fyne.TextStyle{Italic: true}
	openaiKeyNote.Wrapping = fyne.TextWrapWord

	openaiFields := container.NewVBox(
		widget.NewLabel("API URL:"),
		openaiURLEntry,
		widget.NewLabel("Model:"),
		openaiModelEntry,
		widget.NewLabel("API key:"),
		openaiKeyEntry,
		openaiKeyNote,
	)

	llocNote := widget.NewLabel(
		"Translates with two Ollama Cloud models and a judge, and takes terminology from the game's " +
			"official localization. Mod entries left in English are always re-translated.",
	)
	llocNote.Wrapping = fyne.TextWrapWord
	llocFields := container.NewVBox(llocNote)

	updateBackendVisibility := func() {
		ollamaFields.Hidden = g.sel.BackendType != backendOllama
		openaiFields.Hidden = g.sel.BackendType != backendOpenAI
		llocFields.Hidden = g.sel.BackendType != backendLloc
		retranslateCheck.Hidden = g.sel.BackendType == backendLloc
		ollamaFields.Refresh()
		openaiFields.Refresh()
		llocFields.Refresh()
		retranslateCheck.Refresh()
	}

	backendByLabel := map[string]string{
		backendOllamaLabel: backendOllama,
		backendOpenAILabel: backendOpenAI,
		backendLlocLabel:   backendLloc,
	}
	backendLabels := []string{backendOllamaLabel, backendOpenAILabel}
	if g.llocAvailable {
		backendLabels = append(backendLabels, backendLlocLabel)
	}
	backendRadio := widget.NewRadioGroup(backendLabels, func(selected string) {
		if backend, ok := backendByLabel[selected]; ok {
			g.sel.BackendType = backend
		}
		updateBackendVisibility()
	})
	backendRadio.Horizontal = true
	for label, backend := range backendByLabel {
		if backend == g.sel.BackendType {
			backendRadio.SetSelected(label)
		}
	}

	translateNote := widget.NewLabel(
		"Local Ollama: free, private, runs on your machine — install Ollama from https://ollama.com first. " +
			"First run downloads ~1GB. Cloud LLM: better quality, requires an API key (OpenAI, OpenRouter, Groq, etc.).",
	)
	translateNote.Wrapping = fyne.TextWrapWord
	translateNote.TextStyle = fyne.TextStyle{Italic: true}

	translationOpts := container.NewVBox(
		translateNote,
		widget.NewLabel("Translation backend:"),
		backendRadio,
		ollamaFields,
		openaiFields,
		llocFields,
		widget.NewSeparator(),
		retranslateCheck,
		clearCacheCheck,
	)
	translationOpts.Hidden = !g.sel.Translate
	updateBackendVisibility()

	translateCheck := widget.NewCheck("Enable Translation", func(checked bool) {
		g.sel.Translate = checked
		translationOpts.Hidden = !checked
		translationOpts.Refresh()
	})
	translateCheck.Checked = g.sel.Translate

	// --- Start button ---
	startBtn := widget.NewButton("Start Processing", func() {
		if g.sel.Game == "" {
			g.showError("Please select a game")
			return
		}
		if g.sel.Language == "" {
			g.showError("Please select a language")
			return
		}
		if g.sel.Translate && g.sel.BackendType == backendOpenAI && strings.TrimSpace(g.sel.OpenAIAPIKey) == "" {
			g.showError("Cloud LLM backend requires an API key")
			return
		}

		prefs.SetString(prefKeyGame, g.sel.Game)
		prefs.SetString(prefKeyLanguage, g.sel.Language)
		prefs.SetBool(prefKeyTranslate, g.sel.Translate)
		prefs.SetBool(prefKeyCleanup, g.sel.Cleanup)
		prefs.SetBool(prefKeyRetranslateEnglish, g.sel.RetranslateEnglish)
		prefs.SetString(prefKeyBackendType, g.sel.BackendType)
		prefs.SetString(prefKeyOllamaURL, g.sel.OllamaURL)
		prefs.SetString(prefKeyOllamaModel, g.sel.OllamaModel)
		prefs.SetString(prefKeyOpenAIURL, g.sel.OpenAIURL)
		prefs.SetString(prefKeyOpenAIModel, g.sel.OpenAIModel)
		prefs.SetString(prefKeyOpenAIKey, g.sel.OpenAIAPIKey)

		g.showProcessingScreen()
		go g.onComplete(g.sel)
	})
	startBtn.Importance = widget.HighImportance

	// --- Layout ---
	var content *fyne.Container
	if len(installedGames) == 0 {
		noGamesLabel := widget.NewLabel("No Paradox games with workshop mods detected.\nPlease install a supported game via Steam and subscribe to mods.")
		noGamesLabel.Wrapping = fyne.TextWrapWord
		content = container.NewVBox(
			layout.NewSpacer(),
			noGamesLabel,
			layout.NewSpacer(),
		)
	} else {
		content = container.NewVBox(
			gameLabel,
			gameRadio,
			layout.NewSpacer(),
			langLabel,
			langSelect,
			layout.NewSpacer(),
			cleanupCheck,
			layout.NewSpacer(),
			translateCheck,
			translationOpts,
			layout.NewSpacer(),
			startBtn,
		)
	}

	scrollable := container.NewScroll(content)
	scrollable.SetMinSize(fyne.NewSize(640, 600))
	g.window.SetContent(scrollable)
	g.window.Resize(fyne.NewSize(700, 700))
	g.window.CenterOnScreen()
}

// showProcessingScreen switches to the processing/log view.
func (g *GUI) showProcessingScreen() {
	g.statusLabel = widget.NewLabel("Processing...")
	g.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	g.logOutput = widget.NewLabel("")
	g.logOutput.TextStyle = fyne.TextStyle{Monospace: true}
	g.logOutput.Wrapping = fyne.TextWrapOff

	g.logContainer = container.NewVBox(g.logOutput)

	g.logScroll = container.NewScroll(g.logContainer)
	g.logScroll.SetMinSize(fyne.NewSize(800, 600))

	content := container.NewBorder(
		g.statusLabel,
		nil,
		nil,
		nil,
		g.logScroll,
	)

	g.window.SetContent(content)
	g.window.Resize(fyne.NewSize(1000, 700))
	g.window.CenterOnScreen()
}

// Log adds a message to the GUI log output (thread-safe).
func (g *GUI) Log(format string, args ...interface{}) {
	if g.logOutput == nil {
		return
	}

	message := fmt.Sprintf(format, args...)

	g.logMutex.Lock()
	if g.logText != "" {
		g.logText += "\n"
	}
	g.logText += message
	updatedText := g.logText
	g.logMutex.Unlock()

	fyne.Do(func() {
		g.logOutput.SetText(updatedText)
		g.logOutput.Refresh()
		if g.logScroll != nil {
			g.logScroll.ScrollToBottom()
		}
	})
}

// LogUpdate replaces the last log line instead of appending a new one (thread-safe).
func (g *GUI) LogUpdate(format string, args ...interface{}) {
	if g.logOutput == nil {
		return
	}

	message := fmt.Sprintf(format, args...)

	g.logMutex.Lock()
	if idx := strings.LastIndex(g.logText, "\n"); idx >= 0 {
		g.logText = g.logText[:idx+1] + message
	} else {
		g.logText = message
	}
	updatedText := g.logText
	g.logMutex.Unlock()

	fyne.Do(func() {
		g.logOutput.SetText(updatedText)
		g.logOutput.Refresh()
		if g.logScroll != nil {
			g.logScroll.ScrollToBottom()
		}
	})
}

// SetStatus updates the status label (thread-safe).
func (g *GUI) SetStatus(status string) {
	if g.statusLabel == nil {
		return
	}
	fyne.Do(func() {
		g.statusLabel.SetText(status)
		g.statusLabel.Refresh()
	})
}

// ShowLanguageStats adds a button with tooltip showing language statistics.
func (g *GUI) ShowLanguageStats(statsText string) {
	if g.logContainer == nil {
		return
	}

	fyne.Do(func() {
		g.statsButton = widget.NewButton("ⓘ View detailed statistics", func() {
			statsLabel := widget.NewLabel(statsText)
			statsLabel.TextStyle = fyne.TextStyle{Monospace: true}
			statsLabel.Wrapping = fyne.TextWrapOff

			statsScroll := container.NewScroll(statsLabel)
			statsScroll.SetMinSize(fyne.NewSize(500, 350))

			var popup *widget.PopUp
			closeBtn := widget.NewButton("Close", func() {
				popup.Hide()
			})
			closeBtn.Importance = widget.HighImportance

			popup = widget.NewModalPopUp(
				container.NewBorder(
					widget.NewLabel("Language Processing Statistics"),
					closeBtn,
					nil,
					nil,
					statsScroll,
				),
				g.window.Canvas(),
			)
			popup.Resize(fyne.NewSize(550, 450))
			popup.Show()
		})

		g.logContainer.Add(g.statsButton)
		g.logContainer.Refresh()
	})
}

func (g *GUI) showError(message string) {
	dialog := widget.NewLabel(message)
	var popup *widget.PopUp
	popup = widget.NewModalPopUp(
		container.NewVBox(
			dialog,
			widget.NewButton("OK", func() {
				popup.Hide()
			}),
		),
		g.window.Canvas(),
	)
	popup.Show()
}

// ShowAndRun displays the window and runs the application.
func (g *GUI) ShowAndRun() {
	g.window.ShowAndRun()
}

// Close closes the GUI window.
func (g *GUI) Close() {
	if g.window != nil {
		g.window.Close()
	}
}
