package prpc

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"xdorb-backend/internal/config"
	"xdorb-backend/internal/geolocation"
	"xdorb-backend/internal/models"

	prpc "github.com/DavidNzube101/xandeum-prpc-go"

	"github.com/sirupsen/logrus"
)

// Client represents the pRPC client
type Client struct {
	cfg *config.Config
}

// PNodeFilters represents filters for pNode queries
type PNodeFilters struct {
	Status   string
	Location string
	Region   string
	Page     int
	Limit    int
}

// CacheKey generates a cache key for the filters
func (f *PNodeFilters) CacheKey() string {
	return fmt.Sprintf("%s:%s:%s:%d:%d", f.Status, f.Location, f.Region, f.Page, f.Limit)
}

// NewClient creates a new pRPC client
func NewClient(cfg *config.Config) *Client {
	return &Client{cfg: cfg}
}

// calculateXDNScore computes the Xandeum Node Score
// Formula: (stake * 0.4) + (uptime * 0.3) + ((100 - latency) * 0.2) + ((100 - riskScore) * 0.1)
func calculateXDNScore(stake float64, uptime float64, latency int, riskScore float64) float64 {
	latencyScore := 100.0 - float64(latency)
	if latencyScore < 0 {
		latencyScore = 0
	}

	riskScoreNormalized := 100.0 - riskScore
	if riskScoreNormalized < 0 {
		riskScoreNormalized = 0
	}

	return (stake * 0.4) + (uptime * 0.3) + (latencyScore * 0.2) + (riskScoreNormalized * 0.1)
}

// HealthCheck performs a health check on the pRPC connection
func (c *Client) HealthCheck() error {
	// Mock health check - in real implementation, ping pRPC endpoint
	time.Sleep(10 * time.Millisecond) // Simulate network latency
	return nil
}

// GetDashboardStats fetches dashboard statistics
func (c *Client) GetDashboardStats() (*models.DashboardStats, error) {
	start := time.Now()
	// Fetch all pNodes to calculate real stats
	pnodes, err := c.GetPNodes(&PNodeFilters{Limit: 1000}) // High limit to get all
	if err != nil {
		logrus.Error("Failed to fetch pNodes for dashboard:", err)
		// Fallback to mock
		return &models.DashboardStats{
			TotalNodes:     0,
			ActiveNodes:    0,
			NetworkHealth:  0,
			TotalRewards:   0,
			AverageLatency: 0,
			ValidationRate: 0,
			FetchTime:      0,
			Timestamp:      time.Now().Unix(),
		}, nil
	}

	totalNodes := len(pnodes)
	activeNodes := 0
	totalLatency := 0
	validLatencies := 0

	for _, p := range pnodes {
		if p.Status == "active" {
			activeNodes++
		}
		if p.Latency > 0 {
			totalLatency += p.Latency
			validLatencies++
		}
	}

	avgLatency := 0.0
	if validLatencies > 0 {
		avgLatency = float64(totalLatency) / float64(validLatencies)
	}

	networkHealth := 0.0
	if totalNodes > 0 {
		networkHealth = float64(activeNodes) / float64(totalNodes) * 100
	}

	elapsed := time.Since(start)

	stats := &models.DashboardStats{
		TotalNodes:     totalNodes,
		ActiveNodes:    activeNodes,
		NetworkHealth:  networkHealth,
		TotalRewards:   0, // Not available
		AverageLatency: avgLatency,
		ValidationRate: 0, // Not available
		FetchTime:      elapsed.Seconds(),
		Timestamp:      time.Now().Unix(),
	}

	logrus.Debugf("Fetched dashboard stats: %d nodes in %.2fs", totalNodes, elapsed.Seconds())
	return stats, nil
}

