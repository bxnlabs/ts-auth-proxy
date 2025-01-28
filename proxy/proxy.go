package proxy

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"golang.org/x/sync/errgroup"
	"tailscale.com/tsnet"
)

const (
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

func redirectToHttps() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		url := *r.URL
		url.Scheme = "https"
		url.Host = r.Host
		http.Redirect(w, r, url.String(), http.StatusPermanentRedirect)
	})
}

func gracefulShutdown(ctx context.Context, svr *http.Server) error {
	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownGracePeriod)
	defer cancel()
	return svr.Shutdown(ctx)
}

type Proxy struct {
	CacheExpiry time.Duration
	CacheSize   int64
	ControlURL  string
	Hostname    string
	StateDir    string
	TLSCertFile string
	TLSKeyFile  string
	Upstream    *url.URL
}

type wrappedResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *wrappedResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *wrappedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("upstream ResponseWriter does not implement http.Hijacker")
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

		// Create the wrapper response writer
		ww := &wrappedResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Output access log with status code
		defer func() {
			login := "unknown"
			if profile != nil {
				login = profile.Login
			}
			log.Printf("%s - %s [%s] \"%s %s %s\" %d \"%s\"\n",
				r.RemoteAddr,
				login,
				time.Now().Format("02/Jan/2006:15:04:05 -0700"),
				r.Method,
				r.URL,
				r.Proto,
				ww.statusCode,
				r.UserAgent(),
			)
		}()

		// Get user profile from cache if available
		profile, err = cache.get(r.Context(), r.RemoteAddr)
		// Fallback to tailscale if cache miss
		if err != nil {
			// Fetch user info from tailscale
			info, err := tsCli.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil {
				ww.WriteHeader(http.StatusUnauthorized)
				return
			}

			// Tagged nodes don't identify a user.
			if info.Node.IsTagged() {
				ww.WriteHeader(http.StatusForbidden)
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

		// Proxy to upstream using wrapped writer
		proxy.ServeHTTP(ww, r)
	})

	g, ctx := errgroup.WithContext(context.Background())
	var httpHandler http.Handler = mux

	// Start the HTTPS server if TLS cert and key are provided
	if p.TLSCertFile != "" && p.TLSKeyFile != "" {
		// When TLS cert and key are provided, simply redirect HTTP to HTTPS
		httpHandler = redirectToHttps()
		ln, err := ts.Listen("tcp", ":443")
		if err != nil {
			return fmt.Errorf("failed to listen on port 443: %v", err)
		}
		svr := &http.Server{Handler: mux}
		g.Go(func() error {
			if err := svr.ServeTLS(ln, p.TLSCertFile, p.TLSKeyFile); err != nil {
				return fmt.Errorf("failed to serve TLS: %v", err)
			}
			return nil
		})
		g.Go(func() error {
			if err := gracefulShutdown(ctx, svr); err != nil {
				return fmt.Errorf("failed to shutdown HTTPS server: %v", err)
			}
			return nil
		})
	}

	// Start the HTTP server
	ln, err := ts.Listen("tcp", ":80")
	if err != nil {
		return fmt.Errorf("failed to listen on port 80: %v", err)
	}
	svr := http.Server{Handler: httpHandler}
	g.Go(func() error {
		if err := http.Serve(ln, httpHandler); err != nil {
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
