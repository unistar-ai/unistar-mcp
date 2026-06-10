package main

import (
	"os"

	"github.com/STARRY-S/unistar-mcp/pkg/commands"
	"github.com/sirupsen/logrus"
)

func main() {
	if err := commands.Execute(os.Args[1:]); err != nil {
		logrus.Fatal(err)
	}
}
