package prpc

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
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

// HealthCheck performs a health check on the pRPC connection
func (c *Client) HealthCheck() error {
	// Mock health check - in real implementation, ping pRPC endpoint
	time.Sleep(10 * time.Millisecond) // Simulate network latency
	return nil
}

// GetDashboardStats fetches dashboard statistics
func (c *Client) GetDashboardStats() (*models.DashboardStats, error) {
	// Mock data - replace with actual pRPC calls
	stats := &models.DashboardStats{
		TotalNodes:     1250,
		ActiveNodes:    1180,
		NetworkHealth:  98.5,
		TotalRewards:   45678.90,
		AverageLatency: 45.2,
		ValidationRate: 99.1,
		Timestamp:      time.Now().Unix(),
	}

	logrus.Debug("Fetched dashboard stats from pRPC")
	return stats, nil
}

// GetPNodes fetches pNodes with optional filters
func (c *Client) GetPNodes(filters *PNodeFilters) ([]models.PNode, error) {
	var podsResp *prpc.PodsResponse
	var lastErr error

	// Try each seed IP until one works
	for _, seedIP := range c.cfg.PRPCSeedIPs {
		seedClient := prpc.NewClient(seedIP)
		resp, err := seedClient.GetPods()
		if err != nil {
			logrus.Warnf("Failed to get pods from seed %s: %v", seedIP, err)
			lastErr = err
			continue
		}
		if len(resp.Pods) == 0 {
			logrus.Warnf("Seed %s returned empty pods, trying next", seedIP)
			continue
		}
		podsResp = resp
		logrus.Infof("Successfully got %d pods from seed %s", len(resp.Pods), seedIP)
		break
	}

	if podsResp == nil {
		logrus.Errorf("All seed IPs failed, last error: %v", lastErr)
		return nil, fmt.Errorf("failed to get pods from any seed IP: %w", lastErr)
	}

	type pnodeResult struct {
		pnode *models.PNode
		err   error
	}

	results := make(chan pnodeResult, len(podsResp.Pods))
	var wg sync.WaitGroup

	// Limit concurrency to 10
	sem := make(chan struct{}, 10)

	for _, pod := range podsResp.Pods {
		wg.Add(1)
		go func(p prpc.Pod) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire
			defer func() { <-sem }() // Release

			// Extract IP from address (format: ip:port)
			ip := strings.Split(p.Address, ":")[0]

			// Skip if invalid
			if ip == "" {
				results <- pnodeResult{nil, nil}
				return
			}

			// Create client for this pNode
			nodeClient := prpc.NewClient(ip)
			stats, err := nodeClient.GetStats()
			if err != nil {
				logrus.Warnf("Failed to get stats for pNode %s: %v", ip, err)
				results <- pnodeResult{nil, nil}
				return
			}

			// Get geolocation
			loc, err := geolocation.GetLocation(ip)
			locationStr := "Unknown"
			region := "Unknown"
			if err == nil {
				locationStr = loc.GetLocationString()
				region = loc.Region
				if region == "" {
					region = loc.Country
				}
			}

			// Determine status based on last updated
			status := "active"
			lastSeen := time.Unix(stats.LastUpdated, 0)
			if time.Since(lastSeen) > 5*time.Minute {
				status = "inactive"
			} else if time.Since(lastSeen) > 1*time.Minute {
				status = "warning"
			}

			// Calculate uptime (assuming uptime is in seconds)
			uptime := float64(stats.Uptime) / (24 * 3600) * 100 // as percentage
			if uptime > 100 {
				uptime = 100
			}

			pnode := &models.PNode{
				ID:              p.Pubkey, // Use pubkey as ID
				Name:            fmt.Sprintf("Node %s", ip),
				Status:          status,
				Uptime:          uptime,
				Latency:         0, // Not available in stats
				Validations:     0, // Not available
				Rewards:         0, // Not available
				Location:        locationStr,
				Region:          region,
				StorageUsed:     int64(stats.TotalBytes),
				StorageCapacity: int64(stats.FileSize),
				LastSeen:        lastSeen,
				Performance:     0, // Calculate based on uptime or something
				Stake:           0, // Not available
				RiskScore:       0, // Not available
			}

			results <- pnodeResult{pnode, nil}
		}(pod)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var pnodes []models.PNode
	for res := range results {
		if res.pnode != nil {
			// Apply filters
			if filters.Status != "" && filters.Status != "all" && res.pnode.Status != filters.Status {
				continue
			}
			if filters.Region != "" && filters.Region != "all" && res.pnode.Region != filters.Region {
				continue
			}

			pnodes = append(pnodes, *res.pnode)

			// Limit results
			if len(pnodes) >= filters.Limit {
				break
			}
		}
	}

	logrus.Debugf("Fetched %d pNodes from pRPC", len(pnodes))
	return pnodes, nil
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

		// Get geolocation
		loc, err := geolocation.GetLocation(ip)
		locationStr := "Unknown"
		region := "Unknown"
		if err == nil {
			locationStr = loc.GetLocationString()
			region = loc.Region
			if region == "" {
				region = loc.Country
			}
		}

		// Determine status
		status := "active"
		lastSeen := time.Unix(stats.LastUpdated, 0)
		if time.Since(lastSeen) > 5*time.Minute {
			status = "inactive"
		} else if time.Since(lastSeen) > 1*time.Minute {
			status = "warning"
		}

		uptime := float64(stats.Uptime) / (24 * 3600) * 100
		if uptime > 100 {
			uptime = 100
		}

		pnode := &models.PNode{
			ID:              targetPod.Pubkey,
			Name:            fmt.Sprintf("Node %s", ip),
			Status:          status,
			Uptime:          uptime,
			Latency:         0,
			Validations:     0,
			Rewards:         0,
			Location:        locationStr,
			Region:          region,
			StorageUsed:     int64(stats.TotalBytes),
			StorageCapacity: int64(stats.FileSize),
			LastSeen:        lastSeen,
			Performance:     0,
			Stake:           0,
			RiskScore:       0,
		}

		logrus.Debugf("Fetched real pNode %s from pRPC", id)
		return pnode, nil
	}

	// If not found, return mock data as fallback
	logrus.Warnf("pNode %s not found in gossip, returning mock data", id)
	pnode := &models.PNode{
		ID:              id,
		Name:            fmt.Sprintf("Node %s", id),
		Status:          "unknown",
		Uptime:          0,
		Latency:         0,
		Validations:     0,
		Rewards:         0,
		Location:        "Unknown",
		Region:          "Unknown",
		StorageUsed:     0,
		StorageCapacity: 0,
		LastSeen:        time.Now(),
		Performance:     0,
		Stake:           0,
		RiskScore:       0,
	}

	return pnode, nil
}

// GetPNodeHistory fetches historical metrics for a pNode
func (c *Client) GetPNodeHistory(id string, timeRange string) ([]models.PNodeHistory, error) {
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

	logrus.Debugf("Fetched %d history points for pNode %s", len(history), id)
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
