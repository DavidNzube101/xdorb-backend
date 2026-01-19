package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"xdorb-backend/internal"
	"xdorb-backend/internal/cache"
	"xdorb-backend/internal/config"
	"xdorb-backend/internal/gemini"
    "xdorb-backend/internal/helius"
	"xdorb-backend/internal/models"
	"xdorb-backend/internal/prpc"
    "xdorb-backend/internal/updates"
    "xdorb-backend/internal/websocket"
	"xdorb-backend/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/sirupsen/logrus"
)

// Handler contains all HTTP handlers
type Handler struct {
	config       *config.Config
	prpc         *prpc.Client
	cache        *cache.Cache
	firebase     *internal.FirebaseService
	geminiClient *gemini.Client
    heliusClient *helius.Client
    hub          *websocket.Hub
    updateService *updates.Service
}

// NewHandler creates a new handler instance
func NewHandler(cfg *config.Config, hub *websocket.Hub, updateService *updates.Service) *Handler {
	firebase, _ := internal.NewFirebaseService(cfg) // Ignore error for now

	geminiClient, err := gemini.NewClient(cfg.GeminiAPIKey)
	if err != nil {
		logrus.Warnf("Failed to create Gemini client, AI features will be disabled: %v", err)
	}

	return &Handler{
		config:        cfg,
		prpc:          prpc.NewClient(cfg),
		cache:         cache.NewCache(cfg),
		firebase:      firebase,
		geminiClient:  geminiClient,
        heliusClient:  helius.NewClient(cfg),
        hub:           hub,
        updateService: updateService,
	}
}

// SetupRoutes configures all API routes
func SetupRoutes(r *gin.Engine, h *Handler) {
	// Health check (no auth required)
	r.GET("/health", h.HealthCheck)

	// Public API routes (no auth required)
	r.GET("/api/jupiter/quote", h.GetQuote)
    r.GET("/ws", h.HandleWebSocket)

    // Developer API generation (Public)
    r.POST("/api/developer/connect", h.GenerateAPIKey)

    // V1 Free API Routes
    v1Free := r.Group("/v1")
    v1Free.Use(middleware.RateLimit(h.config.RateLimitRPM))
    {
        v1Free.GET("/get-all-pnodes", h.GetV1PNodes)
        v1Free.GET("/pnode/:id", h.GetV1PNodeByID)
    }

    // V1 Gated API Routes
    v1Gated := r.Group("/v1")
    v1Gated.Use(h.DeveloperAuth())
    v1Gated.Use(middleware.RateLimit(h.config.RateLimitRPM))
    {
        // Include free routes in gated as well if requested, but usually separate is fine.
        // The user said "the api gated would be: everything in free + ..."
        // So we can just point to the same handlers or rely on the user having access to both if they have a key.
        // For simplicity, let's just add the specific gated ones here.
        v1Gated.GET("/analytics", h.GetV1Analytics)
        v1Gated.GET("/network", h.GetV1Network)
        v1Gated.GET("/network/:region", h.GetV1Region)
        v1Gated.GET("/leaderboard", h.GetV1Leaderboard)
        v1Gated.GET("/leaderboard/:season", h.GetV1LeaderboardSeason)
    }

	// API v1 routes with authentication
	v1 := r.Group("/api")
	v1.Use(middleware.APIKeyAuth(h.config.ValidAPIKeys))
	v1.Use(middleware.RateLimit(h.config.RateLimitRPM))
	{
        // Email Subscription
        v1.POST("/pnodes/:id/subscribe", h.SubscribeToNode)
        v1.POST("/pnodes/:id/test-email", h.TestEmail)

		// Dashboard
		v1.GET("/dashboard/stats", h.GetDashboardStats)

		// pNodes
		v1.GET("/pnodes", h.GetPNodes)
		v1.POST("/pnodes/refresh", h.RefreshCache)
		v1.GET("/pnodes/:id", h.GetPNodeByID)
		v1.GET("/pnodes/:id/metrics", h.GetPNodeMetrics)
		v1.GET("/pnodes/:id/history", h.GetPNodeHistory)
		v1.GET("/pnodes/:id/peers", h.GetPNodePeers)
		v1.GET("/pnodes/:id/alerts", h.GetPNodeAlerts)
        v1.GET("/pnodes/:id/nfts", h.GetPNodeNFTs)
		v1.GET("/pnodes/:id/registration", h.GetRegistrationInfo)

		// Leaderboard
		v1.GET("/leaderboard", h.GetLeaderboard)

		// Network
		v1.GET("/network/heatmap", h.GetNetworkHeatmap)
		v1.GET("/network/history", h.GetNetworkHistory)
        v1.GET("/network/region/:region/summary", h.GetRegionSummary)

		// Prices
		v1.GET("/prices", h.GetPrices)

        // Updates
        v1.GET("/whats-new", h.GetWhatsNew)

        // Operators
        v1.GET("/operators", h.GetOperators)

		// Analytics
		v1.GET("/analytics", h.GetAnalytics)
		// v1.GET("/analytics/geo", h.GetGeo)
		// v1.GET("/analytics/cpu", h.GetCpu)
		// v1.GET("/analytics/ram", h.GetRam)
		// v1.GET("/analytics/packets", h.GetPackets)
		v1.GET("/ai/network-summary", h.GetIntelligentNetworkSummary)
		v1.POST("/ai/compare-nodes", h.IntelligentNodeComparison)

		// Historical pNodes
		v1.GET("/pnodes/historical", h.GetHistoricalPNodes)
	}
}

// HealthCheck returns system health status
func (h *Handler) HealthCheck(c *gin.Context) {
	health := &models.HealthStatus{
		Status:    "healthy",
		Uptime:    "unknown", // TODO: implement uptime tracking
		Services:  make(map[string]string),
		Timestamp: time.Now().Unix(),
	}

	// Check Redis
	if err := h.cache.Ping(); err != nil {
		health.Services["redis"] = "unhealthy"
		health.Status = "degraded"
		logrus.Warn("Redis health check failed:", err)
	} else {
		health.Services["redis"] = "healthy"
	}

	// Check pRPC (mock for now)
	if err := h.prpc.HealthCheck(); err != nil {
		health.Services["prpc"] = "unhealthy"
		health.Status = "degraded"
		logrus.Warn("pRPC health check failed:", err)
	} else {
		health.Services["prpc"] = "healthy"
	}

	statusCode := http.StatusOK
	if health.Status == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, health)
}

// HandleWebSocket handles websocket requests from the peer.
func (h *Handler) HandleWebSocket(c *gin.Context) {
    websocket.ServeWs(h.hub, c.Writer, c.Request)
}

// GetQuote proxies requests to the Jupiter Quote API
func (h *Handler) GetQuote(c *gin.Context) {
	// Forward all query parameters
	queryString := c.Request.URL.RawQuery
	targetURL := "https://api.jup.ag/swap/v1/quote?" + queryString

	// Create request with API key header
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		logrus.Error("Failed to create request:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to create request",
		})
		return
	}

	// Add Jupiter API key header
	if apiKey := h.config.JupiterAPIKey; apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logrus.Error("Failed to fetch Jupiter quote:", err)
		c.JSON(http.StatusServiceUnavailable, models.APIResponse{
			Error: "Failed to fetch quote from Jupiter",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logrus.Errorf("Jupiter API returned status: %d", resp.StatusCode)
		// Try to read error body
		body, _ := io.ReadAll(resp.Body)
		logrus.Warnf("Jupiter API error body: %s", string(body))

		c.JSON(resp.StatusCode, models.APIResponse{
			Error: "Jupiter API error",
		})
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Error("Failed to read Jupiter response:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to process quote data",
		})
		return
	}

	// Parse raw response to ensure it's valid JSON
	var jupiterResp interface{}
	if err := json.Unmarshal(body, &jupiterResp); err != nil {
		logrus.Error("Failed to parse Jupiter JSON:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Invalid response from Jupiter",
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: jupiterResp})
}

