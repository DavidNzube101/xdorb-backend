package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"xdorb-backend/internal"
	"xdorb-backend/internal/config"
	"xdorb-backend/internal/models"
	"xdorb-backend/internal/prpc"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
)

// Bot represents the Telegram bot
type Bot struct {
	bot        *tgbotapi.BotAPI
	config     *config.Config
	prpc       *prpc.Client
	firebase   *internal.FirebaseService
	httpClient *http.Client
}

// NewBot creates a new Telegram bot instance
func NewBot(cfg *config.Config) (*Bot, error) {
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("telegram bot token not configured")
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	bot.Debug = cfg.Environment == "development"

	logrus.Infof("Telegram bot authorized on account %s", bot.Self.UserName)

	// Initialize Firebase service
	firebase, _ := internal.NewFirebaseService(cfg) // Ignore error for now

	// Set bot commands for auto-suggestions
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Welcome message and overview"},
		{Command: "help", Description: "Show all available commands"},
		{Command: "list_pnodes", Description: "List top pNodes by XDN Score"},
		{Command: "pnode", Description: "Get detailed pNode info"},
		{Command: "xdn_score", Description: "Quick XDN Score view"},
		{Command: "leaderboard", Description: "Show top pNodes leaderboard"},
		{Command: "network", Description: "Show network overview"},
		{Command: "search", Description: "Find pNodes by region"},
		{Command: "ai_summary", Description: "Get AI-powered insights (network or specific pNode)"},
		{Command: "catacombs", Description: "View historical pNodes (resting in peace)"},
	}

	setCommandsConfig := tgbotapi.NewSetMyCommands(commands...)
	_, err = bot.Request(setCommandsConfig)
	if err != nil {
		logrus.Warnf("Failed to set bot commands: %v", err)
	} else {
		logrus.Info("Bot commands set successfully for auto-suggestions")
	}

	return &Bot{
		bot:        bot,
		config:     cfg,
		prpc:       prpc.NewClient(cfg),
		firebase:   firebase,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Start begins the bot's message handling loop
func (b *Bot) Start() error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		go b.handleMessage(update.Message)
	}

	return nil
}

// handleMessage processes incoming messages
func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	logrus.Infof("Received message from %s: %s", msg.From.UserName, text)

	var response string

	switch {
	case strings.HasPrefix(text, "/start"):
		response = b.handleStart()
	case strings.HasPrefix(text, "/help"):
		response = b.handleHelp()
	case strings.HasPrefix(text, "/list_pnodes"):
		response = b.handleListPNodes(text)
	case strings.HasPrefix(text, "/pnode "):
		response = b.handlePNode(text)
	case strings.HasPrefix(text, "/xdn_score "):
		response = b.handleXDNScore(text)
	case strings.HasPrefix(text, "/leaderboard"):
		response = b.handleLeaderboard(text)
	case strings.HasPrefix(text, "/network"):
		response = b.handleNetwork()
	case strings.HasPrefix(text, "/search "):
		response = b.handleSearch(text)
	case strings.HasPrefix(text, "/ai_summary"):
		response = b.handleAISummary(text)
	case strings.HasPrefix(text, "/catacombs"):
		response = b.handleCatacombs(text)
	case strings.HasPrefix(text, "/prune "):
		response = b.handlePrune(text, msg.From.UserName)
	default:
		response = "Unknown command. Use /help to see available commands."
	}

	b.sendMessage(chatID, response)
}

// sendMessage sends a message to the specified chat
func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	msg.DisableWebPagePreview = true

	if _, err := b.bot.Send(msg); err != nil {
		logrus.Errorf("Failed to send message: %v", err)
	}
}

// API helper methods to align with backend

