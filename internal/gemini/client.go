package gemini

import (
	"context"
	"fmt"
	"strings"

	"xdorb-backend/internal/models"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

type Client struct {
	model *genai.GenerativeModel
}

func NewClient(apiKey string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini API key is required")
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	model := client.GenerativeModel("gemini-2.5-flash") 
	return &Client{model: model}, nil
}

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


func (c *Client) GenerateNodeComparison(node1, node2 *models.PNode) (string, error) {
	ctx := context.Background()

	// Helper to format node data into a string
	formatNode := func(node *models.PNode) string {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Name: %s\n", node.Name))
		b.WriteString(fmt.Sprintf("Uptime: %.2f%%\n", node.Uptime))
		b.WriteString(fmt.Sprintf("Latency: %d ms\n", node.Latency))
		b.WriteString(fmt.Sprintf("XDN Score: %.2f\n", node.XDNScore))
		b.WriteString(fmt.Sprintf("CPU Usage: %.2f%%\n", node.CPUPercent))
		b.WriteString(fmt.Sprintf("Packets Out: %d\n", node.PacketsOut))
		b.WriteString(fmt.Sprintf("Location: %s\n", node.Location))
		return b.String()
	}

	prompt := fmt.Sprintf(`
You are an expert analyst for the Xandeum decentralized storage network.
Analyze the following two pNodes and provide a comparison in markdown format.

**Objective:** Determine which node is better for different use cases.

**Node 1 Data:**
%s

**Node 2 Data:**
%s

**Instructions:**
1.  Start with a title: "### AI-Powered Comparison: [Node 1 Name] vs [Node 2 Name]".
2.  Briefly state the main strength of each node in its own paragraph. For example: "**Node 1 ([Node 1 Name])** excels in..."
3.  Provide a final "Recommendation" paragraph. State which node is better for specific tasks (e.g., high-frequency data access, long-term archival storage). Be decisive.
4.  Keep the entire response concise and easy to read. Do not use more than four paragraphs in total.
`, formatNode(node1), formatNode(node2))

	resp, err := c.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("failed to generate content for node comparison: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content generated for node comparison")
	}

	comparison, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected response format from API for node comparison")
	}

	return string(comparison), nil
}
