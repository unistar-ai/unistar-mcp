package utils

import (
	"io"
	"os"

	"github.com/STARRY-S/simple-logrus-formatter/pkg/formatter"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
	"golang.org/x/term"
)

var (
	GitCommit = "head"
	Version   = "dev"
)

// SetupLogrus configures logrus output. When stdio is true the process speaks
// the MCP protocol over stdout, so ALL log levels must be routed to stderr to
// avoid corrupting the JSON-RPC stream. When logFile is non-empty, logs are
// also appended to that file — useful in stdio mode, where the MCP host
// captures stderr and the operator otherwise cannot see the logs.
func SetupLogrus(hideTime bool, stdio bool, logFile string) {
	formatter := &formatter.Formatter{
		HideKeys:        false,
		TimestampFormat: "15:04:05", // hour, time, sec only
		FieldsOrder:     []string{},
	}
	if hideTime {
		formatter.TimestampFormat = "-"
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) { //nolint: gosec
		// Disable if the output is not terminal.
		formatter.NoColors = true
	}
	logrus.SetFormatter(formatter)
	logrus.SetOutput(io.Discard)
	logrus.AddHook(&writer.Hook{
		// Send logs with level higher than warning to stderr.
		Writer: os.Stderr,
		LogLevels: []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
		},
	})

	// In stdio mode stdout is reserved for the MCP protocol, so info/debug/trace
	// logs go to stderr as well; otherwise they go to stdout.
	lowLevelWriter := os.Stdout
	if stdio {
		lowLevelWriter = os.Stderr
	}
	logrus.AddHook(&writer.Hook{
		Writer: lowLevelWriter,
		LogLevels: []logrus.Level{
			logrus.TraceLevel,
			logrus.InfoLevel,
			logrus.DebugLevel,
		},
	})

	// An explicit log file gets every level appended to it, so the operator
	// has a durable, tail-able copy even when the MCP host swallows stderr.
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			logrus.Warnf("failed to open log file %q (logging to stderr only): %s", logFile, err)
		} else {
			logrus.AddHook(&writer.Hook{
				Writer:    f,
				LogLevels: logrus.AllLevels,
			})
		}
	}
}
