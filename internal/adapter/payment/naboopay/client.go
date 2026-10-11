// Package naboopay is the NabooPay (api.naboopay.com, API v2) payment adapter:
// hosted checkouts paid with Wave or Orange Money.
package naboopay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// DefaultBaseURL is NabooPay's production API.
const DefaultBaseURL = "https://api.naboopay.com"

// The v2 API validates this list strictly: lowercase only, and FREE_MONEY or
// upper-case values are answered with "Invalid request body".
var wallets = []string{"wave", "orange_money"}

const maxRateLimitWait = 5 * time.Second

// Client implements port.PaymentGateway.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

var _ port.PaymentGateway = (*Client)(nil)

// New builds a client. baseURL "" means production.
func New(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 20 * time.Second}}
}

// product is exactly the v2 shape: an unknown key makes the API refuse the body.
type product struct {
	Name        string `json:"name"`
	Price       int64  `json:"price"`
	Quantity    int64  `json:"quantity"`
	Description string `json:"description"`
}

type customer struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

type createRequest struct {
	MethodOfPayment  []string  `json:"method_of_payment"`
	Products         []product `json:"products"`
	Customer         customer  `json:"customer"`
	SuccessURL       string    `json:"success_url"`
	ErrorURL         string    `json:"error_url"`
	FeesCustomerSide bool      `json:"fees_customer_side"`
	IsEscrow         bool      `json:"is_escrow"`
	IsMerchant       bool      `json:"is_merchant"`
}

type transaction struct {
	OrderID     string `json:"order_id"`
	CheckoutURL string `json:"checkout_url"`
	Amount      int64  `json:"amount"`
	Status      string `json:"transaction_status"`
}

// CreateCheckout implements port.PaymentGateway. The amount travels as a single
// product line: NabooPay totals the basket rather than taking a figure.
func (c *Client) CreateCheckout(ctx context.Context, req port.CheckoutRequest) (port.CheckoutResult, error) {
	first, last := splitName(req.CustomerName)
	body := createRequest{
		MethodOfPayment: wallets,
		Products: []product{{
			Name: req.Label, Price: req.AmountXOF, Quantity: 1, Description: req.Description,
		}},
		Customer:   customer{FirstName: first, LastName: last, Phone: req.Phone},
		SuccessURL: req.SuccessURL,
		ErrorURL:   req.ErrorURL,
		// The network fee is added on top: the member pays exactly the announced
		// consumption price plus the provider's fee, the owner receives the price.
		FeesCustomerSide: true,
	}
	var out transaction
	if err := c.do(ctx, http.MethodPost, "/api/v2/transactions", body, &out); err != nil {
		return port.CheckoutResult{}, err
	}
	if out.OrderID == "" || out.CheckoutURL == "" {
		return port.CheckoutResult{}, fmt.Errorf("%w: incomplete NabooPay response", domain.ErrPaymentUnavailable)
	}
	amount := out.Amount
	if amount == 0 {
		amount = req.AmountXOF
	}
	return port.CheckoutResult{OrderID: out.OrderID, CheckoutURL: out.CheckoutURL, AmountXOF: amount}, nil
}

// TransactionStatus implements port.PaymentGateway.
func (c *Client) TransactionStatus(ctx context.Context, orderID string) (domain.PaymentStatus, error) {
	var out transaction
	if err := c.do(ctx, http.MethodGet, "/api/v2/transactions/"+url.PathEscape(orderID), nil, &out); err != nil {
		return "", err
	}
	return MapStatus(out.Status), nil
}

// CancelCheckout implements port.PaymentGateway.
func (c *Client) CancelCheckout(ctx context.Context, orderID string) error {
	return c.do(ctx, http.MethodDelete, "/api/v2/transactions/"+url.PathEscape(orderID), nil, nil)
}

// MapStatus reads NabooPay's vocabulary. "completed" is not in the published
// enumeration but appears in the webhook example; an unknown word is never
// treated as paid, it stays pending and is asked again later.
func MapStatus(s string) domain.PaymentStatus {
	switch strings.ToLower(s) {
	case "paid", "paid_and_blocked", "done", "completed":
		return domain.PaymentPaid
	case "cancelled", "canceled":
		return domain.PaymentCancelled
	case "failed", "expired":
		return domain.PaymentFailed
	case "refunded":
		return domain.PaymentRefunded
	default:
		return domain.PaymentPending
	}
}

type rateLimited struct{ wait time.Duration }

func (r rateLimited) Error() string { return "naboopay: rate limited" }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	err := c.doOnce(ctx, method, path, in, out)
	var rl rateLimited
	if !errors.As(err, &rl) {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(min(rl.wait, maxRateLimitWait)):
	}
	if err := c.doOnce(ctx, method, path, in, out); err != nil {
		if errors.As(err, &rl) {
			return fmt.Errorf("%w: NabooPay rate limit reached, try again in a minute", domain.ErrPaymentUnavailable)
		}
		return err
	}
	return nil
}

func (c *Client) doOnce(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrPaymentUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return rateLimited{wait: time.Duration(max(secs, 1)) * time.Second}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: NabooPay refused the API key (%d): %s", domain.ErrPaymentUnavailable, resp.StatusCode, errMessage(raw))
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", domain.ErrPaymentNotFound, errMessage(raw))
	case resp.StatusCode >= 300:
		return fmt.Errorf("%w: NabooPay %s %s returned %d: %s", domain.ErrPaymentUnavailable, method, path, resp.StatusCode, errMessage(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: cannot decode NabooPay response: %v", domain.ErrPaymentUnavailable, err)
	}
	return nil
}

func errMessage(raw []byte) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Detail  any    `json:"detail"`
	}
	if json.Unmarshal(raw, &e) == nil {
		switch {
		case e.Error != "":
			return e.Error
		case e.Message != "":
			return e.Message
		case e.Detail != nil:
			return fmt.Sprint(e.Detail)
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		return "empty response"
	}
	return s
}

func splitName(full string) (first, last string) {
	parts := strings.Fields(full)
	switch len(parts) {
	case 0:
		return "Client", "Kergui"
	case 1:
		return parts[0], parts[0]
	default:
		return parts[0], strings.Join(parts[1:], " ")
	}
}
