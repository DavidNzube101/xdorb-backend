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


func (c *Client) GenerateFleetComparison(nodes []models.PNode) (string, error) {
	ctx := context.Background()

	// Helper to format node data into a string
	formatNode := func(node models.PNode) string {
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

	var nodesData strings.Builder
	for i, node := range nodes {
		nodesData.WriteString(fmt.Sprintf("### Node %d Data:\n%s\n", i+1, formatNode(node)))
	}

	prompt := fmt.Sprintf(`
You are an expert analyst for the Xandeum decentralized storage network.
Analyze the following fleet of %d pNodes and provide a comparative analysis in markdown format.

**Fleet Data:**
%s

**Instructions:**
1.  Start with a title: "### AI Fleet Analysis: %d Nodes".
2.  Identify the top performer in the group and explain why.
3.  Note any regional or technical advantages observed in specific nodes.
4.  Provide a final "Fleet Recommendation" on how to optimize this group of nodes.
5.  Keep the entire response concise and insightful. Use bullet points for readability.
`, len(nodes), nodesData.String(), len(nodes))

	resp, err := c.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("failed to generate content for fleet comparison: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content generated for fleet comparison")
	}

	comparison, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected response format from API for fleet comparison")
	}

	return string(comparison), nil
}

func (c *Client) GenerateRegionSummary(regionName string, nodes []models.PNode) (string, error) {
	ctx := context.Background()

	var nodesInfo strings.Builder
	activeCount := 0
	totalLatency := 0
	for _, n := range nodes {
		if n.Status == "active" {
			activeCount++
		}
		totalLatency += n.Latency
		nodesInfo.WriteString(fmt.Sprintf("- %s: Status=%s, Latency=%dms, XDN=%.1f\n", n.Name, n.Status, n.Latency, n.XDNScore))
	}

	avgLatency := 0.0
	if len(nodes) > 0 {
		avgLatency = float64(totalLatency) / float64(len(nodes))
	}

	prompt := fmt.Sprintf(`
You are a network optimization AI for Xandeum. 
Analyze the following node data for the "%s" region and provide a concise "Optimization Tip" (max 3 sentences).
Focus on what operators in this specific region should do to improve their STOINC rewards.

Region Stats:
- Total Nodes: %d
- Active Nodes: %d
- Average Latency: %.1fms

Node Details:
%s
`, regionName, len(nodes), activeCount, avgLatency, nodesInfo.String())

	resp, err := c.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("failed to generate region summary: %w", err)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content generated for region summary")
	}

	summary, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected response format from API for region summary")
	}

	return string(summary), nil
}
