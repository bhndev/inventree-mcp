// Package imagesearch provides a client for Google Custom Search image queries.
package imagesearch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ImageResult represents a single image search result.
type ImageResult struct {
	Title        string `json:"title"`
	Link         string `json:"link"`
	ThumbnailURL string `json:"thumbnail_url"`
	ContextURL   string `json:"context_url"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// Client queries Google Custom Search for images.
type Client struct {
	apiKey string
	cseID  string
	http   *http.Client
}

// New creates an image search client. Returns nil if either credential is empty,
// allowing callers to treat nil as "image search not configured".
func New(apiKey, cseID string) *Client {
	if apiKey == "" || cseID == "" {
		return nil
	}
	return &Client{
		apiKey: apiKey,
		cseID:  cseID,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Search queries Google Custom Search for images matching the query string.
// num controls how many results to return (1-10, clamped).
func (c *Client) Search(query string, num int) ([]ImageResult, error) {
	if num < 1 {
		num = 1
	}
	if num > 10 {
		num = 10
	}

	u := fmt.Sprintf(
		"https://www.googleapis.com/customsearch/v1?key=%s&cx=%s&q=%s&searchType=image&num=%d",
		url.QueryEscape(c.apiKey),
		url.QueryEscape(c.cseID),
		url.QueryEscape(query),
		num,
	)

	resp, err := c.http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("image search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image search returned status %d", resp.StatusCode)
	}

	var body struct {
		Items []struct {
			Title string `json:"title"`
			Link  string `json:"link"`
			Image struct {
				ThumbnailLink string `json:"thumbnailLink"`
				ContextLink   string `json:"contextLink"`
				Width         int    `json:"width"`
				Height        int    `json:"height"`
			} `json:"image"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding image search response: %w", err)
	}

	results := make([]ImageResult, 0, len(body.Items))
	for _, item := range body.Items {
		results = append(results, ImageResult{
			Title:        item.Title,
			Link:         item.Link,
			ThumbnailURL: item.Image.ThumbnailLink,
			ContextURL:   item.Image.ContextLink,
			Width:        item.Image.Width,
			Height:       item.Image.Height,
		})
	}
	return results, nil
}
