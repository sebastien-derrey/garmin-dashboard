package garmin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// OAuth consumer credentials — Garmin's own Android app key, publicly available via:
// https://thegarth.s3.amazonaws.com/oauth_consumer.json
const (
	oauthConsumerKey    = "fc3e99d2-118c-44b8-8ae3-03370dde24c0"
	oauthConsumerSecret = "E08WAR897WEy2knn7aFBrvegVAf0AFdWBBF"

	clientID   = "GCM_ANDROID_DARK"
	serviceURL = "https://mobile.integration.garmin.com/gcm/android"

	// User agents — must match what garth expects for each stage
	ssoUserAgent   = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
	oauthUserAgent = "com.garmin.android.apps.connectmobile"
	apiUserAgent   = "GCM-iOS-5.22.1.4"
)

// oauth1Token holds the OAuth1 token (needed to refresh OAuth2)
type oauth1Token struct {
	OAuthToken       string `json:"oauth_token"`
	OAuthTokenSecret string `json:"oauth_token_secret"`
}

// oauth2Token holds the OAuth2 token used for API calls
type oauth2Token struct {
	AccessToken            string `json:"access_token"`
	RefreshToken           string `json:"refresh_token"`
	ExpiresIn              int    `json:"expires_in"`
	ExpiresAt              int64  `json:"expires_at"`
	RefreshTokenExpiresIn  int    `json:"refresh_token_expires_in"`
	RefreshTokenExpiresAt  int64  `json:"refresh_token_expires_at"`
}

func (t *oauth2Token) expired() bool {
	return time.Now().Unix() >= t.ExpiresAt
}

// savedTokens is what we write to disk
type savedTokens struct {
	OAuth1 oauth1Token `json:"oauth1"`
	OAuth2 oauth2Token `json:"oauth2"`
}

// Client holds state for making authenticated Garmin API calls
type Client struct {
	jar         *cookiejar.Jar
	httpClient  *http.Client
	oauth1      oauth1Token
	oauth2      oauth2Token
	displayName string
	LoggedIn    bool
}

// NewClient creates a fresh client
func NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		jar: jar,
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
	}
}

