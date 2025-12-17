package internal

import (
	"context"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"google.golang.org/api/option"

	"xdorb-backend/internal/config"
	"xdorb-backend/internal/models"
)

type FirebaseService struct {
	client *firestore.Client
}

func NewFirebaseService(cfg *config.Config) (*FirebaseService, error) {
	if cfg.FirebaseProjectID == "" {
		log.Println("Firebase not configured, skipping initialization")
		return &FirebaseService{}, nil
	}

	ctx := context.Background()
	conf := &firebase.Config{ProjectID: cfg.FirebaseProjectID}

	// Create credentials from env vars
	creds := option.WithCredentialsJSON([]byte(`{
		"type": "service_account",
		"project_id": "` + cfg.FirebaseProjectID + `",
		"private_key": "` + cfg.FirebasePrivateKey + `",
		"client_email": "` + cfg.FirebaseClientEmail + `",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth",
		"token_uri": "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs"
	}`))

	app, err := firebase.NewApp(ctx, conf, creds)
	if err != nil {
		return nil, err
	}

	client, err := app.Firestore(ctx)
	if err != nil {
		return nil, err
	}

	return &FirebaseService{client: client}, nil
}

func (fs *FirebaseService) SavePNode(ctx context.Context, pnode *models.PNode) error {
	if fs.client == nil {
		return nil // Firebase not configured
	}

	// Validate pnode ID - cannot be empty or contain invalid characters for Firestore
	if pnode.ID == "" {
		log.Println("Skipping Firebase save: pNode ID is empty")
		return nil
	}

	// Firestore document IDs cannot contain certain characters or be empty
	// Also cannot have trailing slashes
	if strings.Contains(pnode.ID, "/") || strings.TrimSpace(pnode.ID) != pnode.ID {
		log.Printf("Skipping Firebase save: pNode ID '%s' contains invalid characters", pnode.ID)
		return nil
	}

	docRef := fs.client.Collection("pnodes").Doc(pnode.ID)
	_, err := docRef.Set(ctx, map[string]interface{}{
		"id":              pnode.ID,
		"name":            pnode.Name,
		"status":          pnode.Status,
		"uptime":          pnode.Uptime,
		"latency":         pnode.Latency,
		"validations":     pnode.Validations,
		"rewards":         pnode.Rewards,
		"location":        pnode.Location,
		"region":          pnode.Region,
		"lat":             pnode.Lat,
		"lng":             pnode.Lng,
		"storageUsed":     pnode.StorageUsed,
		"storageCapacity": pnode.StorageCapacity,
		"lastSeen":        pnode.LastSeen.Unix(),
		"performance":     pnode.Performance,
		"stake":           pnode.Stake,
		"riskScore":       pnode.RiskScore,
		"xdnScore":        pnode.XDNScore,
		// New fields
		"isPublic":            pnode.IsPublic,
		"rpcPort":             pnode.RpcPort,
		"version":             pnode.Version,
		"storageUsagePercent": pnode.StorageUsagePercent,
		"updatedAt":           time.Now().Unix(),
	})
	return err
}

func (fs *FirebaseService) SavePNodesBatch(ctx context.Context, pnodes []models.PNode) error {
	if fs.client == nil || len(pnodes) == 0 {
		return nil
	}

	// Firestore batch limit is 500 operations
	const batchSize = 400 
	
	for i := 0; i < len(pnodes); i += batchSize {
		end := i + batchSize
		if end > len(pnodes) {
			end = len(pnodes)
		}

		batch := fs.client.Batch()
		batchCount := 0

		for _, pnode := range pnodes[i:end] {
			// Validation
			if pnode.ID == "" || strings.Contains(pnode.ID, "/") || strings.TrimSpace(pnode.ID) != pnode.ID {
				continue
			}

			docRef := fs.client.Collection("pnodes").Doc(pnode.ID)
			batch.Set(docRef, map[string]interface{}{
				"id":              pnode.ID,
				"name":            pnode.Name,
				"status":          pnode.Status,
				"uptime":          pnode.Uptime,
				"latency":         pnode.Latency,
				"validations":     pnode.Validations,
				"rewards":         pnode.Rewards,
				"location":        pnode.Location,
				"region":          pnode.Region,
				"lat":             pnode.Lat,
				"lng":             pnode.Lng,
				"storageUsed":     pnode.StorageUsed,
				"storageCapacity": pnode.StorageCapacity,
				"lastSeen":        pnode.LastSeen.Unix(),
				"performance":     pnode.Performance,
				"stake":           pnode.Stake,
				"riskScore":       pnode.RiskScore,
				"xdnScore":        pnode.XDNScore,
				// New fields
				"isPublic":            pnode.IsPublic,
				"rpcPort":             pnode.RpcPort,
				"version":             pnode.Version,
				"storageUsagePercent": pnode.StorageUsagePercent,
				"updatedAt":           time.Now().Unix(),
			})
			batchCount++
		}

		if batchCount > 0 {
			if _, err := batch.Commit(ctx); err != nil {
				log.Printf("Failed to commit batch %d: %v", i/batchSize, err)
				// Continue to next batch instead of failing everything
			}
		}
	}
	return nil
}

