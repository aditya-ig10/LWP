package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Part struct {
	Text string `json:"text"`
}

type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type Request struct {
	Contents         []Content `json:"contents"`
	SystemInstruction *Content `json:"system_instruction,omitempty"`
}

type Response struct {
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates"`
}

func Model() string {
	if m := os.Getenv("GEMINI_MODEL"); m != "" {
		return m
	}
	return "gemini-flash-lite-latest"
}

func Chat(prompt string, timeout time.Duration) (string, error) {
	return ChatWithHistory(prompt, nil, timeout)
}

func ChatWithHistory(prompt string, history []Content, timeout time.Duration) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY not set")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", Model(), apiKey)

	contents := make([]Content, 0, len(history)+1)
	contents = append(contents, history...)
	contents = append(contents, Content{
		Role:  "user",
		Parts: []Part{{Text: prompt}},
	})

	reqBody := Request{Contents: contents}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var res Response
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	if len(res.Candidates) == 0 || len(res.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response")
	}

	return res.Candidates[0].Content.Parts[0].Text, nil
}