// GetDashboardStats returns dashboard overview statistics
func (h *Handler) GetDashboardStats(c *gin.Context) {
	cacheKey := "dashboard:stats"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var stats models.DashboardStats
		if err := cached.Unmarshal(&stats); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: stats})
			return
		}
	}

	// Fetch from pRPC
	stats, err := h.prpc.GetDashboardStats()
	if err != nil {
		logrus.Error("Failed to get dashboard stats:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch dashboard statistics",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, stats, h.config.StatsCacheTTL); err != nil {
		logrus.Warn("Failed to cache dashboard stats:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: stats})
}

// Helper to get global pNode list (cached or fresh)
func (h *Handler) getGlobalPNodes() ([]models.PNode, error) {
	cacheKey := "pnodes:global_list_enriched" // Use a new key for the enriched data

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var pnodes []models.PNode
		if err := cached.Unmarshal(&pnodes); err == nil {
			return pnodes, nil
		}
	}

	// Fetch from pRPC (get all)
	pnodes, err := h.prpc.GetPNodes(&prpc.PNodeFilters{Limit: 1000})
	if err != nil {
		return nil, err
	}

	// Get the set of registered pNodes to enrich the list
	registeredPNodes, err := h.getRegisteredPNodesSet()
	if err != nil {
		logrus.Error("Failed to get registered pNodes set for global list, enrichment will be incomplete:", err)
		// Continue without registration data if there's an error, but log it
	}

	// Fetch Pod Credits
	mainnetCredits, devnetCredits, _ := h.getCreditsMaps()

	// Fetch latest snapshot for CPU/RAM enrichment (Best effort)
	var cpuMap map[string]float64
	var ramMap map[string]struct{ used, total float64 }

	// Try to get snapshot from Firestore (should be fast as it's 1 doc)
	// In a real prod env, we'd cache this snapshot in Redis too.
	snapshot, err := h.firebase.GetLatestNetworkSnapshot(context.Background())
	if err == nil && snapshot != nil {
		cpuMap = make(map[string]float64)
		ramMap = make(map[string]struct{ used, total float64 })
		
		for _, cpu := range snapshot.CpuUsage {
			cpuMap[cpu.Node] = cpu.Cpu
		}
		for _, ram := range snapshot.RamUsage {
			ramMap[ram.Node] = struct{ used, total float64 }{ram.Ram, ram.Total}
		}
	}

	// Enrich the nodes
	for i := range pnodes {
		// Registration and Manager
		if manager, ok := registeredPNodes[pnodes[i].ID]; ok {
			pnodes[i].Registered = true
            pnodes[i].Manager = manager
		} else {
			pnodes[i].Registered = false
		}

		// Credits Enrichment and Network Flags
        // Reset flags first
        pnodes[i].IsDevnet = false
        pnodes[i].IsMainnet = false
        pnodes[i].Credits = 0

		if devnetCredits != nil {
			if val, ok := devnetCredits[pnodes[i].ID]; ok {
				pnodes[i].Credits += val // Aggregate for total display? Or handle per-network view?
                pnodes[i].IsDevnet = true
			}
		}
        if mainnetCredits != nil {
            if val, ok := mainnetCredits[pnodes[i].ID]; ok {
                pnodes[i].Credits += val
                pnodes[i].IsMainnet = true
            }
        }

		// Stats Enrichment (if snapshot available)
		if cpuMap != nil {
			displayName := pnodes[i].Name
			if displayName == "" {
				if len(pnodes[i].ID) >= 8 {
					displayName = pnodes[i].ID[:8]
				} else {
					displayName = pnodes[i].ID
				}
			}

			// Enriched CPU
			if val, ok := cpuMap[displayName]; ok {
				pnodes[i].CPUPercent = val
			}
			
			// Enriched RAM
			if val, ok := ramMap[displayName]; ok {
				pnodes[i].MemoryUsed = int64(val.used * 1024 * 1024)
				pnodes[i].MemoryTotal = int64(val.total * 1024 * 1024)
			}
		}
	}

	// Save to Firebase using batch (Efficient)
	go func() {
		// Use a detached context with timeout for the background save
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		
		if err := h.firebase.SavePNodesBatch(ctx, pnodes); err != nil {
			logrus.Warn("Failed to save pNode batch to Firebase:", err)
		}
	}()

	// Prune old nodes concurrently
	go func() {
		if err := h.firebase.PruneOldNodes(context.Background(), 30*24*time.Hour); err != nil {
			logrus.Warn("Failed to prune old nodes:", err)
		}
	}()

	// Cache the enriched global list
	if err := h.cache.Set(cacheKey, pnodes, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache enriched global pNode list:", err)
	}

	return pnodes, nil
}

// GetPNodes returns paginated list of pNodes with optional filters
func (h *Handler) GetPNodes(c *gin.Context) {
	// Parse query parameters
	status := c.Query("status")
	region := c.Query("region")
    network := c.Query("network") // new
	search := strings.ToLower(c.Query("search"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 1000 {
		limit = 50
	}

	// Get all nodes (from cache or pRPC, now pre-enriched)
	allNodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get pNodes:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNodes",
		})
		return
	}

	// Filter in-memory
	var filtered []models.PNode
	for _, node := range allNodes {
		// Status filter
		if status != "" && status != "all" && node.Status != status {
			continue
		}
		// Region filter
		if region != "" && region != "all" && node.Region != region {
			continue	
		}
        // Network filter
        if network == "devnet" && !node.IsDevnet {
            continue
        }
        if network == "mainnet" && !node.IsMainnet {
            continue
        }
		// Search filter (name or location)
		if search != "" {
			if !strings.Contains(strings.ToLower(node.Name), search) &&
				!strings.Contains(strings.ToLower(node.Location), search) {
				continue
			}
		}

		filtered = append(filtered, node)
	}

	// Pagination
	total := len(filtered)
	start := (page - 1) * limit
	end := start + limit

	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	paginated := filtered[start:end]

	c.JSON(http.StatusOK, models.APIResponse{
		Data: paginated,
		Pagination: &models.Pagination{
			Total: total,
			Page:  page,
			Limit: limit,
		},
	})
}

// BroadcastPNodesUpdate fetches fresh pNodes and broadcasts to all WebSocket clients
func (h *Handler) BroadcastPNodesUpdate() {
    pnodes, err := h.getGlobalPNodes()
    if err != nil {
        logrus.Warn("Background broadcast fetch failed:", err)
        return
    }

    payload := map[string]interface{}{
        "type":    "pnodes_update",
        "payload": pnodes,
    }

    data, err := json.Marshal(payload)
    if err != nil {
        logrus.Warn("Failed to marshal broadcast payload:", err)
        return
    }

    h.hub.Broadcast(data)
}

// BroadcastStatsUpdate fetches fresh dashboard stats and broadcasts to all WebSocket clients
func (h *Handler) BroadcastStatsUpdate() {
    // We can reuse the logic from GetDashboardStats but for broadcasting
    stats, err := h.prpc.GetDashboardStats()
    if err != nil {
        logrus.Warn("Background stats broadcast fetch failed:", err)
        return
    }

    payload := map[string]interface{}{
        "type":    "stats_update",
        "payload": stats,
    }

    data, err := json.Marshal(payload)
    if err != nil {
        logrus.Warn("Failed to marshal stats broadcast payload:", err)
        return
    }

    h.hub.Broadcast(data)
}

