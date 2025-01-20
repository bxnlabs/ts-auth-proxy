package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

const (
	HeaderRemoteAddr              = "Remote-Addr"
	HeaderRemotePort              = "Remote-Port"
	HeaderTailscaleName           = "Tailscale-Name"
	HeaderTailscaleProfilePicture = "Tailscale-Profile-Picture"
	HeaderTailscaleUser           = "Tailscale-User"
)

type Server struct {
	BindAddr string
	BindPort int
	Hostname string
	StateDir string
}

func (s *Server) Run() error {
	// Create the state directory if it doesn't exist
	if err := os.MkdirAll(s.StateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %v", err)
	}
	// Ensure the state directory is writable
	fi, err := os.Stat(s.StateDir)
	if err != nil {
		return fmt.Errorf("failed to stat state directory: %v", err)
	}
	if fi.Mode().Perm()&0200 == 0 {
		return fmt.Errorf("state directory is not writable")
	}

	// Create tsnet server
	ts := &tsnet.Server{
		Hostname:   s.Hostname,
		Dir:        s.StateDir,
		ControlURL: ipn.DefaultControlURL,
	}
	defer ts.Close()

	tsCli, err := ts.LocalClient()
	if err != nil {
		return fmt.Errorf("failed to create tailscale client: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		remoteHost := r.Header.Get(HeaderRemoteAddr)
		remotePort := r.Header.Get(HeaderRemotePort)
		if remoteHost == "" || remotePort == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		remoteAddrStr := net.JoinHostPort(remoteHost, remotePort)
		remoteAddr, err := netip.ParseAddrPort(remoteAddrStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

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

		h := w.Header()
		h.Set(HeaderTailscaleName, info.UserProfile.DisplayName)
		h.Set(HeaderTailscaleProfilePicture, info.UserProfile.ProfilePicURL)
		h.Set(HeaderTailscaleUser, info.UserProfile.LoginName)
		w.WriteHeader(http.StatusNoContent)
	})

	addr := net.JoinHostPort(s.BindAddr, fmt.Sprintf("%d", s.BindPort))
	return http.ListenAndServe(addr, mux)
}
