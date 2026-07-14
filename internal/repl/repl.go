package repl

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aditya-ig10/LWP/internal/extract"
	"github.com/aditya-ig10/LWP/internal/llm"
)

func isTerminal() bool {
	fi, _ := os.Stdin.Stat()
	return (fi.Mode() & os.ModeCharDevice) != 0
}

var models = []string{
	"gemini-flash-lite-latest",
	"gemini-2.5-flash",
	"gemini-2.5-pro",
	"gemini-3-flash-preview",
	"gemini-3.1-flash-lite-preview",
}

type session struct {
	scanner  *bufio.Scanner
	model    string
	apiKey   string
	pageURL  string
	pageText string
}

func Start() {
	s := &session{
		scanner: bufio.NewScanner(os.Stdin),
		model:   "gemini-flash-lite-latest",
	}

	if isTerminal() {
		printBanner()
	}

	s.apiKey = os.Getenv("GEMINI_API_KEY")
	if s.apiKey == "" && isTerminal() {
		fmt.Print("\n  GEMINI_API_KEY not set. Enter your API key: ")
		key, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		s.apiKey = strings.TrimSpace(key)
		if s.apiKey == "" {
			fmt.Println("  No key set. Use 'key <your-key>' inside the REPL.")
		}
		os.Setenv("GEMINI_API_KEY", s.apiKey)
	}

	if isTerminal() {
		s.selectModel()
		s.printHelp()
	}

	for s.prompt() {
	}
}

func (s *session) prompt() bool {
	if isTerminal() {
		fmt.Print("\033[36mlwp> \033[0m")
	}
	if !s.scanner.Scan() {
		return false
	}
	input := strings.TrimSpace(s.scanner.Text())
	if input == "" {
		return true
	}

	switch {
	case input == "exit" || input == "quit":
		fmt.Println("bye")
		return false
	case input == "help":
		s.printHelp()
	case input == "model":
		s.selectModel()
	case strings.HasPrefix(input, "model "):
		m := strings.TrimPrefix(input, "model ")
		if m == "" {
			s.selectModel()
		} else {
			s.model = m
			fmt.Printf("  \033[2m→ model set to:\033[0m %s\n", s.model)
		}
	case strings.HasPrefix(input, "key "):
		s.apiKey = strings.TrimPrefix(input, "key ")
		os.Setenv("GEMINI_API_KEY", s.apiKey)
		fmt.Println("  \033[2m→ API key updated\033[0m")
	case looksLikeURL(input):
		s.fetchPage(input)
	default:
		s.askGemini(input)
	}
	return true
}

func printBanner() {
	fmt.Println()
	fmt.Println("  \033[1m╔══════════════════════════════════╗\033[0m")
	fmt.Println("  \033[1m║  LWP — LLM Web Protocol          ║\033[0m")
	fmt.Println("  \033[1m║  Interactive mode                 ║\033[0m")
	fmt.Println("  \033[1m╚══════════════════════════════════╝\033[0m")
	fmt.Println()
}

func (s *session) printHelp() {
	fmt.Println()
	fmt.Println("  \033[1mCommands:\033[0m")
	fmt.Println("    \033[1m<url>\033[0m        fetch a page (e.g. https://example.com)")
	fmt.Println("    \033[1m<question>\033[0m    ask Gemini about the current page")
	fmt.Println("    \033[1mmodel\033[0m          list and select a Gemini model")
	fmt.Println("    \033[1mmodel <name>\033[0m   set model directly")
	fmt.Println("    \033[1mkey <key>\033[0m      set/update your Gemini API key")
	fmt.Println("    \033[1mhelp\033[0m           show this help")
	fmt.Println("    \033[1mexit\033[0m or \033[1mquit\033[0m   exit")
	fmt.Println()
}

func (s *session) selectModel() {
	fmt.Println("\n  \033[1mSelect a model:\033[0m")
	for i, m := range models {
		mark := " "
		if m == s.model {
			mark = "●"
		}
		fmt.Printf("  \033[36m%s\033[0m [\033[1m%d\033[0m] %s\n", mark, i+1, m)
	}
	fmt.Printf("  \033[2m  [0] custom\033[0m\n")
	fmt.Print("  \033[2mnumber (or 0 for custom):\033[0m ")

	line := s.readLine()
	if line == "" {
		fmt.Printf("  \033[2m→ \033[0m%s\n", s.model)
		return
	}

	if line == "0" || strings.EqualFold(line, "custom") {
		fmt.Print("  \033[2mModel name:\033[0m ")
		name := s.readLine()
		if name != "" {
			s.model = name
		}
	} else {
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
			s.model = models[idx-1]
		}
	}

	fmt.Printf("  \033[2m→ active model:\033[0m %s\n", s.model)
}

func (s *session) readLine() string {
	if s.scanner.Scan() {
		return strings.TrimSpace(s.scanner.Text())
	}
	return ""
}

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.Contains(s, ".")
}

func (s *session) fetchPage(rawURL string) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	if _, err := url.Parse(rawURL); err != nil {
		fmt.Printf("  \033[31mInvalid URL: %s\033[0m\n", err)
		return
	}

	fmt.Printf("  \033[2m→ fetching %s ...\033[0m\n", rawURL)

	page, err := extract.Tier1(rawURL, 30*time.Second)
	if err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	s.pageURL = page.URL
	s.pageText = fmt.Sprintf("Page: %s\nTitle: %s\n\n%s", page.URL, page.Title, page.Content)

	fmt.Printf("  \033[2m→ title:\033[0m %s\n", page.Title)
	fmt.Printf("  \033[2m→ elements:\033[0m %d  \033[2mcontent:\033[0m %d chars  \033[2mlatency:\033[0m %dms\n",
		len(page.Elements), page.Metadata.ContentLength, page.Metadata.LatencyMs)
}

func (s *session) askGemini(question string) {
	if s.apiKey == "" {
		fmt.Println("  \033[31mNo API key set. Use 'key <your-key>' first.\033[0m")
		return
	}
	if s.pageText == "" {
		fmt.Println("  \033[33mNo page in context. Enter a URL first.\033[0m")
		return
	}

	prompt := fmt.Sprintf(`I fetched this page:

%s

The user asks: %s`, s.pageText, question)

	fmt.Printf("  \033[2m→ asking %s ...\033[0m\n", s.model)
	answer, err := llm.Chat(prompt, 120*time.Second)
	if err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	fmt.Println()
	fmt.Println(answer)
	fmt.Println()
}
