# Paradox Mod Localization Extender

A small desktop tool that fixes the "raw localization keys instead of translated text" problem you see when you run a Paradox grand-strategy game in a non-English language with mods that ship English-only localization.

It scans every mod you've subscribed to on the Steam Workshop, finds the strings the mod author left untranslated for your language, and writes a sidecar `.yml` file alongside the mod with either:

- **the English text copied into the target-language slot** — instant fix for the "shows the key name" issue, no translation needed, or
- **a real machine translation** — done locally via [Ollama](https://ollama.com), or remotely via any OpenAI-compatible API (OpenAI, OpenRouter, Groq, Together, a self-hosted vLLM/llama.cpp server, …).

Translations are cached per game so you can stop and resume safely, and re-running picks up only newly-added strings.

---

## Quick start (most users)

1. **Subscribe to your mods** on the Steam Workshop and run the game once so it downloads them.
2. **Download** the latest binary for your OS from the [Releases](../../releases) page.
3. **Run it.** It auto-detects which Paradox games you have installed.
4. Pick a game and a target language. Click **Start Processing**.
5. Launch the game in your target language — the missing translations are now filled in.

That's it. No translation backend is required if you only want to get rid of the "raw key names" display issue — leave **Enable Translation** unchecked and the tool simply copies English text into the target-language slot.

If you do want real translations, see [Translation backends](#translation-backends) below.

### Supported games

Europa Universalis 5 · Crusader Kings 3 · Victoria 3 · Hearts of Iron 4 · Stellaris · Imperator: Rome

### Supported target languages

Russian, German, French, Spanish, Simplified Chinese, Japanese, Korean, Polish, Portuguese (Brazilian), Italian, Dutch, Turkish, Arabic.

---

## Translation backends

Translation is optional. When enabled, you pick one of two backends.

### Local Ollama (free, private, default)

Install [Ollama](https://ollama.com) and make sure it's running. The first translation run downloads `qwen2.5:1.5b` (~1 GB). It's small and fast but the quality is mediocre on rarer languages — fine for casual play, not for publishable translations.

You can override the server URL and model in the GUI, or via the `OLLAMA_HOST` environment variable. Pointing the URL at a remote machine running Ollama lets you offload work to a beefier PC on your LAN.

### Cloud LLM (better quality, your API key)

Pick "Cloud LLM (OpenAI-compatible)" and fill in:

- **API URL** — `https://api.openai.com/v1` for OpenAI, or any compatible endpoint (OpenRouter, Groq, Together, DeepInfra, Mistral, a self-hosted vLLM, etc.).
- **Model** — e.g. `gpt-4o-mini`, or whatever your provider exposes.
- **API key** — stored in plain text in the local app preferences file. Don't share that file.

Bring-your-own-key avoids bundling any third-party credentials in the binary.

### lloc (experimental, shown only when available)

A third backend, "lloc (Ollama Cloud)", appears in the list when all of these are found at startup:

- **Python 3.11+** on `PATH`;
- a checkout of [lloc](https://github.com/lintax/lloc) in a `lloc` folder next to this tool (or one or two levels up), or wherever `LLOC_DIR` points;
- a local **Ollama** daemon signed in to Ollama Cloud (`ollama signin`).

Each string is translated by two cloud models and compared by a third; game script syntax is validated instead of masked; and terminology comes from the game's own official localization, which is read once from the game's install folder. Mod entries left in English by the author are always re-translated. Anything lloc leaves out is filled with the English text, as with the other backends.

Its state lives in `<cache dir>/lloc/<game>/` and is incremental: an interrupted run continues where it stopped, and "Clear translation cache" removes it.

---

## How it works

1. Walks each mod folder under `…/Steam/steamapps/workshop/content/<game-id>/`.
2. Parses every `*_l_english.yml`. Identifies keys that the mod author hasn't already translated to your target language.
3. For each untranslated key, either copies the English text or sends it to the chosen LLM with a short system prompt that explains the Paradox placeholder rules.
4. Writes the result into the mod's `localization/<lang>/` folder as **two** files:
   - `0_<modid>_consolidated_l_<lang>.yml` (sorts first in ASCII)
   - `zz_<modid>_consolidated_l_<lang>.yml` (sorts last)
   This is a deliberate hack: different Paradox games use different load-order rules for localization files (FIOS = first-in-order-sorted, others use reverse-alpha), and writing both prefixes guarantees our file wins regardless. The two files are hardlinked on platforms that allow it, so storage cost is negligible.
5. Caches translations per `(game, source-lang, target-lang)` triple so re-runs and other mods that share strings get cache hits.

### Placeholder protection

Paradox localization values can contain non-translatable syntax: `$VARIABLE$`, `[GetModifier(...)]`, `@icon!`, `#bold text#!`, `\n`. Before sending text to the LLM the tool replaces these with neutral `{0} {1} {2}` tokens, validates that every token survives in the response, and restores them afterwards. If a token went missing in translation, the original English string is kept for that key — wrong, but safe.

### Cache

Translation cache is stored in:

- Linux: `~/.cache/paradox-localize/translation_cache_<gameid>_en_<lang>.json`
- macOS: `~/Library/Caches/paradox-localize/translation_cache_*.json`
- Windows: `%LOCALAPPDATA%\paradox-localize\translation_cache_*.json`

The cache is JSON-Lines (one entry per line) so it can be appended to incrementally without rewriting the whole file. Safe to delete; the tool will rebuild it.

### What gets written

The tool only writes to `…/Steam/steamapps/workshop/content/<game-id>/<modid>/localization/<lang>/0_<modid>_consolidated_l_<lang>.yml` (and the `zz_…` twin). It never touches the mod author's own files, and "Delete old consolidated files before processing" cleans up only files matching that pattern.

Re-subscribing to a mod or having Steam re-validate workshop content will overwrite our files; just re-run the tool.

---

## Build from source

### Prerequisites

- Go 1.24+
- CGO enabled (the GUI uses Fyne, which needs a C compiler and OpenGL).
- For cross-compilation: `mingw-w64` for Windows, [osxcross](https://github.com/tpoechtrager/osxcross) for macOS.

### Make targets

All builds drop into `bin/`. Run `make help` for the full list. The common ones:

```bash
make build-linux        # bin/paradox-localize-linux-amd64
make build-windows      # cross-compile → bin/paradox-localize-windows-amd64.exe
make build-mac-intel    # cross-compile → bin/paradox-localize-macos-amd64
make build-mac-arm      # cross-compile → bin/paradox-localize-macos-arm64
make build-all          # all four platforms
make release            # clean + build-all (use this for tagged releases)

make test               # unit tests
make check              # gofmt-check + vet + test
make run                # build + launch GUI
make clean              # rm -rf bin/
```

If your osxcross or LLVM live somewhere other than the defaults, override on the command line:

```bash
make build-mac-arm OSXCROSS_BIN=/opt/osxcross/target/bin LLVM_BIN=/usr/lib/llvm-18/bin
```

osxcross requires `clang` in `PATH`. If you only have `clang-20`, the Makefile's default `LLVM_BIN=/usr/lib/llvm-20/bin` already covers that.

---

## Contributing

Bug reports and PRs are welcome. The tricky bits live in [internal/translation/placeholder.go](internal/translation/placeholder.go) (regex-based syntax protection) and [internal/localization/writer.go](internal/localization/writer.go) (the `0_/zz_` load-order trick). If you change either, the existing tests should still pass; please add new ones for any new edge case you handle.

## License

MIT — see [LICENSE](LICENSE).