func (b *Bot) apiGetPNodes(limit int) ([]models.PNode, error) {
	url := fmt.Sprintf("%s/api/pnodes?limit=%d", b.config.BackendURL, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Data  []models.PNode `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Error != "" {
		return nil, fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data, nil
}

func (b *Bot) apiGetPNodeByID(id string) (*models.PNode, error) {
	url := fmt.Sprintf("%s/api/pnodes/%s", b.config.BackendURL, id)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Data  *models.PNode `json:"data"`
		Error string        `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Error != "" {
		return nil, fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data, nil
}

func (b *Bot) apiGetDashboardStats() (*models.DashboardStats, error) {
	url := fmt.Sprintf("%s/api/dashboard/stats", b.config.BackendURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Data  *models.DashboardStats `json:"data"`
		Error string                 `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Error != "" {
		return nil, fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data, nil
}

func (b *Bot) apiGetPNodeHistory(id, timeRange string, simulated bool) ([]models.PNodeHistory, error) {
	url := fmt.Sprintf("%s/api/pnodes/%s/history?range=%s&simulated=%t", b.config.BackendURL, id, timeRange, simulated)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Data  []models.PNodeHistory `json:"data"`
		Error string                `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Error != "" {
		return nil, fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data, nil
}

func (b *Bot) apiGetRegistrationInfo(id string) (map[string]string, error) {
	url := fmt.Sprintf("%s/api/pnodes/%s/registration", b.config.BackendURL, id)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp struct {
		Data  map[string]string `json:"data"`
		Error string            `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}
	if apiResp.Error != "" {
		return nil, fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data, nil
}

func (b *Bot) apiCompareNodes(node1, node2 *models.PNode) (string, error) {
	url := fmt.Sprintf("%s/api/ai/compare-nodes", b.config.BackendURL)
	payload := map[string]interface{}{
		"node1": node1,
		"node2": node2,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var apiResp struct {
		Data  map[string]string `json:"data"`
		Error string            `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", err
	}
	if apiResp.Error != "" {
		return "", fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data["comparison"], nil
}

func (b *Bot) apiGetNetworkSummary() (string, error) {
	url := fmt.Sprintf("%s/api/ai/network-summary", b.config.BackendURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if b.config.BotAPIKey != "" {
		req.Header.Set("Authorization", b.config.BotAPIKey)
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var apiResp struct {
		Data  map[string]string `json:"data"`
		Error string            `json:"error"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", err
	}
	if apiResp.Error != "" {
		return "", fmt.Errorf(apiResp.Error)
	}
	return apiResp.Data["summary"], nil
}

// handleStart returns the welcome message
func (b *Bot) handleStart() string {
	return `🤖 *Welcome to XDOrb Analytics Bot!*

I provide real-time analytics for Xandeum pNodes. Here's what I can do:

• View top performing pNodes by XDN Score
• Get detailed information about specific pNodes
• Check XDN Scores with formula breakdown
• Explore historical pNodes in the catacombs

*Available Commands:*
/list_pnodes [limit] - List top pNodes (default 10)
/pnode <id> - Get detailed pNode info
/xdn_score <id> - Quick XDN Score view
/leaderboard [limit] - Top pNodes leaderboard
/catacombs [limit] - View historical pNodes
/help - Show this help message

_Data is fetched live from the Xandeum network._`
}

// handleHelp returns the help message
func (b *Bot) handleHelp() string {
	return `*Available Commands:*

/start - Welcome message and overview
/help - Show this help message
/list_pnodes [limit] - List top pNodes by XDN Score (default 10, max 20)
/pnode <id> - Get detailed information about a specific pNode
/xdn_score <id> - Quick view of XDN Score with formula breakdown
/leaderboard [limit] - Show top pNodes leaderboard (alias for /list_pnodes)
/network - Show network overview statistics
/search <region> - Find pNodes in a specific region
/ai_summary [pnode_id] - Get AI-powered insights (network or specific pNode)
/catacombs [limit] - View historical pNodes from the catacombs (default 10, max 20)

*Admin Commands:*
/prune <password> - Prune old pNodes from database (admin only)

*Examples:*
/list_pnodes 5
/pnode abc123
/xdn_score def456
/search Europe
/network
/catacombs 5
/ai_summary
/ai_summary abc123

_Data is fetched in real-time from the Xandeum network._`
}

// handleListPNodes handles the /list_pnodes command
func (b *Bot) handleListPNodes(text string) string {
	limit := 10 // default

	parts := strings.Fields(text)
	if len(parts) > 1 {
		if parsed, err := strconv.Atoi(parts[1]); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	// Get all pNodes to calculate leaderboard
	allNodes, err := b.apiGetPNodes(1000)
	if err != nil {
		logrus.Errorf("Failed to get pNodes: %v", err)
		return "❌ Failed to fetch pNode data. Please try again later."
	}

	if len(allNodes) == 0 {
		return "📊 No pNode data available at the moment. The network may be initializing."
	}

	// Calculate XDN scores (same logic as in handlers.go)
	for i := range allNodes {
		stake := allNodes[i].Stake
		uptime := allNodes[i].Uptime
		latency := allNodes[i].Latency
		riskScore := allNodes[i].RiskScore

		latencyScore := 100.0 - float64(latency)
		if latencyScore < 0 {
			latencyScore = 0
		}

		riskScoreNormalized := 100.0 - riskScore
		if riskScoreNormalized < 0 {
			riskScoreNormalized = 0
		}

		allNodes[i].XDNScore = (stake * 0.4) + (uptime * 0.3) + (latencyScore * 0.2) + (riskScoreNormalized * 0.1)
	}

	// Sort by XDN Score (descending), then by stake (descending) as secondary sort
	for i := 0; i < len(allNodes)-1; i++ {
		for j := i + 1; j < len(allNodes); j++ {
			swap := false
			if allNodes[i].XDNScore < allNodes[j].XDNScore {
				swap = true
			} else if allNodes[i].XDNScore == allNodes[j].XDNScore && allNodes[i].Stake < allNodes[j].Stake {
				swap = true
			}

			if swap {
				allNodes[i], allNodes[j] = allNodes[j], allNodes[i]
			}
		}
	}

	// Take top N nodes
	leaderboard := allNodes
	if len(leaderboard) > limit {
		leaderboard = leaderboard[:limit]
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf("🏆 *Top %d pNodes by XDN Score*\n\n", len(leaderboard)))

	for i, pnode := range leaderboard {
		medal := getMedal(i + 1)
		status := getStatusEmoji(pnode.Status)
		response.WriteString(fmt.Sprintf("%s `%s` - %.1f XDN (%s %s, %.1f%% uptime)\n",
			medal, pnode.ID, pnode.XDNScore, status, pnode.Status, pnode.Uptime))
	}

	response.WriteString("\n_Data fetched live from Xandeum network_")
	return response.String()
}

// handlePNode handles the /pnode command
func (b *Bot) handlePNode(text string) string {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return "❌ Please provide a pNode ID. Usage: /pnode <id>"
	}

	id := parts[1]

	// Get pNode data
	pnode, err := b.apiGetPNodeByID(id)
	if err != nil {
		logrus.Errorf("Failed to get pNode %s: %v", id, err)
		return fmt.Sprintf("❌ Failed to fetch data for pNode %s. Please check the ID and try again.", id)
	}

	if pnode == nil || pnode.ID == "" {
		return fmt.Sprintf("❌ pNode %s not found in the network.", id)
	}

	// Get registration info
	regInfo, regErr := b.apiGetRegistrationInfo(id)
	registrationText := ""
	if regErr == nil && regInfo != nil {
		registrationText = fmt.Sprintf("\n• *Registration Date:* %s\n• *Registration Time:* %s", regInfo["registrationDate"], regInfo["registrationTime"])
	} else {
		registrationText = "\n• *Registration:* Not available"
	}

	status := getStatusEmoji(pnode.Status)
	location := pnode.Location
	if location == "" || location == "Unknown" {
		location = "Unknown"
	}

	response := fmt.Sprintf(`📊 *pNode: %s*

• *Status:* %s %s
• *Uptime:* %.1f%%
• *Latency:* %dms
• *Stake:* %s
• *Risk Score:* %.1f
• *XDN Score:* %.1f
• *Location:* %s
• *Region:* %s
• *Storage:* %s / %s
• *Last Seen:* %s%s

_XDN Formula: (stake × 0.4) + (uptime × 0.3) + ((100 - latency) × 0.2) + ((100 - risk) × 0.1)_`,
		pnode.ID,
		status, pnode.Status,
		pnode.Uptime,
		pnode.Latency,
		formatNumber(pnode.Stake),
		pnode.RiskScore,
		pnode.XDNScore,
		location,
		pnode.Region,
		formatBytes(pnode.StorageUsed),
		formatBytes(pnode.StorageCapacity),
		pnode.LastSeen.Format("2006-01-02 15:04:05 UTC"),
		registrationText)

	return response
}

// handleXDNScore handles the /xdn_score command
func (b *Bot) handleXDNScore(text string) string {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return "❌ Please provide a pNode ID. Usage: /xdn_score <id>"
	}

	id := parts[1]

	// Get pNode data
	pnode, err := b.apiGetPNodeByID(id)
	if err != nil {
		logrus.Errorf("Failed to get pNode %s: %v", id, err)
		return fmt.Sprintf("❌ Failed to fetch data for pNode %s. Please check the ID and try again.", id)
	}

	if pnode == nil || pnode.ID == "" {
		return fmt.Sprintf("❌ pNode %s not found in the network.", id)
	}

	// Calculate components
	stakeComponent := pnode.Stake * 0.4
	uptimeComponent := pnode.Uptime * 0.3
	latencyComponent := (100.0 - float64(pnode.Latency)) * 0.2
	if latencyComponent < 0 {
		latencyComponent = 0
	}
	riskComponent := (100.0 - pnode.RiskScore) * 0.1
	if riskComponent < 0 {
		riskComponent = 0
	}

	response := fmt.Sprintf(`🎯 *XDN Score for pNode %s*

*Total Score: %.1f*

*Breakdown:*
• Stake (40%%): %.1f × 0.4 = %.1f
• Uptime (30%%): %.1f × 0.3 = %.1f
• Latency (20%%): (100 - %d) × 0.2 = %.1f
• Risk Score (10%%): (100 - %.1f) × 0.1 = %.1f

*Formula:* (stake × 0.4) + (uptime × 0.3) + ((100 - latency) × 0.2) + ((100 - risk) × 0.1)`,
		pnode.ID,
		pnode.XDNScore,
		pnode.Stake, stakeComponent,
		pnode.Uptime, uptimeComponent,
		pnode.Latency, latencyComponent,
		pnode.RiskScore, riskComponent)

	return response
}

// handleLeaderboard handles the /leaderboard command (alias for /list_pnodes)
func (b *Bot) handleLeaderboard(text string) string {
	return b.handleListPNodes(strings.Replace(text, "/leaderboard", "/list_pnodes", 1))
}

// handleNetwork handles the /network command
func (b *Bot) handleNetwork() string {
	// Get dashboard stats for network overview
	stats, err := b.apiGetDashboardStats()
	if err != nil {
		logrus.Errorf("Failed to get dashboard stats: %v", err)
		return "❌ Failed to fetch network statistics. Please try again later."
	}

	response := fmt.Sprintf(`🌐 *Xandeum Network Overview*

• *Total Nodes:* %d
• *Active Nodes:* %d
• *Network Health:* %.1f%%
• *Average Latency:* %.1fms
• *Total Rewards:* %s
• *Validation Rate:* %.1f%%
• *Fetch Time:* %.2fs

_Last updated: %s_`,
		stats.TotalNodes,
		stats.ActiveNodes,
		stats.NetworkHealth,
		stats.AverageLatency,
		formatNumber(stats.TotalRewards),
		stats.ValidationRate,
		stats.FetchTime,
		time.Unix(stats.Timestamp, 0).Format("2006-01-02 15:04:05 UTC"))

	return response
}

// handleSearch handles the /search command
func (b *Bot) handleSearch(text string) string {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return "❌ Please provide a region to search. Usage: /search <region>"
	}

	region := strings.Join(parts[1:], " ")

	// Get all pNodes and filter by region
	allNodes, err := b.apiGetPNodes(1000)
	if err != nil {
		logrus.Errorf("Failed to get pNodes for search: %v", err)
		return "❌ Failed to search pNodes. Please try again later."
	}

	var matchingNodes []models.PNode
	for _, node := range allNodes {
		if strings.Contains(strings.ToLower(node.Region), strings.ToLower(region)) ||
			strings.Contains(strings.ToLower(node.Location), strings.ToLower(region)) {
			matchingNodes = append(matchingNodes, node)
		}
	}

	if len(matchingNodes) == 0 {
		return fmt.Sprintf("❌ No pNodes found in region '%s'. Try a different region or check spelling.", region)
	}

	// Sort by XDN score
	for i := 0; i < len(matchingNodes)-1; i++ {
		for j := i + 1; j < len(matchingNodes); j++ {
			if matchingNodes[i].XDNScore < matchingNodes[j].XDNScore {
				matchingNodes[i], matchingNodes[j] = matchingNodes[j], matchingNodes[i]
			}
		}
	}

	// Take top 10
	if len(matchingNodes) > 10 {
		matchingNodes = matchingNodes[:10]
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf("🔍 *pNodes in '%s' (Top %d)*\n\n", region, len(matchingNodes)))

	for i, pnode := range matchingNodes {
		status := getStatusEmoji(pnode.Status)
		response.WriteString(fmt.Sprintf("%d. `%s` - %.1f XDN (%s %.1f%% uptime)\n",
			i+1, pnode.ID, pnode.XDNScore, status, pnode.Uptime))
	}

	response.WriteString("\n_Use /pnode <id> for detailed info_")
	return response.String()
}

// handleAISummary handles the /ai_summary command
func (b *Bot) handleAISummary(text string) string {
	parts := strings.Fields(text)

	// Check if a pNode ID was provided
	if len(parts) > 1 {
		pnodeID := parts[1]
		return b.handleAISummaryForPNode(pnodeID)
	}

	// Network-wide analysis
	return b.handleAISummaryNetwork()
}

// handleAISummaryNetwork handles network-wide AI analysis
func (b *Bot) handleAISummaryNetwork() string {
	// Call AI service via backend
	aiResponse, err := b.apiGetNetworkSummary()
	if err != nil {
		logrus.Errorf("Failed to generate AI insights: %v", err)
		return "❌ AI analysis temporarily unavailable. Please try again later."
	}

	return fmt.Sprintf(`🤖 *AI Network Analysis*

%s

_Analysis based on real-time network data_`, aiResponse)
}

// handleAISummaryForPNode handles AI analysis for a specific pNode
func (b *Bot) handleAISummaryForPNode(pnodeID string) string {
	// Get pNode data
	pnode, err := b.apiGetPNodeByID(pnodeID)
	if err != nil {
		logrus.Errorf("Failed to get pNode %s for AI: %v", pnodeID, err)
		return fmt.Sprintf("❌ Failed to fetch data for pNode %s. Please check the ID and try again.", pnodeID)
	}

	if pnode == nil || pnode.ID == "" {
		return fmt.Sprintf("❌ pNode %s not found in the network.", pnodeID)
	}

	// Get pNode history for trend analysis
	history, err := b.apiGetPNodeHistory(pnodeID, "24h", false)
	if err != nil {
		logrus.Warnf("Failed to get history for pNode %s: %v", pnodeID, err)
		history = []models.PNodeHistory{} // Empty history is ok
	}

	// Get some network context (top 5 nodes for comparison)
	networkNodes, err := b.apiGetPNodes(10)
	if err != nil {
		logrus.Warnf("Failed to get network nodes for comparison: %v", err)
		networkNodes = []models.PNode{}
	}

	// Calculate XDN scores for network comparison
	for i := range networkNodes {
		stake := networkNodes[i].Stake
		uptime := networkNodes[i].Uptime
		latency := networkNodes[i].Latency
		riskScore := networkNodes[i].RiskScore

		latencyScore := 100.0 - float64(latency)
		if latencyScore < 0 {
			latencyScore = 0
		}

		riskScoreNormalized := 100.0 - riskScore
		if riskScoreNormalized < 0 {
			riskScoreNormalized = 0
		}

		networkNodes[i].XDNScore = (stake * 0.4) + (uptime * 0.3) + (latencyScore * 0.2) + (riskScoreNormalized * 0.1)
	}

	// Sort network nodes by XDN score
	for i := 0; i < len(networkNodes)-1; i++ {
		for j := i + 1; j < len(networkNodes); j++ {
			if networkNodes[i].XDNScore < networkNodes[j].XDNScore {
				networkNodes[i], networkNodes[j] = networkNodes[j], networkNodes[i]
			}
		}
	}

	// Prepare data for AI analysis
	analysisData := map[string]interface{}{
		"pnode":       pnode,
		"history":     history,
		"networkRank": b.getPNodeNetworkRank(pnode, networkNodes),
		"topNodes":    networkNodes[:min(5, len(networkNodes))],
	}

	// Call AI service for pNode-specific insights
	aiResponse, err := b.generateAIPNodeInsights(analysisData)
	if err != nil {
		logrus.Errorf("Failed to generate AI pNode insights: %v", err)
		return "❌ AI analysis temporarily unavailable. Please try again later."
	}

	return aiResponse
}

// generateAIInsights generates AI-powered network insights
func (b *Bot) generateAIInsights(data map[string]interface{}) (string, error) {
	// TODO: Implement Gemini API integration
	// For now, return a mock response
	return `🤖 *AI Network Analysis*

*Network Health:* The Xandeum network shows strong performance with high uptime across top nodes.

*Key Insights:*
• Top performers demonstrate consistent low latency
• Stake distribution appears balanced
• Risk scores are generally low across active nodes

*Recommendations:*
• Monitor nodes with latency >50ms
• Consider increasing stake for high-performing nodes
• Network appears stable for production use

_Analysis based on real-time network data_`, nil
}

// generateAIPNodeInsights generates AI-powered insights for a specific pNode
func (b *Bot) generateAIPNodeInsights(data map[string]interface{}) (string, error) {
	pnode := data["pnode"].(*models.PNode)
	rank := data["networkRank"].(int)
	history := data["history"].([]models.PNodeHistory)

	// Performance metrics are calculated within the AI analysis functions as needed

	// Performance assessment
	performance := "excellent"
	if pnode.Uptime < 95 {
		performance = "good"
	}
	if pnode.Uptime < 90 {
		performance = "needs improvement"
	}

	latencyStatus := "excellent"
	if pnode.Latency > 50 {
		latencyStatus = "good"
	}
	if pnode.Latency > 100 {
		latencyStatus = "high"
	}

	// Generate insights
	response := fmt.Sprintf(`🤖 *AI Analysis: pNode %s*

*Performance Overview:*
• *Overall Rating:* %s performance
• *Uptime:* %.1f%% (%s)
• *Latency:* %dms (%s)
• *Network Rank:* #%d out of active nodes
• *XDN Score:* %.1f

*Key Insights:*
• *Reliability:* %s uptime indicates %s stability
• *Responsiveness:* %s latency suggests %s network position
• *Staking:* %.0f stake provides %s economic commitment
• *Risk Profile:* %.1f risk score shows %s operational stability

*Recommendations:*
%s
%s
%s

*Trend Analysis:* %s

_Analysis based on real-time network data_`,
		pnode.ID,
		performance,
		pnode.Uptime, getUptimeDescription(pnode.Uptime),
		pnode.Latency, latencyStatus,
		rank,
		pnode.XDNScore,
		getUptimeDescription(pnode.Uptime), getStabilityAssessment(pnode.Uptime),
		getLatencyDescription(pnode.Latency), getNetworkPosition(pnode.Latency),
		pnode.Stake, getStakeAssessment(pnode.Stake),
		pnode.RiskScore, getRiskAssessment(pnode.RiskScore),
		getUptimeRecommendation(pnode.Uptime),
		getLatencyRecommendation(pnode.Latency),
		getStakeRecommendation(pnode.Stake, rank),
		getTrendAnalysis(history))

	return response, nil
}

// Helper functions for AI analysis
func getUptimeDescription(uptime float64) string {
	if uptime >= 99 {
		return "exceptional"
	}
	if uptime >= 95 {
		return "excellent"
	}
	if uptime >= 90 {
		return "good"
	}
	return "needs attention"
}

func getStabilityAssessment(uptime float64) string {
	if uptime >= 99 {
		return "excellent"
	}
	if uptime >= 95 {
		return "very good"
	}
	if uptime >= 90 {
		return "acceptable"
	}
	return "concerning"
}

func getLatencyDescription(latency int) string {
	if latency <= 20 {
		return "excellent"
	}
	if latency <= 50 {
		return "very good"
	}
	if latency <= 100 {
		return "acceptable"
	}
	return "high"
}

func getNetworkPosition(latency int) string {
	if latency <= 20 {
		return "prime"
	}
	if latency <= 50 {
		return "good"
	}
	if latency <= 100 {
		return "moderate"
	}
	return "needs improvement"
}

func getStakeAssessment(stake float64) string {
	if stake >= 10000 {
		return "strong"
	}
	if stake >= 5000 {
		return "moderate"
	}
	if stake >= 1000 {
		return "basic"
	}
	return "minimal"
}

func getRiskAssessment(riskScore float64) string {
	if riskScore <= 10 {
		return "excellent"
	}
	if riskScore <= 25 {
		return "good"
	}
	if riskScore <= 50 {
		return "moderate"
	}
	return "elevated"
}

func getUptimeRecommendation(uptime float64) string {
	if uptime < 95 {
		return "• *Uptime:* Consider reviewing system configuration to improve reliability"
	}
	return "• *Uptime:* Excellent uptime - maintain current configuration"
}

func getLatencyRecommendation(latency int) string {
	if latency > 100 {
		return "• *Latency:* High latency detected - check network configuration and geographic location"
	}
	if latency > 50 {
		return "• *Latency:* Moderate latency - consider optimizing network setup"
	}
	return "• *Latency:* Excellent response time - network position is optimal"
}

func getStakeRecommendation(stake float64, rank int) string {
	if rank > 10 && stake < 5000 {
		return "• *Staking:* Consider increasing stake to improve network ranking and rewards"
	}
	if stake < 1000 {
		return "• *Staking:* Minimal stake detected - increase stake for better network participation"
	}
	return "• *Staking:* Good stake level - maintain or consider increasing for higher rewards"
}

func getTrendAnalysis(history []models.PNodeHistory) string {
	if len(history) < 2 {
		return "Insufficient historical data for trend analysis"
	}

	// Simple trend analysis
	recent := history[:min(6, len(history))] // Last 6 hours
	avgRecentLatency := 0.0
	avgRecentUptime := 0.0

	for _, h := range recent {
		avgRecentLatency += float64(h.Latency)
		avgRecentUptime += h.Uptime
	}
	avgRecentLatency /= float64(len(recent))
	avgRecentUptime /= float64(len(recent))

	trend := "stable performance"
	if avgRecentUptime > 98 && avgRecentLatency < 30 {
		trend = "excellent recent performance"
	} else if avgRecentUptime < 95 {
		trend = "recent uptime concerns"
	} else if avgRecentLatency > 80 {
		trend = "recent latency increases"
	}

	return fmt.Sprintf("Recent 6-hour trend shows %s (avg %.1f%% uptime, %.0fms latency)",
		trend, avgRecentUptime, avgRecentLatency)
}

// getPNodeNetworkRank calculates the network rank of a pNode
func (b *Bot) getPNodeNetworkRank(targetPnode *models.PNode, networkNodes []models.PNode) int {
	rank := 1
	for _, node := range networkNodes {
		if node.XDNScore > targetPnode.XDNScore {
			rank++
		}
	}
	return rank
}

// Helper functions

func getMedal(position int) string {
	switch position {
	case 1:
		return "🥇"
	case 2:
		return "🥈"
	case 3:
		return "🥉"
	default:
		return fmt.Sprintf("%d.", position)
	}
}

func getStatusEmoji(status string) string {
	switch strings.ToLower(status) {
	case "active":
		return "🟢"
	case "warning":
		return "🟡"
	case "inactive":
		return "🔴"
	default:
		return "⚪"
	}
}

func formatNumber(num float64) string {
	if num >= 1000000 {
		return fmt.Sprintf("%.1fM", num/1000000)
	} else if num >= 1000 {
		return fmt.Sprintf("%.1fK", num/1000)
	}
	return fmt.Sprintf("%.0f", num)
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// handleCatacombs handles the /catacombs command
func (b *Bot) handleCatacombs(text string) string {
	limit := 10 // default

	parts := strings.Fields(text)
	if len(parts) > 1 {
		if parsed, err := strconv.Atoi(parts[1]); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	// Get historical pNodes from Firebase
	historicalNodes, err := b.firebase.GetAllPNodes(context.Background())
	if err != nil {
		logrus.Errorf("Failed to get historical pNodes: %v", err)
		return "❌ Failed to fetch historical pNode data. Please try again later."
	}

	if len(historicalNodes) == 0 {
		return "🪦 *Catacombs are empty*\n\nNo historical pNodes found. The catacombs await their first residents..."
	}

	// Calculate XDN scores for historical nodes
	for i := range historicalNodes {
		stake := historicalNodes[i].Stake
		uptime := historicalNodes[i].Uptime
		latency := historicalNodes[i].Latency
		riskScore := historicalNodes[i].RiskScore

		latencyScore := 100.0 - float64(latency)
		if latencyScore < 0 {
			latencyScore = 0
		}

		riskScoreNormalized := 100.0 - riskScore
		if riskScoreNormalized < 0 {
			riskScoreNormalized = 0
		}

		historicalNodes[i].XDNScore = (stake * 0.4) + (uptime * 0.3) + (latencyScore * 0.2) + (riskScoreNormalized * 0.1)
	}

	// Sort by XDN Score (descending)
	for i := 0; i < len(historicalNodes)-1; i++ {
		for j := i + 1; j < len(historicalNodes); j++ {
			if historicalNodes[i].XDNScore < historicalNodes[j].XDNScore {
				historicalNodes[i], historicalNodes[j] = historicalNodes[j], historicalNodes[i]
			}
		}
	}

	// Take top N nodes
	catacombNodes := historicalNodes
	if len(catacombNodes) > limit {
		catacombNodes = catacombNodes[:limit]
	}

	var response strings.Builder
	response.WriteString(fmt.Sprintf("🪦 *XDOrb Catacombs* - Historical pNodes (Top %d)\n\n", len(catacombNodes)))

	for _, pnode := range catacombNodes {
		status := getStatusEmoji(pnode.Status)
		response.WriteString(fmt.Sprintf("💀 `%s` - %.1f XDN (%s %s, %.1f%% uptime)\n",
			pnode.ID, pnode.XDNScore, status, pnode.Status, pnode.Uptime))
	}

	response.WriteString("\n_Data from the catacombs - may they rest in peace_")
	return response.String()
}

// handlePrune handles the /prune command (admin only)
func (b *Bot) handlePrune(text string, username string) string {
	// Check if user is authorized (only @skipp_dev)
	if username != "@skipp_dev" && username != "skipp_dev" {
		return "❌ Access denied. This command is restricted to administrators only."
	}

	parts := strings.Fields(text)
	if len(parts) < 2 {
		return "❌ Please provide the admin password. Usage: /prune <password>"
	}

	password := parts[1]

	// Check password against env variable
	if password != b.config.TelegramAdminPassword {
		return "❌ Invalid password. Access denied."
	}

	// Perform the prune operation (use short duration for testing instead of 30 days)
	pruneAge := 1 * time.Hour // For testing - prune nodes older than 1 hour
	err := b.firebase.PruneOldNodes(context.Background(), pruneAge)
	if err != nil {
		logrus.Errorf("Failed to prune old nodes: %v", err)
		return "❌ Failed to prune the catacombs. Please try again later."
	}

	return fmt.Sprintf("✅ *Catacombs pruned successfully!*\n\nRemoved pNodes older than %v from the historical database.\n\n_The catacombs have been cleaned... for now._", pruneAge)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
