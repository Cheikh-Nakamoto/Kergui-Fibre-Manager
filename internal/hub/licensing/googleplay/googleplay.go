// Package googleplay is the licensing module's Google Play Billing adapter
// (RFC-006, Two channels): the Play Developer API (androidpublisher v3)
// behind usecase.PlayVerifier, and the check of the OIDC token Pub/Sub signs
// its push of a Real-time Developer Notification with.
//
// It is written on the standard library rather than google.golang.org/api:
// the hub needs two endpoints and one OAuth2 grant, and the client library
// would pull a large dependency tree into a binary that otherwise has none
// of it. The service account signs a JWT with its RSA key (RFC 7523), the
// token endpoint exchanges it for an access token, and the access token is
// cached for its lifetime.
package googleplay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

const (
	// DefaultBaseURL is the Play Developer API.
	DefaultBaseURL = "https://androidpublisher.googleapis.com"
	// DefaultTokenURI is Google's OAuth2 token endpoint, used when the
	// service account JSON names none.
	DefaultTokenURI = "https://oauth2.googleapis.com/token"
	// Scope is the Play Developer API's OAuth2 scope.
	Scope = "https://www.googleapis.com/auth/androidpublisher"

	jwtBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	assertionTTL   = time.Hour
	// tokenMargin renews the access token before Google expires it.
	tokenMargin    = time.Minute
	requestTimeout = 10 * time.Second
	maxResponse    = 1 << 20
)

// ServiceAccount is the part of a Google service account key file the hub
// uses. The account needs the Play Console permission "View financial
// data, orders and cancellation survey responses" and "Manage orders and
// subscriptions" on the app.
type ServiceAccount struct {
	ClientEmail string
	KeyID       string
	TokenURI    string
	Key         *rsa.PrivateKey
}

// ParseServiceAccount reads a service account key file (the JSON the Cloud
// Console downloads).
func ParseServiceAccount(raw []byte) (ServiceAccount, error) {
	var f struct {
		Type         string `json:"type"`
		ClientEmail  string `json:"client_email"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		TokenURI     string `json:"token_uri"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return ServiceAccount{}, errors.New("googleplay: the service account is not a JSON key file")
	}
	if f.Type != "" && f.Type != "service_account" {
		return ServiceAccount{}, fmt.Errorf("googleplay: a %q key is not a service account key", f.Type)
	}
	if f.ClientEmail == "" || f.PrivateKey == "" {
		return ServiceAccount{}, errors.New("googleplay: the service account key needs client_email and private_key")
	}
	key, err := parseRSAKey(f.PrivateKey)
	if err != nil {
		return ServiceAccount{}, err
	}
	sa := ServiceAccount{ClientEmail: f.ClientEmail, KeyID: f.PrivateKeyID, TokenURI: f.TokenURI, Key: key}
	if sa.TokenURI == "" {
		sa.TokenURI = DefaultTokenURI
	}
	return sa, nil
}

