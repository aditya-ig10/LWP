package repl

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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
	"gmail": "https://mail.google.com",
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

const noMarkdownPrompt = `You are a helpful assistant. Respond in PLAIN TEXT only.
Do NOT use Markdown formatting. No bold (**), no italics, no headings, no bullet lists with asterisks.
Use simple text with line breaks and numbers for lists. Never use links like [text](url).
Use plain text URLs instead. Never use code blocks or backticks.
Keep responses clear and readable in a terminal.`

// --- Spinner ---

type spinner struct {
	mu     sync.Mutex
	frames []string
	i      int
	msg    string
	done   chan struct{}
}

var spinFrames = []string{"-", "\\", "|", "/"}

func newSpinner(msg string) *spinner {
	return &spinner{
		frames: spinFrames,
		msg:    msg + " ...",
		done:   make(chan struct{}),
	}
}

func (s *spinner) start() {
	if !isTerminal() {
		fmt.Print(s.msg)
		return
	}
	go func() {
		for {
			select {
			case <-s.done:
				return
			default:
				s.mu.Lock()
				frame := s.frames[s.i%len(s.frames)]
				s.i++
				s.mu.Unlock()
				fmt.Printf("\r  [\033[36m%s\033[0m] %s", frame, s.msg)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

func (s *spinner) stop(doneMsg string) {
	if isTerminal() {
		close(s.done)
		msg := s.msg
		if doneMsg != "" {
			msg = s.msg + " " + doneMsg
		}
		fmt.Printf("\r  [\033[32m*\033[0m] %s\033[K\n", msg)
	} else {
		if doneMsg != "" {
			fmt.Println(doneMsg)
		} else {
			fmt.Println("done")
		}
	}
}

func (s *spinner) fail(errMsg string) {
	if isTerminal() {
		close(s.done)
		fmt.Printf("\r  [\033[31m!\033[0m] %s %s\033[K\n", s.msg, errMsg)
	} else {
		fmt.Println("FAIL:", errMsg)
	}
}

func step(label string) {
	fmt.Printf("  | \033[2m%s\033[0m\n", label)
}

// --- Session ---

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
		fmt.Print("  \033[2mAPI key:\033[0m ")
		key, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		s.apiKey = strings.TrimSpace(key)
		if s.apiKey != "" {
			os.Setenv("GEMINI_API_KEY", s.apiKey)
		}
	}

	if isTerminal() {
		s.selectModel()
	}

	for s.prompt() {
	}
}

func (s *session) prompt() bool {
	if isTerminal() {
		fmt.Print("  \033[1m> ")
	}
	if !s.scanner.Scan() {
		return false
	}
	input := strings.TrimSpace(s.scanner.Text())
	if isTerminal() {
		fmt.Print("\033[0m")
	}
	if input == "" {
		return true
	}

	switch {
	case input == "exit" || input == "quit":
		s.closeBrowser()
		fmt.Println("  \033[2mbye\033[0m")
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
		}
	case strings.HasPrefix(input, "key "):
		s.apiKey = strings.TrimPrefix(input, "key ")
		os.Setenv("GEMINI_API_KEY", s.apiKey)
	case input == "browser" || input == "live":
		s.startBrowser()
	case input == "fetch" || input == "tier1":
		s.closeBrowser()
		s.browserMode = false
		fmt.Println("  \033[2m[+] tier1 mode\033[0m")
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
	fmt.Println("  \033[1m+--- LWP ---+\033[0m")
	fmt.Println("  \033[2m  LLM Web Protocol\033[0m")
	fmt.Println()
}

func (s *session) printHelp() {
	fmt.Println()
	fmt.Println("  \033[1mcommands:\033[0m")
	fmt.Println("    \033[36m<url>\033[0m           fetch a page")
	fmt.Println("    \033[36m<question>\033[0m       ask gemini")
	fmt.Println("    \033[36mmodel\033[0m             select model")
	fmt.Println("    \033[36mkey <key>\033[0m         set API key")
	fmt.Println("    \033[36mhelp\033[0m              this help")
	fmt.Println("    \033[36mexit\033[0m              quit")
	fmt.Println()
	fmt.Println("  \033[1msearch:\033[0m")
	fmt.Println("    \033[2msearch for <query> on <site>\033[0m")
	fmt.Println()
	fmt.Println("  \033[1mbrowser actions:\033[0m")
	fmt.Println("    \033[36mclick <ref>\033[0m         click element")
	fmt.Println("    \033[36mtype <ref> \"text\"\033[0m   type into element")
	fmt.Println("    \033[36msubmit\033[0m               submit form")
	fmt.Println("    \033[36mss\033[0m                   screenshot")
	fmt.Println("    \033[36mrefresh\033[0m              reload")
	fmt.Println("    \033[36melements\033[0m             list elements")
	fmt.Println("    \033[36mfetch\033[0m                tier1 mode")
	fmt.Println()
}

func (s *session) selectModel() {
	mark := " \033[36m[*]\033[0m"
	plain := "    "
	for i, m := range models {
		p := plain
		if m == s.model {
			p = mark
		}
		fmt.Printf("  %s [\033[1m%d\033[0m] %s\n", p, i+1, m)
	}
	fmt.Printf("  %s [\033[1m0\033[0m] custom\n", plain)
	fmt.Print("  \033[2mselect:\033[0m ")

	line := s.readLine()
	if line == "" {
		return
	}
	if line == "0" || strings.EqualFold(line, "custom") {
		fmt.Print("  \033[2mname:\033[0m ")
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
		fmt.Println("\n  [\033[33m!\033[0m] browser closed (idle)")
		s.browser.Close()
		s.browser = nil
	})
}

func (s *session) startBrowser() {
	if s.browser != nil {
		s.browserMode = true
		s.resetIdleTimer()
		return
	}

	sp := newSpinner("launching headless Chrome")
	sp.start()
	b, err := browser.New(true)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	s.browser = b
	s.browserMode = true
	s.resetIdleTimer()
	sp.stop("ready")
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

	step(rawURL)

	sp := newSpinner("loading")
	sp.start()
	page, err := s.browser.Navigate(rawURL, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop(fmt.Sprintf("\033[36m%s\033[0m", page.Title))

	s.pageURL = page.URL
	s.pageText = page.Content
	s.elems = page.Elements
	s.browserMode = true
}

func (s *session) browseSearch(site, query string) {
	u := searchURL(site, query)
	if u == "" {
		fmt.Printf("  [\033[31m!\033[0m] unknown site: %s\n", site)
		return
	}
	step(fmt.Sprintf("\033[36m%s\033[0m \033[2m/ %s\033[0m", site, query))
	s.browseURL(u)
}

func (s *session) printElements() {
	if len(s.elems) == 0 {
		return
	}

	for _, e := range s.elems {
		label := fmt.Sprintf("\033[36m[%d]\033[0m <%s>", e.Ref, e.Tag)
		if e.Type != "" {
			label += fmt.Sprintf(" type=%s", e.Type)
		}
		if e.Name != "" {
			label += fmt.Sprintf(" name=%s", e.Name)
		}
		if e.Text != "" {
			label += fmt.Sprintf(" \033[2m%s\033[0m", truncate(e.Text, 60))
		}
		if e.Href != "" {
			label += fmt.Sprintf(" \033[2m-> %s\033[0m", e.Href)
		}
		fmt.Println("  " + label)
	}
	fmt.Printf("  \033[2m%d elements\033[0m\n", len(s.elems))
}

// --- Browser actions ---

func (s *session) doClick(input string) {
	s.resetIdleTimer()
	if s.browser == nil {
		return
	}

	parts := strings.Fields(input)
	if len(parts) < 2 {
		return
	}

	ref, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	sel := s.selectorForRef(ref)
	if sel == "" {
		return
	}

	sp := newSpinner(fmt.Sprintf("click [%d]", ref))
	sp.start()
	if err := s.browser.Click(sel); err != nil {
		sp.fail(err.Error())
		return
	}

	time.Sleep(1 * time.Second)
	page, err := s.browser.ReExtract()
	if err == nil {
		s.elems = page.Elements
		s.pageText = page.Content
		s.pageURL = page.URL
	}
	sp.stop("")
}

func (s *session) doType(input string) {
	s.resetIdleTimer()
	if s.browser == nil {
		return
	}

	parts := strings.SplitN(input, " ", 3)
	if len(parts) < 3 {
		return
	}

	ref, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	text := strings.Trim(parts[2], "\"")
	sel := s.selectorForRef(ref)
	if sel == "" {
		return
	}

	sp := newSpinner(fmt.Sprintf("type [%d]", ref))
	sp.start()
	if err := s.browser.Type(sel, text); err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop("")
}

func (s *session) doSubmit(selector string) {
	s.resetIdleTimer()
	if s.browser == nil {
		return
	}

	if selector == "" {
		selector = "form"
	}

	sp := newSpinner("submit")
	sp.start()
	if err := s.browser.Submit(selector); err != nil {
		sp.fail(err.Error())
		return
	}

	time.Sleep(2 * time.Second)
	page, err := s.browser.ReExtract()
	if err == nil {
		s.elems = page.Elements
		s.pageText = page.Content
		s.pageURL = page.URL
		sp.stop(fmt.Sprintf("\033[36m%s\033[0m", page.Title))
	} else {
		sp.stop("")
	}
}

func (s *session) doScreenshot() {
	s.resetIdleTimer()
	if s.browser == nil {
		return
	}

	sp := newSpinner("screenshot")
	sp.start()
	buf, err := s.browser.ScreenshotFull()
	if err != nil {
		sp.fail(err.Error())
		return
	}

	fname := fmt.Sprintf("lwp_screenshot_%d.png", time.Now().Unix())
	if err := os.WriteFile(fname, buf, 0644); err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop(fmt.Sprintf("saved %s (%d KB)", fname, len(buf)/1024))
}

func (s *session) doRefresh() {
	s.resetIdleTimer()
	if s.browser == nil {
		return
	}

	sp := newSpinner("refresh")
	sp.start()
	page, err := s.browser.Refresh()
	if err != nil {
		sp.fail(err.Error())
		return
	}

	s.elems = page.Elements
	s.pageText = page.Content
	s.pageURL = page.URL
	sp.stop(fmt.Sprintf("\033[36m%s\033[0m", page.Title))
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
		fmt.Printf("  [\033[31m!\033[0m] unknown site: %s\n", site)
		return
	}

	step(fmt.Sprintf("\033[36m%s\033[0m \033[2m/ %s\033[0m", site, query))

	sp := newSpinner("fetching")
	sp.start()
	page, err := extract.Tier1(u, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop(fmt.Sprintf("\033[36m%s\033[0m  (\033[2m%dkb %dms\033[0m)", page.Title, page.Metadata.ContentLength/1024, page.Metadata.LatencyMs))

	s.pageURL = page.URL
	s.pageText = fmt.Sprintf("Page: %s\nTitle: %s\n\n%s", page.URL, page.Title, page.Content)

	s.askGemini(fmt.Sprintf("What are the results for \"%s\" on this page? Summarize.", query))
}

func (s *session) fetchPage(rawURL string) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	if _, err := url.Parse(rawURL); err != nil {
		fmt.Printf("  [\033[31m!\033[0m] invalid URL\n")
		return
	}

	step(rawURL)

	sp := newSpinner("fetching")
	sp.start()
	page, err := extract.Tier1(rawURL, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop(fmt.Sprintf("\033[36m%s\033[0m  (\033[2m%dkb %dms\033[0m)", page.Title, page.Metadata.ContentLength/1024, page.Metadata.LatencyMs))

	s.pageURL = page.URL
	s.pageText = fmt.Sprintf("Page: %s\nTitle: %s\n\n%s", page.URL, page.Title, page.Content)
}

func (s *session) askGemini(question string) {
	if s.apiKey == "" {
		fmt.Printf("  [\033[31m!\033[0m] no API key. Use \033[36mkey <your-key>\033[0m\n")
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

		prompt = fmt.Sprintf(`%s

I fetched this page:

%s

The user asks: %s`, noMarkdownPrompt, s.pageText, question)
	} else {
		prompt = fmt.Sprintf(`%s

The user says: %s`, noMarkdownPrompt, question)
	}

	sp := newSpinner(fmt.Sprintf("\033[2m%s\033[0m", s.model))
	sp.start()
	answer, err := llm.Chat(prompt, 120*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop("")

	fmt.Println()
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
