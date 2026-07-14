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

	"github.com/aditya-ig10/LWP/internal/extract"
	"github.com/aditya-ig10/LWP/internal/llm"
)

var models = []string{
	"gemini-flash-lite-latest",
	"gemini-2.5-flash",
	"gemini-2.5-pro",
	"gemini-3-flash-preview",
	"gemini-3.1-flash-lite-preview",
}

var knownSites = map[string]string{
	"gmail":         "https://mail.google.com",
	"flipkart":      "https://www.flipkart.com",
	"amazon":        "https://www.amazon.com",
	"google":        "https://www.google.com",
	"youtube":       "https://www.youtube.com",
	"github":        "https://github.com",
	"reddit":        "https://www.reddit.com",
	"twitter":       "https://twitter.com",
	"x":             "https://x.com",
	"linkedin":      "https://www.linkedin.com",
	"myntra":        "https://www.myntra.com",
	"ajio":          "https://www.ajio.com",
	"ebay":          "https://www.ebay.com",
	"walmart":       "https://www.walmart.com",
	"target":        "https://www.target.com",
	"netflix":       "https://www.netflix.com",
	"wikipedia":     "https://www.wikipedia.org",
	"instagram":     "https://www.instagram.com",
	"facebook":      "https://www.facebook.com",
	"stackoverflow": "https://stackoverflow.com",
	"whatsapp":      "https://web.whatsapp.com",
	"discord":       "https://discord.com",
	"notion":        "https://www.notion.so",
	"medium":        "https://medium.com",
	"cnn":           "https://www.cnn.com",
	"bbc":           "https://www.bbc.com",
	"imdb":          "https://www.imdb.com",
}

func resolveSite(name string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if u, ok := knownSites[key]; ok {
		return u, true
	}
	if !strings.Contains(key, ".") && !strings.Contains(key, " ") {
		return "https://www." + key + ".com", true
	}
	return "", false
}

const noMarkdown = "Respond in PLAIN TEXT only. No markdown, no bold, no italics, no headings. Use simple text with line breaks."

const maxHistory = 6

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
		msg:    msg,
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
				time.Sleep(80 * time.Millisecond)
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

// --- Session ---

type exchange struct {
	user string
	ai   string
}

type session struct {
	cfg     *config
	scanner *bufio.Scanner
	model   string
	apiKey  string
	history []exchange
	msgs    []msg
	lastURL string
	lastContent string
}

type msg struct {
	role string
	text string
}

func Start() {
	s := &session{cfg: loadConfig(), model: "gemini-flash-lite-latest"}

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
		fmt.Println()
		os.Exit(0)
	}()

	title()
	if s.apiKey == "" { s.promptKey() }
	if s.apiKey != "" { s.selectModel() }
	for s.prompt() {
	}
}

func title() {
	fmt.Println()
	fmt.Println("  \033[1m+--- LWP ---+\033[0m")
	fmt.Println("  \033[2m  LLM Web Protocol 0.1\033[0m")
	fmt.Println("  \033[2m  no browser. no bloat.\033[0m")
	fmt.Println()
}

func (s *session) promptKey() {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		s.apiKey = key
		return
	}
	fmt.Print("  \033[2mAPI key:\033[0m ")
	k, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	k = strings.TrimSpace(k)
	if k != "" {
		s.apiKey = k
		os.Setenv("GEMINI_API_KEY", k)
		s.cfg.APIKey = k
		s.cfg.save()
	}
}

func (s *session) readLine() string {
	if s.scanner.Scan() { return strings.TrimSpace(s.scanner.Text()) }
	return ""
}

// --- Arrow key model selector ---

