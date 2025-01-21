package main

import (
	"net/url"
	"time"

	"github.com/bxnlabs/ts-auth-proxy/proxy"
	"github.com/spf13/cobra"
	"tailscale.com/ipn"
)

func main() {
	p := proxy.Proxy{}

	rootCmd := &cobra.Command{
		Use:   "ts-auth-proxy [flags] <upstream>",
		Short: "A lightweight authentication server for Tailscale.",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				cmd.PrintErrln("Error: exactly one upstream URL required")
				return
			}
			upstream, err := url.Parse(args[0])
			if err != nil {
				cmd.PrintErrln("Error: could not parse upstream URL:", err)
				return
			}
			p.Upstream = upstream
			if err := p.Run(); err != nil {
				cmd.PrintErrln("Error:", err)
			}
		},
	}
	rootCmd.Flags().StringVarP(&p.BindAddr, "bind-addr", "a", "127.0.0.1", "Address to bind the proxy to")
	rootCmd.Flags().IntVarP(&p.BindPort, "port", "p", 9000, "Port to listen on")
	rootCmd.Flags().Int64VarP(&p.CacheSize, "cache-size", "s", 1000, "Maximum number of entries in the cache")
	rootCmd.Flags().DurationVarP(&p.CacheExpiry, "cache-expiry", "e", 10*time.Minute, "Time after which cache entries expire")
	rootCmd.Flags().StringVarP(&p.ControlURL, "control-url", "c", ipn.DefaultControlURL, "URL for Tailscale control server")
	rootCmd.Flags().StringVarP(&p.Hostname, "hostname", "H", "auth-server", "Hostname for proxy on Tailnet")
	rootCmd.Flags().StringVarP(&p.StateDir, "state-dir", "d", "/var/run/ts-auth-proxy", "Directory to store state in")
	rootCmd.Flags().StringVar(&p.TLSCertFile, "tls-cert", "", "Path to TLS certificate file")
	rootCmd.Flags().StringVar(&p.TLSKeyFile, "tls-key", "", "Path to TLS key file")

	_ = rootCmd.Execute()
}