// RefreshCache clears all cached data and fetches fresh pNodes
func (h *Handler) RefreshCache(c *gin.Context) {
	// Clear all cache
	if err := h.cache.FlushAll(); err != nil {
		logrus.Error("Failed to flush cache:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to clear cache",
		})
		return
	}

	logrus.Info("Cache cleared successfully")

	// Fetch fresh pNodes data and populate global cache
	pnodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to fetch fresh pNodes:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch fresh pNodes data",
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: pnodes})
}

// GetPNodeByID returns detailed information for a specific pNode
func (h *Handler) GetPNodeByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{
			Error: "pNode ID is required",
		})
		return
	}

	// Get the set of registered pNodes to enrich the response
	registeredPNodes, err := h.getRegisteredPNodesSet()
	if err != nil {
		logrus.Error("Failed to get registered pNodes set for detail view:", err)
		// Continue without registration data if there's an error
	}

	// 1. Fetch directly from pRPC to get the full, detailed object
	pnode, err := h.prpc.GetPNodeByID(id)
	if err != nil {
		// If the node is not found via direct pRPC, then check the historical/cached list as a fallback.
		// This handles cases where a node might be temporarily offline but we still have some data.
		if allNodes, cacheErr := h.getGlobalPNodes(); cacheErr == nil {
			for _, node := range allNodes {
				if node.ID == id {
					enriched := enrichNode(&node, registeredPNodes)
					c.JSON(http.StatusOK, models.APIResponse{Data: enriched})
					return
				}
			}
		}

		// If not found anywhere, return the original error
		logrus.Error("Failed to get pNode by ID from pRPC and not found in cache: ", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNode details",
		})
		return
	}

	// 2. Enrich the fresh data with registration status
	enriched := enrichNode(pnode, registeredPNodes)

	// 3. Cache the full, enriched result for this specific node
	cacheKey := "pnode:" + id
	if err := h.cache.Set(cacheKey, enriched, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache detailed pNode:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: enriched})
}

// GetPNodeMetrics returns real-time metrics for a specific pNode
func (h *Handler) GetPNodeMetrics(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{
			Error: "pNode ID is required",
		})
		return
	}

	cacheKey := "pnode:metrics:" + id

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var pnode models.PNode
		if err := cached.Unmarshal(&pnode); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: pnode})
			return
		}
	}

	// Fetch real data from pRPC
	pnode, err := h.prpc.GetPNodeByID(id)
	if err != nil {
		// Return 200 with error JSON for frontend to handle
		c.JSON(http.StatusOK, models.APIResponse{
			Error: "Failed to fetch real-time metrics from pNode",
		})
		return
	}

	// Cache the result for 30 seconds
	if err := h.cache.Set(cacheKey, pnode, 30*time.Second); err != nil {
		logrus.Warn("Failed to cache pNode metrics:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: pnode})
}

// enrichNode is a helper to add registration status and manager to a node
func enrichNode(node *models.PNode, registeredPNodes map[string]string) *models.PNode {
	if node == nil {
		return nil
	}
	if manager, ok := registeredPNodes[node.ID]; ok {
		node.Registered = true
        node.Manager = manager
	} else {
		node.Registered = false
	}
	return node
}

// GetPNodeHistory returns historical metrics for a pNode
func (h *Handler) GetPNodeHistory(c *gin.Context) {
	id := c.Param("id")
	timeRange := c.DefaultQuery("range", "24h")
	simulatedStr := c.DefaultQuery("simulated", "false")
	simulated := simulatedStr == "true"

	cacheKey := "pnode:history:" + id + ":" + timeRange + ":" + simulatedStr

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var history []models.PNodeHistory
		if err := cached.Unmarshal(&history); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: history})
			return
		}
	}

	// Fetch from pRPC
	history, err := h.prpc.GetPNodeHistory(id, timeRange, simulated)
	if err != nil {
		logrus.Error("Failed to get pNode history:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNode history",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, history, h.config.HistoryCacheTTL); err != nil {
		logrus.Warn("Failed to cache pNode history:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: history})
}

// GetPNodePeers returns connected peers for a pNode
func (h *Handler) GetPNodePeers(c *gin.Context) {
	id := c.Param("id")

	cacheKey := "pnode:peers:" + id

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var peers []models.PeerInfo
		if err := cached.Unmarshal(&peers); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: peers})
			return
		}
	}

	// Fetch from pRPC
	peers, err := h.prpc.GetPNodePeers(id)
	if err != nil {
		logrus.Error("Failed to get pNode peers:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNode peers",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, peers, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache pNode peers:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: peers})
}

// GetPNodeAlerts returns alerts for a pNode
func (h *Handler) GetPNodeAlerts(c *gin.Context) {
	// For now, return mock alerts (pRPC integration needed)
	alerts := []models.Alert{
		{
			ID:        "alert-1",
			Type:      "uptime",
			Message:   "Uptime dropped below 95%",
			Timestamp: time.Now().Add(-1 * time.Hour).Unix(),
			Severity:  "medium",
		},
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: alerts})
}

