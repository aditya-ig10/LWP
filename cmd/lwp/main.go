package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aditya-ig10/LWP/internal/extract"
	"github.com/aditya-ig10/LWP/internal/llm"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "fetch":
		runFetch()
	case "chat":
		runChat()
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lwp <command> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  fetch <url>            extract page content (Tier 1)")
	fmt.Fprintln(os.Stderr, "    --pretty             pretty-print JSON output")
	fmt.Fprintln(os.Stderr, "    --timeout <sec>      request timeout (default 30)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  chat <url> \"<question>\"  fetch + ask Gemini about page")
	fmt.Fprintln(os.Stderr, "    --timeout <sec>      request timeout (default 60)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "env:")
	fmt.Fprintln(os.Stderr, "  GEMINI_API_KEY         required for chat command")
	fmt.Fprintln(os.Stderr, "  GEMINI_MODEL           model name (default gemini-flash-lite-latest)")
	os.Exit(2)
}

func runFetch() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: lwp fetch <url> [--pretty] [--timeout <sec>]")
		os.Exit(2)
	}

	url := os.Args[2]
	pretty := false
	timeout := 30 * time.Second

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--pretty":
			pretty = true
		case "--timeout":
			if i+1 < len(os.Args) {
				d, err := time.ParseDuration(os.Args[i+1] + "s")
				if err == nil {
					timeout = d
				}
				i++
			}
		}
	}

	page, err := extract.Tier1(url, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var out []byte
	if pretty {
		out, _ = json.MarshalIndent(page, "", "  ")
	} else {
		out, _ = json.Marshal(page)
	}

	fmt.Println(string(out))
}

func runChat() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: lwp chat <url> \"<question>\" [--timeout <sec>]")
		os.Exit(2)
	}

	url := os.Args[2]
	question := os.Args[3]
	timeout := 60 * time.Second

	for i := 4; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--timeout":
			if i+1 < len(os.Args) {
				d, err := time.ParseDuration(os.Args[i+1] + "s")
				if err == nil {
					timeout = d
				}
				i++
			}
		}
	}

	fmt.Fprintf(os.Stderr, "fetching %s ...\n", url)
	page, err := extract.Tier1(url, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	summary, _ := json.Marshal(struct {
		Title    string `json:"title"`
		Elements int    `json:"elements"`
		Size     int    `json:"content_size"`
	}{
		Title:    page.Title,
		Elements: len(page.Elements),
		Size:     page.Metadata.ContentLength,
	})
	fmt.Fprintf(os.Stderr, "extracted: %s\n", summary)

	prompt := fmt.Sprintf(`I fetched the page %s (title: %s).

Here is the extracted page content:

%s

Interactive elements on the page:
`, page.URL, page.Title, page.Content)

	for _, e := range page.Elements {
		line := fmt.Sprintf("  [%d] %s", e.Ref, e.Type)
		if e.Text != "" {
			line += " \"" + e.Text + "\""
		}
		if e.Href != "" {
			line += " -> " + e.Href
		}
		prompt += line + "\n"
	}

	prompt += fmt.Sprintf("\nThe user asks: %s\n", question)

	fmt.Fprintf(os.Stderr, "asking Gemini %s ...\n", llm.Model())
	answer, err := llm.Chat(prompt, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(answer)
}