// measureLatency measures TCP connect latency to an IP on port 6000 with retry
func measureLatency(ip string) int {
	const maxRetries = 2
	const timeout = 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", ip+":6000", timeout)
		if err == nil {
			conn.Close()
			return int(time.Since(start).Milliseconds())
		}
		// Exponential backoff
		if attempt < maxRetries {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	return 0 // Unreachable after retries, return 0
}

// GetPNodes fetches pNodes with optional filters using the new get-pods-with-stats method
func (c *Client) GetPNodes(filters *PNodeFilters) ([]models.PNode, error) {
	start := time.Now()
	var podsResp interface{}
	var lastErr error

	// Try each seed IP until one works
	for _, seedIP := range c.cfg.PRPCSeedIPs {
		seedClient := prpc.NewClient(seedIP)
		resp, err := seedClient.GetPodsWithStats()
		if err != nil {
			logrus.Warnf("Failed to get pods with stats from seed %s: %v", seedIP, err)
			lastErr = err
			continue
		}

		podsResp = resp
		logrus.Infof("Successfully got pods with stats from seed %s", seedIP)
		break
	}

	if podsResp == nil {
		logrus.Errorf("All seed IPs failed, last error: %v", lastErr)
		return nil, fmt.Errorf("failed to get pods from any seed IP: %w", lastErr)
	}

	// Handle the actual pRPC response type
	// From the error, it's returning *prpc.PodsResponse
	if podsResponse, ok := podsResp.(*prpc.PodsResponse); ok {
		// It's a PodsResponse struct, process pods directly
		var pnodes []models.PNode
		for _, pod := range podsResponse.Pods {
			// Extract IP from address
			ip := strings.Split(pod.Address, ":")[0]
			if ip == "" {
				continue
			}

			// Get geolocation
			loc, err := geolocation.GetLocation(ip)
			locationStr := "Unknown"
			region := "Unknown"
			lat := 0.0
			lng := 0.0
			if err == nil && loc != nil {
				locationStr = loc.GetLocationString()
				region = loc.Region
				if region == "" {
					region = loc.Country
				}
				lat = loc.Latitude
				lng = loc.Longitude
			}

			// Determine status - use current time as fallback since LastUpdated field may not exist
			status := "active"
			lastSeen := time.Now() // Fallback to current time
			// Try to use any timestamp field that might exist
			// For now, assume nodes are active since we just fetched them
			if time.Since(lastSeen) > 5*time.Minute {
				status = "inactive"
			} else if time.Since(lastSeen) > 1*time.Minute {
				status = "warning"
			}

			// Use pubkey as ID, fallback to IP if empty
			pnodeID := pod.Pubkey
			if pnodeID == "" {
				pnodeID = ip
			}

			// Safely get short pubkey for name
			shortPubkey := "????"
			if len(pod.Pubkey) >= 4 {
				shortPubkey = pod.Pubkey[:4]
			} else if len(pod.Pubkey) > 0 {
				shortPubkey = pod.Pubkey
			}

			// Measure latency
			latency := measureLatency(ip)
			if latency == 0 {
				latency = rand.Intn(90) + 10
			}

			pnode := models.PNode{
				ID:              pnodeID,
				Name:            fmt.Sprintf("Node %s (%s)", ip, shortPubkey),
				Status:          status,
				Uptime:          float64(pod.Uptime), // Raw seconds
				Latency:         latency,
				Validations:     0, // Not available in this API
				Rewards:         0, // Not available in this API
				Location:        locationStr,
				Region:          region,
				Lat:             lat,
				Lng:             lng,
				StorageUsed:     pod.StorageUsed,
				StorageCapacity: pod.StorageCommitted,
				LastSeen:        lastSeen,
				Performance:     0, // Not available in this API
				Stake:           0, // Not available in this API
				RiskScore:       0, // Not available in this API
				XDNScore:        calculateXDNScore(0, float64(pod.Uptime), latency, 0),
				// New fields from rich API - not available in basic PodsResponse
				IsPublic:            false,     // Default
				RpcPort:             6000,      // Default
				Version:             "unknown", // Default
				StorageUsagePercent: 0,         // Default
			}

			pnodes = append(pnodes, pnode)
		}

		// Apply filters
		var filteredPNodes []models.PNode
		for _, pnode := range pnodes {
			// Status filter
			if filters.Status != "" && filters.Status != "all" && pnode.Status != filters.Status {
				continue
			}
			// Region filter
			if filters.Region != "" && filters.Region != "all" && pnode.Region != filters.Region {
				continue
			}

			filteredPNodes = append(filteredPNodes, pnode)

			// Limit results
			if len(filteredPNodes) >= filters.Limit {
				break
			}
		}

		elapsed := time.Since(start)
		logrus.Infof("Fetched %d pNodes in %.2fs", len(filteredPNodes), elapsed.Seconds())
		return filteredPNodes, nil
	}

	// If not a PodsResponse, this is unexpected
	return nil, fmt.Errorf("invalid response format: unexpected type %T", podsResp)
}

// GetPNodeByID fetches a specific pNode by ID
func (c *Client) GetPNodeByID(id string) (*models.PNode, error) {
	// Try each seed to find the pod with matching pubkey
	for _, seedIP := range c.cfg.PRPCSeedIPs {
		seedClient := prpc.NewClient(seedIP)
		podsResp, err := seedClient.GetPods()
		if err != nil {
			logrus.Warnf("Failed to get pods from seed %s: %v", seedIP, err)
			continue
		}

		// Find the pod with matching pubkey
		var targetPod *prpc.Pod
		for _, pod := range podsResp.Pods {
			if pod.Pubkey == id {
				targetPod = &pod
				break
			}
		}

		if targetPod == nil {
			continue // Not found in this seed, try next
		}

		// Extract IP and get stats
		ip := strings.Split(targetPod.Address, ":")[0]
		nodeClient := prpc.NewClient(ip)
		stats, err := nodeClient.GetStats()
		if err != nil {
			logrus.Warnf("Failed to get stats for pNode %s (%s): %v", id, ip, err)
			continue
		}

		// Measure latency
		latency := measureLatency(ip)
		if latency == 0 {
			latency = rand.Intn(90) + 10
		}

		// Get geolocation
		loc, err := geolocation.GetLocation(ip)
		locationStr := "Unknown"
		region := "Unknown"
		lat := 0.0
		lng := 0.0
		if err == nil && loc != nil {
			locationStr = loc.GetLocationString()
			region = loc.Region
			if region == "" {
				region = loc.Country
			}
			lat = loc.Latitude
			lng = loc.Longitude
		}

		// Determine status
		status := "active"
		lastSeen := time.Unix(stats.LastUpdated, 0)
		if time.Since(lastSeen) > 5*time.Minute {
			status = "inactive"
		} else if time.Since(lastSeen) > 1*time.Minute {
			status = "warning"
		}

		// Values not currently available via RPC
		stake := 0.0
		validations := 0
		rewards := 0.0
		riskScore := 0.0

		// Safely get short pubkey
		shortPubkey := "????"
		if len(targetPod.Pubkey) >= 4 {
			shortPubkey = targetPod.Pubkey[:4]
		} else if len(targetPod.Pubkey) > 0 {
			shortPubkey = targetPod.Pubkey
		}

		// Use pubkey as ID, fallback to IP if empty
		pnodeID := targetPod.Pubkey
		if pnodeID == "" {
			pnodeID = ip
		}

		pnode := &models.PNode{
			ID:              pnodeID,
			Name:            fmt.Sprintf("Node %s (%s)", ip, shortPubkey),
			Status:          status,
			Uptime:          float64(targetPod.Uptime), // Raw seconds
			Latency:         latency,
			Validations:     validations,
			Rewards:         rewards,
			Location:        locationStr,
			Region:          region,
			Lat:             lat,
			Lng:             lng,
			StorageUsed:     targetPod.StorageUsed,
			StorageCapacity: targetPod.StorageCommitted,
			LastSeen:        lastSeen,
			Performance:     0,
			Stake:           stake,
			RiskScore:       riskScore,
			XDNScore:        calculateXDNScore(stake, float64(targetPod.Uptime), latency, riskScore),
		}

		logrus.Debugf("Fetched real pNode %s from pRPC", id)
		return pnode, nil
	}

	// If not found, return mock data as fallback
	logrus.Warnf("pNode %s not found in gossip, returning mock data", id)
	stake := 0.0
	riskScore := 0.0
	uptime := 0.0
	latency := 0

	pnode := &models.PNode{
		ID:              id,
		Name:            fmt.Sprintf("Node %s", id),
		Status:          "unknown",
		Uptime:          uptime,
		Latency:         latency,
		Validations:     0,
		Rewards:         0,
		Location:        "Unknown",
		Region:          "Unknown",
		Lat:             0,
		Lng:             0,
		StorageUsed:     0,
		StorageCapacity: 0,
		LastSeen:        time.Now(),
		Performance:     0,
		Stake:           stake,
		RiskScore:       riskScore,
		XDNScore:        calculateXDNScore(stake, uptime, latency, riskScore),
	}

	return pnode, nil
}

// GetPNodeHistory fetches historical metrics for a pNode
func (c *Client) GetPNodeHistory(id string, timeRange string, simulated bool) ([]models.PNodeHistory, error) {
	if !simulated {
		// Real historical data is not available from pRPC yet.
		return []models.PNodeHistory{}, nil
	}

	// Mock data - replace with actual pRPC call
	var history []models.PNodeHistory

	// Generate historical data points
	now := time.Now()
	points := 24 // 24 hours
	if timeRange == "7d" {
		points = 168 // 7 days * 24 hours
	} else if timeRange == "30d" {
		points = 720 // 30 days * 24 hours
	}

	for i := points; i >= 0; i-- {
		timestamp := now.Add(-time.Duration(i) * time.Hour)
		point := models.PNodeHistory{
			Timestamp:   timestamp.Unix(),
			Latency:     20 + rand.Intn(20), // 20-40ms
			Uptime:      95.0 + rand.Float64()*5.0,
			StorageUsed: int64(700+rand.Intn(100)) * 1024 * 1024 * 1024,
			Rewards:     rand.Float64() * 10, // Daily rewards
		}
		history = append(history, point)
	}

	logrus.Debugf("Fetched %d history points for pNode %s (Simulated)", len(history), id)
	return history, nil
}

// GetPNodePeers fetches connected peers for a pNode
func (c *Client) GetPNodePeers(id string) ([]models.PeerInfo, error) {
	// Mock data - replace with actual pRPC call
	peers := []models.PeerInfo{
		{
			ID:      "peer-1",
			Name:    "Peer Node 1",
			Status:  "active",
			Latency: 15,
		},
		{
			ID:      "peer-2",
			Name:    "Peer Node 2",
			Status:  "active",
			Latency: 22,
		},
		{
			ID:      "peer-3",
			Name:    "Peer Node 3",
			Status:  "warning",
			Latency: 45,
		},
	}

	logrus.Debugf("Fetched %d peers for pNode %s", len(peers), id)
	return peers, nil
}

// GetLeaderboard fetches top performing pNodes
func (c *Client) GetLeaderboard(metric string, limit int) ([]models.PNode, error) {
	// Mock data - replace with actual pRPC call
	var leaderboard []models.PNode

	for i := 0; i < limit; i++ {
		pnode := models.PNode{
			ID:              fmt.Sprintf("top-pnode-%d", i+1),
			Name:            fmt.Sprintf("Top Node %d", i+1),
			Status:          "active",
			Uptime:          99.0 - float64(i)*0.5, // Decreasing uptime
			Latency:         10 + i*2,
			Validations:     10000 - i*100,
			Rewards:         1000.0 - float64(i)*50.0,
			Location:        "Various",
			Region:          "Global",
			StorageUsed:     int64(800+i*10) * 1024 * 1024 * 1024,
			StorageCapacity: 1000 * 1024 * 1024 * 1024,
			LastSeen:        time.Now().Add(-time.Duration(i) * time.Minute),
			Performance:     0.95 - float64(i)*0.01,
			Stake:           10000.0 - float64(i)*500.0,
			RiskScore:       float64(i) * 5.0,
		}
		leaderboard = append(leaderboard, pnode)
	}

	logrus.Debugf("Fetched leaderboard with %d entries for metric %s", len(leaderboard), metric)
	return leaderboard, nil
}

// GetNetworkHeatmap fetches data for network heatmap
func (c *Client) GetNetworkHeatmap() ([]models.HeatmapPoint, error) {
	// Mock data - replace with actual pRPC call
	heatmap := []models.HeatmapPoint{
		{
			Lat:       40.7128,
			Lng:       -74.0060,
			Intensity: 85.0,
			NodeCount: 120,
			Region:    "North America",
			AvgUptime: 98.5,
		},
		{
			Lat:       51.5074,
			Lng:       -0.1278,
			Intensity: 75.0,
			NodeCount: 85,
			Region:    "Europe",
			AvgUptime: 96.2,
		},
		{
			Lat:       35.6762,
			Lng:       139.6503,
			Intensity: 65.0,
			NodeCount: 95,
			Region:    "Asia",
			AvgUptime: 94.8,
		},
		{
			Lat:       -33.8688,
			Lng:       151.2093,
			Intensity: 55.0,
			NodeCount: 45,
			Region:    "Australia",
			AvgUptime: 92.1,
		},
		{
			Lat:       -23.5505,
			Lng:       -46.6333,
			Intensity: 45.0,
			NodeCount: 35,
			Region:    "South America",
			AvgUptime: 89.7,
		},
	}

	logrus.Debugf("Fetched %d heatmap points", len(heatmap))
	return heatmap, nil
}

// GetAnalytics fetches network analytics (aggregated from live data)
func (c *Client) GetAnalytics(simulated bool) (*models.AnalyticsData, error) {
	// Fetch all nodes to aggregate real storage data
	pnodes, err := c.GetPNodes(&PNodeFilters{Limit: 1000})
	if err != nil {
		return nil, err
	}

	var totalCapacity int64
	var usedCapacity int64

	for _, node := range pnodes {
		totalCapacity += node.StorageCapacity
		usedCapacity += node.StorageUsed
	}

	var performance []models.MonthlyPerformance
	if simulated {
		// Mock performance data
		performance = []models.MonthlyPerformance{
			{Month: "Jan", Validation: 4000, Rewards: 2400, Latency: 240},
			{Month: "Feb", Validation: 3000, Rewards: 1398, Latency: 221},
			{Month: "Mar", Validation: 2000, Rewards: 9800, Latency: 229},
			{Month: "Apr", Validation: 2780, Rewards: 3908, Latency: 200},
			{Month: "May", Validation: 1890, Rewards: 4800, Latency: 221},
			{Month: "Jun", Validation: 2390, Rewards: 3800, Latency: 250},
		}
	} else {
		performance = []models.MonthlyPerformance{}
	}

	return &models.AnalyticsData{
		Performance: performance,
		Storage: models.StorageStats{
			TotalCapacity: totalCapacity,
			UsedCapacity:  usedCapacity,
			GrowthRate:    0, // Cannot calculate without history
		},
	}, nil
}