// GetLeaderboard returns top performing pNodes ranked by XDN Score
func (h *Handler) GetLeaderboard(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
    network := c.Query("network")

	cacheKey := "leaderboard:xdn:" + strconv.Itoa(limit) + ":" + network

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var leaderboard []models.PNode
		if err := cached.Unmarshal(&leaderboard); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: leaderboard})
			return
		}
	}

	// Fetch all nodes (from shared global cache)
	allNodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get nodes for leaderboard:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch leaderboard data",
		})
		return
	}

    // Filter by network
    var filtered []models.PNode
    if network != "" {
        for _, n := range allNodes {
            if network == "devnet" && n.IsDevnet {
                filtered = append(filtered, n)
            } else if network == "mainnet" && n.IsMainnet {
                filtered = append(filtered, n)
            }
        }
    } else {
        filtered = allNodes
    }

	// Create a copy to avoid mutating the cached array
	nodesCopy := make([]models.PNode, len(filtered))
	copy(nodesCopy, filtered)
	filtered = nodesCopy

	// Calculate XDN scores and sort
	// XDN Score = (stake * 0.4) + (uptime * 0.3) + ((100 - latency) * 0.2) + ((100 - riskScore) * 0.1)
	for i := range filtered {
		stake := filtered[i].Stake
		uptime := filtered[i].Uptime
		latency := filtered[i].Latency
		riskScore := filtered[i].RiskScore

		uptimePercent := uptime / 86400 * 100
		if uptimePercent > 100 {
			uptimePercent = 100
		}

		latencyScore := 100.0 - float64(latency)
		if latencyScore < 0 {
			latencyScore = 0
		}

		riskScoreNormalized := 100.0 - riskScore
		if riskScoreNormalized < 0 {
			riskScoreNormalized = 0
		}

		filtered[i].XDNScore = (stake * 0.4) + (uptimePercent * 0.3) + (latencyScore * 0.2) + (riskScoreNormalized * 0.1)
	}

	// Sort by XDN Score (descending), then by stake (descending) as secondary sort
	for i := 0; i < len(filtered)-1; i++ {
		for j := i + 1; j < len(filtered); j++ {
			swap := false
			if filtered[i].XDNScore < filtered[j].XDNScore {
				swap = true
			} else if filtered[i].XDNScore == filtered[j].XDNScore && filtered[i].Stake < filtered[j].Stake {
				swap = true
			}

			if swap {
				filtered[i], filtered[j] = filtered[j], filtered[i]
			}
		}
	}

	// Take top N nodes
	leaderboard := filtered
	if len(leaderboard) > limit {
		leaderboard = leaderboard[:limit]
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, leaderboard, h.config.StatsCacheTTL); err != nil {
		logrus.Warn("Failed to cache leaderboard:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: leaderboard})
}

// GetNetworkHeatmap returns data for the network heatmap
func (h *Handler) GetNetworkHeatmap(c *gin.Context) {
	cacheKey := "network:heatmap"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var heatmap []models.HeatmapPoint
		if err := cached.Unmarshal(&heatmap); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: heatmap})
			return
		}
	}

	// Generate real heatmap from pNode data (shared global cache)
	pnodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get pNodes for heatmap:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNodes for heatmap",
		})
		return
	}

	// Create heatmap points using real pNode coordinates
	var heatmap []models.HeatmapPoint

	// Group pNodes by approximate location (lat/lng rounded to 1 decimal for clustering)
	locationGroups := make(map[string][]models.PNode)

	for _, pnode := range pnodes {
		// Use real coordinates if available, otherwise fallback to region
		lat, lng := pnode.Lat, pnode.Lng
		if lat == 0 && lng == 0 {
			// Fallback to region coordinates
			region := pnode.Region
			if region == "" || region == "Unknown" {
				region = "Unknown"
			}
			lat, lng = h.getRegionCoordinates(region)
		}

		if lat == 0 && lng == 0 {
			continue // Skip if no coordinates
		}

		// Round to 1 decimal place for clustering nearby nodes
		key := fmt.Sprintf("%.1f,%.1f", lat, lng)
		locationGroups[key] = append(locationGroups[key], pnode)
	}

	for key, nodes := range locationGroups {
		if len(nodes) == 0 {
			continue
		}

		// Parse coordinates back
		var lat, lng float64
		fmt.Sscanf(key, "%f,%f", &lat, &lng)

		// Calculate averages
		totalUptime := 0.0
		for _, node := range nodes {
			totalUptime += node.Uptime
		}
		avgUptime := totalUptime / float64(len(nodes))

		// Calculate intensity based on node count and uptime
		intensity := (float64(len(nodes)) / 3.0) * (avgUptime / 100.0) * 100
		if intensity > 100 {
			intensity = 100
		} else if intensity < 5 {
			intensity = 5
		}

		// Use most common region in the group
		regionCounts := make(map[string]int)
		for _, node := range nodes {
			region := node.Region
			if region == "" || region == "Unknown" {
				region = "Unknown"
			}
			regionCounts[region]++
		}

		maxRegion := "Unknown"
		maxCount := 0
		for region, count := range regionCounts {
			if count > maxCount {
				maxCount = count
				maxRegion = region
			}
		}

		heatmap = append(heatmap, models.HeatmapPoint{
			Lat:       lat,
			Lng:       lng,
			Intensity: intensity,
			NodeCount: len(nodes),
			Region:    maxRegion,
			AvgUptime: avgUptime,
		})
	}

	// If no data, return empty array
	if len(heatmap) == 0 {
		heatmap = []models.HeatmapPoint{}
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, heatmap, h.config.StatsCacheTTL); err != nil {
		logrus.Warn("Failed to cache heatmap:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: heatmap})
}

// getRegionCoordinates returns approximate coordinates for a region
func (h *Handler) getRegionCoordinates(region string) (float64, float64) {
	coordinates := map[string][2]float64{
		// Major continents
		"North America": {40.7128, -74.0060},  // New York
		"Europe":        {51.5074, -0.1278},   // London
		"Asia":          {35.6762, 139.6503},  // Tokyo
		"South America": {-23.5505, -46.6333}, // São Paulo
		"Africa":        {-26.2041, 28.0473},  // Johannesburg
		"Australia":     {-33.8688, 151.2093}, // Sydney
		"Oceania":       {-33.8688, 151.2093}, // Sydney

		// European countries/regions
		"France":      {46.6034, 1.8883},  // France center
		"Grand-Est":   {48.5734, 7.7521},  // Strasbourg, Grand-Est
		"Germany":     {51.1657, 10.4515}, // Germany center
		"UK":          {54.7024, -3.2765}, // UK center
		"Italy":       {41.8719, 12.5674}, // Rome
		"Spain":       {40.4637, -3.7492}, // Madrid
		"Netherlands": {52.1326, 5.2913},  // Netherlands center

		// Eastern Europe
		"Romania":        {45.9432, 24.9668}, // Romania center
		"Neamt":          {47.0000, 26.3667}, // Neamt, Romania
		"Poland":         {51.9194, 19.1451}, // Poland center
		"Czech Republic": {49.8175, 15.4730}, // Czech Republic center
		"Hungary":        {47.1625, 19.5033}, // Hungary center

		// Asian countries
		"Japan": {36.2048, 138.2529}, // Japan center
		"China": {35.8617, 104.1954}, // China center
		"India": {20.5937, 78.9629},  // India center
		"Korea": {35.9078, 127.7669}, // Korea center

		// American regions
		"California": {36.7783, -119.4179}, // California center
		"Texas":      {31.9686, -99.9018},  // Texas center
		"Florida":    {27.6648, -81.5158},  // Florida center
		"New York":   {40.7128, -74.0060},  // New York

		// Default for unknown
		"Unknown": {20.0, 0.0}, // Equator, Prime Meridian
	}

	if coord, exists := coordinates[region]; exists {
		return coord[0], coord[1]
	}

	// Try to match partial region names
	for name, coord := range coordinates {
		if strings.Contains(strings.ToLower(region), strings.ToLower(name)) ||
			strings.Contains(strings.ToLower(name), strings.ToLower(region)) {
			return coord[0], coord[1]
		}
	}

	return 20.0, 0.0 // Default to equator if not found
}

// getMockHeatmap returns fallback mock data
func (h *Handler) getMockHeatmap() []models.HeatmapPoint {
	return []models.HeatmapPoint{
		{Lat: 40.7128, Lng: -74.0060, Intensity: 85, NodeCount: 120, Region: "North America", AvgUptime: 98.5},
		{Lat: 51.5074, Lng: -0.1278, Intensity: 75, NodeCount: 85, Region: "Europe", AvgUptime: 96.2},
		{Lat: 35.6762, Lng: 139.6503, Intensity: 65, NodeCount: 95, Region: "Asia", AvgUptime: 94.8},
		{Lat: -33.8688, Lng: 151.2093, Intensity: 55, NodeCount: 45, Region: "Australia", AvgUptime: 92.1},
		{Lat: -23.5505, Lng: -46.6333, Intensity: 45, NodeCount: 35, Region: "South America", AvgUptime: 89.7},
	}
}

// GetNetworkHistory returns historical network metrics
func (h *Handler) GetNetworkHistory(c *gin.Context) {
	timeRange := c.DefaultQuery("range", "24h")
	cacheKey := "network:history:" + timeRange

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var history []models.PNodeHistory
		if err := cached.Unmarshal(&history); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: history})
			return
		}
	}

	// Fetch all historical nodes from Firebase
	allNodes, err := h.firebase.GetAllPNodes(c.Request.Context())
	if err != nil {
		logrus.Error("Failed to get historical pNodes for network history:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch historical data",
		})
		return
	}

	if len(allNodes) == 0 {
		c.JSON(http.StatusOK, models.APIResponse{Data: []models.PNodeHistory{}})
		return
	}

	// Determine time range and grouping interval
	var cutoff time.Time
	var interval time.Duration
	now := time.Now()

	switch timeRange {
	case "7d":
		cutoff = now.Add(-7 * 24 * time.Hour)
		interval = 6 * time.Hour // 4 points per day
	case "30d":
		cutoff = now.Add(-30 * 24 * time.Hour)
		interval = 24 * time.Hour // 1 point per day
	default: // "24h"
		cutoff = now.Add(-24 * time.Hour)
		interval = 1 * time.Hour // 1 point per hour
	}

	// Group nodes by time interval
	groupedUptime := make(map[time.Time][]float64)
	for _, node := range allNodes {
		if node.LastSeen.Before(cutoff) {
			continue
		}
		// Truncate the timestamp to the grouping interval
		truncatedTime := node.LastSeen.Truncate(interval)
		groupedUptime[truncatedTime] = append(groupedUptime[truncatedTime], node.Uptime)
	}

	// Calculate average uptime for each interval
	var history []models.PNodeHistory
	for timestamp, uptimes := range groupedUptime {
		if len(uptimes) == 0 {
			continue
		}
		var totalUptime float64
		for _, u := range uptimes {
			totalUptime += u
		}
		avgUptime := totalUptime / float64(len(uptimes))

		history = append(history, models.PNodeHistory{
			Timestamp: timestamp.Unix(),
			Uptime:    avgUptime,
			// Other fields can be calculated similarly if needed
		})
	}

	// Sort history by timestamp
	for i := 0; i < len(history)-1; i++ {
		for j := i + 1; j < len(history); j++ {
			if history[i].Timestamp > history[j].Timestamp {
				history[i], history[j] = history[j], history[i]
			}
		}
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, history, h.config.HistoryCacheTTL); err != nil {
		logrus.Warn("Failed to cache network history:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: history})
}

// GetRegionSummary returns an AI-generated summary for a specific region
func (h *Handler) GetRegionSummary(c *gin.Context) {
    regionName := c.Param("region")
    if regionName == "" {
        c.JSON(http.StatusBadRequest, models.APIResponse{Error: "Region name is required"})
        return
    }

    if h.geminiClient == nil {
		c.JSON(http.StatusServiceUnavailable, models.APIResponse{
			Error: "The AI service is not configured on the backend.",
		})
		return
	}

    // Get all nodes
    allNodes, err := h.getGlobalPNodes()
    if err != nil {
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: "Failed to fetch node data"})
        return
    }

    // Filter for region
    var regionNodes []models.PNode
    for _, n := range allNodes {
        if n.Region == regionName {
            regionNodes = append(regionNodes, n)
        }
    }

    if len(regionNodes) == 0 {
        c.JSON(http.StatusNotFound, models.APIResponse{Error: "No nodes found in this region"})
        return
    }

    summary, err := h.geminiClient.GenerateRegionSummary(regionName, regionNodes)
    if err != nil {
        c.JSON(http.StatusInternalServerError, models.APIResponse{Error: fmt.Sprintf("AI analysis failed: %v", err)})
        return
    }

    c.JSON(http.StatusOK, models.APIResponse{
        Data: map[string]string{
            "summary": summary,
        },
    })
}

