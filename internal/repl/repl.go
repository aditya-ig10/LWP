package repl

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aditya-ig10/LWP/internal/browser"
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

var knownSites = map[string]string{
	"gmail":  "https://mail.google.com",
}

var siteSearchURLs = map[string]string{
	"flipkart":  "https://www.flipkart.com/search?q=%s",
	"amazon":    "https://www.amazon.com/s?k=%s",
	"amazon.in": "https://www.amazon.in/s?k=%s",
	"myntra":    "https://www.myntra.com/%s",
	"ajio":      "https://www.ajio.com/search/?text=%s",
	"ebay":      "https://www.ebay.com/sch/i.html?_nkw=%s",
	"walmart":   "https://www.walmart.com/search?q=%s",
	"target":    "https://www.target.com/s?searchTerm=%s",
	"bestbuy":   "https://www.bestbuy.com/site/searchpage.jsp?st=%s",
	"linkedin":  "https://www.linkedin.com/search/results/all/?keywords=%s",
	"reddit":    "https://www.reddit.com/search/?q=%s",
	"youtube":   "https://www.youtube.com/results?search_query=%s",
	"github":    "https://github.com/search?q=%s",
	"google":    "https://www.google.com/search?q=%s",
	"twitter":   "https://twitter.com/search?q=%s",
	"x.com":     "https://x.com/search?q=%s",
}

type session struct {
	scanner            *bufio.Scanner
	model              string
	apiKey             string
	pageURL            string
	pageText           string
	browser            *browser.Browser
	browserMode        bool
	elems              []browser.Element
	browserIdle        *time.Timer
	browserIdleTimeout time.Duration
}

func Start() {
	s := &session{
		scanner: bufio.NewScanner(os.Stdin),
		model:   "gemini-flash-lite-latest",
	}

	if isTerminal() {
		printBanner()
	}

	s.browserIdleTimeout = 60 * time.Second

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
		s.closeBrowser()
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
	case input == "browser" || input == "live":
		s.startBrowser()
	case input == "fetch" || input == "tier1":
		s.closeBrowser()
		s.browserMode = false
		fmt.Println("  \033[2m→ switched to Tier 1 (HTTP fetch) mode\033[0m")
	case strings.HasPrefix(input, "click "):
		s.doClick(input)
	case strings.HasPrefix(input, "type "):
		s.doType(input)
	case input == "submit":
		s.doSubmit("")
	case strings.HasPrefix(input, "submit "):
		s.doSubmit(strings.TrimPrefix(input, "submit "))
	case input == "ss" || input == "screenshot":
		s.doScreenshot()
	case input == "refresh":
		s.doRefresh()
	case input == "elements":
		s.printElements()
	case looksLikeURL(input):
		if s.browserMode && s.browser != nil {
			s.browseURL(input)
		} else {
			s.fetchPage(input)
		}
	default:
		if site, query, ok := parseSearchIntent(input); ok {
			if s.browserMode && s.browser != nil {
				s.browseSearch(site, query)
			} else {
				s.searchSite(site, query)
			}
		} else if url := extractURLFromSearch(input); url != "" {
			if s.browserMode && s.browser != nil {
				s.browseURL(url)
			} else {
				s.fetchPage(url)
			}
		} else {
			s.askGemini(input)
		}
	}
	return true
}

func printBanner() {
	fmt.Println()
	fmt.Println("  \033[1m╔══════════════════════════════════════╗\033[0m")
	fmt.Println("  \033[1m║  LWP — LLM Web Protocol              ║\033[0m")
	fmt.Println("  \033[1m║  Interactive mode                     ║\033[0m")
	fmt.Println("  \033[1m╚══════════════════════════════════════╝\033[0m")
	fmt.Println()
}

