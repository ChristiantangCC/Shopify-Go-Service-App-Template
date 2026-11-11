package shopify

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenStoreSurvivesARestartEncrypted(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTokenStore(dir, "secret")
	if store.Connected("example-shop.myshopify.com") {
		t.Fatal("connected before any token")
	}
	if err := store.Save("example-shop.myshopify.com", "shpat_abc"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "shopify-token.enc"))
	if bytes.Contains(raw, []byte("shpat_abc")) {
		t.Fatal("token stored in plain text")
	}
	reopened, _ := NewTokenStore(dir, "secret")
	if token, err := reopened.Token("example-shop.myshopify.com"); err != nil || token != "shpat_abc" {
		t.Fatalf("after restart: %q %v", token, err)
	}
	if _, err := reopened.Token("other.myshopify.com"); err != ErrNotConnected {
		t.Fatal("token handed out for another shop")
	}
	if withOtherSecret, _ := NewTokenStore(dir, "rotated"); withOtherSecret.Connected("example-shop.myshopify.com") {
		t.Fatal("read with another secret")
	}
	reopened.Forget("example-shop.myshopify.com")
	if again, _ := NewTokenStore(dir, "secret"); again.Connected("example-shop.myshopify.com") {
		t.Fatal("token kept after Forget")
	}
}

func TestTokenStoreKeepsATokenPerStore(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTokenStore(dir, "secret")
	_ = store.Save("one.myshopify.com", "shpat_one")
	_ = store.Save("two.myshopify.com", "shpat_two")

	reopened, _ := NewTokenStore(dir, "secret")
	for shop, want := range map[string]string{"one.myshopify.com": "shpat_one", "two.myshopify.com": "shpat_two"} {
		if token, err := reopened.Token(shop); err != nil || token != want {
			t.Fatalf("%s after restart: %q %v", shop, token, err)
		}
	}
	reopened.Forget("one.myshopify.com")
	again, _ := NewTokenStore(dir, "secret")
	if again.Connected("one.myshopify.com") || !again.Connected("two.myshopify.com") {
		t.Fatal("Forget should drop one store's token and keep the other")
	}
}

func TestTokenStoreReadsTheSingleStoreFileOfEarlierVersions(t *testing.T) {
	dir := t.TempDir()
	old, _ := NewTokenStore(dir, "secret")
	raw, err := old.encrypt(storedFile{Shop: "example-shop.myshopify.com", Token: "shpat_abc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shopify-token.enc"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTokenStore(dir, "secret")
	if token, err := store.Token("example-shop.myshopify.com"); err != nil || token != "shpat_abc" {
		t.Fatalf("old file: %q %v", token, err)
	}
	_ = store.Save("two.myshopify.com", "shpat_two")
	if reopened, _ := NewTokenStore(dir, "secret"); !reopened.Connected("example-shop.myshopify.com") || !reopened.Connected("two.myshopify.com") {
		t.Fatal("first store lost when a second one connected")
	}
}