// GetPrices returns current crypto prices
func (h *Handler) GetPrices(c *gin.Context) {
	cacheKey := "prices:xand"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var prices models.PriceData
		if err := cached.Unmarshal(&prices); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: prices})
			return
		}
	}

	// Fetch from CoinGecko (Xandeum + Solana for conversion rates)
	url := "https://api.coingecko.com/api/v3/simple/price?ids=xandeum,solana&vs_currencies=sol,eth,btc,usd,eur"
	resp, err := http.Get(url)
	if err != nil {
		logrus.Error("Failed to fetch prices from CoinGecko:", err)
		c.JSON(http.StatusServiceUnavailable, models.APIResponse{
			Error: "Failed to fetch price data",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logrus.Errorf("CoinGecko API returned status: %d", resp.StatusCode)
		c.JSON(http.StatusServiceUnavailable, models.APIResponse{
			Error: "Price data provider unavailable",
		})
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Error("Failed to read CoinGecko response:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to process price data",
		})
		return
	}

	// Parse raw response
	var raw map[string]map[string]float64
	if err := json.Unmarshal(body, &raw); err != nil {
		logrus.Error("Failed to unmarshal price data:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Invalid price data format",
		})
		return
	}

	xandData, okXand := raw["xandeum"]
	solData, okSol := raw["solana"]

	if !okXand {
		logrus.Error("Xandeum data missing from response")
		c.JSON(http.StatusNotFound, models.APIResponse{
			Error: "Xandeum price data not found",
		})
		return
	}

	var prices models.PriceData

	// Direct values from CoinGecko
	prices.Xand.Sol = xandData["sol"]
	prices.Xand.Eth = xandData["eth"]
	prices.Xand.Btc = xandData["btc"]

	// Calculated values
	// If direct USD pair exists for xand, use it. Otherwise derive from SOL or ETH.
	// Since raw response might not have 'usd' for xandeum (as seen in curl), we use SOL price.

	// Check if we have SOL price to convert
	if okSol && prices.Xand.Sol > 0 {
		solUsd := solData["usd"]
		solEur := solData["eur"]

		// Calculate USDC/USDT (Approx 1:1 with USD)
		prices.Xand.Usdc = prices.Xand.Sol * solUsd
		prices.Xand.Usdt = prices.Xand.Sol * solUsd // Assuming USDT ~ USD

		// Calculate EURC (Approx 1:1 with EUR)
		prices.Xand.Eurc = prices.Xand.Sol * solEur
	} else {
		// Fallback: If Xand has direct USD (unlikely based on curl), or just leave as 0
		if val, ok := xandData["usd"]; ok {
			prices.Xand.Usdc = val
			prices.Xand.Usdt = val
		}
		if val, ok := xandData["eur"]; ok {
			prices.Xand.Eurc = val
		}
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, prices, h.config.PriceCacheTTL); err != nil {
		logrus.Warn("Failed to cache prices:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: prices})
}

// GetWhatsNew returns the latest features and updates
func (h *Handler) GetWhatsNew(c *gin.Context) {
    updates := models.WhatsNew{
        ID:      "jan-2026-update-v2",
        Version: "1.2.0",
        Updates: []models.FeatureUpdate{
            {
                Title:       "Developer APIs & Docs",
                Description: "Build on top of Xandeum Network data with our new public and gated APIs. Generate secure keys and access comprehensive documentation.",
                Icon:        "code",
            },
            {
                Title:       "Email Alerts & Subscriptions",
                Description: "Never miss a beat. Subscribe to daily or twice-daily email performance reports for your favorite pNodes.",
                Icon:        "bell",
            },
            // {
            //     Title:       "Real-time WebSocket Data",
            //     Description: "Experience instant network updates without page refreshes. pNodes and dashboard stats now stream live.",
            //     Icon:        "zap",
            // },
            // {
            //     Title:       "Network Operators & Fleet Management",
            //     Description: "Analyze managers and their pNode fleets. New paginated directory with detailed pNode modals.",
            //     Icon:        "users",
            // },
            // {
            //     Title:       "Interactive STOINC Calculator",
            //     Description: "Project your potential earnings with our new 4-step carousel calculator. Export results to styled PDF.",
            //     Icon:        "calculator",
            // },
            // {
            //     Title:       "Multi-Node Fleet Comparison",
            //     Description: "Compare up to 10 nodes side-by-side with AI-powered performance analysis.",
            //     Icon:        "bar-chart",
            // },
            // {
            //     Title:       "Dynamic Regional Inspection",
            //     Description: "Deep dive into specific geographical sectors with focused heatmaps and AI-generated regional summaries.",
            //     Icon:        "globe",
            // },
        },
    }

    c.JSON(http.StatusOK, models.APIResponse{Data: updates})
}

// GetOperators aggregates operator data from the CSV
func (h *Handler) GetOperators(c *gin.Context) {
    cacheKey := "operators:data:v3"

    // Try cache first
    if cached, err := h.cache.Get(cacheKey); err == nil {
        var operators []models.Operator
        if err := cached.Unmarshal(&operators); err == nil {
            // Ensure PNodes is not nil for each operator
            for i := range operators {
                if operators[i].PNodes == nil {
                    operators[i].PNodes = []string{}
                }
            }
            c.JSON(http.StatusOK, models.APIResponse{Data: operators})
            return
        }
    }

    // Read the CSV file
    records, err := h.readCSVFile("pnodes-data-2025-12-11.csv")
    if err != nil {
        logrus.Error("Failed to read operators CSV file:", err)
        c.JSON(http.StatusInternalServerError, models.APIResponse{
            Error: "Could not read operator data.",
        })
        return
    }

    operatorMap := make(map[string]*models.Operator)

    // CSV format: Index, pNode Identity Pubkey, Manager, Registered Time, Version
    for i, row := range records {
        if i == 0 { // Skip header
            continue
        }
        if len(row) < 3 {
            continue
        }

        manager := strings.TrimSpace(row[2])
        if manager == "" {
            continue
        }

        if _, exists := operatorMap[manager]; !exists {
            operatorMap[manager] = &models.Operator{
                Manager:    manager,
                Owned:      0,
                Registered: 0,
                PNodes:     []string{},
            }
        }

        op := operatorMap[manager]
        op.Owned++
        
        // Collect pNode ID (column index 1)
        if len(row) > 1 {
            op.PNodes = append(op.PNodes, strings.TrimSpace(row[1]))
        }

        // Check if Registered Time is present and not empty
        if len(row) > 3 && strings.TrimSpace(row[3]) != "" {
            op.Registered++
        }
    }

    // Convert map to slice
    var operators []models.Operator
    for _, op := range operatorMap {
        operators = append(operators, *op)
    }

    // Sort by owned count (descending)
    sort.Slice(operators, func(i, j int) bool {
        return operators[i].Owned > operators[j].Owned
    })

    // Cache the result for 1 hour
    if err := h.cache.Set(cacheKey, operators, 1*time.Hour); err != nil {
        logrus.Warn("Failed to cache operators data:", err)
    }

    c.JSON(http.StatusOK, models.APIResponse{Data: operators})
}

// GetAnalytics returns network analytics
func (h *Handler) GetAnalytics(c *gin.Context) {
	simulatedStr := c.DefaultQuery("simulated", "false")
	simulated := simulatedStr == "true"
	cacheKey := "network:analytics:" + simulatedStr

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var analytics models.AnalyticsData
		if err := cached.Unmarshal(&analytics); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: analytics})
			return
		}
	}

	// 1. Try to get rich snapshot from Firestore (populated by bot job)
	if !simulated {
		snapshot, err := h.firebase.GetLatestNetworkSnapshot(c.Request.Context())
		if err == nil && snapshot != nil {
			// Cache the result
			if err := h.cache.Set(cacheKey, snapshot, h.config.StatsCacheTTL); err != nil {
				logrus.Warn("Failed to cache analytics snapshot:", err)
			}
			c.JSON(http.StatusOK, models.APIResponse{Data: snapshot})
			return
		}
		logrus.Warn("Failed to get analytics snapshot from Firestore, falling back to live pRPC:", err)
	}

	// 2. Fallback: Fetch from pRPC (Live, but might be missing heavy stats)
	analytics, err := h.prpc.GetAnalytics(simulated)
	if err != nil {
		logrus.Error("Failed to get analytics:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch analytics data",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, analytics, h.config.StatsCacheTTL); err != nil {
		logrus.Warn("Failed to cache analytics:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: analytics})
}

// GetHistoricalPNodes returns all historical pNodes from Firebase
func (h *Handler) GetHistoricalPNodes(c *gin.Context) {
	cacheKey := "pnodes:historical"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var pnodes []models.PNode
		if err := cached.Unmarshal(&pnodes); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: pnodes})
			return
		}
	}

	// Fetch from Firebase
	pnodes, err := h.firebase.GetAllPNodes(context.Background())
	if err != nil {
		logrus.Error("Failed to get historical pNodes:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch historical pNodes",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, pnodes, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache historical pNodes:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: pnodes})
}

