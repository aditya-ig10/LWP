# LWP — LLM Web Protocol

Low-render web access layer for LLMs. Skip GUI rendering, extract structured useful info at max speed. One unified schema across any site.

## Architecture

Three-tier extraction based on site complexity:

| Tier | Method | Latency | Works on |
|------|--------|---------|----------|
| 1 | Raw HTTP + HTML parse | ~50-500ms | Static/SSR sites, blogs, docs, e-commerce |
| 2 | Headless JS exec (block CSS/img) | ~300-800ms | Most SPAs (React, Vue, Next) |
| 3 | Full render + vision | ~1-5s | Canvas apps, custom UI |

Auto-detects + caches tier per domain.

## Quick start

```bash
go install github.com/aditya-ig10/LWP/cmd/lwp@latest

# Fetch a page (Tier 1 extraction)
lwp fetch "https://example.com" --pretty

# Ask Gemini about a page
export GEMINI_API_KEY="your-key"
lwp chat "https://www.amazon.com/s?k=mens+perfume" "What are the best deals?"
```

## Commands

### `lwp fetch <url>`

Extract page content as structured JSON (no browser).

```bash
lwp fetch "https://example.com" --pretty
```

Flags: `--pretty`, `--timeout <sec>` (default 30)

### `lwp chat <url> "<question>"`

Fetch a page, then ask Gemini to analyze it.

```bash
export GEMINI_API_KEY="your-key"
lwp chat "https://news.ycombinator.com" "Summarize the top stories"
```

Flags: `--timeout <sec>` (default 60)

Uses model `gemini-flash-lite-latest` (set via `GEMINI_MODEL` env var).

## Output schema

```jsonc
{
  "url": "https://example.com",
  "title": "Example Domain",
  "content": "compact page text...",
  "sections": [
    { "heading": "Section Title", "level": 2 }
  ],
  "elements": [
    { "ref": 1, "type": "link", "text": "Learn more", "href": "..." },
    { "ref": 2, "type": "input", "name": "email", "text": "Email" },
    { "ref": 3, "type": "button", "text": "Submit" }
  ],
  "metadata": {
    "tier": 1,
    "latency_ms": 475,
    "content_length": 240,
    "fetched_at": "2026-07-14T17:03:11Z"
  }
}
```

## Why Go

- Single binary, no runtime deps
- Cross-compile any platform
- `golang.org/x/net/html` — zero-dependency HTML parser
- `chromedp` (future Tier 2) — native Chrome DevTools Protocol, no Node/Playwright
- Concurrency built-in (connection pooling, parallel extraction)

## Why not MCP

Direct LLM integration — no middleware. LWP is a protocol/runtime, not an MCP server wrapper. Works with any local LLM or API via the chat interface.

## Roadmap

- [x] Tier 1 — raw HTTP + HTML extraction
- [ ] Tier 2 — headless JS execution (chromedp)
- [ ] Tier 3 — full render + vision fallback
- [ ] Smart domain tiering + caching
- [ ] Action layer (click, type, scroll)
- [ ] Session/auth management
- [ ] Standalone protocol spec
