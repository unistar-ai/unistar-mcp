package commands

import (
	"github.com/STARRY-S/unistar-mcp/pkg/server"
	"github.com/STARRY-S/unistar-mcp/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type httpCmd struct {
	*baseCmd
	address string
}

func newHTTPCmd() *httpCmd {
	cc := &httpCmd{}

	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:   "http",
		Short: "Run the unistar-mcp server in Streamable HTTP mode",
		Long:  "Run the unistar-mcp server over Streamable HTTP, exposing the MCP endpoint at /mcp.",
		PreRun: func(cmd *cobra.Command, args []string) {
			utils.SetupLogrus(cc.hideLogTime, false)
			if cc.debug {
				logrus.SetLevel(logrus.DebugLevel)
				logrus.Debugf("Debug output enabled")
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := server.New(server.Options{
				Address:     cc.address,
				LazyLoading: cc.lazyLoading,
			})
			return srv.StartHTTP(signalContext)
		},
	})

	flags := cc.cmd.Flags()
	flags.StringVarP(&cc.address, "address", "a", ":8080", "listen address for HTTP mode")

	return cc
}
