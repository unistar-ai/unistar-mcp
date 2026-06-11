package commands

import (
	"github.com/STARRY-S/unistar-mcp/pkg/server"
	"github.com/STARRY-S/unistar-mcp/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func Execute(args []string) error {
	cmd := newServerCmd()
	cmd.addCommands()
	cmd.cmd.SetArgs(args)

	_, err := cmd.cmd.ExecuteC()
	if err != nil {
		if signalContext.Err() != nil {
			return signalContext.Err()
		}
		return err
	}
	return nil
}

type serverCmd struct {
	*baseCmd
}

func newServerCmd() *serverCmd {
	cc := &serverCmd{}

	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:   "unistar-mcp",
		Short: "Start the unistar-mcp CI workflow helper server (stdio mode).",
		PreRun: func(cmd *cobra.Command, args []string) {
			// stdio mode: stdout carries the MCP protocol, so all logs to stderr.
			utils.SetupLogrus(cc.hideLogTime, true)
			if cc.debug {
				logrus.SetLevel(logrus.DebugLevel)
				logrus.Debugf("Debug output enabled")
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := server.New(server.Options{
				LazyLoading: cc.lazyLoading,
			})
			return srv.StartStdio()
		},
	})
	cc.cmd.Version = getVersion()
	cc.cmd.SilenceUsage = true
	cc.cmd.SilenceErrors = true

	flags := cc.cmd.PersistentFlags()
	flags.BoolVarP(&cc.baseCmd.debug, "debug", "", false, "enable debug output")
	flags.BoolVarP(&cc.baseCmd.lazyLoading, "lazy", "", false,
		"expose lazy-loading meta tools (tool_list/tool_describe/tool_call) instead of full tool schemas")

	return cc
}

func (cc *serverCmd) getCommand() *cobra.Command {
	return cc.cmd
}

func (cc *serverCmd) addCommands() {
	addCommands(
		cc.cmd,
		newHTTPCmd(),
		newVersionCmd(),
	)
}
