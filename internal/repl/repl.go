package repl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/aditya-ig10/LWP/internal/llm"
	"github.com/aditya-ig10/LWP/internal/browser"
	"github.com/aditya-ig10/LWP/internal/extract"
)

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

const noMarkdown = "Respond in PLAIN TEXT only. No markdown, no bold, no italics, no headings. Use simple text with line breaks. Use plain URLs like https://example.com. Never use brackets for links."

const maxHistory = 6
const pageTextMax = 3000

// --- Config ---

type config struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/lwp"
}

func configPath() string {
	return configDir() + "/config.json"
}

func loadConfig() *config {
	os.MkdirAll(configDir(), 0700)
	data, err := os.ReadFile(configPath())
	if err != nil {
		return &config{}
	}
	var cfg config
	json.Unmarshal(data, &cfg)
	return &cfg
}

func (c *config) save() {
	os.MkdirAll(configDir(), 0700)
	data, _ := json.MarshalIndent(c, "", "  ")
	os.WriteFile(configPath(), data, 0600)
}

// --- Spinner ---

type spinner struct {
	mu     sync.Mutex
	frames []string
	i      int
	msg    string
	done   chan struct{}
}

func newSpinner(msg string) *spinner {
	return &spinner{
		frames: []string{"-", "\\", "|", "/"},
		msg:    msg + " ...",
		done:   make(chan struct{}),
	}
}

func (s *spinner) start() {
	go func() {
		for {
			select {
			case <-s.done:
				return
			default:
				s.mu.Lock()
				f := s.frames[s.i%len(s.frames)]
				s.i++
				s.mu.Unlock()
				fmt.Printf("\r  [\033[36m%s\033[0m] %s\033[K", f, s.msg)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

func (s *spinner) stop(doneMsg string) {
	close(s.done)
	m := s.msg
	if doneMsg != "" {
		m = m + " " + doneMsg
	}
	fmt.Printf("\r  [\033[32m*\033[0m] %s\033[K\n", m)
}

func (s *spinner) fail(errMsg string) {
	close(s.done)
	fmt.Printf("\r  [\033[31m!\033[0m] %s %s\033[K\n", s.msg, errMsg)
}

// --- Exchange (for conversation memory) ---

type exchange struct {
	user string
	ai   string
}

// --- Session ---

type session struct {
	cfg              *config
	scanner          *bufio.Scanner
	model            string
	apiKey           string
	pageURL          string
	pageText         string
	br               *browser.Browser
	browserMode      bool
	elems            []browser.Element
	shutdownTimer    *time.Timer
	history          []exchange
	messages         []message
}

type message struct {
	role    string
	content string
}

func Start() {
	s := &session{
		cfg:   loadConfig(),
		model: "gemini-flash-lite-latest",
	}

	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		s.apiKey = key
	} else if s.cfg.APIKey != "" {
		s.apiKey = s.cfg.APIKey
		os.Setenv("GEMINI_API_KEY", s.apiKey)
	}
	if s.cfg.Model != "" {
		s.model = s.cfg.Model
		os.Setenv("GEMINI_MODEL", s.model)
	}

	s.scanner = bufio.NewScanner(os.Stdin)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		s.killBrowser()
		fmt.Println()
		os.Exit(0)
	}()

	printTitle()

	if s.apiKey == "" {
		s.promptAPIKey()
	}
	if s.apiKey != "" {
		s.selectModel()
	}

	for s.prompt() {
	}
}

func printTitle() {
	fmt.Println()
	fmt.Println("  \033[1m+--- LWP ---+\033[0m")
	fmt.Println("  \033[2m  web. for AI.\033[0m")
	fmt.Println()
}

func (s *session) promptAPIKey() {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		s.apiKey = key
		return
	}
	fmt.Print("  \033[2mAPI key:\033[0m ")
	key, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	key = strings.TrimSpace(key)
	if key != "" {
		s.apiKey = key
		os.Setenv("GEMINI_API_KEY", key)
		s.cfg.APIKey = key
		s.cfg.save()
	}
}

// --- Arrow-key model selector ---

func readRaw(timeout time.Duration) ([]byte, error) {
	buf := make([]byte, 4)
	if timeout <= 0 {
		n, err := os.Stdin.Read(buf)
		if n == 0 {
			return nil, fmt.Errorf("no data")
		}
		return buf[:n], err
	}
	rch := make(chan []byte, 1)
	ech := make(chan error, 1)
	go func() {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			rch <- buf[:n]
		}
		if err != nil {
			ech <- err
		}
	}()
	select {
	case b := <-rch:
		return b, nil
	case err := <-ech:
		return nil, err
	case <-time.After(timeout):
		return nil, nil
	}
}

