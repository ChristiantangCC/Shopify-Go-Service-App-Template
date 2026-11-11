package shopify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

var issued = time.Unix(1_800_000_000, 0)

func signedToken(t *testing.T, secret string, header, claims map[string]any) string {
	t.Helper()
	rawHeader, _ := json.Marshal(header)
	payload, _ := json.Marshal(claims)
	body := base64.RawURLEncoding.EncodeToString(rawHeader) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func sessionToken(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()
	return signedToken(t, secret, map[string]any{"alg": "HS256", "typ": "JWT"}, claims)
}

func validClaims() map[string]any {
	return map[string]any{"dest": "https://example-shop.myshopify.com", "aud": "client-id", "exp": issued.Add(time.Minute).Unix(), "nbf": issued.Add(-time.Second).Unix()}
}

func TestVerifySessionTokenAcceptsShopifysToken(t *testing.T) {
	claims, err := VerifySessionToken(sessionToken(t, "secret", validClaims()), "client-id", "secret", issued)
	if err != nil || claims.Shop() != "example-shop.myshopify.com" {
		t.Fatalf("claims %+v err %v", claims, err)
	}
}

func TestVerifySessionTokenRejects(t *testing.T) {
	otherApp := validClaims()
	otherApp["aud"] = "other"
	expired := validClaims()
	expired["exp"] = issued.Add(-time.Minute).Unix()
	cases := map[string]string{
		"wrong secret": sessionToken(t, "not-the-secret", validClaims()),
		"other app":    sessionToken(t, "secret", otherApp),
		"expired":      sessionToken(t, "secret", expired),
		"alg none":     signedToken(t, "secret", map[string]any{"alg": "none", "typ": "HS256"}, validClaims()),
		"garbage":      "a.b",
	}
	for name, token := range cases {
		if _, err := VerifySessionToken(token, "client-id", "secret", issued); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVerifySessionTokenParsesTheAlgorithmInsteadOfMatchingText(t *testing.T) {
	// The header claims alg none but mentions HS256 in another field; a
	// substring check would be fooled.
	crafted := signedToken(t, "secret", map[string]any{"alg": "none", "kid": "HS256"}, validClaims())
	if _, err := VerifySessionToken(crafted, "client-id", "secret", issued); err == nil {
		t.Fatal("crafted alg-none token accepted")
	}
}
