package extract

import (
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"

	"github.com/aditya-ig10/LWP/internal/schema"
)

func Tier1(url string, timeout time.Duration) (*schema.Page, error) {
	start := time.Now()

	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
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

	return page, nil
}

func extractPage(n *html.Node, page *schema.Page) {
	title := extractTitle(n)
	if title != "" {
		page.Title = title
	}

	var textParts []string
	var sections []schema.Section
	var elements []schema.Element
	refCounter := 0

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "meta", "link":
				return
			case "h1", "h2", "h3", "h4", "h5", "h6":
				text := collectText(n)
				if text != "" {
					sections = append(sections, schema.Section{
						Heading: text,
						Level:   headingLevel(n.Data),
					})
				}
			case "p", "div", "article", "section", "blockquote":
				text := collectText(n)
				if text != "" {
					textParts = append(textParts, text)
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
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)

	page.Content = compactText(textParts)
	page.Sections = sections
	page.Elements = elements
	page.Metadata.ContentLength = len(page.Content)
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
