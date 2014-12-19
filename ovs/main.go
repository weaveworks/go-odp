package main

import (
	"os"
	"fmt"
	"strings"
	"github.com/dpw/go-openvswitch/openvswitch"
)

func die(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, f, a...)
	os.Stderr.WriteString("\n")
	os.Exit(1)
}

type commandDispatch interface {
	run(args []string, pos int)
}

type command func (args []string)

func (cmd command) run(args []string, pos int) {
	cmd(args[pos:])
}

type commandMap map[string]commandDispatch

func (cm commandMap) run(args []string, pos int) {
	if pos >= len(args) {
		die("Subcommand required by \"%s\".  Try \"%s help\"", strings.Join(args[:pos], " "), os.Args[0])
	}

	cd, ok := cm[args[pos]]

	if !ok {
		die("Unknown command \"%s\".  Try \"%s help\"", strings.Join(args[:pos + 1], " "), os.Args[0])
	}

	cd.run(args, pos + 1)
}

var commands = commandMap {
	"datapath": commandMap {
		"create": command(createDatapath),
		"delete": command(deleteDatapath),
	},
}

func main() {
	commands.run(os.Args, 1)
}


func createDatapath(args []string) {
	dpif, err := openvswitch.NewDpif()
	if err != nil {
		panic(err)
	}

	for _, name := range(args) {
		_, err = dpif.CreateDatapath(name)
		if err != nil {
			panic(err)
		}
	}

	err = dpif.Close()
	if err != nil {
		panic(err)
	}
}

func deleteDatapath(args []string) {
	dpif, err := openvswitch.NewDpif()
	if err != nil {
		panic(err)
	}

	for _, name := range(args) {
		dp, err := dpif.LookupDatapath(name)
		if err != nil {
			panic(err)
		}

		err = dp.Delete()
		if err != nil {
			panic(err)
		}
	}

	err = dpif.Close()
	if err != nil {
		panic(err)
	}
}
