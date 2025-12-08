package api

import (
	"math/rand"
	"net/http"
	"strconv"
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

	// API v1 routes with authentication
	v1 := r.Group("/api")
	v1.Use(middleware.APIKeyAuth(h.config.ValidAPIKeys))
	v1.Use(middleware.RateLimit(h.config.RateLimitRPM))
	{
		// Dashboard
		v1.GET("/dashboard/stats", h.GetDashboardStats)

		// pNodes
		v1.GET("/pnodes", h.GetPNodes)
		v1.GET("/pnodes/:id", h.GetPNodeByID)
		v1.GET("/pnodes/:id/history", h.GetPNodeHistory)
		v1.GET("/pnodes/:id/peers", h.GetPNodePeers)
		v1.GET("/pnodes/:id/alerts", h.GetPNodeAlerts)

		// Leaderboard
		v1.GET("/leaderboard", h.GetLeaderboard)

		// Network
		v1.GET("/network/heatmap", h.GetNetworkHeatmap)
		v1.GET("/network/history", h.GetNetworkHistory)
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

// GetPNodes returns paginated list of pNodes with optional filters
func (h *Handler) GetPNodes(c *gin.Context) {
	// Parse query parameters
	status := c.Query("status")
	location := c.Query("location")
	region := c.Query("region")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}

	filters := &prpc.PNodeFilters{
		Status:   status,
		Location: location,
		Region:   region,
		Page:     page,
		Limit:    limit,
	}

	cacheKey := "pnodes:list:" + filters.CacheKey()

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var pnodes []models.PNode
		if err := cached.Unmarshal(&pnodes); err == nil {
			logrus.Debug("Serving pNodes from cache")
			c.JSON(http.StatusOK, models.APIResponse{Data: pnodes})
			return
		}
	}

	// Fetch from pRPC
	pnodes, err := h.prpc.GetPNodes(filters)
	if err != nil {
		logrus.Error("Failed to get pNodes:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch pNodes",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, pnodes, h.config.PNodeCacheTTL); err != nil {
		logrus.Warn("Failed to cache pNodes:", err)
	} else {
		logrus.Debug("Cached pNodes data")
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

	cacheKey := "pnode:" + id

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var pnode models.PNode
		if err := cached.Unmarshal(&pnode); err == nil {
			logrus.Debug("Serving pNode from cache")
			c.JSON(http.StatusOK, models.APIResponse{Data: pnode})
			return
		}
	}

	// Fetch from pRPC
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
	} else {
		logrus.Debug("Cached pNode data")
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: pnode})
}

// GetPNodeHistory returns historical metrics for a pNode
func (h *Handler) GetPNodeHistory(c *gin.Context) {
	id := c.Param("id")
	timeRange := c.DefaultQuery("range", "24h")

	cacheKey := "pnode:history:" + id + ":" + timeRange

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var history []models.PNodeHistory
		if err := cached.Unmarshal(&history); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: history})
			return
		}
	}

	// Fetch from pRPC
	history, err := h.prpc.GetPNodeHistory(id, timeRange)
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

// GetLeaderboard returns top performing pNodes
func (h *Handler) GetLeaderboard(c *gin.Context) {
	metric := c.DefaultQuery("metric", "rewards")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	cacheKey := "leaderboard:" + metric + ":" + strconv.Itoa(limit)

	// Try cache first
	if cached, err := h.cache.Get(cacheKey); err == nil {
		var leaderboard []models.PNode
		if err := cached.Unmarshal(&leaderboard); err == nil {
			c.JSON(http.StatusOK, models.APIResponse{Data: leaderboard})
			return
		}
	}

	// Fetch from pRPC
	leaderboard, err := h.prpc.GetLeaderboard(metric, limit)
	if err != nil {
		logrus.Error("Failed to get leaderboard:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch leaderboard",
		})
		return
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

	// Fetch from pRPC
	heatmap, err := h.prpc.GetNetworkHeatmap()
	if err != nil {
		logrus.Error("Failed to get network heatmap:", err)
		c.JSON(http.StatusInternalServerError, models.APIResponse{
			Error: "Failed to fetch network heatmap",
		})
		return
	}

	// Cache the result
	if err := h.cache.Set(cacheKey, heatmap, h.config.StatsCacheTTL); err != nil {
		logrus.Warn("Failed to cache heatmap:", err)
	}

	c.JSON(http.StatusOK, models.APIResponse{Data: heatmap})
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
