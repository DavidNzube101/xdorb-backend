package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"xdorb-backend/internal/cache"
	"xdorb-backend/internal/config"
	"xdorb-backend/internal/models"
	"xdorb-backend/internal/prpc"
	"xdorb-backend/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Handler contains all HTTP handlers
type Handler struct {
	config *config.Config
	prpc   *prpc.Client
	cache  *cache.Cache
}

// NewHandler creates a new handler instance
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		config: cfg,
		prpc:   prpc.NewClient(cfg),
		cache:  cache.NewCache(cfg),
	}
}

// SetupRoutes configures all API routes
func SetupRoutes(r *gin.Engine, h *Handler) {
	// Health check (no auth required)
	r.GET("/health", h.HealthCheck)

	// Public API routes (no auth required)
	r.GET("/api/jupiter/quote", h.GetQuote)

	// API v1 routes with authentication
	v1 := r.Group("/api")
	v1.Use(middleware.APIKeyAuth(h.config.ValidAPIKeys))
	v1.Use(middleware.RateLimit(h.config.RateLimitRPM))
	{
		// Dashboard
		v1.GET("/dashboard/stats", h.GetDashboardStats)

		// pNodes
		v1.GET("/pnodes", h.GetPNodes)
		v1.POST("/pnodes/refresh", h.RefreshCache)
		v1.GET("/pnodes/:id", h.GetPNodeByID)
		v1.GET("/pnodes/:id/history", h.GetPNodeHistory)
		v1.GET("/pnodes/:id/peers", h.GetPNodePeers)
		v1.GET("/pnodes/:id/alerts", h.GetPNodeAlerts)

		// Leaderboard
		v1.GET("/leaderboard", h.GetLeaderboard)

		// Network
		v1.GET("/network/heatmap", h.GetNetworkHeatmap)
		v1.GET("/network/history", h.GetNetworkHistory)

		// Prices
		v1.GET("/prices", h.GetPrices)

		// Analytics
		v1.GET("/analytics", h.GetAnalytics)
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

// GetQuote proxies requests to the Jupiter Quote API
func (h *Handler) GetQuote(c *gin.Context) {
	// Forward all query parameters
	queryString := c.Request.URL.RawQuery
	targetURL := "https://api.jup.ag/swap/v1/quote?" + queryString

	resp, err := http.Get(targetURL)
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
	cacheKey := "pnodes:global_list"

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

	// Cache the global list
	if err := h.cache.Set(cacheKey, pnodes, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache global pNode list:", err)
	}

	return pnodes, nil
}

// GetPNodes returns paginated list of pNodes with optional filters
func (h *Handler) GetPNodes(c *gin.Context) {
	// Parse query parameters
	status := c.Query("status")
	region := c.Query("region")
	search := strings.ToLower(c.Query("search"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}

	// Get all nodes (from cache or pRPC)
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

	c.JSON(http.StatusOK, models.APIResponse{Data: paginated})
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

	// 1. Try to find in global list first (fastest and most consistent)
	if allNodes, err := h.getGlobalPNodes(); err == nil {
		for _, node := range allNodes {
			if node.ID == id {
				c.JSON(http.StatusOK, models.APIResponse{Data: node})
				return
			}
		}
	}

	// 2. If not in global list, try specific cache key
	cacheKey := "pnode:" + id
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var pnode models.PNode
		if err := cached.Unmarshal(&pnode); err == nil {
			logrus.Debug("Serving pNode from cache")
			c.JSON(http.StatusOK, models.APIResponse{Data: pnode})
			return
		}
	}

	// 3. Fetch from pRPC (fallback for nodes not in main list?)
	pnode, err := h.prpc.GetPNodeByID(id)
	if err != nil {
		logrus.Error("Failed to get pNode:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNode details",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, pnode, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache pNode:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: pnode})
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

	cacheKey := "leaderboard:xdn:" + strconv.Itoa(limit)

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

	// Create a copy to avoid mutating the cached array
	nodesCopy := make([]models.PNode, len(allNodes))
	copy(nodesCopy, allNodes)
	allNodes = nodesCopy

	// Calculate XDN scores and sort
	// XDN Score = (stake * 0.4) + (uptime * 0.3) + ((100 - latency) * 0.2) + ((100 - riskScore) * 0.1)
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

	// Mock historical network data - replace with real pRPC call
	var history []models.PNodeHistory
	now := time.Now()
	points := 24 // 24 hours default
	if timeRange == "7d" {
		points = 168 // 7 days
	} else if timeRange == "30d" {
		points = 720 // 30 days
	}

	for i := points; i >= 0; i-- {
		timestamp := now.Add(-time.Duration(i) * time.Hour)
		point := models.PNodeHistory{
			Timestamp:   timestamp.Unix(),
			Latency:     40 + rand.Intn(20),                                   // 40-60ms average network latency
			Uptime:      97.0 + rand.Float64()*3.0,                            // 97-100% network uptime
			StorageUsed: int64(700000+rand.Intn(100000)) * 1024 * 1024 * 1024, // TB in bytes
			Rewards:     rand.Float64() * 1000,                                // Network-wide rewards per hour
		}
		history = append(history, point)
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, history, h.config.HistoryCacheTTL); err != nil {
		logrus.Warn("Failed to cache network history:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: history})
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

	// Cache the result (5 minutes)
	if err := h.cache.Set(cacheKey, prices, 5*time.Minute); err != nil {
		logrus.Warn("Failed to cache prices:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: prices})
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

	// Fetch from pRPC
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
