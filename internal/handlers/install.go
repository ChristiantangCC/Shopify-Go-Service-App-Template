package handlers

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/shopify"
)

// Install handles GET /, the App URL Shopify loads inside the admin when the
// app is opened. Every opening trades the admin's session token (`id_token`)
// for that store's offline access token and stores it: the first opening
// connects the store, and later ones pick up newly granted scopes (a token
// keeps the scopes it was issued with). That is the only reason the app has an
// admin page.
//
// With a multi-store install (one Plus organisation) every store opens the
// app once and gets its own token; the client ID and secret are shared.
type Install struct {
	// Shops are the stores that may connect.
	Shops        []string
	ClientID     string
	ClientSecret string
	Tokens       *shopify.TokenStore
	Logger       *slog.Logger
}

func (h Install) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The admin embeds this page; allow only the admin (and, once the store is
	// known, the store) to frame it.
	w.Header().Set("Content-Security-Policy", "frame-ancestors https://admin.shopify.com")

	idToken := r.URL.Query().Get("id_token")
	if idToken == "" {
		h.page(w, http.StatusBadRequest, "Open this app from the Shopify admin.")
		return
	}
	claims, err := shopify.VerifySessionToken(idToken, h.ClientID, h.ClientSecret, time.Now())
	if err != nil {
		h.Logger.Warn("[Install] Rejected session token", "error", err)
		h.page(w, http.StatusUnauthorized, "This link is not valid. Open the app from the Shopify admin again.")
		return
	}
	shop := claims.Shop()
	if !slices.Contains(h.Shops, shop) {
		h.Logger.Warn("[Install] Opened from a store that is not configured", "shop", shop)
		h.page(w, http.StatusForbidden, "This app is not set up for this store.")
		return
	}
	w.Header().Set("Content-Security-Policy", fmt.Sprintf("frame-ancestors https://%s https://admin.shopify.com", shop))

	token, err := shopify.ExchangeForOfflineToken(r.Context(), shopify.ExchangeOptions{Shop: shop, ClientID: h.ClientID, ClientSecret: h.ClientSecret, SessionToken: idToken})
	if err != nil {
		h.Logger.Error("[Install] Token exchange failed", "shop", shop, "error", err)
		h.page(w, http.StatusBadGateway, "Could not connect to Shopify. Try opening the app again.")
		return
	}
	if err := h.Tokens.Save(shop, token); err != nil {
		h.Logger.Error("[Install] Could not store the token", "shop", shop, "error", err)
		h.page(w, http.StatusInternalServerError, "Could not store the connection. Check the service's data volume.")
		return
	}
	h.Logger.Info("[Install] Connected", "shop", shop)
	h.page(w, http.StatusOK, "Connected. This app works in the background; nothing to set up here.")
}

func (h Install) page(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>Connected</title><style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:40px;color:#303030}</style></head><body><p>%s</p></body></html>`, html.EscapeString(message))
}
