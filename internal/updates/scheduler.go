package updates

import (
	"context"
	"fmt"
	"net/smtp"
	"time"

	"xdorb-backend/internal"
	"xdorb-backend/internal/config"
	"xdorb-backend/internal/models"
    "xdorb-backend/internal/prpc"

	"github.com/sirupsen/logrus"
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

        body := fmt.Sprintf(`
Hello,

Here is the latest update for pNode %s:

Status: %s
Uptime: %.2f%%
Latency: %d ms
Rewards: %.4f
Performance: %.2f%%

View more details on the dashboard: https://xdorb.xyz/pnodes/%s

Best regards,
Xandeum Network Monitor
`,
 name, pnode.Status, pnode.Uptime, pnode.Latency, pnode.Rewards, pnode.Performance, pnode.ID)

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

    // Prepare email body
    name := pnode.Name
    if name == "" {
        name = pnode.ID
        if len(name) > 8 {
            name = name[:8]
        }
    }
    
    subject := fmt.Sprintf("Test Update: %s", name)

    body := fmt.Sprintf(`
Hello,

This is a TEST update for pNode %s triggered by the developer console.

Status: %s
Uptime: %.2f%%
Latency: %d ms
Rewards: %.4f
Performance: %.2f%%

View more details on the dashboard: https://xdorb.xyz/pnodes/%s

Best regards,
Xandeum Network Monitor
`, name, pnode.Status, pnode.Uptime, pnode.Latency, pnode.Rewards, pnode.Performance, pnode.ID)

    return s.sendEmail(email, subject, body)
}

func (s *Service) sendEmail(to, subject, body string) error {
    from := s.config.UpdateServiceEmail
    password := s.config.UpdateServiceEmailAppPassword
    
    if from == "" || password == "" {
        return fmt.Errorf("email service not configured")
    }

    msg := "From: " + from + "\n" +
        "To: " + to + "\n" +
        "Subject: " + subject + "\n\n" +
        body

    // Assuming Gmail for now as per "APP PASSWORD" hint
    return smtp.SendMail("smtp.gmail.com:587",
        smtp.PlainAuth("", from, password, "smtp.gmail.com"),
        from, []string{to}, []byte(msg))
}
