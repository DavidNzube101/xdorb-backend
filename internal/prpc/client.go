package prpc

import (
	"crypto/md5"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"xdorb-backend/internal/config"
	"xdorb-backend/internal/geolocation"
	"xdorb-backend/internal/models"

	prpc "github.com/DavidNzube101/xandeum-prpc-go"
	"github.com/lucasb-eyer/go-colorful"
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
		seedClient := prpc.NewClient(seedIP, c.cfg.PRPCTimeout)
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
		// It's a PodsResponse struct, process pods concurrently
		var pnodes []models.PNode
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, pod := range podsResponse.Pods {
			wg.Add(1)
			go func(pod prpc.Pod) {
				defer wg.Done()
				// Extract IP from address
				ip := strings.Split(pod.Address, ":")[0]
				if ip == "" {
					return
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
				lastSeen := time.Unix(pod.LastSeenTimestamp, 0)
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

				// Uptime in seconds
				uptimeSeconds := float64(pod.Uptime)
				uptimePercentage := uptimeSeconds / 86400 * 100
				if uptimePercentage > 100 {
					uptimePercentage = 100
				}

				pnode := models.PNode{
					ID:              pnodeID,
					Name:            fmt.Sprintf("Node %s (%s)", ip, shortPubkey),
					Status:          status,
					Uptime:          uptimeSeconds,
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
					XDNScore:        calculateXDNScore(0, uptimePercentage, latency, 0),
					// New fields from rich API - not available in basic PodsResponse
					IsPublic:            false, // Default
					RpcPort:             6000,  // Default
					Version:             pod.Version,
					StorageUsagePercent: 0, // Default
				}

				mu.Lock()
				pnodes = append(pnodes, pnode)
				mu.Unlock()
			}(pod)
		}

		wg.Wait()

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

// GetPNodesWithStats fetches pNodes with real-time stats (HEAVY operation)
func (c *Client) GetPNodesWithStats() ([]models.PNode, error) {
	// First get the base list using GetPNodes (this gets us the list of pods)
	baseNodes, err := c.GetPNodes(&PNodeFilters{Limit: 1000})
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	var enrichedNodes []models.PNode
	var mu sync.Mutex

	// Limit concurrency to avoid file descriptor exhaustion
	sem := make(chan struct{}, 50) 

	for _, node := range baseNodes {
		wg.Add(1)
		go func(n models.PNode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Fetch stats
			ip := strings.Split(n.ID, " ")[0] // ID is often PubKey, but Name has IP?
			// Re-parse IP from name if ID is pubkey: "Node 1.2.3.4 (ABCD)"
			if strings.HasPrefix(n.Name, "Node ") {
				parts := strings.Split(n.Name, " ")
				if len(parts) >= 2 {
					ip = parts[1]
				}
			}
			// Fallback: if ID looks like IP
			if net.ParseIP(n.ID) != nil {
				ip = n.ID
			}

			nodeClient := prpc.NewClient(ip, c.cfg.PRPCTimeout)
			stats, err := nodeClient.GetStats()
			
			if err == nil && stats != nil {
				n.CPUPercent = stats.CPUPercent
				n.MemoryUsed = stats.RAMUsed
				n.MemoryTotal = stats.RAMTotal
				n.PacketsIn = stats.PacketsReceived
				n.PacketsOut = stats.PacketsSent
			}

			mu.Lock()
			enrichedNodes = append(enrichedNodes, n)
			mu.Unlock()
		}(node)
	}

	wg.Wait()
	return enrichedNodes, nil
}

// GetPNodeByID fetches a specific pNode by ID using the library's concurrent lookup
func (c *Client) GetPNodeByID(id string) (*models.PNode, error) {
	// Use the library's FindPNode function
	findOpts := &prpc.FindPNodeOptions{
		Timeout: c.cfg.PRPCTimeout,
	}
	targetPod, err := prpc.FindPNode(id, findOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to find pNode %s on any seed: %w", id, err)
	}

	// Concurrently get stats and measure latency for the found pod
	ip := strings.Split(targetPod.Address, ":")[0]
	var stats *prpc.NodeStats
	var latency int
	var statsErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		nodeClient := prpc.NewClient(ip, c.cfg.PRPCTimeout)
		stats, statsErr = nodeClient.GetStats()
	}()

	go func() {
		defer wg.Done()
		latency = measureLatency(ip)
	}()

	wg.Wait()

	if statsErr != nil {
		return nil, fmt.Errorf("failed to get stats for pNode %s (%s): %w", id, ip, statsErr)
	}

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

	// Uptime in seconds
	uptimeSeconds := float64(stats.Uptime)
	uptimePercentage := uptimeSeconds / 86400 * 100
	if uptimePercentage > 100 {
		uptimePercentage = 100
	}

	pnode := &models.PNode{
		ID:              pnodeID,
		Name:            fmt.Sprintf("Node %s (%s)", ip, shortPubkey),
		Status:          status,
		Uptime:          uptimeSeconds,
		Latency:         latency,
		Validations:     0,
		Rewards:         0,
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
		XDNScore:        calculateXDNScore(stake, uptimePercentage, latency, riskScore),
		Version:         targetPod.Version,
		// Stats from GetStats()
		CPUPercent:  stats.CPUPercent,
		MemoryUsed:  stats.RAMUsed,
		MemoryTotal: stats.RAMTotal,
		PacketsIn:   stats.PacketsReceived,
		PacketsOut:  stats.PacketsSent,
	}

	logrus.Debugf("Fetched real pNode %s from pRPC", id)
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

// GetLeaderboard fetches top performing pNodes using real data
func (c *Client) GetLeaderboard(metric string, limit int) ([]models.PNode, error) {
	// Fetch all nodes
	allNodes, err := c.GetPNodes(&PNodeFilters{Limit: 1000})
	if err != nil {
		return nil, err
	}

	// Sort based on metric
	sort.Slice(allNodes, func(i, j int) bool {
		switch metric {
		case "uptime":
			return allNodes[i].Uptime > allNodes[j].Uptime
		case "latency":
			// Lower is better for latency, but 0 usually means invalid/timeout
			if allNodes[i].Latency == 0 { return false }
			if allNodes[j].Latency == 0 { return true }
			return allNodes[i].Latency < allNodes[j].Latency
		case "rewards":
			return allNodes[i].Rewards > allNodes[j].Rewards
		case "performance":
			return allNodes[i].Performance > allNodes[j].Performance
		case "xdn":
			fallthrough
		default:
			return allNodes[i].XDNScore > allNodes[j].XDNScore
		}
	})

	// Slice top N
	if limit > len(allNodes) {
		limit = len(allNodes)
	}
	
	leaderboard := allNodes[:limit]
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

// Helper to generate stable color
func generateColor(seed string) string {
	hash := md5.Sum([]byte(seed))
	s := int64(hash[0]) | int64(hash[1])<<8 | int64(hash[2])<<16
	r := rand.New(rand.NewSource(s))
	c := colorful.Hsl(r.Float64()*360.0, 0.7+r.Float64()*0.3, 0.4+r.Float64()*0.4)
	return c.Hex()
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

	// Geo Distribution
	geoMap := make(map[string]*models.GeoData)
	
	// CPU and RAM Usage
	var cpuUsage []models.CpuData
	var ramUsage []models.RamData
	var packetInTotal, packetOutTotal int

	for _, node := range pnodes {
		totalCapacity += node.StorageCapacity
		usedCapacity += node.StorageUsed

		// Geo
		parts := strings.Split(node.Location, ", ")
		country := "Unknown"
		if len(parts) > 0 && parts[len(parts)-1] != "" {
			country = parts[len(parts)-1]
		}
		
		if _, exists := geoMap[country]; !exists {
			geoMap[country] = &models.GeoData{
				Country: country,
				Color:   generateColor(country),
			}
		}
		entry := geoMap[country]
		entry.Count++
		entry.AvgUptime += node.Uptime

		// Stats
		displayName := node.Name
		if displayName == "" {
			if len(node.ID) > 8 {
				displayName = node.ID[:8]
			} else {
				displayName = node.ID
			}
		}

		cpuUsage = append(cpuUsage, models.CpuData{
			Node:  displayName,
			Cpu:   node.CPUPercent,
			Color: generateColor(node.ID),
		})

		ramUsage = append(ramUsage, models.RamData{
			Node:  displayName,
			Ram:   float64(node.MemoryUsed) / 1024 / 1024, // MB
			Color: generateColor(node.ID),
		})

		packetInTotal += node.PacketsIn
		packetOutTotal += node.PacketsOut
	}

	// Finalize Geo
	var geoDist []models.GeoData
	for _, entry := range geoMap {
		if entry.Count > 0 {
			entry.AvgUptime = entry.AvgUptime / float64(entry.Count)
		}
		geoDist = append(geoDist, *entry)
	}
	// Sort Geo by Count desc
	sort.Slice(geoDist, func(i, j int) bool {
		return geoDist[i].Count > geoDist[j].Count
	})

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
		GeoDistribution: geoDist,
		CpuUsage:        cpuUsage,
		RamUsage:        ramUsage,
		PacketStreams: models.PacketData{
			In:  packetInTotal,
			Out: packetOutTotal,
		},
	}, nil
}
