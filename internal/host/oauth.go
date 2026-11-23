package host

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"encoding/json"
	"net/http"
	"net/url"

	"loom/internal/flow"
)

type cachedToken struct {
	token   string
	expires time.Time
}

// oauth2Token returns a cached client-credentials token, refreshing when it
// is within a minute of expiry.
func (l *Live) oauth2Token(ctx context.Context, params *flow.OAuth2Params) (string, error) {
	if params.TokenURL == "" || params.ClientID == "" {
		return "", fmt.Errorf("oauth2: tokenUrl and clientId are required")
	}
	key := params.TokenURL + "\x00" + params.ClientID + "\x00" + params.Scope
	l.tokenMu.Lock()
	if cached, ok := l.tokenCache[key]; ok && time.Until(cached.expires) > time.Minute {
		l.tokenMu.Unlock()
		return cached.token, nil
	}
	l.tokenMu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", params.ClientID)
	if params.ClientSecret != "" {
		form.Set("client_secret", params.ClientSecret)
	}
	if params.Scope != "" {
		form.Set("scope", params.Scope)
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, params.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	tokenReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	resp, err := l.Client.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("oauth2: token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("oauth2: token endpoint returned %d", resp.StatusCode)
	}
	var payload struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
		Error       string  `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("oauth2: decode token response: %w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("oauth2: %s", payload.Error)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("oauth2: token response has no access_token")
	}
	expires := time.Now().Add(time.Duration(payload.ExpiresIn * float64(time.Second)))
	if payload.ExpiresIn <= 0 {
		expires = time.Now().Add(55 * time.Minute)
	}
	l.tokenMu.Lock()
	l.tokenCache[key] = cachedToken{token: payload.AccessToken, expires: expires}
	l.tokenMu.Unlock()
	return payload.AccessToken, nil
}
