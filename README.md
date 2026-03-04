# SwitchFrame

Browser-based live video switcher. Built on [Prism](https://github.com/zsiec/prism).

## Quick Demo

**Install dependencies:**

```bash
# macOS
brew install fdk-aac openh264 pkg-config

# Linux (Debian/Ubuntu)
sudo apt-get install -y libfdk-aac-dev libopenh264-dev pkg-config
```

**Run:**

```bash
git clone https://github.com/zsiec/switchframe.git && cd switchframe
cd ui && npm ci && cd ..
make demo
```

Open **http://localhost:5173** in your browser. Four simulated cameras will appear.

## What You Can Try

- Click source buttons or press **1-4** to set preview
- Press **Space** or click **CUT**
- Click **AUTO** or press **Enter** for dissolve
- Drag the **T-bar** for manual transitions
- Click **FTB** for fade to black
- Adjust audio faders, toggle mute/AFV
- Click **REC** / **SRT** in the header
- Press **?** for all keyboard shortcuts
- Add `?mode=simple` to URL for volunteer mode

## System Requirements

- Go 1.25+, Node.js 22+
- macOS: `brew install fdk-aac openh264 pkg-config`
- Linux (Debian/Ubuntu): `sudo apt-get install -y libfdk-aac-dev libopenh264-dev pkg-config`

## Development

```bash
make dev          # Start without demo sources
make test-all     # Run all tests (Go + Vitest + Playwright)
make build        # Production build with embedded UI
make docker       # Docker image
```

## Architecture

See [CLAUDE.md](CLAUDE.md) for detailed architecture, file layout, and conventions.
