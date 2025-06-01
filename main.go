package main

import (
	"time"

	"github.com/bxnlabs/ts-auth-proxy/server"
	"github.com/spf13/cobra"
	"tailscale.com/ipn"
)

func main() {
	s := server.Server{}

	rootCmd := &cobra.Command{
		Use:   "ts-auth-proxy [flags]",
		Short: "A lightweight Tailscale authentication server.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := s.Run(); err != nil {
				cmd.PrintErrln("Error:", err)
			}
		},
	}
	rootCmd.Flags().Int64VarP(&s.CacheSize, "cache-size", "s", 1000, "Maximum number of entries in the cache")
	rootCmd.Flags().DurationVarP(&s.CacheExpiry, "cache-expiry", "e", 10*time.Minute, "Time after which cache entries expire")
	rootCmd.Flags().StringVarP(&s.ControlURL, "control-url", "c", ipn.DefaultControlURL, "URL for Tailscale control server")
	rootCmd.Flags().StringVarP(&s.Hostname, "hostname", "H", "auth-server", "Hostname for proxy on Tailnet")
	rootCmd.Flags().StringVarP(&s.StateDir, "state-dir", "d", "/var/run/ts-auth-proxy", "Directory to store state in")

	_ = rootCmd.Execute()
}
