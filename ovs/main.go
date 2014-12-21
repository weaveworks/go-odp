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

func (f Flags) CheckNArg(min int, max int) bool {
	if f.NArg() < min {
		return printErr("Insufficient arguments")
	}

	if f.NArg() > max {
		return printErr("Excess arguments")
	}

	return true
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
	"vport": subcommands {
		"create": subcommands {
			"internal": command(createInternalVport),
			"vxlan" : command(createVxlanVport),
		},
		"delete": command(deleteVport),
		"list": command(listVports),
	},
}

func main() {
	if (!commands.run(os.Args, 1)) {
		os.Exit(1)
	}
}

func createDatapath(f Flags) bool {
	f.Parse()
	if !f.CheckNArg(1, 1) { return false }

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	_, err = dpif.CreateDatapath(f.Arg(0))
	if err != nil { return printErr("%s", err) }

	return true
}

func deleteDatapath(f Flags) bool {
	f.Parse()
	if !f.CheckNArg(1, 1) { return false }

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	name := f.Arg(0)
	dp, err := dpif.LookupDatapath(name)
	if err != nil { return printErr("%s", err) }

	if dp == nil {
		return printErr("Cannot find datapath \"%s\"", name);
	}

	err = dp.Delete()
	if err != nil { return printErr("%s", err) }

	return true
}

func listDatapaths(f Flags) bool {
	f.Parse()
	if !f.CheckNArg(0, 0) { return false }

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dps, err := dpif.EnumerateDatapaths()
	for name := range(dps) {
		fmt.Printf("%s\n", name)
	}

	return true
}

func createInternalVport(f Flags) bool {
	f.Parse()
	return createVport(f, openvswitch.INTERNAL_VPORT_SPEC)
}

func createVxlanVport(f Flags) bool {
	var destPort uint
	// default taken from ovs/lib/netdev-vport.c
	f.UintVar(&destPort, "destport", 4789, "destination UDP port number")
	f.Parse()

	if destPort > 65535 {
		return printErr("destport too large")
	}

	return createVport(f, openvswitch.NewVxlanVportSpec(uint16(destPort)))
}

func createVport(f Flags, spec openvswitch.VportSpec) bool {
	if !f.CheckNArg(2, 2) { return false }

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(f.Arg(0))
	if err != nil { return printErr("%s", err) }

	_, err = dp.CreateVport(f.Arg(1), spec)
	if err != nil { return printErr("%s", err) }

	return true
}

func deleteVport(f Flags) bool {
	f.Parse()
	if !f.CheckNArg(1, 1) { return false }

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	name := f.Arg(0)
	vport, err := dpif.LookupVport(name)
	if err != nil { return printErr("%s", err) }

	if vport == nil {
		return printErr("Cannot find port \"%s\"", name);
	}

	err = vport.Delete()
	if err != nil { return printErr("%s", err) }

	return true
}

func listVports(f Flags) bool {
	f.Parse()
	if !f.CheckNArg(1, 1) { return false }

	dpif, err := openvswitch.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(f.Arg(0))
	if err != nil { return printErr("%s", err) }

	vports, err := dp.EnumerateVports()
	for name := range(vports) {
		fmt.Printf("%s\n", name)
	}

	return true
}
