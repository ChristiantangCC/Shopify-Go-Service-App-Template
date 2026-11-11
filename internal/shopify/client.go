package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// maxThrottleRetries bounds how often a THROTTLED request is resent.
	maxThrottleRetries = 3
	// maxTransientRetries bounds how often a request is resent after a
	// Shopify 5xx or a network error.
	maxTransientRetries = 3
	// maxThrottleWait caps the computed leaky-bucket wait, so a very
	// expensive query cannot stall a caller for minutes.
	maxThrottleWait = 30 * time.Second
)

// Client sends Admin API GraphQL requests to the store named on the request's
// context (WithShop), with that store's token.
type Client struct {
	// endpoint is the GraphQL URL of a store (overridden in tests).
	endpoint   func(shop string) string
	tokens     *TokenStore
	httpClient *http.Client
	// backoff is the wait before transient retry n (1, 2, 3).
	backoff func(n int) time.Duration
}

// NewClient creates an Admin API client that uses the stored tokens.
func NewClient(apiVersion string, tokens *TokenStore) *Client {
	return &Client{
		endpoint: func(shop string) string {
			return fmt.Sprintf("https://%s/admin/api/%s/graphql.json", shop, apiVersion)
		},
		tokens:     tokens,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		backoff:    func(n int) time.Duration { return time.Duration(1<<(n-1)) * 500 * time.Millisecond },
	}
}

// GraphQLError is one entry of a GraphQL `errors` array.
type GraphQLError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

// GraphQLErrors is returned when Shopify answers with top-level GraphQL errors.
type GraphQLErrors []GraphQLError

func (e GraphQLErrors) Error() string {
	if len(e) == 0 {
		return "graphql: unknown error"
	}
	return "graphql: " + e[0].Message
}

type graphqlResponse struct {
	Data       json.RawMessage `json:"data"`
	Errors     GraphQLErrors   `json:"errors"`
	Extensions struct {
		Cost struct {
			RequestedQueryCost float64 `json:"requestedQueryCost"`
			ThrottleStatus     struct {
				CurrentlyAvailable float64 `json:"currentlyAvailable"`
				RestoreRate        float64 `json:"restoreRate"`
			} `json:"throttleStatus"`
		} `json:"cost"`
	} `json:"extensions"`
}

var errUnauthorized = errors.New("shopify: unauthorized")

// transientError is a failure that may succeed when resent: a 5xx or 429 from
// Shopify, or a network error. executed is false when Shopify certainly did
// not run the request (429), so even a mutation can be resent. retryAfter
// carries a server-given wait.
type transientError struct {
	err        error
	executed   bool
	retryAfter time.Duration
}

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// Do runs a GraphQL query or mutation against the store named on ctx (see
// WithShop) and decodes `data` into out.
//
//   - 401: the app was uninstalled from that store (or the token revoked). Its
//     token is dropped and ErrNotConnected returned until the app is opened
//     there again.
//   - THROTTLED or HTTP 429: nothing was executed, so it waits and retries.
//   - 5xx or a network error: Shopify may or may not have run the request.
//     Queries are retried with backoff. Mutations only when they carry an
//     @idempotent key, because resending e.g. productCreate could create the
//     product twice.
func (c *Client) Do(ctx context.Context, query string, variables map[string]any, out any) error {
	shop, ok := ShopFrom(ctx)
	if !ok {
		return ErrNoShop
	}
	retrySafe := isRetrySafe(query)
	throttleRetries, transientRetries := 0, 0
	for {
		res, err := c.send(ctx, shop, query, variables)
		if errors.Is(err, errUnauthorized) {
			c.tokens.Forget(shop)
			return ErrNotConnected
		}
		var transient *transientError
		if errors.As(err, &transient) && (retrySafe || !transient.executed) && transientRetries < maxTransientRetries && ctx.Err() == nil {
			transientRetries++
			wait := c.backoff(transientRetries)
			if transient.retryAfter > 0 {
				wait = transient.retryAfter
			}
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if isThrottled(res.Errors) && throttleRetries < maxThrottleRetries {
			throttleRetries++
			if err := sleep(ctx, throttleWait(res)); err != nil {
				return err
			}
			continue
		}
		if len(res.Errors) > 0 {
			return res.Errors
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(res.Data, out)
	}
}

func (c *Client) send(ctx context.Context, shop, query string, variables map[string]any) (*graphqlResponse, error) {
	token, err := c.tokens.Token(shop)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(shop), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", token)

	res, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &transientError{err: fmt.Errorf("admin api: %w", err), executed: true}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode == http.StatusUnauthorized {
		return nil, errUnauthorized
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, &transientError{err: errors.New("admin api: Shopify answered 429"), retryAfter: retryAfter(res)}
	}
	if res.StatusCode >= 500 {
		return nil, &transientError{err: fmt.Errorf("admin api: Shopify answered %d", res.StatusCode), executed: true}
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("admin api: Shopify answered %d", res.StatusCode)
	}
	var parsed graphqlResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("admin api: %w", err)
	}
	return &parsed, nil
}

// retryAfter reads a Retry-After header of whole seconds, 0 when absent.
func retryAfter(res *http.Response) time.Duration {
	seconds, err := strconv.Atoi(res.Header.Get("Retry-After"))
	if err != nil || seconds < 0 {
		return 0
	}
	wait := time.Duration(seconds) * time.Second
	if wait > maxThrottleWait {
		return maxThrottleWait
	}
	return wait
}

// idempotentKey matches an @idempotent(key: ...) directive with a value.
var idempotentKey = regexp.MustCompile(`@idempotent\s*\(\s*key\s*:\s*[^)\s]`)

// isRetrySafe reports whether resending the operation can't change the store
// twice: a query, or a mutation with an @idempotent key.
func isRetrySafe(query string) bool {
	if idempotentKey.MatchString(query) {
		return true
	}
	return !strings.HasPrefix(operationType(query), "mutation")
}

// operationType returns the document with leading whitespace and # comments
// removed, which starts with "query", "mutation" or "{".
func operationType(query string) string {
	for {
		query = strings.TrimSpace(query)
		if !strings.HasPrefix(query, "#") {
			return query
		}
		_, rest, found := strings.Cut(query, "\n")
		if !found {
			return ""
		}
		query = rest
	}
}

func isThrottled(errs GraphQLErrors) bool {
	for _, e := range errs {
		if e.Extensions.Code == "THROTTLED" {
			return true
		}
	}
	return false
}

// throttleWait is how long until the bucket holds enough points for the
// request, capped at maxThrottleWait.
func throttleWait(res *graphqlResponse) time.Duration {
	cost := res.Extensions.Cost
	missing := cost.RequestedQueryCost - cost.ThrottleStatus.CurrentlyAvailable
	if cost.ThrottleStatus.RestoreRate <= 0 || missing <= 0 {
		return time.Second
	}
	wait := time.Duration(math.Ceil(missing/cost.ThrottleStatus.RestoreRate)) * time.Second
	if wait > maxThrottleWait {
		return maxThrottleWait
	}
	return wait
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
