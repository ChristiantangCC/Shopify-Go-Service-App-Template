package shopify

import (
	"context"
	"errors"
)

// ErrNoShop means the request's context names no store: the route is missing
// the middleware that picks one (see handlers.ProxyShop and handlers.APIShop).
var ErrNoShop = errors.New("shopify: no shop on the request context")

type shopKey struct{}

// WithShop returns ctx naming the store (myshopify domain) the request is
// for. Client.Do sends its requests to that store, with that store's token.
func WithShop(ctx context.Context, shop string) context.Context {
	return context.WithValue(ctx, shopKey{}, shop)
}

// ShopFrom returns the store placed in ctx by WithShop.
func ShopFrom(ctx context.Context) (string, bool) {
	shop, ok := ctx.Value(shopKey{}).(string)
	return shop, ok && shop != ""
}
