# Tonelab

Natural-language control for your DAW. Type what you want — "turn down the vocal track", "pan the guitar left" — and Tonelab's agent resolves it to a real parameter change and sends it straight to REAPER over OSC. No menus, no hunting for the right knob.

## How it works

- **UI** — a single Go + Wails v3 desktop app (native webview, no Electron), plain TypeScript frontend
- **Agent** — natural-language input resolved to tool calls via an LLM; bring your own API key or point it at a local model
- **OSC** — tool calls become real-time OSC commands sent to a running REAPER instance

## Status

Early development. Core round-trip (UI → Go → OSC → REAPER) is being wired up.

## Running locally

Requires Go 1.24+, Node, and the [Wails v3 CLI](https://v3.wails.io/getting-started/installation/) (`wails3`).

```
wails3 dev
```

## Repo layout

```
backend/
  agent/   — orchestrator, tool-calling, JSON schemas
  osc/     — OSC transport (go-osc), connection management
  daw/     — typed Go API over DAW OSC addresses (REAPER first)
  app/     — Wails services, the boundary the frontend calls into
frontend/
  src/     — TypeScript UI
```