func (s *session) printHelp() {
	fmt.Println()
	fmt.Println("  \033[1mCommands:\033[0m")
	fmt.Println("    \033[1m<url>\033[0m            fetch a page")
	fmt.Println("    \033[1m<question>\033[0m        ask Gemini")
	fmt.Println("    \033[1mmodel\033[0m              select model")
	fmt.Println("    \033[1mkey <key>\033[0m          set API key")
	fmt.Println("    \033[1mhelp\033[0m               this help")
	fmt.Println("    \033[1mexit\033[0m               quit")
	fmt.Println()
	fmt.Println("  \033[1mSearch:\033[0m")
	fmt.Println("    \"search for <query> on <site>\"")
	fmt.Println("    \"search for <url>\"")
	fmt.Println()
	fmt.Println("  \033[1mBrowser mode (type \033[36mbrowser\033[0m \033[1mto start):\033[0m")
	fmt.Println("    \033[1mclick <ref>\033[0m          click element by ref number")
	fmt.Println("    \033[1mtype <ref> \"<text>\"\033[0m   type text into element")
	fmt.Println("    \033[1msubmit [ref]\033[0m         submit form")
	fmt.Println("    \033[1mss\033[0m                   take screenshot")
	fmt.Println("    \033[1mrefresh\033[0m              reload page")
	fmt.Println("    \033[1melements\033[0m             list all interactive elements")
	fmt.Println("    \033[1mfetch\033[0m                switch back to Tier 1 mode")
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

// --- URL detection ---

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return true
	}
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	words := strings.Fields(s)
	return len(words) == 1 && strings.Contains(s, ".")
}

func extractURLFromSearch(input string) string {
	lower := strings.ToLower(input)
	for _, prefix := range []string{"search for ", "search ", "open ", "go to "} {
		if strings.HasPrefix(lower, prefix) {
			rest := strings.TrimSpace(input[len(prefix):])
			if rest == "" {
				return ""
			}
			if looksLikeSingleURL(rest) {
				if !strings.HasPrefix(rest, "http://") && !strings.HasPrefix(rest, "https://") {
					rest = "https://" + rest
				}
				return rest
			}
		}
	}
	return ""
}

func looksLikeSingleURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return true
	}
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	words := strings.Fields(s)
	return len(words) == 1 && strings.Contains(s, ".")
}

