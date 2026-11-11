package config

import (
	"slices"
	"testing"
)

func load(t *testing.T, shop, shops string) (Config, error) {
	t.Helper()
	t.Setenv("SHOPIFY_CLIENT_ID", "client-id")
	t.Setenv("SHOPIFY_CLIENT_SECRET", "secret")
	t.Setenv("SHOPIFY_SHOP", shop)
	t.Setenv("SHOPIFY_SHOPS", shops)
	return Load()
}

func TestLoadShops(t *testing.T) {
	tests := []struct {
		name, shop, shops string
		want              []string
	}{
		{"one store", "One.myshopify.com", "", []string{"one.myshopify.com"}},
		{"several stores", "", "one.myshopify.com, two.myshopify.com", []string{"one.myshopify.com", "two.myshopify.com"}},
		{"both, without duplicates", "one.myshopify.com", "two.myshopify.com,one.myshopify.com", []string{"one.myshopify.com", "two.myshopify.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := load(t, tt.shop, tt.shops)
			if err != nil || !slices.Equal(cfg.Shops, tt.want) {
				t.Fatalf("got %v %v, want %v", cfg.Shops, err, tt.want)
			}
		})
	}
	if _, err := load(t, "", ""); err == nil {
		t.Fatal("no store configured should fail")
	}
	if _, err := load(t, "", "one.myshopify.com,example.com"); err == nil {
		t.Fatal("a domain that is not myshopify should fail")
	}
}
