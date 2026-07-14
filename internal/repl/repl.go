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

var siteSearchURLs = map[string]string{
	"flipkart":  "https://www.flipkart.com/search?q=%s",
	"amazon":    "https://www.amazon.com/s?k=%s",
	"amazon.in": "https://www.amazon.in/s?k=%s",
	"myntra":    "https://www.myntra.com/%s",
	"ajio":      "https://www.ajio.com/search/?text=%s",
	"ebay":      "https://www.ebay.com/sch/i.html?_nkw=%s",
	"ebay.com":  "https://www.ebay.com/sch/i.html?_nkw=%s",
	"walmart":   "https://www.walmart.com/search?q=%s",
	"target":    "https://www.target.com/s?searchTerm=%s",
	"bestbuy":   "https://www.bestbuy.com/site/searchpage.jsp?st=%s",
	"linkedin":  "https://www.linkedin.com/search/results/all/?keywords=%s",
	"reddit":    "https://www.reddit.com/search/?q=%s",
	"youtube":   "https://www.youtube.com/results?search_query=%s",
	"github":    "https://github.com/search?q=%s",
	"google":    "https://www.google.com/search?q=%s",
	"stackoverflow": "https://stackoverflow.com/search?q=%s",
	"twitter":   "https://twitter.com/search?q=%s",
	"x.com":     "https://x.com/search?q=%s",
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
		if site, query, ok := parseSearchIntent(input); ok {
			s.searchSite(site, query)
		} else {
			s.askGemini(input)
		}
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
	fmt.Println("  \033[1mSearch:\033[0m")
	fmt.Println("    \"search for <query> on <site>\"  search a site and analyze results")
	fmt.Println("    \"find <query> on <site>\"         same")
	fmt.Println("    \"<query> <site>\"                 auto-detect site search")
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
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return true
	}
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	words := strings.Fields(s)
	if len(words) != 1 {
		return false
	}
	return strings.Contains(s, ".") && !strings.Contains(s, " ")
}

func parseSearchIntent(input string) (site string, query string, ok bool) {
	lower := strings.ToLower(input)

	// Pattern: "search for <query> on <site>" or "find <query> on <site>"
	for _, prefix := range []string{"search for ", "search ", "find ", "show me "} {
		if strings.HasPrefix(lower, prefix) {
			rest := input[len(prefix):]
			if idx := strings.LastIndex(strings.ToLower(rest), " on "); idx >= 0 {
				site = strings.TrimSpace(rest[idx+4:])
				query = strings.TrimSpace(rest[:idx])
				if u := searchURL(site, query); u != "" {
					return site, query, true
				}
			}
		}
	}

	// Pattern: "<query> on <site>"
	if idx := strings.LastIndex(lower, " on "); idx >= 0 {
		site = strings.TrimSpace(input[idx+4:])
		query = strings.TrimSpace(input[:idx])
		if u := searchURL(site, query); u != "" {
			return site, query, true
		}
	}

	return "", "", false
}

func searchURL(site, query string) string {
	siteKey := strings.ToLower(strings.TrimSpace(site))
	siteKey = strings.TrimPrefix(siteKey, "www.")
	siteKey = strings.TrimSuffix(siteKey, ".com")
	siteKey = strings.TrimSuffix(siteKey, ".in")

	tmpl, ok := siteSearchURLs[siteKey]
	if !ok {
		return ""
	}
	return fmt.Sprintf(tmpl, url.QueryEscape(query))
}

func (s *session) searchSite(site, query string) {
	u := searchURL(site, query)
	if u == "" {
		fmt.Printf("  \033[31mUnknown site: %s\033[0m\n", site)
		return
	}

	fmt.Printf("  \033[2m→ searching %s for \"%s\" ...\033[0m\n", site, query)

	page, err := extract.Tier1(u, 30*time.Second)
	if err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	s.pageURL = page.URL
	s.pageText = fmt.Sprintf("Page: %s\nTitle: %s\n\n%s", page.URL, page.Title, page.Content)

	fmt.Printf("  \033[2m→ title:\033[0m %s\n", page.Title)
	fmt.Printf("  \033[2m→ elements:\033[0m %d  \033[2mcontent:\033[0m %d chars  \033[2mlatency:\033[0m %dms\n",
		len(page.Elements), page.Metadata.ContentLength, page.Metadata.LatencyMs)

	s.askGemini(fmt.Sprintf("What are the best results for \"%s\" on this page? Summarize the top products.", query))
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

	var prompt string
	if s.pageText != "" {
		prompt = fmt.Sprintf(`I fetched this page:

%s

The user asks: %s`, s.pageText, question)
	} else {
		prompt = question
	}

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