func (s *session) selectModel() {
	if !isTerminal() {
		return
	}
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		s.selectModelSimple()
		return
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := 0
	for i, m := range models {
		if m == s.model {
			selected = i
			break
		}
	}

	draw := func() {
		fmt.Print("\033[?25l")
		for i, m := range models {
			mark := "  "
			cur := "  "
			if i == selected {
				mark = "\033[36m[*]\033[0m"
				cur = " \033[36m<\033[0m"
			}
			fmt.Printf("\r\033[K  %s [\033[1m%d\033[0m] %s%s\n", mark, i+1, m, cur)
		}
		fmt.Printf("\r\033[K  [\033[2m0\033[0m] custom\n")
		fmt.Printf("\r\033[K  \033[2m\u2191/\u2193 navigate, enter to select\033[0m")
	}

	draw()

	readSeq := func() ([]byte, error) {
		b, err := readRaw(0)
		if err != nil || len(b) == 0 {
			return b, err
		}
		if b[0] != 0x1b {
			return b, nil
		}
		rest, _ := readRaw(50 * time.Millisecond)
		if len(rest) > 0 {
			b = append(b, rest...)
		}
		return b, nil
	}

	for {
		b, err := readSeq()
		if err != nil || len(b) == 0 {
			continue
		}
		if len(b) >= 3 && b[0] == 0x1b && b[1] == '[' {
			switch b[2] {
			case 'A':
				if selected > 0 {
					selected--
				}
			case 'B':
				if selected < len(models)-1 {
					selected++
				}
			}
			fmt.Print("\033[" + strconv.Itoa(len(models)+2) + "A")
			draw()
			continue
		}
		c := b[0]
		if c >= '1' && c <= '9' {
			idx := int(c - '1')
			if idx < len(models) {
				selected = idx
				break
			}
		}
		if c == '0' {
			selected = -1
			break
		}
		if c == 0x0d || c == 0x0a {
			break
		}
		if c == 0x03 {
			os.Exit(0)
		}
	}

	fmt.Print("\033[?25h")
	fmt.Print("\033[" + strconv.Itoa(len(models)+2) + "B")
	fmt.Println()

	if selected >= 0 && selected < len(models) {
		s.model = models[selected]
		os.Setenv("GEMINI_MODEL", s.model)
	} else if selected == -1 {
		term.Restore(int(os.Stdin.Fd()), oldState)
		fmt.Print("  \033[2mname:\033[0m ")
		name, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		name = strings.TrimSpace(name)
		if name != "" {
			s.model = name
			os.Setenv("GEMINI_MODEL", s.model)
		}
		newState, _ := term.MakeRaw(int(os.Stdin.Fd()))
		defer term.Restore(int(os.Stdin.Fd()), newState)
	}

	if s.model != s.cfg.Model {
		s.cfg.Model = s.model
		s.cfg.save()
	}
}

func (s *session) selectModelSimple() {
	for i, m := range models {
		mark := "  "
		if m == s.model {
			mark = " \033[36m[*]\033[0m"
		}
		fmt.Printf("  %s [\033[1m%d\033[0m] %s\n", mark, i+1, m)
	}
	fmt.Print("  \033[2mselect:\033[0m ")
	line := s.readLine()
	if line == "0" || strings.EqualFold(line, "custom") {
		fmt.Print("  \033[2mname:\033[0m ")
		name := s.readLine()
		if name != "" {
			s.model = name
			os.Setenv("GEMINI_MODEL", s.model)
		}
	} else {
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
			s.model = models[idx-1]
			os.Setenv("GEMINI_MODEL", s.model)
		}
	}
}

func (s *session) readLine() string {
	if s.scanner.Scan() {
		return strings.TrimSpace(s.scanner.Text())
	}
	return ""
}

// --- Chat UI ---

func (s *session) addMsg(role, content string) {
	s.messages = append(s.messages, message{role, content})
}

func (s *session) redraw() {
	fmt.Print("\033[H\033[J")
	printTitle()
	for _, m := range s.messages {
		if m.role == "user" {
			fmt.Printf("  \033[1m> %s\033[0m\n", m.content)
		} else {
			for _, line := range strings.Split(strings.TrimSpace(m.content), "\n") {
				fmt.Printf("  %s\n", line)
			}
			fmt.Println()
		}
	}
}

