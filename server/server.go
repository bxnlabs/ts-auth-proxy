package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"golang.org/x/sync/errgroup"
	"tailscale.com/tsnet"
)

const (
	HeaderTailscaleRemoteAddr = "Tailscale-Remote-Addr"
	HeaderTailscaleRemotePort = "Tailscale-Remote-Port"
	HeaderTailscaleUserAvatar = "Tailscale-User-Avatar"
	HeaderTailscaleUserLogin  = "Tailscale-User-Login"
	HeaderTailscaleUserName   = "Tailscale-User-Name"

	serverShutdownGracePeriod = 30 * time.Second
)

type userProfile struct {
	Avatar string
	Login  string
	Name   string
}

type cache struct {
	client *ristretto.Cache[string, *userProfile]
}

func (c *cache) get(_ context.Context, addr string) (*userProfile, error) {
	profile, ok := c.client.Get(addr)
	if !ok {
		return nil, fmt.Errorf("addr not found: %s", addr)
	}
	return profile, nil
}

func (c *cache) set(_ context.Context, addr string, profile *userProfile, expiry time.Duration) error {
	c.client.SetWithTTL(addr, profile, 1, expiry)
	return nil
}

func newCache(maxTokens int64) (*cache, error) {
	client, err := ristretto.NewCache(&ristretto.Config[string, *userProfile]{
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

func gracefulShutdown(ctx context.Context, svr *http.Server) error {
	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownGracePeriod)
	defer cancel()
	return svr.Shutdown(ctx)
}

type Server struct {
	CacheExpiry time.Duration
	CacheSize   int64
	ControlURL  string
	Hostname    string
	StateDir    string
	TrustedCIDR string
	Upstream    *url.URL
}

func (p *Server) Run() error {
	// Parse the trusted CIDR ranges
	var trustedCIDRs []netip.Prefix
	for _, cidr := range strings.Split(p.TrustedCIDR, ",") {
		trustedCIDRs = append(trustedCIDRs, netip.MustParsePrefix(cidr))
	}

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
	defer func() {
		_ = ts.Close()
	}()

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

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Parse remote address from headers
		remoteHost := r.Header.Get(HeaderTailscaleRemoteAddr)
		remotePort := r.Header.Get(HeaderTailscaleRemotePort)
		if remoteHost == "" || remotePort == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		remoteAddr, err := netip.ParseAddrPort(net.JoinHostPort(remoteHost, remotePort))
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// If the remote address is within the trusted CIDR range, allow access
		for _, cidr := range trustedCIDRs {
			if cidr.Contains(remoteAddr.Addr()) {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		// Get user profile from cache if available
		var profile *userProfile
		profile, err = cache.get(r.Context(), remoteHost)
		// Fallback to tailscale if cache miss
		if err != nil {
			// Fetch user info from tailscale
			info, err := tsCli.WhoIs(r.Context(), remoteAddr.String())
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
			_ = cache.set(r.Context(), remoteHost, profile, p.CacheExpiry)
		}

		// Set headers
		h := w.Header()
		h.Set(HeaderTailscaleUserAvatar, profile.Avatar)
		h.Set(HeaderTailscaleUserLogin, profile.Login)
		h.Set(HeaderTailscaleUserName, profile.Name)
	})

	g, ctx := errgroup.WithContext(context.Background())
	var httpHandler http.Handler = mux

	svr := http.Server{Handler: httpHandler}
	g.Go(func() error {
		if err := svr.ListenAndServe(); err != nil {
			return fmt.Errorf("failed to serve HTTP: %v", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := gracefulShutdown(ctx, &svr); err != nil {
			return fmt.Errorf("failed to shutdown HTTP server: %v", err)
		}
		return nil
	})

	return g.Wait()
}
