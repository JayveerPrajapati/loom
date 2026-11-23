package host

import (
	"context"
	"fmt"
	"strings"

	"net/http"

	"loom/internal/flow"
)

func (l *Live) Slack(ctx context.Context, credID, channel, text string) error {
	var webhookURL string
	var token string

	if l.ResolveCredential != nil {
		if cred, ok := l.ResolveCredential(credID); ok {
			if u, ok := cred.Data["webhookUrl"].(string); ok && strings.TrimSpace(u) != "" {
				webhookURL = strings.TrimSpace(u)
			} else if t, ok := cred.Data["token"].(string); ok && strings.TrimSpace(t) != "" {
				token = strings.TrimSpace(t)
			} else if strings.HasPrefix(strings.TrimSpace(cred.Raw), "http") {
				webhookURL = strings.TrimSpace(cred.Raw)
			} else if strings.TrimSpace(cred.Raw) != "" {
				token = strings.TrimSpace(cred.Raw)
			}
		}
	}

	if webhookURL == "" && token == "" {
		if strings.HasPrefix(credID, "http") {
			webhookURL = credID
		} else if strings.HasPrefix(credID, "xox") {
			token = credID
		}
	}

	if webhookURL != "" {
		payload := map[string]any{"text": text}
		if channel != "" {
			payload["channel"] = channel
		}
		resp, err := l.HTTP(ctx, flow.HTTPRequest{
			Method:      http.MethodPost,
			URL:         webhookURL,
			Body:        payload,
			ContentType: "application/json",
		})
		if err != nil {
			return fmt.Errorf("slack webhook error: %w", err)
		}
		if resp.Status >= 400 {
			return fmt.Errorf("slack webhook error (status %d): %v", resp.Status, resp.Body)
		}
		return nil
	}

	if token != "" {
		payload := map[string]any{
			"channel": channel,
			"text":    text,
		}
		resp, err := l.HTTP(ctx, flow.HTTPRequest{
			Method: http.MethodPost,
			URL:    "https://slack.com/api/chat.postMessage",
			Headers: map[string]string{
				"Authorization": "Bearer " + token,
			},
			Body:        payload,
			ContentType: "application/json",
		})
		if err != nil {
			return fmt.Errorf("slack API error: %w", err)
		}
		if resp.Status >= 400 {
			return fmt.Errorf("slack API error (status %d): %v", resp.Status, resp.Body)
		}
		if ok, exists := resp.Body["ok"].(bool); exists && !ok {
			return fmt.Errorf("slack API rejected message: %v", resp.Body["error"])
		}
		return nil
	}

	return fmt.Errorf("slack: no webhook URL or bot token configured for credential %q", credID)
}
