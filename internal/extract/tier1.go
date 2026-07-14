package extract

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"github.com/aditya-ig10/LWP/internal/schema"
)

// In-memory page cache for Tier 1
type cacheEntry struct {
	page *schema.Page
	expires time.Time
}

var (
	cacheMu    sync.Mutex
	cache      = make(map[string]*cacheEntry)
	cacheTTL   = 30 * time.Second
	cacheMax   = 64

	// Shared HTTP transport with connection reuse
	sharedTransport = &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	}
)

func cacheGet(url string) *schema.Page {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[url]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(cache, url)
		}
		return nil
	}
	return e.page
}

func cacheSet(url string, page *schema.Page) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cache) >= cacheMax {
		// Evict oldest
		var oldest string
		var oldestTime time.Time
		for k, v := range cache {
			if oldest == "" || v.expires.Before(oldestTime) {
				oldest = k
				oldestTime = v.expires
			}
		}
		delete(cache, oldest)
	}
	cache[url] = &cacheEntry{page: page, expires: time.Now().Add(cacheTTL)}
}

var boilerplateIDs = []string{
	"nav", "navbar", "navigation", "menu", "sidebar", "footer", "header",
	"cookie", "cookies", "consent", "banner", "popup", "modal", "overlay",
	"advertisement", "ads", "ad", "social", "share", "comments", "comment",
	"related", "recommendations", "sidebar-right", "sidebar-left",
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

func isContentContainer(n *html.Node) bool {
	switch n.Data {
	case "main", "article", "section", "blockquote":
		return true
	}
	return false
}

func Tier1(url string, timeout time.Duration) (*schema.Page, error) {
	if p := cacheGet(url); p != nil {
		return p, nil
	}
	start := time.Now()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	client := &http.Client{Timeout: timeout, Transport: sharedTransport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, 10<<20)

	doc, err := html.Parse(limited)
	if err != nil {
		return nil, err
	}

	latency := time.Since(start).Milliseconds()

	page := &schema.Page{
		URL: resp.Request.URL.String(),
		Metadata: schema.Metadata{
			Tier:      1,
			LatencyMs: latency,
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}

	extractPage(doc, page)

	cacheSet(url, page)
	return page, nil
}

func extractPage(n *html.Node, page *schema.Page) {
	title := extractTitle(n)
	if title != "" {
		page.Title = title
	}

	var sections []schema.Section
	var elements []schema.Element
	refCounter := 0

	// Collect content blocks with scores
	type block struct {
		text  string
		score int
		isSec bool
		level int
	}
	var blocks []block

	var walk func(*html.Node, int)
	walk = func(n *html.Node, depth int) {
		if n.Type == html.ElementNode {
			tag := n.Data

			// Skip non-content tags entirely
			switch tag {
			case "script", "style", "noscript", "svg", "meta", "link", "iframe":
				return
			}

			// Skip boilerplate sections
			if tag == "nav" || tag == "footer" || isBoilerplate(n) {
				return
			}

			switch tag {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				text := collectText(n)
				if text != "" {
					lvl := headingLevel(tag)
					blocks = append(blocks, block{
						text:  text,
						score: 10,
						isSec: true,
						level: lvl,
					})
				}
			case "p":
				text := collectText(n)
				if text != "" {
					// Paragraphs get base score + bonus for length
					s := len(text)
					score := 3
					if s > 80 {
						score = 5
					}
					if s > 200 {
						score = 6
					}
					if isContentContainer(n.Parent) {
						score += 2
					}
					blocks = append(blocks, block{text: text, score: score})
				}
			case "div", "section", "article", "blockquote":
				// Only extract if contains meaningful text directly (not just children)
				direct := collectText(n)
				linkCount := countLinks(n)
				wordCount := len(strings.Fields(direct))
				if wordCount > 15 {
					score := 4
					if isContentContainer(n) {
						score = 6
					}
					if linkCount > 0 && wordCount/linkCount < 5 {
						score = 1 // mostly links, likely boilerplate
					}
					blocks = append(blocks, block{text: direct, score: score})
				}
			case "a":
				href := attr(n, "href")
				if href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") {
					refCounter++
					elements = append(elements, schema.Element{
						Ref:  refCounter,
						Type: "link",
						Text: collectText(n),
						Href: href,
					})
				}
			case "input":
				inputType := attr(n, "type")
				if inputType == "hidden" {
					return
				}
				refCounter++
				elem := schema.Element{
					Ref:  refCounter,
					Type: "input",
					Name: attr(n, "name"),
				}
				if label := findLabel(n); label != "" {
					elem.Text = label
				} else {
					elem.Text = attr(n, "placeholder")
				}
				elements = append(elements, elem)
			case "button":
				refCounter++
				elements = append(elements, schema.Element{
					Ref:  refCounter,
					Type: "button",
					Text: collectText(n),
				})
			case "select":
				refCounter++
				elem := schema.Element{
					Ref:  refCounter,
					Type: "select",
					Name: attr(n, "name"),
				}
				if label := findLabel(n); label != "" {
					elem.Text = label
				}
				elements = append(elements, elem)
			case "textarea":
				refCounter++
				elem := schema.Element{
					Ref:  refCounter,
					Type: "textarea",
					Name: attr(n, "name"),
				}
				if label := findLabel(n); label != "" {
					elem.Text = label
				}
				elements = append(elements, elem)
			default:
				// li, td, th, etc - skip at this level, content captured via parent
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, depth+1)
		}
	}
	walk(n, 0)

	// Sort blocks by score (collect high scoring first)
	// For efficiency: just collect all, sort, and take top
	// Simple approach: take all blocks, deduplicate, and maintain order but filter low-score
	var high, medium, low []block
	for _, b := range blocks {
		if b.isSec {
			high = append(high, b)
			continue
		}
		switch {
		case b.score >= 5:
			high = append(high, b)
		case b.score >= 3:
			medium = append(medium, b)
		default:
			low = append(low, b)
		}
	}

	// Build content: headings + high > medium (only include medium if space)
	var ordered []block
	seen := map[string]bool{}

	addBlock := func(b block) {
		key := collapseWS(strings.ToLower(b.text))
		if len(key) < 10 || seen[key] {
			return
		}
		seen[key] = true
		ordered = append(ordered, b)
	}

	// Preserve original order from the walk
	for _, b := range high {
		addBlock(b)
	}
	// Only include medium-scoring blocks if content is sparse (under 1KB)
	totalContent := 0
	for _, b := range ordered {
		totalContent += len(b.text)
	}
	if totalContent < 1024 {
		for _, b := range medium {
			addBlock(b)
		}
	}

	// Build output
	var textParts []string
	for _, b := range ordered {
		textParts = append(textParts, b.text)
		if b.isSec {
			sections = append(sections, schema.Section{
				Heading: b.text,
				Level:   b.level,
			})
		}
	}

	page.Content = compactText(textParts)
	page.Sections = sections
	page.Elements = elements
	page.Metadata.ContentLength = len(page.Content)

	// Hard cap at ~100k chars to limit token usage
	if len(page.Content) > 100000 {
		page.Content = page.Content[:100000]
	}
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
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
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
	return ""
}

func headingLevel(tag string) int {
	switch tag {
	case "h1":
		return 1
	case "h2":
		return 2
	case "h3":
		return 3
	case "h4":
		return 4
	case "h5":
		return 5
	case "h6":
		return 6
	}
	return 0
}

func compactText(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimSpace(p))
	}
	return collapseWS(b.String())
}

func collapseWS(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
		} else {
			b.WriteRune(r)
			space = false
		}
	}
	return strings.TrimSpace(b.String())
}
