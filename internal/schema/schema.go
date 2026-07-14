package schema

type Page struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Content  string    `json:"content"`
	Sections []Section `json:"sections,omitempty"`
	Elements []Element `json:"elements,omitempty"`
	Metadata Metadata  `json:"metadata"`
}

type Section struct {
	Heading string `json:"heading"`
	Text    string `json:"text"`
	Level   int    `json:"level"`
}

type Element struct {
	Ref  int    `json:"ref"`
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Href string `json:"href,omitempty"`
	Name string `json:"name,omitempty"`
}

type Metadata struct {
	Tier          int    `json:"tier"`
	LatencyMs     int64  `json:"latency_ms"`
	ContentLength int    `json:"content_length"`
	FetchedAt     string `json:"fetched_at"`
}
