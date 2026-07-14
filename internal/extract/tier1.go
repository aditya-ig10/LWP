package extract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

// --- Persistent cookie jar ---

type jar struct {
	mu      sync.Mutex
	entries map[string][]*http.Cookie
}

func newJar() *jar {
	j := &jar{entries: map[string][]*http.Cookie{}}
	j.load()
	return j
}

func cookiePath() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/lwp/cookies.json"
}

func (j *jar) load() {
	data, err := os.ReadFile(cookiePath())
	if err != nil {
		return
	}
	json.Unmarshal(data, &j.entries)
}

func (j *jar) save() {
	data, _ := json.MarshalIndent(j.entries, "", " ")
	os.MkdirAll(cookiePath()[:len(cookiePath())-len("/cookies.json")], 0700)
	os.WriteFile(cookiePath(), data, 0600)
}

func (j *jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	key := u.Host
	existing := j.entries[key]
	merged := make([]*http.Cookie, 0, len(existing)+len(cookies))
	seen := map[string]bool{}
	for _, c := range existing {
		k := c.Name + "=" + c.Domain + "=" + c.Path
		seen[k] = true
		merged = append(merged, c)
	}
	for _, c := range cookies {
		k := c.Name + "=" + c.Domain + "=" + c.Path
		if !seen[k] {
			merged = append(merged, c)
			seen[k] = true
		} else {
			for i, e := range merged {
				if e.Name+"="+e.Domain+"="+e.Path == k {
					merged[i] = c
					break
				}
			}
		}
	}
	j.entries[key] = merged
	j.save()
}

func (j *jar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.entries[u.Host]
}

// --- Session info ---

type SessionInfo struct {
	Host   string `json:"host"`
	Domain string `json:"domain"`
	Name   string `json:"name"`
}

func Sessions() ([]SessionInfo, error) {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.config/lwp/cookies.json")
	if err != nil {
		return nil, nil
	}
	var entries map[string][]*http.Cookie
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	var out []SessionInfo
	for host, cookies := range entries {
		for _, c := range cookies {
			if c.Name == "session" || c.Name == "token" || strings.Contains(c.Name, "auth") || strings.Contains(c.Name, "sid") || strings.Contains(c.Name, "login") {
				out = append(out, SessionInfo{Host: host, Domain: c.Domain, Name: c.Name})
				break
			}
		}
	}
	return out, nil
}

func ClearSessions() error {
	home, _ := os.UserHomeDir()
	return os.WriteFile(home+"/.config/lwp/cookies.json", []byte("{}"), 0600)
}

// --- HTTP client ---

type Client struct {
	http    *http.Client
	jar     *jar
	headers http.Header
}

var (
	sharedJar    = newJar()
	sharedClient *Client
	clientMu     sync.Mutex
)

func DefaultClient() *Client {
	clientMu.Lock()
	defer clientMu.Unlock()
	if sharedClient == nil {
		sharedClient = newClient()
	}
	return sharedClient
}

func newClient() *Client {
	httpJar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	c := &Client{
		headers: http.Header{
			"User-Agent":      {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"},
			"Accept":          {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
			"Accept-Language": {"en-US,en;q=0.5"},
		},
		jar: sharedJar,
	}
	c.http = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        8,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  false,
		},
		Jar: httpJar,
	}
	// Restore persistent cookies into the HTTP jar
	for host, cookies := range sharedJar.entries {
		if u, err := url.Parse("https://" + host); err == nil {
			httpJar.SetCookies(u, cookies)
		}
	}
	return c
}

func (c *Client) saveCookies() {
	for _, host := range c.jarHosts() {
		if u, err := url.Parse("https://" + host); err == nil {
			cookies := c.http.Jar.Cookies(u)
			if len(cookies) > 0 {
				c.jar.SetCookies(u, cookies)
			}
		}
	}
}

func (c *Client) jarHosts() []string {
	c.jar.mu.Lock()
	defer c.jar.mu.Unlock()
	hosts := make([]string, 0, len(c.jar.entries))
	for h := range c.jar.entries {
		hosts = append(hosts, h)
	}
	return hosts
}