func readRaw(timeout time.Duration) ([]byte, error) {
	buf := make([]byte, 4)
	if timeout <= 0 {
		n, err := os.Stdin.Read(buf)
		if n == 0 { return nil, fmt.Errorf("no data") }
		return buf[:n], err
	}
	rch := make(chan []byte, 1)
	ech := make(chan error, 1)
	go func() {
		n, err := os.Stdin.Read(buf)
		if n > 0 { rch <- buf[:n] }
		if err != nil { ech <- err }
	}()
	select {
	case b := <-rch: return b, nil
	case err := <-ech: return nil, err
	case <-time.After(timeout): return nil, nil
	}
}

func (s *session) selectModel() {
	if !isTerminal() { return }
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		s.selectModelSimple()
		return
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	sel := 0
	for i, m := range models {
		if m == s.model { sel = i; break }
	}

	draw := func() {
		fmt.Print("\033[?25l")
		for i, m := range models {
			mark, cur := "  ", "  "
			if i == sel { mark = "\033[36m[*]\033[0m"; cur = " \033[36m<\033[0m" }
			fmt.Printf("\r\033[K  %s [\033[1m%d\033[0m] %s%s\n", mark, i+1, m, cur)
		}
		fmt.Printf("\r\033[K  [\033[2m0\033[0m] custom\n")
		fmt.Printf("\r\033[K  \033[2m\u2191/\u2193 navigate, enter to select\033[0m")
	}
	draw()

	readSeq := func() ([]byte, error) {
		b, err := readRaw(0)
		if err != nil || len(b) == 0 { return b, err }
		if b[0] != 0x1b { return b, nil }
		rest, _ := readRaw(50 * time.Millisecond)
		if len(rest) > 0 { b = append(b, rest...) }
		return b, nil
	}

	for {
		b, _ := readSeq()
		if len(b) == 0 { continue }
		if len(b) >= 3 && b[0] == 0x1b && b[1] == '[' {
			switch b[2] {
			case 'A': if sel > 0 { sel-- }
			case 'B': if sel < len(models)-1 { sel++ }
			}
			fmt.Print("\033[" + strconv.Itoa(len(models)+2) + "A")
			draw()
			continue
		}
		c := b[0]
		if c >= '1' && c <= '9' { idx := int(c - '1'); if idx < len(models) { sel = idx; break } }
		if c == '0' { sel = -1; break }
		if c == 0x0d || c == 0x0a { break }
		if c == 0x03 { os.Exit(0) }
	}

	fmt.Print("\033[?25h")
	fmt.Print("\033[" + strconv.Itoa(len(models)+2) + "B")
	fmt.Println()

	if sel >= 0 && sel < len(models) {
		s.model = models[sel]
		os.Setenv("GEMINI_MODEL", s.model)
	} else if sel == -1 {
		term.Restore(int(os.Stdin.Fd()), oldState)
		fmt.Print("  \033[2mname:\033[0m ")
		n, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		n = strings.TrimSpace(n)
		if n != "" { s.model = n; os.Setenv("GEMINI_MODEL", s.model) }
		newState, _ := term.MakeRaw(int(os.Stdin.Fd()))
		defer term.Restore(int(os.Stdin.Fd()), newState)
	}
	if s.model != s.cfg.Model { s.cfg.Model = s.model; s.cfg.save() }
}

func (s *session) selectModelSimple() {
	for i, m := range models {
		mark := "  "
		if m == s.model { mark = " \033[36m[*]\033[0m" }
		fmt.Printf("  %s [\033[1m%d\033[0m] %s\n", mark, i+1, m)
	}
	fmt.Print("  \033[2mselect:\033[0m ")
	line := s.readLine()
	if line == "0" || strings.EqualFold(line, "custom") {
		fmt.Print("  \033[2mname:\033[0m ")
		n := s.readLine()
		if n != "" { s.model = n; os.Setenv("GEMINI_MODEL", s.model) }
	} else {
		var idx int
		if _, err := fmt.Sscanf(line, "%d", &idx); err == nil && idx >= 1 && idx <= len(models) {
			s.model = models[idx-1]; os.Setenv("GEMINI_MODEL", s.model)
		}
	}
}

