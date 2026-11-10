package keycloak

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwksResponse struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

type JWKSCache struct {
	url  string
	http *http.Client
	ttl  time.Duration

	mu          sync.RWMutex
	keys        map[string]*rsa.PublicKey
	nextRefresh time.Time
}

func NewJWKSCache(jwksURL string, ttl time.Duration) *JWKSCache {
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &JWKSCache{
		url:  jwksURL,
		http: &http.Client{Timeout: 10 * time.Second},
		ttl:  ttl,
		keys: map[string]*rsa.PublicKey{},
	}
}

func (c *JWKSCache) Keyfunc(ctx context.Context) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		// method check можно оставить тут тоже
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}

		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("missing kid in token header")
		}

		if err := c.ensure(ctx); err != nil {
			return nil, err
		}

		c.mu.RLock()
		pub := c.keys[kid]
		c.mu.RUnlock()

		if pub == nil {
			// попробуем принудительно обновить один раз
			if err := c.forceRefresh(ctx); err != nil {
				return nil, err
			}
			c.mu.RLock()
			pub = c.keys[kid]
			c.mu.RUnlock()
		}

		if pub == nil {
			return nil, fmt.Errorf("unknown kid: %s", kid)
		}
		return pub, nil
	}
}

func (c *JWKSCache) ensure(ctx context.Context) error {
	c.mu.RLock()
	ok := time.Now().Before(c.nextRefresh) && len(c.keys) > 0
	c.mu.RUnlock()
	if ok {
		return nil
	}
	return c.forceRefresh(ctx)
}

func (c *JWKSCache) forceRefresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// double-check
	if time.Now().Before(c.nextRefresh) && len(c.keys) > 0 {
		// In force refresh, we want to bypass TTL check unless we JUST refreshed.
		// Let's assume we don't spam Keycloak more than once every 5 seconds.
		if time.Since(c.nextRefresh.Add(-c.ttl)) < 5*time.Second {
			return nil
		}
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", c.url, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("jwks fetch failed: status=%d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("jwks decode: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" || k.Kid == "" {
			continue
		}
		pub, err := rsaPublicKeyFromNE(k.N, k.E)
		if err != nil {
			continue
		}
		newKeys[k.Kid] = pub
	}

	if len(newKeys) == 0 {
		return fmt.Errorf("jwks: no usable rsa keys found")
	}

	c.keys = newKeys
	c.nextRefresh = time.Now().Add(c.ttl)
	return nil
}

func rsaPublicKeyFromNE(nB64, eB64 string) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}

	n := new(big.Int).SetBytes(nb)

	e := 0
	for _, b := range eb {
		e = e<<8 + int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("invalid exponent")
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}