func (fs *FirebaseService) GetAllPNodes(ctx context.Context) ([]models.PNode, error) {
	if fs.client == nil {
		return []models.PNode{}, nil
	}

	docs, err := fs.client.Collection("pnodes").Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}

	var pnodes []models.PNode
	for _, doc := range docs {
		data := doc.Data()
		pnode := models.PNode{
			ID:              getString(data, "id"),
			Name:            getString(data, "name"),
			Status:          getString(data, "status"),
			Uptime:          getFloat64(data, "uptime"),
			Latency:         int(getFloat64(data, "latency")),
			Validations:     int(getFloat64(data, "validations")),
			Rewards:         getFloat64(data, "rewards"),
			Location:        getString(data, "location"),
			Region:          getString(data, "region"),
			Lat:             getFloat64(data, "lat"),
			Lng:             getFloat64(data, "lng"),
			StorageUsed:     int64(getFloat64(data, "storageUsed")),
			StorageCapacity: int64(getFloat64(data, "storageCapacity")),
			LastSeen:        time.Unix(int64(getFloat64(data, "lastSeen")), 0),
			Performance:     getFloat64(data, "performance"),
			Stake:           getFloat64(data, "stake"),
			RiskScore:       getFloat64(data, "riskScore"),
			XDNScore:        getFloat64(data, "xdnScore"),
			// New fields
			IsPublic:            getBool(data, "isPublic"),
			RpcPort:             int(getFloat64(data, "rpcPort")),
			Version:             getString(data, "version"),
			StorageUsagePercent: getFloat64(data, "storageUsagePercent"),
		}
		pnodes = append(pnodes, pnode)
	}
	return pnodes, nil
}

func (fs *FirebaseService) PruneOldNodes(ctx context.Context, maxAge time.Duration) error {
	if fs.client == nil {
		return nil
	}

	cutoff := time.Now().Add(-maxAge).Unix()
	query := fs.client.Collection("pnodes").Where("lastSeen", "<", cutoff)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return err
	}

	// If no documents to delete, return early
	if len(docs) == 0 {
		log.Println("No old pNodes to prune")
		return nil
	}

	batch := fs.client.Batch()
	for _, doc := range docs {
		batch.Delete(doc.Ref)
	}

	_, err = batch.Commit(ctx)
	return err
}

func (fs *FirebaseService) SaveNetworkSnapshot(ctx context.Context, snapshot *models.AnalyticsData) error {
	if fs.client == nil {
		return nil
	}

	_, err := fs.client.Collection("network_snapshots").Doc("latest").Set(ctx, snapshot)
	return err
}

func (fs *FirebaseService) GetLatestNetworkSnapshot(ctx context.Context) (*models.AnalyticsData, error) {
	if fs.client == nil {
		return nil, nil
	}

	doc, err := fs.client.Collection("network_snapshots").Doc("latest").Get(ctx)
	if err != nil {
		return nil, err
	}

	var snapshot models.AnalyticsData
	if err := doc.DataTo(&snapshot); err != nil {
		return nil, err
	}

	return &snapshot, nil
}

func (fs *FirebaseService) Close() error {
	if fs.client != nil {
		return fs.client.Close()
	}
	return nil
}

func getString(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getFloat64(data map[string]interface{}, key string) float64 {
	if val, ok := data[key]; ok {
		if num, ok := val.(float64); ok {
			return num
		}
	}
	return 0
}

func getBool(data map[string]interface{}, key string) bool {
	if val, ok := data[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}