// --- REPL ---

func (s *session) prompt() bool {
	if !isTerminal() {
		if !s.scanner.Scan() {
			return false
		}
		input := strings.TrimSpace(s.scanner.Text())
		if input == "" {
			return true
		}
		s.handleInput(input)
		return true
	}
	fmt.Print("  \033[1m> ")
	if !s.scanner.Scan() {
		return false
	}
	input := strings.TrimSpace(s.scanner.Text())
	fmt.Print("\033[0m\n")
	if input == "" {
		return true
	}
	s.handleInput(input)
	return true
}

func (s *session) handleInput(input string) {
	switch {
	case input == "exit" || input == "quit":
		s.killBrowser()
		s.addMsg("assistant", "bye.")
		s.redraw()
		os.Exit(0)
	case input == "help":
		s.printHelp()
	case input == "model":
		s.selectModel()
	case strings.HasPrefix(input, "model "):
		m := strings.TrimPrefix(input, "model ")
		if m != "" {
			s.model = m
			os.Setenv("GEMINI_MODEL", m)
			s.cfg.Model = m
			s.cfg.save()
		}
	case strings.HasPrefix(input, "key "):
		s.apiKey = strings.TrimPrefix(input, "key ")
		os.Setenv("GEMINI_API_KEY", s.apiKey)
		s.cfg.APIKey = s.apiKey
		s.cfg.save()
	case input == "browser" || input == "live":
		s.startBrowser()
	case input == "fetch" || input == "tier1":
		s.killBrowser()
		s.browserMode = false
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
		s.openPage(input)
	default:
		if site, query, ok := parseSearchIntent(input); ok {
			s.searchSite(site, query)
		} else if url := extractURLFromSearch(input); url != "" {
			s.openPage(url)
		} else {
			s.askAI(input)
		}
	}
}

func (s *session) printHelp() {
	fmt.Println()
	fmt.Println("  \033[1mcommands:\033[0m")
	fmt.Println("    \033[36m<url>\033[0m             fetch a page")
	fmt.Println("    \033[36m<question>\033[0m         ask gemini")
	fmt.Println("    \033[36mmodel\033[0m               open model selector")
	fmt.Println("    \033[36mkey <key>\033[0m           save API key")
	fmt.Println("    \033[36mhelp\033[0m                this help")
	fmt.Println("    \033[36mexit\033[0m                quit")
	fmt.Println()
	fmt.Println("  \033[1mbrowser:\033[0m")
	fmt.Println("    \033[36mclick <ref>\033[0m         click element")
	fmt.Println("    \033[36mtype <ref> \"text\"\033[0m   type into element")
	fmt.Println("    \033[36msubmit\033[0m               submit form")
	fmt.Println("    \033[36mss\033[0m                   screenshot")
	fmt.Println("    \033[36mrefresh\033[0m              reload")
	fmt.Println("    \033[36melements\033[0m             list elements")
	fmt.Println()
}

// --- URL / Intent parsing ---

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

// --- Browser lifecycle ---

func (s *session) resetShutdown() {
	if s.br == nil {
		return
	}
	if s.shutdownTimer != nil {
		s.shutdownTimer.Stop()
	}
	s.shutdownTimer = time.AfterFunc(20*time.Second, func() {
		s.br.Close()
		s.br = nil
		s.browserMode = false
	})
}

func (s *session) startBrowser() {
	if s.br != nil {
		s.browserMode = true
		s.resetShutdown()
		return
	}
	sp := newSpinner("launching headless Chrome")
	sp.start()
	b, err := browser.New(true)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	s.br = b
	s.browserMode = true
	s.resetShutdown()
	sp.stop("ready")
}

func (s *session) killBrowser() {
	if s.shutdownTimer != nil {
		s.shutdownTimer.Stop()
		s.shutdownTimer = nil
	}
	if s.br != nil {
		s.br.Close()
		s.br = nil
	}
	s.browserMode = false
}

// --- Page opening ---