// getRegisteredPNodesSet reads the CSV file and returns a map of registered pubkeys to their managers
func (h *Handler) getRegisteredPNodesSet() (map[string]string, error) {
	cacheKey := "pnodes:registered_map:v2"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var registeredMap map[string]string
		if err := cached.Unmarshal(&registeredMap); err == nil {
			return registeredMap, nil
		}
	}

	// Read the CSV file
	file, err := h.readCSVFile("pnodes-data-2025-12-11.csv")
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV file: %w", err)
	}

	registeredMap := make(map[string]string)
	// Check if pubkey exists in the CSV
    // Index, pNode Identity Pubkey (1), Manager (2), Registered Time (3), Version (4)
	for i, row := range file {
        if i == 0 { continue } // Skip header
		if len(row) > 2 && strings.TrimSpace(row[1]) != "" {
			pubkey := strings.TrimSpace(row[1])
            manager := strings.TrimSpace(row[2])
			registeredMap[pubkey] = manager
		}
	}

	// Cache the result for 24 hours
	if err := h.cache.Set(cacheKey, registeredMap, 24*time.Hour); err != nil {
		logrus.Warn("Failed to cache registered pNodes map:", err)
	}

	return registeredMap, nil
}

// readCSVFile reads a CSV file and returns the rows as slices of strings
func (h *Handler) readCSVFile(filename string) ([][]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	return rows, nil
}