// --- Chat UI ---

func (s *session) addMsg(role, text string) {
	s.msgs = append(s.msgs, msg{role, text})
}

func (s *session) redraw() {
	fmt.Print("\033[H\033[J")
	title()
	for _, m := range s.msgs {
		if m.role == "user" {
			fmt.Printf("  \033[1m> %s\033[0m\n", m.text)
		} else {
			for _, line := range strings.Split(strings.TrimSpace(m.text), "\n") {
				fmt.Printf("  %s\n", line)
			}
			fmt.Println()
		}
	}
}

// --- REPL ---

func (s *session) prompt() bool {
	if !isTerminal() {
		if !s.scanner.Scan() { return false }
		i := strings.TrimSpace(s.scanner.Text())
		if i == "" { return true }
		s.handle(i)
		return true
	}
	fmt.Print("  \033[1m> ")
	if !s.scanner.Scan() { return false }
	i := strings.TrimSpace(s.scanner.Text())
	fmt.Print("\033[0m\n")
	if i == "" { return true }
	s.handle(i)
	return true
}

func (s *session) handle(i string) {
	switch {
	case i == "exit" || i == "quit":
		s.addMsg("assistant", "bye.")
		s.redraw()
		os.Exit(0)
	case i == "help":
		s.help()
	case i == "model":
		s.selectModel()
	case strings.HasPrefix(i, "model "):
		if m := strings.TrimPrefix(i, "model "); m != "" { s.model = m; os.Setenv("GEMINI_MODEL", m); s.cfg.Model = m; s.cfg.save() }
	case strings.HasPrefix(i, "key "):
		if k := strings.TrimPrefix(i, "key "); k != "" { s.apiKey = k; os.Setenv("GEMINI_API_KEY", k); s.cfg.APIKey = k; s.cfg.save() }
	case i == "sessions":
		s.showSessions()
	case i == "clearcookies":
		extract.ClearSessions()
		s.addMsg("assistant", "sessions cleared.")
		s.redraw()
	case i == "stats":
		s.showStats()
	case looksLikeURL(i):
		s.fetch(i)
	default:
		if u := tryOpen(i); u != "" { s.fetch(u); return }
		if site, query, ok := parseSearch(i); ok { s.search(site, query); return }
		if u := extractURL(i); u != "" { s.fetch(u); return }
		if u := trySearch(i); u != "" { s.fetch(u); return }
		if u := trySite(i); u != "" { s.fetch(u); return }
		s.ask(i)
	}
}

func (s *session) help() {
	fmt.Println()
	fmt.Println("  \033[1mcommands:\033[0m")
	fmt.Println("    \033[36m<url>\033[0m               fetch page and show content")
	fmt.Println("    \033[36mopen <site>\033[0m          resolve and open a site")
	fmt.Println("    \033[36msearch <q> on <s>\033[0m    search site for query")
	fmt.Println("    \033[36msearch <q>\033[0m           search Google")
	fmt.Println("    \033[36m<question>\033[0m           ask Gemini with context")
	fmt.Println("    \033[36mmodel\033[0m                switch AI model")
	fmt.Println("    \033[36mkey <key>\033[0m            save API key")
	fmt.Println("    \033[36msessions\033[0m             list saved sessions")
	fmt.Println("    \033[36mclearcookies\033[0m         clear all cookies")
	fmt.Println("    \033[36mstats\033[0m                show protocol stats")
	fmt.Println("    \033[36mhelp\033[0m                 this help")
	fmt.Println("    \033[36mexit\033[0m                 quit")
	fmt.Println()
}

func (s *session) showSessions() {
	sessions, _ := extract.Sessions()
	if len(sessions) == 0 {
		s.addMsg("assistant", "no saved sessions.")
		s.redraw()
		return
	}
	var b strings.Builder
	b.WriteString("saved sessions:\n")
	for _, se := range sessions {
		b.WriteString(fmt.Sprintf("  %s (%s)\n", se.Host, se.Name))
	}
	s.addMsg("assistant", b.String())
	s.redraw()
}

