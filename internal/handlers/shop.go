package handlers

import (
	"net/http"
	"slices"
	"strings"

	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/appproxy"
	"github.com/ChristiantangCC/Shopify-Go-Service-App-Template/internal/shopify"
)

// ShopHeader names the store an /api call is for, as its myshopify domain.
// Shopify Flow can fill it with {{shop.myshopifyDomain}}.
const ShopHeader = "X-Shopify-Shop-Domain"

// ProxyShop makes an App Proxy request work on the store it came from: the
// shop Shopify signed into the request (checked by appproxy.Verify, which must
// run first).
func ProxyShop(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := appproxy.CallerFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r.WithContext(shopify.WithShop(r.Context(), caller.Shop)))
	})
}

// APIShop makes an /api request work on the store named in the ShopHeader
// header, which must be one of shops. With a single configured store the
// header may be left out.
func APIShop(shops []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			shop := strings.ToLower(strings.TrimSpace(r.Header.Get(ShopHeader)))
			switch {
			case shop == "" && len(shops) == 1:
				shop = shops[0]
			case shop == "":
				writeError(w, http.StatusBadRequest, ShopHeader+" header required: this service works for several stores")
				return
			case !slices.Contains(shops, shop):
				writeError(w, http.StatusBadRequest, "unknown shop in "+ShopHeader)
				return
			}
			next.ServeHTTP(w, r.WithContext(shopify.WithShop(r.Context(), shop)))
		})
	}
}
