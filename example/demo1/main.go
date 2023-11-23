package main

import (
	"log"
	"os"

	"mlib.com/cli"
)

type fooCommand struct {
}

func (c *fooCommand) Help() string {
	return "foo"
}

func (c *fooCommand) Run(args []string) int {
	return 0
}
func (c *fooCommand) Synopsis() string {
	return "foo"
}

type barCommand struct {
}

func (c *barCommand) Help() string {
	return "foo"
}

func (c *barCommand) Run(args []string) int {
	return 0
}
func (c *barCommand) Synopsis() string {
	return "foo"
}

func fooCommandFactory() (cli.Command, error) {
	return &fooCommand{}, nil
}

func barCommandFactory() (cli.Command, error) {
	return &barCommand{}, nil
}

func main() {
	c := cli.NewCLI("app", "1.0.0")
	c.Args = os.Args[1:]
	c.Commands = map[string]cli.CommandFactory{
		"foo": fooCommandFactory,
		"bar": barCommandFactory,
	}

	exitStatus, err := c.Run()
	if err != nil {
		log.Println(err)
	}

	os.Exit(exitStatus)
}
