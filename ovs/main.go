package main

import (
	"os"
	"fmt"
	"strings"
	"flag"
	"github.com/dpw/go-openvswitch/openvswitch"
)

func printErr(f string, a ...interface{}) bool {
	fmt.Fprintf(os.Stderr, f, a...)
	os.Stderr.WriteString("\n")
	return false
}

type commandDispatch interface {
	run(args []string, pos int) bool
}


type subcommands map[string]commandDispatch

func (cmds subcommands) run(args []string, pos int) bool {
	if pos >= len(args) {
		return printErr("Subcommand required by \"%s\".  Try \"%s help\"", strings.Join(args[:pos], " "), args[0])
	}

	cd, ok := cmds[args[pos]]

	if !ok {
		return printErr("Unknown command \"%s\".  Try \"%s help\"", strings.Join(args[:pos + 1], " "), args[0])
	}

	return cd.run(args, pos + 1)
}


type Flags struct {
	*flag.FlagSet
	args []string
}

func (f Flags) Parse() {
	f.FlagSet.Parse(f.args)
}

type command func (f Flags) bool

func (fcmd command) run(args []string, pos int) bool {
	f := flag.NewFlagSet(strings.Join(args[:pos], " "), flag.ExitOnError)
	return fcmd(Flags{f, args[pos:]})
}


type possibleSubcommands struct {
	command command
	subcommands subcommands
}

func (cmds possibleSubcommands) run(args []string, pos int) bool {
	if pos >= len(args) {
		return cmds.command.run(args, pos)
	}

	return cmds.subcommands.run(args, pos)
}


var commands = subcommands {
	"datapath": possibleSubcommands {
		listDatapaths,
		subcommands {
			"create": command(createDatapath),
			"delete": command(deleteDatapath),
		},
	},
}

func main() {
	if (!commands.run(os.Args, 1)) {
		os.Exit(1)
	}
}

func createDatapath(f Flags) bool {
	f.Parse()

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	for _, name := range(f.Args()) {
		_, err = dpif.CreateDatapath(name)
		if err != nil { return printErr("%s", err) }
	}

	return true
}

func deleteDatapath(f Flags) bool {
	f.Parse()

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	for _, name := range(f.Args()) {
		dp, err := dpif.LookupDatapath(name)
		if err != nil { return printErr("%s", err) }

		if dp == nil {
			return printErr("Cannot find datapath \"%s\"", name);
		}

		err = dp.Delete()
		if err != nil { return printErr("%s", err) }
	}

	return true
}

func listDatapaths(f Flags) bool {
	f.Parse()

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	name2dp, err := dpif.EnumerateDatapaths()
	for name := range(name2dp) {
		fmt.Printf("%s\n", name)
	}

	return true
}
