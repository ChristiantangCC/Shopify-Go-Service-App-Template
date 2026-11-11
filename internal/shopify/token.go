// Package shopify talks to the Shopify Admin API with each store's offline
// access token, obtained when the app is first opened in that store's admin.
package shopify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrNotConnected means no token is stored for the store yet: the app has not
// been opened in that store's admin since it was installed (or Shopify
// rejected the token).
var ErrNotConnected = errors.New("shopify: not connected, open the app in the store's admin once")

// TokenStore keeps every connected store's offline access token in memory and
// in one encrypted file (AES-GCM, key derived from the client secret), so they
// survive restarts and deploys without a database. Each store has its own
// token, also when several stores of one Plus organisation share the install.
type TokenStore struct {
	path string
	key  [32]byte

	mu     sync.RWMutex
	tokens map[string]storedToken
}

type storedToken struct {
	Token   string    `json:"token"`
	SavedAt time.Time `json:"savedAt"`
}

// storedFile is the token file's content.
type storedFile struct {
	// Tokens by myshopify domain.
	Tokens map[string]storedToken `json:"tokens,omitempty"`

	// Shop, Token and SavedAt are the single-store format written by earlier
	// versions. Still read, so an upgrade keeps the store connected.
	Shop    string    `json:"shop,omitempty"`
	Token   string    `json:"token,omitempty"`
	SavedAt time.Time `json:"savedAt,omitzero"`
}

// NewTokenStore opens the token file in dir; a missing or unreadable file (a
// changed client secret) means "not connected yet".
func NewTokenStore(dir, clientSecret string) (*TokenStore, error) {
	store := &TokenStore{
		path:   filepath.Join(dir, "shopify-token.enc"),
		key:    sha256.Sum256([]byte("shopify-app-service-token:" + clientSecret)),
		tokens: map[string]storedToken{},
	}
	raw, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("token store: %w", err)
	}
	saved, err := store.decrypt(raw)
	if err != nil {
		return store, nil
	}
	for shop, token := range saved.Tokens {
		if token.Token != "" {
			store.tokens[shop] = token
		}
	}
	if saved.Shop != "" && saved.Token != "" {
		if _, ok := store.tokens[saved.Shop]; !ok {
			store.tokens[saved.Shop] = storedToken{Token: saved.Token, SavedAt: saved.SavedAt}
		}
	}
	return store, nil
}

// Token returns the stored token for shop.
func (s *TokenStore) Token(shop string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	saved, ok := s.tokens[shop]
	if !ok {
		return "", ErrNotConnected
	}
	return saved.Token, nil
}

// Connected reports whether a token is stored for shop.
func (s *TokenStore) Connected(shop string) bool {
	_, err := s.Token(shop)
	return err == nil
}

// Save stores a new token for shop (file first, then memory). Other stores'
// tokens are kept.
func (s *TokenStore) Save(shop, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]storedToken, len(s.tokens)+1)
	for name, saved := range s.tokens {
		next[name] = saved
	}
	next[shop] = storedToken{Token: token, SavedAt: time.Now().UTC()}
	if err := s.write(next); err != nil {
		return err
	}
	s.tokens = next
	return nil
}

// Forget drops shop's token after Shopify rejected it (app uninstalled from
// that store). The next opening of the app in that store's admin connects
// again. Other stores stay connected.
func (s *TokenStore) Forget(shop string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[shop]; !ok {
		return
	}
	delete(s.tokens, shop)
	if len(s.tokens) == 0 {
		_ = os.Remove(s.path)
		return
	}
	_ = s.write(s.tokens)
}

// write replaces the token file with tokens (temp file, then rename).
func (s *TokenStore) write(tokens map[string]storedToken) error {
	raw, err := s.encrypt(storedFile{Tokens: tokens})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("token store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("token store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("token store: %w", err)
	}
	return nil
}

func (s *TokenStore) encrypt(value storedFile) ([]byte, error) {
	plain, _ := json.Marshal(value)
	gcm, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (s *TokenStore) decrypt(raw []byte) (storedFile, error) {
	var value storedFile
	gcm, err := s.aead()
	if err != nil {
		return value, err
	}
	if len(raw) < gcm.NonceSize() {
		return value, errors.New("token store: file too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return value, err
	}
	return value, json.Unmarshal(plain, &value)
}

func (s *TokenStore) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