// Login authenticates using the current Garmin SSO JSON API + OAuth1→OAuth2 exchange
func (c *Client) Login(email, password string) error {
	loginParams := url.Values{
		"clientId": {clientID},
		"locale":   {"en-US"},
		"service":  {serviceURL},
	}

	// ── Step 1: GET sign-in page to set SSO session cookies ─────────────
	signInURL := "https://sso.garmin.com/mobile/sso/en/sign-in?" + url.Values{"clientId": {clientID}}.Encode()
	req, _ := http.NewRequest("GET", signInURL, nil)
	c.setSSOHeaders(req, "")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Site", "none")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("getting sign-in page: %w", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("Sign-in page: HTTP %d", resp.StatusCode)

	// ── Step 2: POST JSON credentials ───────────────────────────────────
	creds := map[string]interface{}{
		"username":     email,
		"password":     password,
		"rememberMe":   false,
		"captchaToken": "",
	}
	body, _ := json.Marshal(creds)

	req, _ = http.NewRequest("POST",
		"https://sso.garmin.com/mobile/api/login?"+loginParams.Encode(),
		strings.NewReader(string(body)),
	)
	c.setSSOHeaders(req, signInURL)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting credentials: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Printf("Login API: HTTP %d", resp.StatusCode)

	var loginResp struct {
		ResponseStatus struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"responseStatus"`
		ServiceTicketID string `json:"serviceTicketId"`
		CustomerMFAInfo struct {
			MFALastMethodUsed string `json:"mfaLastMethodUsed"`
		} `json:"customerMfaInfo"`
	}
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return fmt.Errorf("parsing login response (HTTP %d): %w\nbody: %s",
			resp.StatusCode, err, truncate(string(respBody), 400))
	}

	switch loginResp.ResponseStatus.Type {
	case "SUCCESSFUL":
		// good
	case "MFA_REQUIRED":
		return fmt.Errorf("MFA/2FA is required on this account. Please disable 2FA in Garmin Connect settings, or provide a verification code (MFA support coming soon)")
	case "INVALID_CREDENTIALS", "":
		return fmt.Errorf("invalid credentials — check your Garmin Connect email and password")
	default:
		return fmt.Errorf("unexpected login response %q: %s", loginResp.ResponseStatus.Type, loginResp.ResponseStatus.Message)
	}

	ticket := loginResp.ServiceTicketID
	if ticket == "" {
		return fmt.Errorf("no service ticket in login response: %s", truncate(string(respBody), 300))
	}
	log.Printf("Got service ticket: %s...", ticket[:min(20, len(ticket))])

	// ── Step 3: Cloudflare LB cookie (best-effort, backend pinning) ──────
	// garth does this right before the OAuth1 call to pin the CF backend
	cfReq, _ := http.NewRequest("GET", "https://sso.garmin.com/portal/sso/embed", nil)
	c.setSSOHeaders(cfReq, "https://sso.garmin.com/mobile/sso/en/sign-in")
	cfReq.Header.Set("Sec-Fetch-Site", "same-origin")
	if cfResp, cfErr := c.httpClient.Do(cfReq); cfErr == nil {
		io.ReadAll(cfResp.Body)
		cfResp.Body.Close()
		log.Printf("CF LB cookie: HTTP %d", cfResp.StatusCode)
	}

	// ── Step 4: Get OAuth1 token (consumer-signed GET) ───────────────────
	oauth1, err := c.getOAuth1Token(ticket)
	if err != nil {
		return fmt.Errorf("OAuth1 token exchange: %w", err)
	}
	log.Printf("Got OAuth1 token")

	// ── Step 5: Exchange OAuth1 for OAuth2 ───────────────────────────────
	oauth2, err := c.exchangeOAuth2(oauth1, true)
	if err != nil {
		return fmt.Errorf("OAuth2 exchange: %w", err)
	}
	log.Printf("Got OAuth2 access token (expires in %ds)", oauth2.ExpiresIn)

	c.oauth1 = oauth1
	c.oauth2 = oauth2
	c.LoggedIn = true

	// Fetch display name
	if profile, err := c.fetchProfile(); err == nil {
		c.displayName = profile
	}

	return nil
}

// getOAuth1Token exchanges a service ticket for an OAuth1 token
// Uses consumer-only OAuth1 signing (no token yet)
func (c *Client) getOAuth1Token(ticket string) (oauth1Token, error) {
	// Build URL — login-url value is NOT pre-encoded here (requests_oauthlib parses
	// the URL and decodes query params before building the signature base string)
	queryParams := url.Values{
		"ticket":             {ticket},
		"login-url":          {serviceURL},
		"accepts-mfa-tokens": {"true"},
	}
	rawURL := "https://connectapi.garmin.com/oauth-service/oauth/preauthorized?" + queryParams.Encode()

	// OAuth1 signing params — NOTE: oauth_token must be OMITTED when there is no
	// token (consumer-only auth). Including oauth_token="" changes the signature.
	nonce := randomNonce()
	ts := fmt.Sprintf("%d", time.Now().Unix())
	oauthParams := map[string]string{
		"oauth_consumer_key":     oauthConsumerKey,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        ts,
		"oauth_version":          "1.0",
	}

	// Merge OAuth header params with URL query params for signature
	allParams := mergeParams(oauthParams, map[string]string{
		"ticket":             ticket,
		"login-url":          serviceURL,
		"accepts-mfa-tokens": "true",
	})

	sig := signOAuth1("GET",
		"https://connectapi.garmin.com/oauth-service/oauth/preauthorized",
		allParams, oauthConsumerSecret, "")
	oauthParams["oauth_signature"] = sig

	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("Authorization", buildOAuth1Header(oauthParams))
	req.Header.Set("User-Agent", oauthUserAgent)

	// Copy SSO session cookies — garth passes parent session cookies to the
	// OAuth1Session, which sends them along to connectapi.garmin.com
	ssoURL, _ := url.Parse("https://sso.garmin.com")
	for _, ck := range c.jar.Cookies(ssoURL) {
		req.AddCookie(ck)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return oauth1Token{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return oauth1Token{}, fmt.Errorf("OAuth1 preauthorized returned HTTP %d: %s",
			resp.StatusCode, truncate(string(body), 300))
	}

	// Response is URL-encoded: oauth_token=xxx&oauth_token_secret=yyy
	parsed, err := url.ParseQuery(string(body))
	if err != nil {
		return oauth1Token{}, fmt.Errorf("parsing OAuth1 response: %w", err)
	}

	token := oauth1Token{
		OAuthToken:       parsed.Get("oauth_token"),
		OAuthTokenSecret: parsed.Get("oauth_token_secret"),
	}
	if token.OAuthToken == "" {
		return oauth1Token{}, fmt.Errorf("empty oauth_token in response: %s", truncate(string(body), 300))
	}
	return token, nil
}

// exchangeOAuth2 exchanges an OAuth1 token for an OAuth2 token
func (c *Client) exchangeOAuth2(o1 oauth1Token, isLogin bool) (oauth2Token, error) {
	exchangeURL := "https://connectapi.garmin.com/oauth-service/oauth/exchange/user/2.0"

	nonce := randomNonce()
	ts := fmt.Sprintf("%d", time.Now().Unix())

	formData := url.Values{}
	if isLogin {
		formData.Set("audience", "GARMIN_CONNECT_MOBILE_ANDROID_DI")
	}

	oauthParams := map[string]string{
		"oauth_consumer_key":     oauthConsumerKey,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        ts,
		"oauth_token":            o1.OAuthToken,
		"oauth_version":          "1.0",
	}

	// POST body params are included in OAuth1 signature for application/x-www-form-urlencoded
	bodyParams := map[string]string{}
	if isLogin {
		bodyParams["audience"] = "GARMIN_CONNECT_MOBILE_ANDROID_DI"
	}
	allParams := mergeParams(oauthParams, bodyParams)

	sig := signOAuth1("POST", exchangeURL, allParams, oauthConsumerSecret, o1.OAuthTokenSecret)
	oauthParams["oauth_signature"] = sig

	req, _ := http.NewRequest("POST", exchangeURL, strings.NewReader(formData.Encode()))
	req.Header.Set("Authorization", buildOAuth1Header(oauthParams))
	req.Header.Set("User-Agent", oauthUserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return oauth2Token{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return oauth2Token{}, fmt.Errorf("OAuth2 exchange returned HTTP %d: %s",
			resp.StatusCode, truncate(string(body), 300))
	}

	var token oauth2Token
	if err := json.Unmarshal(body, &token); err != nil {
		return oauth2Token{}, fmt.Errorf("parsing OAuth2 token: %w", err)
	}

	now := time.Now().Unix()
	token.ExpiresAt = now + int64(token.ExpiresIn)
	token.RefreshTokenExpiresAt = now + int64(token.RefreshTokenExpiresIn)
	return token, nil
}

func (c *Client) fetchProfile() (string, error) {
	body, err := c.get("/userprofile-service/socialProfile")
	if err != nil {
		return "", err
	}
	var p struct {
		DisplayName string `json:"displayName"`
		UserName    string `json:"userName"`
	}
	json.Unmarshal(body, &p)
	if p.DisplayName != "" {
		return p.DisplayName, nil
	}
	return p.UserName, nil
}

func (c *Client) DisplayName() string {
	return c.displayName
}

// SaveTokens saves OAuth tokens to disk for session reuse
func (c *Client) SaveTokens(filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(savedTokens{OAuth1: c.oauth1, OAuth2: c.oauth2})
}

// LoadTokens restores a saved session from disk
func (c *Client) LoadTokens(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	var saved savedTokens
	if err := json.NewDecoder(f).Decode(&saved); err != nil {
		return err
	}
	if saved.OAuth2.AccessToken == "" {
		return fmt.Errorf("no access token in saved file")
	}

	c.oauth1 = saved.OAuth1
	c.oauth2 = saved.OAuth2

	// Refresh if expired
	if c.oauth2.expired() {
		log.Println("OAuth2 token expired, refreshing...")
		refreshed, err := c.exchangeOAuth2(c.oauth1, false)
		if err != nil {
			return fmt.Errorf("token refresh failed: %w", err)
		}
		c.oauth2 = refreshed
	}

	c.LoggedIn = true
	if profile, err := c.fetchProfile(); err == nil {
		c.displayName = profile
	}
	log.Printf("Restored session for %s", c.displayName)
	return nil
}

// ── OAuth1 signing helpers ────────────────────────────────────────────────

// signOAuth1 computes an OAuth1 HMAC-SHA1 signature
func signOAuth1(method, rawURL string, params map[string]string, consumerSecret, tokenSecret string) string {
	// Build sorted parameter string (percent-encode key=value pairs)
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	paramStr := strings.Join(parts, "&")

	// Signature base string
	base := strings.Join([]string{
		strings.ToUpper(method),
		percentEncode(rawURL),
		percentEncode(paramStr),
	}, "&")

	// Signing key
	key := percentEncode(consumerSecret) + "&" + percentEncode(tokenSecret)

	// HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(key))
	mac.Write([]byte(base))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// buildOAuth1Header builds the Authorization header value
func buildOAuth1Header(params map[string]string) string {
	// Required header params (exclude empty oauth_token)
	order := []string{
		"oauth_consumer_key", "oauth_nonce", "oauth_signature",
		"oauth_signature_method", "oauth_timestamp", "oauth_token", "oauth_version",
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		v, ok := params[k]
		if !ok || (k == "oauth_token" && v == "") {
			continue
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, percentEncode(v)))
	}
	return "OAuth " + strings.Join(parts, ", ")
}

// mergeParams merges two string maps into one (for OAuth1 signature computation)
func mergeParams(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// percentEncode encodes a string per RFC 3986 as required by OAuth 1.0a.
// Only ALPHA / DIGIT / "-" / "." / "_" / "~" are left unencoded.
// url.QueryEscape must NOT be used here — it encodes space as "+" and
// may encode "~", both of which produce wrong OAuth1 signatures.
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// randomNonce generates a random 16-byte hex string
func randomNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// setSSOHeaders sets browser-like headers for the SSO endpoints (needed to avoid Cloudflare blocks)
func (c *Client) setSSOHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", ssoUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
