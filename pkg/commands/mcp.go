package commands

import (
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
		Use:   "retro-mcp",
		Short: "Advanced CI Workflow MCP Helper Agent.",
		Run: func(cmd *cobra.Command, args []string) {
			// TODO: Execute github action workflow MCP server here in default entrypoiunt
		},
	})
	cc.cmd.Version = getVersion()
	cc.cmd.SilenceUsage = true
	cc.cmd.SilenceErrors = true

	flags := cc.cmd.PersistentFlags()
	flags.BoolVarP(&cc.baseCmd.debug, "debug", "", false, "enable debug output")

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