// GetGeo returns geographical distribution of nodes
func (h *Handler) GetGeo(c *gin.Context) {
	cacheKey := "analytics:geo"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var geo []models.GeoData
		if err := cached.Unmarshal(&geo); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: geo})
			return
		}
	}

	// Get all nodes
	pnodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get pNodes for geo:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch geographical data",
		})
		return
	}

	// Group by region, calculate count and avg uptime
	regionGroups := make(map[string][]models.PNode)
	for _, node := range pnodes {
		region := node.Region
		if region == "" || region == "Unknown" {
			region = "Unknown"
		}
		regionGroups[region] = append(regionGroups[region], node)
	}

	var geoData []models.GeoData
	for region, nodes := range regionGroups {
		count := len(nodes)
		var totalUptime float64
		for _, node := range nodes {
			totalUptime += node.Uptime
		}
		avgUptime := totalUptime / float64(count)

		// Map region to country code for flag
		countryCode := getCountryCodeForRegion(region)

		geoData = append(geoData, models.GeoData{
			Country:   region,
			Count:     count,
			AvgUptime: avgUptime,
			Flag:      getFlagEmoji(countryCode),
			Color:     "",
		})
	}

	// Generate colors
	colors := generateUniqueColors(len(geoData))
	for i := range geoData {
		geoData[i].Color = colors[i]
	}

	// Cache for 30 seconds
	if err := h.cache.Set(cacheKey, geoData, 30*time.Second); err != nil {
		logrus.Warn("Failed to cache geo data:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: geoData})
}

// GetCpu returns CPU utilization data for all nodes
func (h *Handler) GetCpu(c *gin.Context) {
	cacheKey := "analytics:cpu"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var cpuData []models.CpuData
		if err := cached.Unmarshal(&cpuData); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: cpuData})
			return
		}
	}

	// Get all nodes
	pnodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get pNodes for CPU:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch CPU data",
		})
		return
	}

	// Fetch real CPU metrics for nodes (limit to avoid overload)
	const maxConcurrent = 100
	semaphore := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var cpuData []models.CpuData

	// Generate unique colors
	colors := generateUniqueColors(len(pnodes))

	for i, node := range pnodes {
		wg.Add(1)
		go func(idx int, n models.PNode) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			nodeCacheKey := fmt.Sprintf("pnode:metrics:%s", n.ID)
			cpu := 0.0

			// Try cache first for the individual node
			if cached, err := h.cache.Get(nodeCacheKey); err == nil {
				var metrics models.PNode
				if err := cached.Unmarshal(&metrics); err == nil {
					cpu = metrics.CPUPercent
				}
			} else {
				// If not in cache, fetch and then cache it
				realNode, err := h.prpc.GetPNodeByID(n.ID)
				if err == nil && realNode != nil {
					cpu = realNode.CPUPercent
					// Cache the whole node object for future use by other endpoints
					if err := h.cache.Set(nodeCacheKey, realNode, 5*time.Minute); err != nil {
						logrus.Warnf("Failed to cache metrics for node %s: %v", n.ID, err)
					}
				} else {
					// Fallback to the global list's value if pRPC fails
					cpu = n.CPUPercent
				}
			}

			mu.Lock()
			cpuData = append(cpuData, models.CpuData{
				Node:  n.ID,
				Cpu:   cpu,
				Color: colors[idx],
			})
			mu.Unlock()
		}(i, node)
	}

	wg.Wait()

	// Sort by node ID for consistency
	sort.Slice(cpuData, func(i, j int) bool {
		return cpuData[i].Node < cpuData[j].Node
	})

	// Log sample data for debugging
	if len(cpuData) > 0 {
		logrus.Infof("CPU data for %d nodes, sample first: %+v", len(cpuData), cpuData[0])
	}

	// Cache for 5 seconds
	if err := h.cache.Set(cacheKey, cpuData, 5*time.Second); err != nil {
		logrus.Warn("Failed to cache CPU data:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: cpuData})
}

// GetRam returns RAM utilization data for all nodes
func (h *Handler) GetRam(c *gin.Context) {
	cacheKey := "analytics:ram"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var ramData []models.RamData
		if err := cached.Unmarshal(&ramData); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: ramData})
			return
		}
	}

	pnodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get pNodes for RAM:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch RAM data",
		})
		return
	}

	// Fetch real RAM metrics for nodes (limit to avoid overload)
	const maxConcurrent = 100
	semaphore := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ramData []models.RamData

	// Generate unique colors
	colors := generateUniqueColors(len(pnodes))

	for i, node := range pnodes {
		wg.Add(1)
		go func(idx int, n models.PNode) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			nodeCacheKey := fmt.Sprintf("pnode:metrics:%s", n.ID)
			ram := 0.0

			// Try cache first for the individual node
			if cached, err := h.cache.Get(nodeCacheKey); err == nil {
				var metrics models.PNode
				if err := cached.Unmarshal(&metrics); err == nil {
					if metrics.MemoryTotal > 0 {
						ram = (float64(metrics.MemoryUsed) / float64(metrics.MemoryTotal)) * 100
					}
				}
			} else {
				// If not in cache, fetch and then cache it
				realNode, err := h.prpc.GetPNodeByID(n.ID)
				if err == nil && realNode != nil {
					if realNode.MemoryTotal > 0 {
						ram = (float64(realNode.MemoryUsed) / float64(realNode.MemoryTotal)) * 100
					}
					// Cache the whole node object for future use by other endpoints
					if err := h.cache.Set(nodeCacheKey, realNode, 5*time.Minute); err != nil {
						logrus.Warnf("Failed to cache metrics for node %s: %v", n.ID, err)
					}
				} else {
					// Fallback to the global list's value if pRPC fails
					if n.MemoryTotal > 0 {
						ram = (float64(n.MemoryUsed) / float64(n.MemoryTotal)) * 100
					}
				}
			}

			mu.Lock()
			ramData = append(ramData, models.RamData{
				Node:  n.ID,
				Ram:   ram,
				Color: colors[idx],
			})
			mu.Unlock()
		}(i, node)
	}

	wg.Wait()

	// Sort by node ID for consistency
	sort.Slice(ramData, func(i, j int) bool {
		return ramData[i].Node < ramData[j].Node
	})

	// Log sample data for debugging
	if len(ramData) > 0 {
		logrus.Infof("RAM data for %d nodes, sample first: %+v", len(ramData), ramData[0])
	}

	// Cache for 5 seconds
	if err := h.cache.Set(cacheKey, ramData, 5*time.Second); err != nil {
		logrus.Warn("Failed to cache RAM data:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: ramData})
}

// GetPackets returns global packet streams
func (h *Handler) GetPackets(c *gin.Context) {
	cacheKey := "analytics:packets"

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var packetData models.PacketData
		if err := cached.Unmarshal(&packetData); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: packetData})
			return
		}
	}

	// Get all nodes
	pnodes, err := h.getGlobalPNodes()
	if err != nil {
		logrus.Error("Failed to get pNodes for packets:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch packet data",
		})
		return
	}

	// Fetch real packet metrics for nodes (limit to avoid overload)
	const maxConcurrent = 100
	semaphore := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalIn, totalOut int

	for _, node := range pnodes {
		wg.Add(1)
		go func(n models.PNode) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			nodeCacheKey := fmt.Sprintf("pnode:metrics:%s", n.ID)
			var packetsIn, packetsOut int

			// Try cache first for the individual node
			if cached, err := h.cache.Get(nodeCacheKey); err == nil {
				var metrics models.PNode
				if err := cached.Unmarshal(&metrics); err == nil {
					packetsIn = metrics.PacketsIn
					packetsOut = metrics.PacketsOut
				}
			} else {
				// If not in cache, fetch and then cache it
				realNode, err := h.prpc.GetPNodeByID(n.ID)
				if err == nil && realNode != nil {
					packetsIn = realNode.PacketsIn
					packetsOut = realNode.PacketsOut
					// Cache the whole node object for future use by other endpoints
					if err := h.cache.Set(nodeCacheKey, realNode, 5*time.Minute); err != nil {
						logrus.Warnf("Failed to cache metrics for node %s: %v", n.ID, err)
					}
				} else {
					// Fallback to the global list's value if pRPC fails
					packetsIn = n.PacketsIn
					packetsOut = n.PacketsOut
				}
			}

			mu.Lock()
			totalIn += packetsIn
			totalOut += packetsOut
			mu.Unlock()
		}(node)
	}

	wg.Wait()

	packetData := models.PacketData{
		In:  totalIn,
		Out: totalOut,
	}

	// Log sample data for debugging
	logrus.Infof("Packet data: %+v", packetData)

	// Cache for 5 seconds
	if err := h.cache.Set(cacheKey, packetData, 5*time.Second); err != nil {
		logrus.Warn("Failed to cache packet data:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: packetData})
}

// Helper functions
func getCountryCodeForRegion(region string) string {
	regionToCountry := map[string]string{
		"North America": "US",
		"Europe":        "DE",
		"Asia":          "JP",
		"South America": "BR",
		"Africa":        "ZA",
		"Australia":     "AU",
		"Oceania":       "AU",
		"Unknown":       "UN",
	}
	if code, ok := regionToCountry[region]; ok {
		return code
	}
	return "UN"
}

func getFlagEmoji(countryCode string) string {
	// Simple flag emoji from country code
	if countryCode == "UN" {
		return "🏳️"
	}
	return string(rune(0x1F1E6+int(countryCode[0])-65)) + string(rune(0x1F1E6+int(countryCode[1])-65))
}

func generateUniqueColors(n int) []string {
	palette, err := colorful.WarmPalette(n)
	if err != nil {
		// Fallback to distinct colors
		colors := make([]string, n)
		for i := 0; i < n; i++ {
			hue := float64(i) / float64(n) * 360
			col := colorful.Hsv(hue, 0.7, 0.8)
			colors[i] = col.Hex()
		}
		return colors
	}
	colors := make([]string, n)
	for i, col := range palette {
		colors[i] = col.Hex()
	}
	return colors
}

// GetIntelligentNetworkSummary returns an AI-generated summary of the network
func (h *Handler) GetIntelligentNetworkSummary(c *gin.Context) {
	if h.geminiClient == nil {
		c.JSON(http.StatusServiceUnavailable, models.APIResponse{
			Error: "The AI service is not configured on the backend.",
		})
		return
	}

	// Get all nodes
	allNodes, err := h.getGlobalPNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch node data for analysis.",
		})
		return
	}

	// Aggregate data
	totalNodes := len(allNodes)
	activeNodes := 0
	registeredNodes := 0
	var totalLatency int
	var validLatencyCount int
	var totalUptime float64
	versionCounts := make(map[string]int)
	regionCounts := make(map[string]int)

	for _, node := range allNodes {
		if node.Status == "active" {
			activeNodes++
		}
		if node.Registered {
			registeredNodes++
		}
		if node.Latency > 0 {
			totalLatency += node.Latency
			validLatencyCount++
		}
		totalUptime += node.Uptime
		versionCounts[node.Version]++
		regionCounts[node.Region]++
	}

	avgLatency := 0.0
	if validLatencyCount > 0 {
		avgLatency = float64(totalLatency) / float64(validLatencyCount)
	}
	avgUptime := 0.0
	if totalNodes > 0 {
		avgUptime = totalUptime / float64(totalNodes)
	}

	// Create a summary string for the prompt
	var summaryBuilder strings.Builder
	summaryBuilder.WriteString(fmt.Sprintf("Total Nodes: %d\n", totalNodes))
	summaryBuilder.WriteString(fmt.Sprintf("Active Nodes: %d (%.1f%%)\n", activeNodes, float64(activeNodes)/float64(totalNodes)*100))
	summaryBuilder.WriteString(fmt.Sprintf("Registered Nodes: %d (%.1f%%)\n", registeredNodes, float64(registeredNodes)/float64(totalNodes)*100))
	summaryBuilder.WriteString(fmt.Sprintf("Average Latency: %.2f ms\n", avgLatency))
	summaryBuilder.WriteString(fmt.Sprintf("Average Uptime: %.2f%%\n", avgUptime))

	summaryBuilder.WriteString("\nVersion Distribution:\n")
	for version, count := range versionCounts {
		summaryBuilder.WriteString(fmt.Sprintf("- %s: %d nodes (%.1f%%)\n", version, count, float64(count)/float64(totalNodes)*100))
	}

	summaryBuilder.WriteString("\nRegion Distribution:\n")
	for region, count := range regionCounts {
		summaryBuilder.WriteString(fmt.Sprintf("- %s: %d nodes (%.1f%%)\n", region, count, float64(count)/float64(totalNodes)*100))
	}

	// Generate summary with Gemini
	summary, err := h.geminiClient.GenerateNetworkSummary(summaryBuilder.String())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: fmt.Sprintf("Failed to generate AI summary: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data: map[string]string{
			"summary": summary,
		},
	})
}