func (s *session) showStats() {
	s.addMsg("assistant", "LWP 0.1 - no browser, no bloat.\nUses Go HTTP client + golang.org/x/net/html.\nRAM: ~10MB idle. Cookies: persistent in ~/.config/lwp/cookies.json")
	s.redraw()
}

// --- Intent parsing ---

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") { return true }
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") { return false }
	return len(strings.Fields(s)) == 1 && strings.Contains(s, ".")
}

func tryOpen(i string) string {
	l := strings.ToLower(i)
	for _, p := range []string{"open ", "take me to ", "launch ", "visit "} {
		if strings.HasPrefix(l, p) {
			if u, ok := resolveSite(strings.TrimSpace(i[len(p):])); ok { return u }
		}
	}
	return ""
}

func trySite(i string) string {
	if len(strings.Fields(i)) != 1 { return "" }
	if u, ok := resolveSite(i); ok { return u }
	return ""
}

func trySearch(i string) string {
	l := strings.ToLower(i)
	prefixes := []string{"search for ", "search ", "find ", "show me ", "look for ", "lookup "}
	typos := []string{"searc ", "serch ", "sarch ", "fidn "}
	q := ""
	for _, p := range prefixes {
		if strings.HasPrefix(l, p) {
			r := strings.TrimSpace(i[len(p):])
			if r != "" && !strings.Contains(strings.ToLower(r), " on ") { q = r; break }
		}
	}
	if q == "" {
		for _, p := range typos {
			if strings.HasPrefix(l, p) {
				r := strings.TrimSpace(i[len(p):])
				if r != "" { q = r; break }
			}
		}
	}
	if q == "" { return "" }
	return "https://www.google.com/search?q=" + url.QueryEscape(q)
}

func extractURL(i string) string {
	l := strings.ToLower(i)
	for _, p := range []string{"search for ", "search ", "open ", "go to "} {
		if strings.HasPrefix(l, p) {
			r := strings.TrimSpace(i[len(p):])
			if r == "" { continue }
			if u, ok := resolveSite(r); ok { return u }
			if looksLikeSingleURL(r) {
				if !strings.HasPrefix(r, "http://") && !strings.HasPrefix(r, "https://") { r = "https://" + r }
				return r
			}
		}
	}
	return ""
}

func looksLikeSingleURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") { return true }
	if !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") { return false }
	return len(strings.Fields(s)) == 1
}

func parseSearch(i string) (site, query string, ok bool) {
	l := strings.ToLower(i)
	prefixes := []string{"search for ", "search ", "find ", "show me ", "look for ", "lookup "}

	for _, p := range prefixes {
		if strings.HasPrefix(l, p) {
			r := i[len(p):]
			if idx := strings.LastIndex(strings.ToLower(r), " on "); idx >= 0 {
				site = strings.TrimSpace(r[idx+4:]); query = strings.TrimSpace(r[:idx])
				if searchURL(site, query) != "" { return site, query, true }
			}
		}
	}
	if len(l) >= 6 && (strings.HasPrefix(l, "searc") || strings.HasPrefix(l, "serch") || strings.HasPrefix(l, "sarch")) {
		if idx := strings.Index(l, " "); idx >= 0 {
			r := i[idx+1:]
			if idx2 := strings.LastIndex(strings.ToLower(r), " on "); idx2 >= 0 {
				site = strings.TrimSpace(r[idx2+4:]); query = strings.TrimSpace(r[:idx2])
				if searchURL(site, query) != "" { return site, query, true }
			}
		}
	}
	if idx := strings.LastIndex(l, " on "); idx >= 0 {
		site = strings.TrimSpace(i[idx+4:]); query = strings.TrimSpace(i[:idx])
		if searchURL(site, query) != "" { return site, query, true }
	}
	return "", "", false
}

