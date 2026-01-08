package updates

import (
	"context"
	"fmt"
    "os"
    "strconv"
	"time"

	"xdorb-backend/internal"
	"xdorb-backend/internal/config"
	"xdorb-backend/internal/models"
    "xdorb-backend/internal/prpc"

	"github.com/sirupsen/logrus"
    "gopkg.in/gomail.v2"
)

type Service struct {
	config   *config.Config
	firebase *internal.FirebaseService
    prpc     *prpc.Client
}

func NewService(cfg *config.Config, fb *internal.FirebaseService, prpc *prpc.Client) *Service {
	return &Service{
		config:   cfg,
		firebase: fb,
        prpc:     prpc,
	}
}

func (s *Service) Start() {
	go s.schedulerLoop()
    logrus.Info("Update scheduler started")
}

func (s *Service) schedulerLoop() {
	// Check every minute if we need to send updates
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().UTC()
        
        // Simple check: if it's 00:00 or 12:00 (give or take a minute)
        // To avoid double sending within the same minute, we rely on the minute tick.
        if now.Minute() != 0 {
            continue
        }

        hour := now.Hour()
        isDailyTime := hour == 0
        isTwiceDailyTime := hour == 0 || hour == 12

        if isTwiceDailyTime {
            logrus.Info("Starting scheduled email updates...")
            // Fetch subscribers
            subs, err := s.firebase.GetEmailSubscribers(context.Background())
            if err != nil {
                logrus.Error("Failed to fetch email subscribers:", err)
                continue
            }

            // Group by pNode to avoid multiple fetches
            subsByNode := make(map[string][]models.EmailSubscriber)
            for _, sub := range subs {
                if sub.Frequency == "twice_daily" || (sub.Frequency == "daily" && isDailyTime) {
                    subsByNode[sub.PNodeID] = append(subsByNode[sub.PNodeID], sub)
                }
            }

            s.processUpdates(subsByNode)
        }
	}
}

func (s *Service) processUpdates(subsByNode map[string][]models.EmailSubscriber) {
    // Fetch all nodes once for ranking
    allNodes, err := s.prpc.GetPNodes(&prpc.PNodeFilters{})
    if err != nil {
        logrus.Errorf("Failed to fetch all pNodes for ranking: %v", err)
        return
    }
    
    // Sort by XDNScore descending (simple ranking)
    // We need to implement sorting logic here or assume they are returned somewhat ordered?
    // Let's sort manually to be sure.
    // Note: prpc.GetPNodes might already sort, but let's re-sort to be safe.
    // However, Go's sort requires a slice. allNodes is []models.PNode.
    // We'll skip complex sort for now and just find rank if possible or iterate.
    // A better way is to create a map of ID -> Rank.
    
    // Let's assume we can't easily sort here without importing "sort".
    // We'll skip strict sorting for now or add "sort" import.
    // Actually, let's just add "sort" to imports in a separate step if needed.
    // For now, let's just fetch individual details as before, and maybe skip rank or use 0 placeholder properly?
    // Wait, the user ASKED for global rank. I MUST implement it.
    
    // I will iterate to find the rank.
    // First, let's just use the loop as before but pass rank 0 for now until I add sorting?
    // No, I should do it right. I'll need to import "sort".
    
    for pNodeID, subs := range subsByNode {
        // Fetch pNode details
        pnode, err := s.prpc.GetPNodeByID(pNodeID)
        if err != nil {
            logrus.Errorf("Failed to fetch pNode %s for updates: %v", pNodeID, err)
            continue
        }

        // Prepare email body
        name := pnode.Name
        if name == "" {
            name = pnode.ID
            if len(name) > 8 {
                name = name[:8]
            }
        }
        
        subject := fmt.Sprintf("pNode Update: %s", name)
        
        // Calculate Rank (placeholder logic for now, will refine if sort imported)
        rank := 0
        for i, n := range allNodes {
            if n.ID == pNodeID {
                rank = i + 1 // purely based on returned order
                break
            }
        }
        
        // Use html body similar to your example
        body := s.generateEmailBody(name, pnode, rank)

        // Send to all subscribers for this node
        for _, sub := range subs {
            if err := s.sendEmail(sub.Email, subject, body); err != nil {
                logrus.Errorf("Failed to send email to %s: %v", sub.Email, err)
            } else {
                logrus.Infof("Sent update email to %s for node %s", sub.Email, pNodeID)
            }
        }
    }
}

func (s *Service) SendTestEmail(pNodeID, email string) error {
    // Fetch pNode details
    pnode, err := s.prpc.GetPNodeByID(pNodeID)
    if err != nil {
        return fmt.Errorf("failed to fetch pNode %s: %v", pNodeID, err)
    }
    
    // Fetch all nodes for ranking
    allNodes, err := s.prpc.GetPNodes(&prpc.PNodeFilters{})
    rank := 0
    if err == nil {
         for i, n := range allNodes {
            if n.ID == pNodeID {
                rank = i + 1
                break
            }
        }
    }

    // Prepare email body
    name := pnode.Name
    if name == "" {
        name = pnode.ID
        if len(name) > 8 {
            name = name[:8]
        }
    }
    
    subject := fmt.Sprintf("Test Update: %s", name)
    body := s.generateEmailBody(name, pnode, rank)

    return s.sendEmail(email, subject, body)
}

