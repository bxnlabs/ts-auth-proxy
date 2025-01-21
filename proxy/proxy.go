package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/dgraph-io/ristretto"
	"tailscale.com/tsnet"
)

const (
	HeaderTailscaleUserAvatar = "Tailscale-User-Avatar"
	HeaderTailscaleUserLogin  = "Tailscale-User-Login"
	HeaderTailscaleUserName   = "Tailscale-User-Name"
)

type userProfile struct {
	Avatar string
	Login  string
	Name   string
}

type cache struct {
	client *ristretto.Cache
}

func (c *cache) get(_ context.Context, addr string) (*userProfile, error) {
	data, ok := c.client.Get(addr)
	if !ok {
		return nil, fmt.Errorf("addr not found: %s", addr)
	}
	profile, ok := data.(*userProfile)
	if !ok {
		return nil, fmt.Errorf("unexpected data type: %T", data)
	}
	return profile, nil
}

func (c *cache) set(_ context.Context, addr string, profile *userProfile, expiry time.Duration) error {
	c.client.SetWithTTL(addr, profile, 1, expiry)
	return nil
}

func newCache(maxTokens int64) (*cache, error) {
	client, err := ristretto.NewCache(&ristretto.Config{
		// Authors recommend setting NumCounters to 10x the number of items
		// we expect to keep in the cache when full
		// See: https://github.com/dgraph-io/ristretto/blob/65472b1ba6fd5d37f34b3d6f807b47fe3b1f4b6d/cache.go#L97
		NumCounters: maxTokens * 10,
		MaxCost:     maxTokens,
		// Authors recommend using `64` as the BufferItems value for good performance.
		// See: https://github.com/dgraph-io/ristretto/blob/65472b1ba6fd5d37f34b3d6f807b47fe3b1f4b6d/cache.go#L125
		BufferItems: 64,
	})
	if err != nil {
		return nil, err
	}
	return &cache{client: client}, nil
}

type Proxy struct {
	BindAddr    string
	BindPort    int
	CacheExpiry time.Duration
	CacheSize   int64
	ControlURL  string
	Hostname    string
	StateDir    string
	TLSCertFile string
	TLSKeyFile  string
	Upstream    *url.URL
}

func (p *Proxy) Run() error {
	// Create the state directory if it doesn't exist
	if err := os.MkdirAll(p.StateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %v", err)
	}
	// Ensure the state directory is writable
	fi, err := os.Stat(p.StateDir)
	if err != nil {
		return fmt.Errorf("failed to stat state directory: %v", err)
	}
	if fi.Mode().Perm()&0200 == 0 {
		return fmt.Errorf("state directory is not writable")
	}

	// Create tsnet server
	ts := &tsnet.Server{
		Hostname:   p.Hostname,
		Dir:        p.StateDir,
		ControlURL: p.ControlURL,
	}
	defer ts.Close()

	// Create ts local client to fetch user info
	tsCli, err := ts.LocalClient()
	if err != nil {
		return fmt.Errorf("failed to create tailscale client: %v", err)
	}

	// Initialize the in-memory cache
	cache, err := newCache(p.CacheSize)
	if err != nil {
		return fmt.Errorf("failed to create cache: %v", err)
	}

	// Create reverse proxy to upstream
	proxy := httputil.NewSingleHostReverseProxy(p.Upstream)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var profile *userProfile
		var err error

		// Get user profile from cache if available
		profile, err = cache.get(r.Context(), r.RemoteAddr)
		// Fallback to tailscale if cache miss
		if err != nil {
			// Fetch user info from tailscale
			info, err := tsCli.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// Tagged nodes don't identify a user.
			if info.Node.IsTagged() {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// Cache user profile
			profile = &userProfile{
				Avatar: info.UserProfile.ProfilePicURL,
				Login:  info.UserProfile.LoginName,
				Name:   info.UserProfile.DisplayName,
			}
			_ = cache.set(r.Context(), r.RemoteAddr, profile, p.CacheExpiry)
		}

		// Set headers
		h := r.Header
		h.Set(HeaderTailscaleUserAvatar, profile.Avatar)
		h.Set(HeaderTailscaleUserLogin, profile.Login)
		h.Set(HeaderTailscaleUserName, profile.Name)

		// Proxy to upstream
		proxy.ServeHTTP(w, r)
	})

	addr := net.JoinHostPort(p.BindAddr, fmt.Sprintf("%d", p.BindPort))
	if p.TLSCertFile != "" && p.TLSKeyFile != "" {
		return http.ListenAndServeTLS(addr, p.TLSCertFile, p.TLSKeyFile, mux)
	}
	return http.ListenAndServe(addr, mux)
}