func parseSearchIntent(input string) (site string, query string, ok bool) {
	lower := strings.ToLower(input)
	for _, prefix := range []string{"search for ", "search ", "find ", "show me "} {
		if strings.HasPrefix(lower, prefix) {
			rest := input[len(prefix):]
			if idx := strings.LastIndex(strings.ToLower(rest), " on "); idx >= 0 {
				site = strings.TrimSpace(rest[idx+4:])
				query = strings.TrimSpace(rest[:idx])
				if searchURL(site, query) != "" {
					return site, query, true
				}
			}
		}
	}
	if idx := strings.LastIndex(lower, " on "); idx >= 0 {
		site = strings.TrimSpace(input[idx+4:])
		query = strings.TrimSpace(input[:idx])
		if searchURL(site, query) != "" {
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

// --- Browser mode ---

func (s *session) resetIdleTimer() {
	if s.browser == nil {
		return
	}
	if s.browserIdle != nil {
		s.browserIdle.Stop()
	}
	s.browserIdle = time.AfterFunc(s.browserIdleTimeout, func() {
		fmt.Println("\n  \033[33m⚠ Browser auto-closed (idle timeout)\033[0m")
		s.browser.Close()
		s.browser = nil
	})
}

func (s *session) startBrowser() {
	if s.browser != nil {
		fmt.Println("  \033[2m→ browser already running\033[0m")
		s.browserMode = true
		s.resetIdleTimer()
		return
	}

	fmt.Print("  \033[2m→ launching headless Chrome ...\033[0m")
	b, err := browser.New(true)
	if err != nil {
		fmt.Printf(" \033[31m%s\033[0m\n", err)
		return
	}
	s.browser = b
	s.browserMode = true
	s.resetIdleTimer()
	fmt.Println(" \033[32mdone\033[0m")
}

func (s *session) closeBrowser() {
	if s.browserIdle != nil {
		s.browserIdle.Stop()
		s.browserIdle = nil
	}
	if s.browser != nil {
		s.browser.Close()
		s.browser = nil
	}
	s.browserMode = false
}

func (s *session) browseURL(rawURL string) {
	s.resetIdleTimer()
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	fmt.Printf("  \033[2m→ browsing %s ...\033[0m\n", rawURL)

	page, err := s.browser.Navigate(rawURL, 30*time.Second)
	if err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	s.pageURL = page.URL
	s.pageText = page.Content
	s.elems = page.Elements
	s.browserMode = true

	fmt.Printf("  \033[2m→ title:\033[0m %s\n", page.Title)
	fmt.Printf("  \033[2m→ elements:\033[0m %d  \033[2mcontent:\033[0m %d chars\n",
		len(page.Elements), len(page.Content))

	s.printElementSummary()
}

func (s *session) browseSearch(site, query string) {
	u := searchURL(site, query)
	if u == "" {
		fmt.Printf("  \033[31mUnknown site: %s\033[0m\n", site)
		return
	}
	fmt.Printf("  \033[2m→ searching %s for \"%s\" ...\033[0m\n", site, query)
	s.browseURL(u)
}

func (s *session) printElements() {
	if len(s.elems) == 0 {
		fmt.Println("  \033[33mNo elements loaded. Browse a page first.\033[0m")
		return
	}

	fmt.Println()
	for _, e := range s.elems {
		label := fmt.Sprintf("\033[36m[%d]\033[0m <%s>", e.Ref, e.Tag)
		if e.Type != "" {
			label += fmt.Sprintf(" type=%s", e.Type)
		}
		if e.Name != "" {
			label += fmt.Sprintf(" name=%s", e.Name)
		}
		if e.Text != "" {
			label += fmt.Sprintf(" \033[2m\"%s\"\033[0m", truncate(e.Text, 60))
		}
		if e.Href != "" {
			label += fmt.Sprintf(" \033[2m→ %s\033[0m", e.Href)
		}
		fmt.Println("  " + label)
	}
	fmt.Printf("  \033[2mtotal: %d elements\033[0m\n", len(s.elems))
	fmt.Println()
}

func (s *session) printElementSummary() {
	if len(s.elems) == 0 {
		return
	}

	var links, inputs, buttons int
	for _, e := range s.elems {
		switch e.Tag {
		case "a":
			links++
		case "input", "select", "textarea":
			inputs++
		case "button":
			buttons++
		}
	}

	var parts []string
	if links > 0 {
		parts = append(parts, fmt.Sprintf("%d links", links))
	}
	if inputs > 0 {
		parts = append(parts, fmt.Sprintf("%d inputs", inputs))
	}
	if buttons > 0 {
		parts = append(parts, fmt.Sprintf("%d buttons", buttons))
	}
	if len(parts) > 0 {
		fmt.Printf("  \033[2m→ \033[0m%s\n", strings.Join(parts, ", "))
	}
}

// --- Browser actions ---

func (s *session) doClick(input string) {
	s.resetIdleTimer()
	if s.browser == nil {
		fmt.Println("  \033[33mBrowser not started. Type 'browser' first.\033[0m")
		return
	}

	parts := strings.Fields(input)
	if len(parts) < 2 {
		fmt.Println("  \033[33mUsage: click <ref>\033[0m")
		return
	}

	ref, err := strconv.Atoi(parts[1])
	if err != nil {
		fmt.Println("  \033[33mInvalid ref number\033[0m")
		return
	}

	sel := s.selectorForRef(ref)
	if sel == "" {
		fmt.Printf("  \033[33mNo element with ref %d\033[0m\n", ref)
		return
	}

	fmt.Printf("  \033[2m→ clicking [%d] ...\033[0m\n", ref)
	if err := s.browser.Click(sel); err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	time.Sleep(1 * time.Second)
	page, err := s.browser.ReExtract()
	if err == nil {
		s.elems = page.Elements
		s.pageText = page.Content
		s.pageURL = page.URL
	}

	fmt.Println("  \033[32mdone\033[0m")
}

func (s *session) doType(input string) {
	s.resetIdleTimer()
	if s.browser == nil {
		fmt.Println("  \033[33mBrowser not started. Type 'browser' first.\033[0m")
		return
	}

	parts := strings.SplitN(input, " ", 3)
	if len(parts) < 3 {
		fmt.Println("  \033[33mUsage: type <ref> \"<text>\"\033[0m")
		return
	}

	ref, err := strconv.Atoi(parts[1])
	if err != nil {
		fmt.Println("  \033[33mInvalid ref number\033[0m")
		return
	}

	text := strings.Trim(parts[2], "\"")
	sel := s.selectorForRef(ref)
	if sel == "" {
		fmt.Printf("  \033[33mNo element with ref %d\033[0m\n", ref)
		return
	}

	fmt.Printf("  \033[2m→ typing into [%d] ...\033[0m\n", ref)
	if err := s.browser.Type(sel, text); err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	fmt.Println("  \033[32mdone\033[0m")
}

func (s *session) doSubmit(selector string) {
	s.resetIdleTimer()
	if s.browser == nil {
		fmt.Println("  \033[33mBrowser not started. Type 'browser' first.\033[0m")
		return
	}

	if selector == "" {
		selector = "form"
	}

	fmt.Print("  \033[2m→ submitting ...\033[0m")
	if err := s.browser.Submit(selector); err != nil {
		fmt.Printf(" \033[31m%s\033[0m\n", err)
		return
	}

	time.Sleep(2 * time.Second)
	page, err := s.browser.ReExtract()
	if err == nil {
		s.elems = page.Elements
		s.pageText = page.Content
		s.pageURL = page.URL
		fmt.Printf(" \033[32mdone\033[0m  \033[2m→ %s\033[0m\n", page.Title)
	} else {
		fmt.Println(" \033[32mdone\033[0m")
	}
}

func (s *session) doScreenshot() {
	s.resetIdleTimer()
	if s.browser == nil {
		fmt.Println("  \033[33mBrowser not started. Type 'browser' first.\033[0m")
		return
	}

	buf, err := s.browser.ScreenshotFull()
	if err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	fname := fmt.Sprintf("lwp_screenshot_%d.png", time.Now().Unix())
	if err := os.WriteFile(fname, buf, 0644); err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	fmt.Printf("  \033[2m→ saved: %s (%d KB)\033[0m\n", fname, len(buf)/1024)
}

func (s *session) doRefresh() {
	s.resetIdleTimer()
	if s.browser == nil {
		fmt.Println("  \033[33mBrowser not started. Type 'browser' first.\033[0m")
		return
	}

	page, err := s.browser.Refresh()
	if err != nil {
		fmt.Printf("  \033[31mError: %s\033[0m\n", err)
		return
	}

	s.elems = page.Elements
	s.pageText = page.Content
	s.pageURL = page.URL

	fmt.Printf("  \033[2m→ refreshed: %s\033[0m\n", page.Title)
}

func (s *session) selectorForRef(ref int) string {
	for _, e := range s.elems {
		if e.Ref == ref {
			return e.Selector
		}
	}
	return ""
}

// --- Tier 1 mode ---

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
		if len(s.elems) > 0 {
			var elemBlock strings.Builder
			elemBlock.WriteString("\n\nInteractive elements on this page:\n")
			for _, e := range s.elems {
				line := fmt.Sprintf("  [%d] <%s>", e.Ref, e.Tag)
				if e.Text != "" {
					line += fmt.Sprintf(" \"%s\"", truncate(e.Text, 80))
				}
				if e.Href != "" {
					line += fmt.Sprintf(" href=%s", e.Href)
				}
				if e.Type != "" {
					line += fmt.Sprintf(" type=%s", e.Type)
				}
				elemBlock.WriteString(line + "\n")
			}
			elemBlock.WriteString("\nYou can interact with elements using: click <ref>, type <ref> \"text\", submit")
			s.pageText += elemBlock.String()
		}

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
