package cmd

import (
	"github.com/bxnlabs/ts-auth-server/pkg/server"
	"github.com/spf13/cobra"
)

func Execute() error {
	s := server.Server{}

	rootCmd := &cobra.Command{
		Use:   "ts-auth-server [flags] tailnet",
		Short: "A lightweight authentication server for Tailscale.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := s.Run(); err != nil {
				cmd.PrintErrln("Error:", err)
			}
		},
	}
	rootCmd.Flags().StringVarP(&s.BindAddr, "bind-addr", "a", "127.0.0.1", "Address to bind the server to")
	rootCmd.Flags().IntVarP(&s.BindPort, "port", "p", 9000, "Port to listen on")
	rootCmd.Flags().StringVarP(&s.Hostname, "hostname", "H", "auth-server", "Hostname for the server")
	rootCmd.Flags().StringVarP(&s.StateDir, "state-dir", "d", "/var/run/ts-auth-server", "Directory to store state in")

	return rootCmd.Execute()
}