// IntelligentNodeComparison handles the AI-powered comparison of multiple nodes
func (h *Handler) IntelligentNodeComparison(c *gin.Context) {
	if h.geminiClient == nil {
		c.JSON(http.StatusServiceUnavailable, models.APIResponse{
			Error: "The AI service is not configured on the backend.",
		})
		return
	}

	var requestBody models.CompareFleetRequest

	if err := c.ShouldBindJSON(&requestBody); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{
			Error: "Invalid request body: " + err.Error(),
		})
		return
	}

	if len(requestBody.Nodes) < 2 {
		c.JSON(http.StatusBadRequest, models.APIResponse{
			Error: "Request body must contain at least two nodes in the 'nodes' array",
		})
		return
	}

	// Generate comparison with Gemini
	comparison, err := h.geminiClient.GenerateFleetComparison(requestBody.Nodes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: fmt.Sprintf("Failed to generate AI fleet comparison: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, models.APIResponse{
		Data: map[string]string{
			"comparison": comparison,
		},
	})
}

func (h *Handler) GetRegistrationInfo(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{
			Error: "pNode ID is required",
		})
		return
	}

	records, err := h.readCSVFile("pnodes-data-2025-12-11.csv")
	if err != nil {
		logrus.Error("Failed to read registration CSV file:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Could not read registration data.",
		})
		return
	}

	for i, row := range records {
		if i == 0 {
			continue
		}
		if len(row) > 3 && strings.TrimSpace(row[1]) == id {
			registeredTime := strings.TrimSpace(row[3])
			parts := strings.Split(registeredTime, ", ")
			registrationDate := ""
			registrationTime := ""
			if len(parts) == 2 {
				registrationDate = parts[0]
				registrationTime = parts[1]
			} else {
				registrationDate = registeredTime
				registrationTime = ""
			}
			c.JSON(http.StatusOK, models.APIResponse{
				Data: map[string]string{
					"registrationDate": registrationDate,
					"registrationTime": registrationTime,
				},
			})
			return
		}
	}

	        c.JSON(http.StatusNotFound, models.APIResponse{
	                Error: "Registration information not found for this node.",
	        })
	}
	
	// getCreditsMap fetches pod credits from external API (Mainnet & Devnet) and returns separate maps
	func (h *Handler) getCreditsMaps() (map[string]float64, map[string]float64, error) {
		cacheKeyMainnet := "pod_credits_map_mainnet"
		cacheKeyDevnet := "pod_credits_map_devnet"
	
		var mainnetCredits, devnetCredits map[string]float64
	
		// Try cache first
		if cachedMain, err := h.cache.Get(cacheKeyMainnet); err == nil {
			cachedMain.Unmarshal(&mainnetCredits)
		}
		if cachedDev, err := h.cache.Get(cacheKeyDevnet); err == nil {
			cachedDev.Unmarshal(&devnetCredits)
		}
	
		if mainnetCredits != nil && devnetCredits != nil {
			return mainnetCredits, devnetCredits, nil
		}
	
		mainnetCredits = make(map[string]float64)
		devnetCredits = make(map[string]float64)
	
		urls := map[string]string{
			"mainnet": "https://podcredits.xandeum.network/api/mainnet-pod-credits",
			"devnet":  "https://podcredits.xandeum.network/api/pods-credits", // Assuming pods-credits is devnet based on your earlier prompt
		}
	
		for netType, url := range urls {
			resp, err := http.Get(url)
			if err != nil {
				logrus.Warnf("Failed to fetch %s credits from %s: %v", netType, url, err)
				continue
			}
			defer resp.Body.Close()
	
			var creds struct {
				PodsCredits []struct {
					Credits float64 `json:"credits"`
					PodID   string  `json:"pod_id"`
				} `json:"pods_credits"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
				logrus.Warnf("Failed to decode %s credits: %v", netType, err)
				continue
			}
	
			targetMap := mainnetCredits
			if netType == "devnet" {
				targetMap = devnetCredits
			}
	
			for _, pc := range creds.PodsCredits {
	            // Normalize ID for consistent matching
	            id := strings.TrimSpace(pc.PodID)
				targetMap[id] = pc.Credits
			}
		}
	
		// Cache for 10 minutes
		h.cache.Set(cacheKeyMainnet, mainnetCredits, 10*time.Minute)
		h.cache.Set(cacheKeyDevnet, devnetCredits, 10*time.Minute)
	
		return mainnetCredits, devnetCredits, nil
	}
