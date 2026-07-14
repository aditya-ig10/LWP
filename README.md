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

# Interactive REPL mode
export GEMINI_API_KEY="your-key"
lwp

# Or one-shot commands:
lwp fetch "https://example.com" --pretty
lwp chat "https://www.amazon.com/s?k=mens+perfume" "What are the best deals?"
```

## Modes

### Interactive REPL (`lwp`)

Run `lwp` with no arguments to open an interactive session:

```
  ╔══════════════════════════════════╗
  ║  LWP — LLM Web Protocol          ║
  ║  Interactive mode                 ║
  ╚══════════════════════════════════╝

  Select a model:
  ● [1] gemini-flash-lite-latest
    [2] gemini-2.5-flash
    [3] gemini-2.5-pro
    [4] gemini-3-flash-preview
    [5] gemini-3.1-flash-lite-preview

lwp> https://www.amazon.com/s?k=mens+perfume
  → title: Amazon.com : mens perfume on sale
  → elements: 570  content: 256792 chars  latency: 475ms

lwp> What are the best deals under ₹3000?
  → asking gemini-flash-lite-latest ...
  (Gemini responds with product recommendations)
```

Commands inside the REPL:
| Input | Action |
|-------|--------|
| `<url>` | Fetch page and store in context |
| `<question>` | Ask Gemini about current page |
| `model` | List and select a Gemini model |
| `model <name>` | Set model directly |
| `key <key>` | Set/update API key |
| `help` | Show help |
| `exit` / `quit` | Exit |

### `lwp fetch <url>`

Extract page content as structured JSON (no browser).

```bash
lwp fetch "https://example.com" --pretty
```

Flags: `--pretty`, `--timeout <sec>` (default 30)

### `lwp chat <url> "<question>"`

Fetch a page, then ask Gemini to analyze it (one-shot).

```bash
export GEMINI_API_KEY="your-key"
lwp chat "https://news.ycombinator.com" "Summarize the top stories"
```

Flags: `--timeout <sec>` (default 60)

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
- [x] Interactive REPL mode (model select, chat, fetch)
- [ ] Tier 2 — headless JS execution (chromedp)
- [ ] Tier 3 — full render + vision fallback
- [ ] Smart domain tiering + caching
- [ ] Action layer (click, type, scroll)
- [ ] Session/auth management
- [ ] Standalone protocol spec