func searchURL(site, query string) string {
	key := strings.ToLower(strings.TrimSpace(site))
	key = strings.TrimPrefix(key, "www.")
	key = strings.TrimSuffix(key, ".com")
	key = strings.TrimSuffix(key, ".in")
	siteURLs := map[string]string{
		"google":    "https://www.google.com/search?q=%s",
		"flipkart":  "https://www.flipkart.com/search?q=%s",
		"amazon":    "https://www.amazon.com/s?k=%s",
		"amazon.in": "https://www.amazon.in/s?k=%s",
		"myntra":    "https://www.myntra.com/%s",
		"ajio":      "https://www.ajio.com/search/?text=%s",
		"ebay":      "https://www.ebay.com/sch/i.html?_nkw=%s",
		"walmart":   "https://www.walmart.com/search?q=%s",
		"target":    "https://www.target.com/s?searchTerm=%s",
		"bestbuy":   "https://www.bestbuy.com/site/searchpage.jsp?st=%s",
		"reddit":    "https://www.reddit.com/search/?q=%s",
		"youtube":   "https://www.youtube.com/results?search_query=%s",
		"github":    "https://github.com/search?q=%s",
		"twitter":   "https://twitter.com/search?q=%s",
		"linkedin":  "https://www.linkedin.com/search/results/all/?keywords=%s",
	}
	if tmpl, ok := siteURLs[key]; ok {
		return fmt.Sprintf(tmpl, url.QueryEscape(query))
	}
	return ""
}

// --- Core: Fetch ---