func parseRSAKey(p string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(p))
	if block == nil {
		return nil, errors.New("googleplay: the service account private_key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("googleplay: the service account private_key is not an RSA key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("googleplay: the service account private_key cannot be parsed")
}

// Config wires a Client.
type Config struct {
	// PackageName is the app's application id on Google Play.
	PackageName    string
	ServiceAccount ServiceAccount
	// BaseURL replaces DefaultBaseURL (tests). The token URI comes from
	// the service account.
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// Client calls the Play Developer API. It is safe for concurrent use.
type Client struct {
	pkg  string
	sa   ServiceAccount
	base string
	http *http.Client
	now  func() time.Time

	mu      sync.Mutex
	access  string
	expires time.Time
}

var _ usecase.PlayVerifier = (*Client)(nil)

// New builds a Client.
func New(cfg Config) (*Client, error) {
	if cfg.PackageName == "" {
		return nil, errors.New("googleplay: a package name is required")
	}
	if cfg.ServiceAccount.Key == nil || cfg.ServiceAccount.ClientEmail == "" {
		return nil, errors.New("googleplay: a service account is required")
	}
	c := &Client{pkg: cfg.PackageName, sa: cfg.ServiceAccount, base: strings.TrimRight(cfg.BaseURL, "/"),
		http: cfg.HTTP, now: cfg.Now}
	if c.base == "" {
		c.base = DefaultBaseURL
	}
	if c.sa.TokenURI == "" {
		c.sa.TokenURI = DefaultTokenURI
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: requestTimeout}
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

// --- the Play Developer API ---------------------------------------------------

// subscriptionV2 is the part of SubscriptionPurchaseV2 the hub reads.
type subscriptionV2 struct {
	SubscriptionState          string `json:"subscriptionState"`
	AcknowledgementState       string `json:"acknowledgementState"`
	LinkedPurchaseToken        string `json:"linkedPurchaseToken"`
	ExternalAccountIdentifiers struct {
		ObfuscatedExternalAccountID string `json:"obfuscatedExternalAccountId"`
	} `json:"externalAccountIdentifiers"`
	LineItems []struct {
		ProductID        string `json:"productId"`
		ExpiryTime       string `json:"expiryTime"`
		AutoRenewingPlan struct {
			RecurringPrice struct {
				CurrencyCode string `json:"currencyCode"`
				Units        string `json:"units"`
			} `json:"recurringPrice"`
		} `json:"autoRenewingPlan"`
		// OfferPhase says which phase of the offer the line item is in;
		// freeTrial is present during a free trial.
		OfferPhase struct {
			FreeTrial *struct{} `json:"freeTrial"`
		} `json:"offerPhase"`
	} `json:"lineItems"`
}

// Purchase is purchases.subscriptionsv2.get.
func (c *Client) Purchase(ctx context.Context, token string) (usecase.PlayPurchase, error) {
	if token == "" {
		return usecase.PlayPurchase{}, usecase.ErrPlayPurchaseInvalid
	}
	var raw subscriptionV2
	if err := c.call(ctx, http.MethodGet, c.purchasesURL("subscriptionsv2/tokens/"+url.PathEscape(token)), nil, &raw); err != nil {
		return usecase.PlayPurchase{}, err
	}
	p := usecase.PlayPurchase{
		State:               usecase.PlayState(raw.SubscriptionState),
		Acknowledged:        raw.AcknowledgementState == "ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED",
		LinkedPurchaseToken: raw.LinkedPurchaseToken,
		ObfuscatedAccountID: raw.ExternalAccountIdentifiers.ObfuscatedExternalAccountID,
	}
	for _, li := range raw.LineItems {
		exp, err := time.Parse(time.RFC3339Nano, li.ExpiryTime)
		if err != nil {
			return usecase.PlayPurchase{}, fmt.Errorf("%w: unreadable expiryTime %q", usecase.ErrPlayUnavailable, li.ExpiryTime)
		}
		units, _ := strconv.ParseInt(li.AutoRenewingPlan.RecurringPrice.Units, 10, 64)
		p.LineItems = append(p.LineItems, usecase.PlayLineItem{
			ProductID: li.ProductID, ExpiresAt: exp.UTC(), FreeTrial: li.OfferPhase.FreeTrial != nil,
			PriceUnits: units, Currency: li.AutoRenewingPlan.RecurringPrice.CurrencyCode,
		})
	}
	return p, nil
}

// Acknowledge is purchases.subscriptions.acknowledge. Google refunds a
// subscription not acknowledged within three days.
func (c *Client) Acknowledge(ctx context.Context, productID, token string) error {
	u := c.purchasesURL("subscriptions/" + url.PathEscape(productID) + "/tokens/" + url.PathEscape(token) + ":acknowledge")
	return c.call(ctx, http.MethodPost, u, []byte("{}"), nil)
}

func (c *Client) purchasesURL(rest string) string {
	return c.base + "/androidpublisher/v3/applications/" + url.PathEscape(c.pkg) + "/purchases/" + rest
}

func (c *Client) call(ctx context.Context, method, u string, body []byte, out any) error {
	access, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return fmt.Errorf("%w: %v", usecase.ErrPlayUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", usecase.ErrPlayUnavailable, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		// The cached access token was revoked: get a new one next time.
		c.mu.Lock()
		c.access = ""
		c.mu.Unlock()
		return fmt.Errorf("%w: HTTP 401", usecase.ErrPlayUnavailable)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusBadRequest:
		// 400 is how Google answers a malformed or foreign token.
		return fmt.Errorf("%w: HTTP %d", usecase.ErrPlayPurchaseInvalid, resp.StatusCode)
	case resp.StatusCode/100 != 2:
		// 403 is the service account lacking the Play Console permission:
		// a configuration problem, not the buyer's.
		return fmt.Errorf("%w: HTTP %d", usecase.ErrPlayUnavailable, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: unreadable answer: %v", usecase.ErrPlayUnavailable, err)
	}
	return nil
}

// --- OAuth2 -------------------------------------------------------------------

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.access != "" && now.Before(c.expires) {
		return c.access, nil
	}
	assertion, err := c.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {jwtBearerGrant}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %v", usecase.ErrPlayUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: token: %v", usecase.ErrPlayUnavailable, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: token: HTTP %d", usecase.ErrPlayUnavailable, resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("%w: token: unreadable answer", usecase.ErrPlayUnavailable)
	}
	c.access = tok.AccessToken
	c.expires = now.Add(time.Duration(tok.ExpiresIn)*time.Second - tokenMargin)
	return c.access, nil
}

// assertion is the RS256 JWT the service account signs for the grant.
func (c *Client) assertion(now time.Time) (string, error) {
	hdr := map[string]string{"alg": "RS256", "typ": "JWT"}
	if c.sa.KeyID != "" {
		hdr["kid"] = c.sa.KeyID
	}
	claims := map[string]any{
		"iss": c.sa.ClientEmail, "scope": Scope, "aud": c.sa.TokenURI,
		"iat": now.Unix(), "exp": now.Add(assertionTTL).Unix(),
	}
	h, _ := json.Marshal(hdr)
	p, _ := json.Marshal(claims)
	signing := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.sa.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("googleplay: sign the grant: %w", err)
	}
	return signing + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