// --- Page ---

type Page struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Content  string    `json:"content"`
	Sections []Section `json:"sections,omitempty"`
	Links    []Link    `json:"links,omitempty"`
	Forms    []Form    `json:"forms,omitempty"`
	Status   int       `json:"status"`
	Error    string    `json:"error,omitempty"`
	Metadata Metadata  `json:"metadata"`
}

type Section struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
	Level   int    `json:"level"`
}

type Link struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

type Form struct {
	Ref      int       `json:"ref"`
	Action   string    `json:"action"`
	Method   string    `json:"method"`
	Fields   []Field   `json:"fields"`
	Submit   string    `json:"submit,omitempty"`
}

type Field struct {
	Ref         int    `json:"ref"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
	Value       string `json:"value,omitempty"`
	Options     []string `json:"options,omitempty"`
}

type Metadata struct {
	LatencyMs     int64  `json:"latency_ms"`
	ContentLength int    `json:"content_length"`
	FetchedAt     string `json:"fetched_at"`
	HasAuthForm   bool   `json:"has_auth_form"`
}

// --- Core protocol: Fetch ---

func Fetch(rawURL string, timeout time.Duration) (*Page, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	start := time.Now()
	c := DefaultClient()
	req := &http.Request{
		Method: "GET",
		URL:    parsed,
		Header: c.headers.Clone(),
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	c.saveCookies()

	latency := time.Since(start).Milliseconds()
	limited := io.LimitReader(resp.Body, 5<<20)

	doc, err := html.Parse(limited)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	page := &Page{
		URL:    resp.Request.URL.String(),
		Status: resp.StatusCode,
	}

	extractPage(doc, page)
	page.Metadata.LatencyMs = latency
	page.Metadata.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	page.Metadata.ContentLength = len(page.Content)

	return page, nil
}

// --- Core protocol: Search ---

func Search(query string, site string, timeout time.Duration) (*Page, error) {
	searchURLs := map[string]string{
		"google":    "https://www.google.com/search?q=%s",
		"bing":      "https://www.bing.com/search?q=%s",
		"duckduckgo":"https://duckduckgo.com/?q=%s",
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

	u := ""
	if site != "" {
		key := strings.ToLower(strings.TrimSpace(site))
		key = strings.TrimPrefix(key, "www.")
		key = strings.TrimSuffix(key, ".com")
		key = strings.TrimSuffix(key, ".in")
		if tmpl, ok := searchURLs[key]; ok {
			u = fmt.Sprintf(tmpl, url.QueryEscape(query))
		}
	}
	if u == "" {
		u = fmt.Sprintf("https://www.google.com/search?q=%s", url.QueryEscape(query))
	}
	return Fetch(u, timeout)
}

// --- Core protocol: Submit ---

func Submit(rawURL string, fields map[string]string, timeout time.Duration) (*Page, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	start := time.Now()
	c := DefaultClient()

	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}

	req := &http.Request{
		Method: "POST",
		URL:    parsed,
		Header: c.headers.Clone(),
		Body:   io.NopCloser(strings.NewReader(form.Encode())),
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}
	defer resp.Body.Close()

	c.saveCookies()

	latency := time.Since(start).Milliseconds()
	limited := io.LimitReader(resp.Body, 5<<20)

	doc, err := html.Parse(limited)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	page := &Page{
		URL:    resp.Request.URL.String(),
		Status: resp.StatusCode,
	}
	extractPage(doc, page)
	page.Metadata.LatencyMs = latency
	page.Metadata.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	page.Metadata.ContentLength = len(page.Content)

	return page, nil
}

// --- HTML extraction ---

var boilerplateIDs = []string{
	"nav", "navbar", "navigation", "menu", "sidebar", "footer", "header",
	"cookie", "cookies", "consent", "banner", "popup", "modal", "overlay",
	"advertisement", "ads", "ad", "social", "share", "comments", "comment",
	"related", "recommendations",
}

var boilerplateClasses = []string{
	"nav", "navbar", "footer", "header", "sidebar", "cookie", "consent",
	"advertisement", "ad", "social", "share", "comments", "related",
	"sidebar", "menu", "popup", "modal", "overlay",
}

func isBoilerplate(n *html.Node) bool {
	for _, a := range n.Attr {
		lower := strings.ToLower(a.Val)
		if a.Key == "id" {
			for _, b := range boilerplateIDs {
				if strings.Contains(lower, b) {
					return true
				}
			}
		}
		if a.Key == "class" {
			for _, b := range boilerplateClasses {
				if strings.Contains(lower, b) {
					return true
				}
			}
		}
	}
	return false
}

func isLoginRelated(n *html.Node) bool {
	for _, a := range n.Attr {
		lower := strings.ToLower(a.Val)
		if a.Key == "id" || a.Key == "class" || a.Key == "name" {
			if strings.Contains(lower, "login") || strings.Contains(lower, "signin") ||
				strings.Contains(lower, "log-in") || strings.Contains(lower, "sign-in") ||
				strings.Contains(lower, "auth") || strings.Contains(lower, "password") {
				return true
			}
		}
	}
	return false
}

func extractPage(n *html.Node, page *Page) {
	page.Title = extractTitle(n)

	type block struct {
		text  string
		score int
		isSec bool
		level int
	}

	var blocks []block
	var links []Link
	var forms []Form
	formRef := 0
	hasAuthForm := false

	var walk func(*html.Node, int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode {
			tag := n.Data
			switch tag {
			case "script", "style", "noscript", "svg", "meta", "link", "iframe":
				return
			}
			if tag == "nav" || tag == "footer" || isBoilerplate(n) {
				return
			}
			switch tag {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				text := collectText(n)
				if text != "" {
					blocks = append(blocks, block{text: text, score: 10, isSec: true, level: headingLevel(tag)})
				}
			case "p":
				text := collectText(n)
				if text != "" {
					s := len(text)
					score := 3
					if s > 80 { score = 5 }
					if s > 200 { score = 6 }
					if isContentContainer(n.Parent) { score += 2 }
					blocks = append(blocks, block{text: text, score: score})
				}
			case "a":
				href := attr(n, "href")
				if href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") {
					links = append(links, Link{Text: collectText(n), Href: href})
				}
			case "form":
				formRef++
				f := parseForm(n, formRef)
				if f != nil {
					forms = append(forms, *f)
					if isLoginForm(f) {
						hasAuthForm = true
					}
				}
			case "div", "section", "article", "blockquote":
				direct := collectText(n)
				linkCount := countLinks(n)
				wordCount := len(strings.Fields(direct))
				if wordCount > 15 {
					score := 4
					if isContentContainer(n) { score = 6 }
					if linkCount > 0 && wordCount/linkCount < 5 { score = 1 }
					if score > 1 {
						blocks = append(blocks, block{text: direct, score: score})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth+1)
		}
	}
	walk(n, 0)

	// Score-sort blocks, preserve order within tiers
	var high, medium []block
	seen := map[string]bool{}
	for _, b := range blocks {
		key := collapseWS(strings.ToLower(b.text))
		if len(key) < 10 || seen[key] {
			continue
		}
		seen[key] = true
		if b.isSec || b.score >= 5 {
			high = append(high, b)
		} else if b.score >= 3 {
			medium = append(medium, b)
		}
	}

	total := 0
	for _, b := range high {
		total += len(b.text)
	}
	if total < 1024 {
		high = append(high, medium...)
	}

	var textParts []string
	for _, b := range high {
		textParts = append(textParts, b.text)
	}

	page.Content = compactText(textParts)
	page.Links = links
	page.Forms = forms
	page.Metadata.HasAuthForm = hasAuthForm

	if len(page.Content) > 100000 {
		page.Content = page.Content[:100000]
	}
}

func parseForm(n *html.Node, ref int) *Form {
	f := &Form{
		Ref:    ref,
		Action: attr(n, "action"),
		Method: strings.ToUpper(attr(n, "method")),
	}
	if f.Method == "" { f.Method = "GET" }
	if f.Action == "" { f.Action = "#" }

	fieldRef := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input":
				t := attr(n, "type")
				if t == "hidden" || t == "submit" || t == "image" {
					if t == "submit" || t == "image" {
						f.Submit = attr(n, "value")
					}
					return
				}
				fieldRef++
				field := Field{
					Ref:   fieldRef,
					Type:  t,
					Name:  attr(n, "name"),
					Placeholder: attr(n, "placeholder"),
					Value: attr(n, "value"),
					Required: attr(n, "required") == "required" || attr(n, "required") == "",
				}
				if label := findLabel(n); label != "" {
					field.Label = label
				}
				f.Fields = append(f.Fields, field)
			case "select":
				fieldRef++
				field := Field{
					Ref:   fieldRef,
					Type:  "select",
					Name:  attr(n, "name"),
					Required: attr(n, "required") == "required",
				}
				if label := findLabel(n); label != "" {
					field.Label = label
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "option" {
						field.Options = append(field.Options, attr(c, "value"))
					}
				}
				f.Fields = append(f.Fields, field)
			case "textarea":
				fieldRef++
				field := Field{
					Ref:   fieldRef,
					Type:  "textarea",
					Name:  attr(n, "name"),
					Placeholder: attr(n, "placeholder"),
					Required: attr(n, "required") == "required",
				}
				if label := findLabel(n); label != "" {
					field.Label = label
				}
				f.Fields = append(f.Fields, field)
			case "button":
				bt := attr(n, "type")
				if bt == "submit" || bt == "" {
					f.Submit = collectText(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	if len(f.Fields) == 0 {
		return nil
	}
	return f
}

func isLoginForm(f *Form) bool {
	hasPassword := false
	hasSubmit := f.Submit != ""
	for _, field := range f.Fields {
		if field.Type == "password" {
			hasPassword = true
		}
		if strings.Contains(strings.ToLower(field.Name), "login") ||
			strings.Contains(strings.ToLower(field.Name), "user") ||
			strings.Contains(strings.ToLower(field.Name), "email") {
			hasSubmit = true
		}
	}
	return hasPassword && hasSubmit
}

// --- Helpers ---

func isContentContainer(n *html.Node) bool {
	switch n.Data {
	case "main", "article", "section", "blockquote":
		return true
	}
	return false
}

func countLinks(n *html.Node) int {
	count := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			count++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return count
}

func extractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return collectText(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := extractTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func collectText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript":
				return
			}
		}
		if n.Type == html.TextNode {
			s := strings.TrimSpace(n.Data)
			if s != "" {
				if b.Len() > 0 { b.WriteByte(' ') }
				b.WriteString(s)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func findLabel(n *html.Node) string {
	id := attr(n, "id")
	if id == "" {
		return ""
	}
	p := n.Parent
	for p != nil {
		if p.Type == html.ElementNode && p.Data == "label" {
			return collectText(p)
		}
		if p.Type == html.ElementNode && p.Data == "form" {
			break
		}
		p = p.Parent
	}
	// Also look for label with 'for' attribute matching id
	var search func(*html.Node) string
	search = func(n *html.Node) string {
		if n.Type == html.ElementNode && n.Data == "label" && attr(n, "for") == id {
			return collectText(n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if t := search(c); t != "" {
				return t
			}
		}
		return ""
	}
	return search(n)
}

func headingLevel(tag string) int {
	switch tag {
	case "h1": return 1
	case "h2": return 2
	case "h3": return 3
	case "h4": return 4
	case "h5": return 5
	case "h6": return 6
	}
	return 0
}

func compactText(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 { b.WriteString("\n\n") }
		b.WriteString(strings.TrimSpace(p))
	}
	return collapseWS(b.String())
}

func collapseWS(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !space { b.WriteByte(' '); space = true }
		} else {
			b.WriteRune(r)
			space = false
		}
	}
	return strings.TrimSpace(b.String())
}