func (s *session) fetch(rawURL string) {
	s.addMsg("user", rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	sp := newSpinner("\U0001F310 " + rawURL)
	sp.start()
	page, err := extract.Fetch(rawURL, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		s.msgs = s.msgs[:len(s.msgs)-1]
		return
	}
	detail := fmt.Sprintf("\033[36m%s\033[0m  (\033[2m%dms %dkb\033[0m)", page.Title, page.Metadata.LatencyMs, page.Metadata.ContentLength/1024)
	sp.stop(detail)

	s.lastURL = page.URL
	s.lastContent = page.Content
	s.history = nil

	// Check for auth form
	if page.Metadata.HasAuthForm && len(page.Forms) > 0 {
		s.handleAuth(page)
		return
	}

	if s.apiKey != "" {
		s.askAI("What is on this page? Give a 2-3 sentence summary.")
	} else {
		s.showPage(page)
	}
}

func (s *session) search(site, query string) {
	u := searchURL(site, query)
	if u == "" { return }
	s.fetch(u)
}

// --- Auth handler ---

func (s *session) handleAuth(page *extract.Page) {
	fmt.Printf("\n  \033[33m!\033[0m Login detected on \033[36m%s\033[0m\n", page.URL)

	// Show form fields
	for _, f := range page.Forms {
		fmt.Printf("  Form: %s %s\n", f.Method, f.Action)
		for _, field := range f.Fields {
			req := ""
			if field.Required { req = " *" }
			label := field.Label
			if label == "" { label = field.Name }
			fmt.Printf("    [\033[36m%d\033[0m] %s (%s)%s\n", field.Ref, label, field.Type, req)
		}
	}

	fmt.Printf("  Enter field values as \033[36mref=value\033[0m (space-separated), or press Enter to skip:\n  \033[2m> ")
	s.scanner.Scan()
	input := strings.TrimSpace(s.scanner.Text())
	fmt.Print("\033[0m")

	if input == "" {
		s.addMsg("assistant", "Login required. Visit "+page.URL+" in your browser, then paste the page URL here.")
		s.redraw()
		return
	}

	// Parse field=value pairs
	fields := map[string]string{}
	for _, part := range strings.Fields(input) {
		if idx := strings.Index(part, "="); idx >= 0 {
			refStr := strings.TrimSpace(part[:idx])
			val := strings.TrimSpace(part[idx+1:])
			ref, err := strconv.Atoi(refStr)
			if err != nil { continue }
			for _, f := range page.Forms {
				for _, field := range f.Fields {
					if field.Ref == ref {
						fields[field.Name] = val
					}
				}
			}
		}
	}

	if len(fields) == 0 { return }

	action := page.Forms[0].Action
	if !strings.HasPrefix(action, "http") {
		base, _ := url.Parse(page.URL)
		if resolved, err := base.Parse(action); err == nil {
			action = resolved.String()
		}
	}

	sp := newSpinner("submitting login")
	sp.start()
	result, err := extract.Submit(action, fields, 30*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	detail := fmt.Sprintf("%d %s", result.Status, result.Title)
	sp.stop(detail)

	s.lastURL = result.URL
	s.lastContent = result.Content
	s.history = nil

	if s.apiKey != "" {
		s.askAI("What happened after login? Summarize.")
	} else {
		s.showPage(result)
	}
}

// --- Page display (for no-API-key mode) ---

func (s *session) showPage(page *extract.Page) {
	header := fmt.Sprintf("opened \033[36m%s\033[0m  (\033[2m%dms %dkb\033[0m)", page.Title, page.Metadata.LatencyMs, page.Metadata.ContentLength/1024)
	var b strings.Builder
	b.WriteString(header + "\n\n")

	content := page.Content
	if len(content) > 2000 {
		content = content[:2000] + "\n[... truncated ...]"
	}
	b.WriteString(content)

	if len(page.Links) > 0 {
		b.WriteString(fmt.Sprintf("\n\n\033[2m%d links\033[0m", len(page.Links)))
	}
	if len(page.Forms) > 0 {
		b.WriteString(fmt.Sprintf("\n\033[2m%d form(s) detected\033[0m", len(page.Forms)))
	}

	s.addMsg("assistant", b.String())
	s.redraw()
}

// --- AI chat ---

func (s *session) ask(question string) {
	if s.apiKey == "" {
		s.addMsg("user", question)
		s.addMsg("assistant", "set an API key first: \033[36mkey <your-key>\033[0m")
		s.redraw()
		return
	}
	s.askAI(question)
}

func (s *session) askAI(question string) {
	if s.apiKey == "" { return }

	var prompt string
	if s.lastContent != "" {
		text := s.lastContent
		if len(text) > 3000 { text = text[:3000] + "..." }
		prompt = noMarkdown + "\n\nCurrent page: " + s.lastURL + "\n\nPage content:\n" + text + "\n\nThe user asks: " + question
	} else {
		prompt = noMarkdown + "\n\nThe user says: " + question
	}

	history := buildHistory(s.history)

	os.Setenv("GEMINI_MODEL", s.model)
	sp := newSpinner(s.model)
	sp.start()
	answer, err := llm.ChatWithHistory(prompt, history, 120*time.Second)
	if err != nil {
		sp.fail(err.Error())
		return
	}
	sp.stop("")

	s.history = append(s.history, exchange{user: question, ai: answer})
	if len(s.history) > maxHistory { s.history = s.history[len(s.history)-maxHistory:] }

	s.addMsg("assistant", answer)
	s.redraw()
}

func buildHistory(exchanges []exchange) []llm.Content {
	var out []llm.Content
	start := 0
	if len(exchanges) > maxHistory { start = len(exchanges) - maxHistory }
	for _, ex := range exchanges[start:] {
		if ex.user != "" { out = append(out, llm.Content{Role: "user", Parts: []llm.Part{{Text: ex.user}}}) }
		if ex.ai != "" { out = append(out, llm.Content{Role: "model", Parts: []llm.Part{{Text: ex.ai}}}) }
	}
	return out
}

func isTerminal() bool {
	fi, _ := os.Stdin.Stat()
	return (fi.Mode() & os.ModeCharDevice) != 0
}
