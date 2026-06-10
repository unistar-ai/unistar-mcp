package commands

import (
	"github.com/STARRY-S/retro-mcp/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type httpCmd struct {
	*baseCmd
}

func newHTTPCmd() *httpCmd {
	cc := &httpCmd{}

	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:   "http",
		Short: "Run MCP Server in HTTP mode",
		Long:  "",
		PreRun: func(cmd *cobra.Command, args []string) {
			utils.SetupLogrus(cc.hideLogTime)
			if cc.debug {
				logrus.SetLevel(logrus.DebugLevel)
				logrus.Debugf("Debug output enabled")
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc.cmd.Help()
			return nil
		},
	})

	// flags := cc.baseCmd.cmd.Flags()

	addCommands(cc.cmd)
	return cc
}