func (s *session) openPage(rawURL string) {
	s.addMsg("user", rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	if s.browserMode && s.br != nil {
		s.browseURL(rawURL)
	} else {
		s.fetchPage(rawURL)
	}
}

func (s *session) fetchPage(rawURL string) {
	sp := newSpinner("fetching")
	sp.start()
	page, err := extract.Tier1(rawURL, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	detail := fmt.Sprintf("\033[36m%s\033[0m  (\033[2m%dkb %dms\033[0m)", page.Title, page.Metadata.ContentLength/1024, page.Metadata.LatencyMs)
	sp.stop("opened " + detail)

	s.pageURL = page.URL
	s.pageText = page.Content
	s.elems = nil
	s.history = nil

	s.askAI("What is on this page? Give a 2-3 sentence summary.")
}

func (s *session) browseURL(rawURL string) {
	s.resetShutdown()
	sp := newSpinner("loading")
	sp.start()
	page, err := s.br.Navigate(rawURL, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop("opened \033[36m" + page.Title + "\033[0m")

	s.pageURL = page.URL
	s.pageText = page.Content
	s.elems = page.Elements
	s.history = nil

	if hasAuthPage(page) {
		s.handleAuth(rawURL)
		return
	}

	s.askAI("What is on this page? Give a 2-3 sentence summary.")
}

func (s *session) browseSearch(site, query string) {
	u := searchURL(site, query)
	if u == "" {
		s.addMsg("assistant", "unknown site: "+site)
		s.redraw()
		return
	}
	s.browseURL(u)
}

// --- Auth flow ---

func hasAuthPage(page *browser.Page) bool {
	lower := strings.ToLower(page.Title + " " + page.Content)
	signals := []string{"sign in", "log in", "login", "password"}
	count := 0
	for _, sig := range signals {
		if strings.Contains(lower, sig) {
			count++
		}
	}
	return count >= 2
}

func (s *session) handleAuth(rawURL string) {
	fmt.Printf("\n  \033[33m!\033[0m This page requires authentication.\n")
	fmt.Printf("    Open this URL in your browser to sign in:\n")
	fmt.Printf("    \033[36m%s\033[0m\n\n", rawURL)
	fmt.Printf("    Press Enter here after signing in.\n")

	s.scanner.Scan()

	page, err := s.br.ReExtract()
	if err != nil {
		s.addMsg("assistant", "Auth failed: "+err.Error())
		s.redraw()
		return
	}

	s.pageURL = page.URL
	s.pageText = page.Content
	s.elems = page.Elements
	s.history = nil

	s.askAI("What is on this page now? Give a 2-3 sentence summary.")
}

// --- Browser actions ---

func (s *session) printElements() {
	if len(s.elems) == 0 {
		return
	}
	for _, e := range s.elems {
		label := fmt.Sprintf("\033[36m[%d]\033[0m <%s>", e.Ref, e.Tag)
		if e.Type != "" {
			label += " type=" + e.Type
		}
		if e.Name != "" {
			label += " name=" + e.Name
		}
		if e.Text != "" {
			label += " \033[2m" + truncate(e.Text, 60) + "\033[0m"
		}
		if e.Href != "" {
			label += " \033[2m-> " + e.Href + "\033[0m"
		}
		fmt.Println("  " + label)
	}
	fmt.Printf("  \033[2m%d elements\033[0m\n", len(s.elems))
}

func (s *session) doClick(input string) {
	s.resetShutdown()
	if s.br == nil {
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
	if err := s.br.Click(sel); err != nil {
		sp.fail(err.Error())
		return
	}
	time.Sleep(1 * time.Second)
	page, err := s.br.ReExtract()
	if err == nil {
		s.elems = page.Elements
		s.pageText = page.Content
		s.pageURL = page.URL
	}
	sp.stop("")
	s.askAI("What happened after the click?")
}

func (s *session) doType(input string) {
	s.resetShutdown()
	if s.br == nil {
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
	if err := s.br.Type(sel, text); err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop("")
}

func (s *session) doSubmit(selector string) {
	s.resetShutdown()
	if s.br == nil {
		return
	}
	if selector == "" {
		selector = "form"
	}
	sp := newSpinner("submit")
	sp.start()
	if err := s.br.Submit(selector); err != nil {
		sp.fail(err.Error())
		return
	}
	time.Sleep(2 * time.Second)
	page, err := s.br.ReExtract()
	if err == nil {
		s.elems = page.Elements
		s.pageText = page.Content
		s.pageURL = page.URL
		sp.stop(page.Title)
	} else {
		sp.stop("")
	}
	s.askAI("What happened after the submit?")
}

func (s *session) doScreenshot() {
	s.resetShutdown()
	if s.br == nil {
		return
	}
	sp := newSpinner("screenshot")
	sp.start()
	buf, err := s.br.ScreenshotFull()
	if err != nil {
		sp.fail(err.Error())
		return
	}
	fname := fmt.Sprintf("lwp_%d.png", time.Now().Unix())
	if err := os.WriteFile(fname, buf, 0644); err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop("saved " + fname)
}

func (s *session) doRefresh() {
	s.resetShutdown()
	if s.br == nil {
		return
	}
	sp := newSpinner("refresh")
	sp.start()
	page, err := s.br.Refresh()
	if err != nil {
		sp.fail(err.Error())
		return
	}
	s.elems = page.Elements
	s.pageText = page.Content
	s.pageURL = page.URL
	sp.stop(page.Title)
	s.askAI("What is on this page now?")
}

func (s *session) selectorForRef(ref int) string {
	for _, e := range s.elems {
		if e.Ref == ref {
			return e.Selector
		}
	}
	return ""
}

// --- Tier 1 search ---

func (s *session) searchSite(site, query string) {
	u := searchURL(site, query)
	if u == "" {
		s.addMsg("assistant", "unknown site: "+site)
		s.redraw()
		return
	}
	sp := newSpinner("fetching")
	sp.start()
	page, err := extract.Tier1(u, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	detail := fmt.Sprintf("\033[36m%s\033[0m  (\033[2m%dkb %dms\033[0m)", page.Title, page.Metadata.ContentLength/1024, page.Metadata.LatencyMs)
	sp.stop(detail)

	s.pageURL = page.URL
	s.pageText = page.Content
	s.elems = nil
	s.history = nil

	s.askAI(fmt.Sprintf("What are the results for \"%s\" on this page? Summarize briefly.", query))
}

// --- AI chat with history ---

func (s *session) askAI(question string) {
	if s.apiKey == "" {
		s.addMsg("assistant", "No API key set. Use \033[36mkey <your-key>\033[0m")
		s.redraw()
		return
	}

	s.addMsg("user", question)

	// Build prompt with page context (trimmed for token efficiency)
	var prompt string
	if s.pageText != "" {
		text := s.pageText
		if len(text) > pageTextMax {
			text = text[:pageTextMax] + "..."
		}
		elemBlock := ""
		if len(s.elems) > 0 {
			var b strings.Builder
			b.WriteString("\n\nInteractive elements:\n")
			count := 0
			for _, e := range s.elems {
				if count >= 20 {
					break
				}
				line := fmt.Sprintf("  [%d] <%s>", e.Ref, e.Tag)
				if e.Text != "" {
					line += " \"" + truncate(e.Text, 60) + "\""
				}
				if e.Href != "" {
					line += " href=" + e.Href
				}
				b.WriteString(line + "\n")
				count++
			}
			elemBlock = b.String()
		}
		prompt = noMarkdown + "\n\nCurrent page: " + s.pageURL + "\nTitle: " + truncate(findTitle(s.pageText), 100) + "\n\nPage content:\n" + text + elemBlock + "\n\nThe user asks: " + question
	} else {
		prompt = noMarkdown + "\n\nThe user says: " + question
	}

	// Add conversation history (token-efficient: last N exchanges)
	history := buildHistory(s.history)

	os.Setenv("GEMINI_MODEL", s.model)
	sp := newSpinner(s.model)
	sp.start()
	answer, err := llm.ChatWithHistory(prompt, history, 120*time.Second)
	if err != nil {
		sp.fail(err.Error())
		s.messages = s.messages[:len(s.messages)-1]
		return
	}
	sp.stop("")

	// Store exchange in history
	s.history = append(s.history, exchange{user: question, ai: answer})
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}

	s.addMsg("assistant", answer)
	s.redraw()
}

func findTitle(text string) string {
	lines := strings.SplitN(text, "\n", 5)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 3 {
			return line
		}
	}
	if len(text) > 100 {
		return text[:100]
	}
	return text
}

func buildHistory(exchanges []exchange) []llm.Content {
	var out []llm.Content
	start := 0
	if len(exchanges) > maxHistory {
		start = len(exchanges) - maxHistory
	}
	for _, ex := range exchanges[start:] {
		if ex.user != "" {
			out = append(out, llm.Content{Role: "user", Parts: []llm.Part{{Text: ex.user}}})
		}
		if ex.ai != "" {
			out = append(out, llm.Content{Role: "model", Parts: []llm.Part{{Text: ex.ai}}})
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func isTerminal() bool {
	fi, _ := os.Stdin.Stat()
	return (fi.Mode() & os.ModeCharDevice) != 0
}
