package gemini

import (
	"context"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// Client represents a client for the Gemini API
type Client struct {
	model *genai.GenerativeModel
}

// NewClient creates a new Gemini client
func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini API key is required")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	model := client.GenerativeModel("gemini-2.5-flash") // Use a fast and capable model
	return &Client{model: model}, nil
}

// GenerateNetworkSummary generates an AI-powered summary of the network stats
func (c *Client) GenerateNetworkSummary(stats string) (string, error) {
	ctx := context.Background()
	prompt := fmt.Sprintf(`
Analyze the following summary of the Xandeum pNode network and provide a concise, insightful summary in markdown format.
Focus on overall health, performance, diversity, and provide one key recommendation.

Network Data:
%s
`, stats)

	resp, err := c.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content generated")
	}

	summary, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected response format from API")
	}

	return string(summary), nil
}
