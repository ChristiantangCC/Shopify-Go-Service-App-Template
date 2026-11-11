package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/shopify"
)

// shopSeen runs a request with the given shop header through APIShop and
// returns the status and the store the handler was told to work on.
func shopSeen(shops []string, header string) (int, string) {
	var seen string
	handler := APIShop(shops)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, _ = shopify.ShopFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/example/products", nil)
	if header != "" {
		req.Header.Set(ShopHeader, header)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, seen
}

func TestAPIShop(t *testing.T) {
	one := []string{"one.myshopify.com"}
	two := []string{"one.myshopify.com", "two.myshopify.com"}
	tests := []struct {
		name     string
		shops    []string
		header   string
		wantCode int
		wantShop string
	}{
		{"single store needs no header", one, "", http.StatusOK, "one.myshopify.com"},
		{"header picks the store", two, "Two.myshopify.com ", http.StatusOK, "two.myshopify.com"},
		{"several stores need the header", two, "", http.StatusBadRequest, ""},
		{"unknown store", two, "three.myshopify.com", http.StatusBadRequest, ""},
		{"unknown store with a single one configured", one, "three.myshopify.com", http.StatusBadRequest, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, shop := shopSeen(tt.shops, tt.header); code != tt.wantCode || shop != tt.wantShop {
				t.Fatalf("got %d %q, want %d %q", code, shop, tt.wantCode, tt.wantShop)
			}
		})
	}
}

func TestInstallRejectsAStoreThatIsNotConfigured(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"dest": "https://three.myshopify.com", "aud": "client-id", "exp": time.Now().Add(time.Minute).Unix(), "nbf": time.Now().Add(-time.Second).Unix()})
	body := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(body))
	idToken := body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	tokens, _ := shopify.NewTokenStore(t.TempDir(), "secret")
	install := Install{Shops: []string{"one.myshopify.com", "two.myshopify.com"}, ClientID: "client-id", ClientSecret: "secret", Tokens: tokens, Logger: discard}
	rec := httptest.NewRecorder()
	install.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?id_token="+url.QueryEscape(idToken), nil))
	if rec.Code != http.StatusForbidden || tokens.Connected("three.myshopify.com") {
		t.Fatalf("got %d, want 403 and no token stored", rec.Code)
	}
}