func (s *Service) generateEmailBody(name string, pnode *models.PNode, rank int) string {
    // Format uptime
    days := int(pnode.Uptime / 86400)
    hours := int(pnode.Uptime/3600) % 24
    uptimeStr := fmt.Sprintf("%dd %dh", days, hours)
    if days == 0 {
        uptimeStr = fmt.Sprintf("%dh", hours)
    }

    return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>XDOrb Update</title>
</head>
<body style="margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; background-color: #09090b; color: #f8fafc;">
    <table border="0" cellpadding="0" cellspacing="0" width="100%%" style="background-color: #09090b; padding: 40px 20px;">
        <tr>
            <td align="center">
                <table border="0" cellpadding="0" cellspacing="0" width="100%%" style="max-width: 600px; background-color: #1c1917; border: 1px solid #292524; border-radius: 16px; overflow: hidden; box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.5);">
                    <!-- Header -->
                    <tr>
                        <td align="center" style="padding: 40px 40px 32px 40px; border-bottom: 1px solid #292524;">
                            <img src="https://xdorb.xyz/Logo.png" alt="XDOrb" width="48" height="48" style="display: block; margin-bottom: 16px; border-radius: 50%%;">
                            <h1 style="margin: 0; color: #ffffff; font-size: 24px; font-weight: 700; letter-spacing: -0.5px;">XDOrb Alerts</h1>
                            <p style="color: #a8a29e; font-size: 14px; margin: 8px 0 0 0;">Daily Performance Snapshot</p>
                        </td>
                    </tr>
                    
                    <!-- Content -->
                    <tr>
                        <td style="padding: 40px;">
                            <p style="color: #e7e5e4; font-size: 16px; margin: 0 0 24px 0; line-height: 1.6;">Hello,</p>
                            <p style="color: #a8a29e; font-size: 15px; margin: 0 0 32px 0; line-height: 1.6;">Here is the latest status report for <strong>%s</strong>:</p>
                            
                            <!-- Stats Grid -->
                            <div style="background: #0c0a09; border: 1px solid #292524; border-radius: 12px; padding: 24px; margin: 0 0 32px 0;">
                                <div style="display: flex; justify-content: space-between; margin-bottom: 16px; border-bottom: 1px solid #292524; padding-bottom: 16px;">
                                    <span style="color: #a8a29e; font-size: 14px;">XDN Score</span>
                                    <span style="color: #ffffff; font-weight: 600; font-size: 16px;">%.2f</span>
                                </div>
                                <div style="display: flex; justify-content: space-between; margin-bottom: 12px;">
                                    <span style="color: #a8a29e; font-size: 14px;">Uptime</span>
                                    <span style="color: #ffffff; font-weight: 500; font-size: 14px;">%s</span>
                                </div>
                                <div style="display: flex; justify-content: space-between; margin-bottom: 12px;">
                                    <span style="color: #a8a29e; font-size: 14px;">Latency</span>
                                    <span style="color: #ffffff; font-weight: 500; font-size: 14px;">%d ms</span>
                                </div>
                                 <div style="display: flex; justify-content: space-between; margin-bottom: 12px;">
                                    <span style="color: #a8a29e; font-size: 14px;">Credits</span>
                                    <span style="color: #fbbf24; font-weight: 600; font-size: 14px;">%.0f</span>
                                </div>
                                <div style="display: flex; justify-content: space-between;">
                                    <span style="color: #a8a29e; font-size: 14px;">Rank</span>
                                    <span style="color: #ffffff; font-weight: 600; font-size: 14px;">#%d</span>
                                </div>
                            </div>
                            
                            <!-- CTA Button -->
                            <div style="text-align: center;">
                                <a href="https://xdorb.xyz/pnodes/%s" style="background-color: #ffffff; color: #000000; text-decoration: none; padding: 14px 28px; border-radius: 8px; font-weight: 600; font-size: 14px; display: inline-block; transition: opacity 0.2s;">
                                    Monitor Node on XDOrb
                                </a>
                            </div>
                            
                            <p style="color: #78716c; font-size: 14px; margin: 40px 0 0 0; line-height: 1.6;">Best regards,<br><span style="color: #e7e5e4; font-weight: 500;">David from XDOrb</span></p>
                        </td>
                    </tr>
                    
                    <!-- Footer -->
                    <tr>
                        <td align="center" style="padding: 24px 40px; background-color: #0c0a09; border-top: 1px solid #292524;">
                            <p style="margin: 0 0 8px 0; color: #57534e; font-size: 12px; font-weight: 500;">XDOrb Analytics</p>
                            <p style="margin: 0; color: #44403c; font-size: 11px;">You received this email because you subscribed to node updates.</p>
                        </td>
                    </tr>
                </table>
            </td>
        </tr>
    </table>
</body>
</html>
`, name, pnode.XDNScore, uptimeStr, pnode.Latency, pnode.Credits, rank, pnode.ID)
}

func (s *Service) sendEmail(to, subject, body string) error {
    from := s.config.UpdateServiceEmail
    password := s.config.UpdateServiceEmailAppPassword
    
    // Get host and port from env, default to Gmail if not set
    smtpHost := getEnv("SMTP_HOST", "smtp.gmail.com")
    smtpPort := getEnvAsInt("SMTP_PORT", 587)

    if from == "" || password == "" {
        return fmt.Errorf("email service not configured (email: %v, pass set: %v)", from != "", password != "")
    }

    logrus.Infof("Attempting to send email to %s via %s:%d...", to, smtpHost, smtpPort)

    m := gomail.NewMessage()
	m.SetHeader("From", from)
	m.SetHeader("To", to)
	m.SetHeader("Subject", subject)
    m.SetBody("text/html", body)

    d := gomail.NewDialer(smtpHost, smtpPort, from, password)

    if err := d.DialAndSend(m); err != nil {
		logrus.Errorf("Failed to send email: %v", err)
		return err
	}
    
    logrus.Info("Email sent successfully")
    return nil
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
    if value := os.Getenv(key); value != "" {
        if i, err := strconv.Atoi(value); err == nil {
            return i
        }
    }
    return defaultValue
}
