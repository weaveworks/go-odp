package main

import (
	"os"
	"fmt"
	"strings"
	"flag"
	"net"
	"encoding/hex"
	"github.com/dpw/go-odp/odp"
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

func (f Flags) Parse() bool {
	f.FlagSet.Parse(f.args)
	if f.NArg() > 0 { return printErr("Excess arguments") }
	return true
}


type command struct {
	cmd func ([]string, Flags) bool
	fixedArgs int
}

func (cmd command) run(args []string, pos int) bool {
	if len(args) < pos + cmd.fixedArgs {
		return printErr("Insufficient arguments")
	}

	f := flag.NewFlagSet(strings.Join(args[:pos], " "), flag.ExitOnError)
	n := pos + cmd.fixedArgs
	return cmd.cmd(args[pos:n], Flags{f, args[n:]})
}


type possibleSubcommands struct {
	command commandDispatch
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
		command{listDatapaths, 0},
		subcommands {
			"create": command{createDatapath, 1},
			"delete": command{deleteDatapath, 1},
		},
	},
	"vport": subcommands {
		"create": subcommands {
			"netdev": command{createNetdevVport, 2},
			"internal": command{createInternalVport, 2},
			"vxlan" : command{createVxlanVport, 2},
		},
		"delete": command{deleteVport, 1},
		"list": command{listVports, 1},
	},
	"flow": subcommands {
		"create": command{createFlow, 1},
		"delete": command{deleteFlow, 1},
		"list": command{listFlows, 1},
	},
}

func main() {
	if (!commands.run(os.Args, 1)) {
		os.Exit(1)
	}
}

func createDatapath(args []string, f Flags) bool {
	if !f.Parse() { return false }

	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	_, err = dpif.CreateDatapath(args[0])
	if err != nil { return printErr("%s", err) }

	return true
}

func deleteDatapath(args []string, f Flags) bool {
	if !f.Parse() { return false }

	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil { return printErr("%s", err) }

	if odp.IsNoSuchDatapathError(err) {
		return printErr("Cannot find datapath \"%s\"", args[0]);
	}

	err = dp.Delete()
	if err != nil { return printErr("%s", err) }

	return true
}

func listDatapaths(_ []string, f Flags) bool {
	if !f.Parse() { return false }

	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dps, err := dpif.EnumerateDatapaths()
	for name := range(dps) {
		fmt.Printf("%s\n", name)
	}

	return true
}

func createNetdevVport(args []string, f Flags) bool {
	if !f.Parse() { return false }
	return createVport(args[0], odp.NewNetdevVportSpec(args[1]))
}

func createInternalVport(args []string, f Flags) bool {
	if !f.Parse() { return false }
	return createVport(args[0], odp.NewInternalVportSpec(args[1]))
}

func createVxlanVport(args []string, f Flags) bool {
	var destPort uint
	// default taken from ovs/lib/netdev-vport.c
	f.UintVar(&destPort, "destport", 4789, "destination UDP port number")
	if !f.Parse() { return false }

	if destPort > 65535 {
		return printErr("destport too large")
	}

	return createVport(args[0], odp.NewVxlanVportSpec(args[1], uint16(destPort)))
}

func createVport(dpname string, spec odp.VportSpec) bool {
	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(dpname)
	if err != nil { return printErr("%s", err) }

	_, err = dp.CreateVport(spec)
	if err != nil { return printErr("%s", err) }

	return true
}

func deleteVport(args []string, f Flags) bool {
	if !f.Parse() { return false }

	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	vport, err := dpif.LookupVport(args[0])
	if err != nil {
		if odp.IsNoSuchVportError(err) {
			return printErr("Cannot find port \"%s\"", args[0]);
		}

		return printErr("%s", err)
	}

	err = vport.Handle.Delete()
	if err != nil { return printErr("%s", err) }

	return true
}

func listVports(args []string, f Flags) bool {
	if !f.Parse() { return false }

	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil { return printErr("%s", err) }

	vports, err := dp.EnumerateVports()
	for _, vport := range(vports) {
		spec := vport.Spec
		fmt.Printf("%s %s", spec.TypeName(), spec.Name())

		switch spec := spec.(type) {
		case odp.VxlanVportSpec:
			fmt.Printf(" --destport=%d", spec.DestPort)
			break
		}

		fmt.Printf("\n")
	}

	return true
}

func parseMAC(s string) (mac [6]byte, err error) {
	hwa, err := net.ParseMAC(s)
	if err != nil { return }

	if len(hwa) != 6 {
		err = fmt.Errorf("invalid MAC address: %s", s)
		return
	}

	copy(mac[:], hwa)
	return
}

func parseIpv4(s string) (res [4]byte, err error) {
	ip := net.ParseIP(s)
	if ip != nil { ip = ip.To4() }
	if ip == nil || len(ip) != 4 {
		err = fmt.Errorf("invalid IPv4 address \"%s\"", s)
	} else {
		copy(res[:], ip)
	}
	return
}

func ipv4ToString(ip [4]byte) string {
	return net.IP(ip[:]).To4().String()
}

func parseTunnelId(s string) (res [8]byte, err error) {
	x, err := hex.DecodeString(s)
	if err != nil { return }

	if len(x) == 8 {
		copy(res[:], x)
	} else {
		err = fmt.Errorf("invalid tunnel Id \"%s\"", s)
	}

	return
}

func flagsToFlowSpec(f Flags, dpif *odp.Dpif) (odp.FlowSpec, bool) {
	flow := odp.NewFlowSpec()

	var ethSrc, ethDst string
	f.StringVar(&ethSrc, "ethsrc", "", "key: ethernet source MAC")
	f.StringVar(&ethDst, "ethdst", "", "key: ethernet destination MAC")

	var setTunId, setTunIpv4Src, setTunIpv4Dst string
	var setTunTos, setTunTtl int
	var setTunDf, setTunCsum bool
	f.StringVar(&setTunId, "set-tunnel-id", "", "action: set tunnel ID")
	f.StringVar(&setTunIpv4Src, "set-tunnel-ipv4-src", "", "action: set tunnel ipv4 source address")
	f.StringVar(&setTunIpv4Dst, "set-tunnel-ipv4-dst", "", "action: set tunnel ipv4 destination address")
	f.IntVar(&setTunTos, "set-tunnel-tos", -1, "action: set tunnel ToS")
	f.IntVar(&setTunTtl, "set-tunnel-ttl", -1, "action: set tunnel TTL")
	f.BoolVar(&setTunDf, "set-tunnel-df", false, "action: set tunnel DF")
	f.BoolVar(&setTunCsum, "set-tunnel-csum", false, "action: set tunnel checksum")

	var output string
	f.StringVar(&output, "output", "", "action: output to vport")

	if !f.Parse() { return flow, false }

	if (ethSrc != "") != (ethDst != "") {
		return flow, printErr("Must supply both 'ethsrc' and 'ethdst' options")
	}

	if ethSrc != "" {
		src, err := parseMAC(ethSrc)
		if err != nil { return flow, printErr("%s", err) }
		dst, err := parseMAC(ethDst)
		if err != nil { return flow, printErr("%s", err) }

		flow.AddKey(odp.NewEthernetFlowKey(src, dst))
	}

	// Actions are ordered, but flags aren't.  As a temporary
	// hack, we already put SET actions before an OUTPUT action.

	if setTunIpv4Src != "" || setTunIpv4Dst != "" {
		var ta odp.TunnelAttrs
		var err error

		if setTunId != "" {
			ta.TunnelId, err = parseTunnelId(setTunId)
			if err != nil { return flow, printErr("%s", err) }
			ta.TunnelIdPresent = true
		}

		if setTunIpv4Src != "" {
			ta.Ipv4Src, err = parseIpv4(setTunIpv4Src)
			if err != nil { return flow, printErr("%s", err) }
			ta.Ipv4SrcPresent = true
		}

		if setTunIpv4Dst != "" {
			ta.Ipv4Dst, err = parseIpv4(setTunIpv4Dst)
			if err != nil { return flow, printErr("%s", err) }
			ta.Ipv4DstPresent = true
		}

		if setTunTos > 0 {
			ta.Tos = uint8(setTunTos)
			ta.TosPresent = true
		}

		if setTunTtl > 0 {
			ta.Ttl = uint8(setTunTtl)
			ta.TtlPresent = true
		}

		ta.Df = setTunDf
		ta.Csum = setTunCsum

		flow.AddAction(odp.SetTunnelAction{ta})
	}

	if output != "" {
		vport, err := dpif.LookupVport(output)
		if err != nil { return flow, printErr("%s", err) }
		flow.AddAction(odp.NewOutputAction(vport.Handle))
	}

	return flow, true
}

func createFlow(args []string, f Flags) bool {
	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	flow, ok := flagsToFlowSpec(f, dpif)
	if !ok { return false }

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil { return printErr("%s", err) }

	err = dp.CreateFlow(flow)
	if err != nil { return printErr("%s", err) }

	return true
}

func deleteFlow(args []string, f Flags) bool {
	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	flow, ok := flagsToFlowSpec(f, dpif)
	if !ok { return false }

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil { return printErr("%s", err) }

	err = dp.DeleteFlow(flow)
	if err != nil { return printErr("%s", err) }

	return true
}

func listFlows(args []string, f Flags) bool {
	if !f.Parse() { return false }

	dpif, err := odp.NewDpif()
	if err != nil { return printErr("%s", err) }
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil { return printErr("%s", err) }

	flows, err := dp.EnumerateFlows()
	if err != nil { return printErr("%s", err) }

	for _, flow := range(flows) {
		if !printFlow(flow, dp, args[0]) { return false }
	}

	return true
}

func printFlow(flow odp.FlowSpec, dp odp.DatapathHandle, dpname string) bool {
	os.Stdout.WriteString(dpname)

	for _, fk := range(flow.FlowKeys) {
		if fk.Ignored() { continue }

		switch fk := fk.(type) {
		case odp.EthernetFlowKey:
			s := fk.EthSrc()
			d := fk.EthDst()
			fmt.Printf(" --ethsrc=%s --ethdst=%s",
				net.HardwareAddr(s[:]),
				net.HardwareAddr(d[:]))
			break

		default:
			fmt.Printf("%v", fk)
			break
		}
	}

	for _, a := range(flow.Actions) {
		switch a := a.(type) {
		case odp.OutputAction:
			if (!printOutputAction(a, dp, dpname)) {
				return false
			}
			break

		case odp.SetTunnelAction:
			printSetTunnelAction(a)
			break

		default:
			fmt.Printf("%v", a)
			break
		}
	}

	os.Stdout.WriteString("\n")
	return true
}

func printOutputAction(a odp.OutputAction, dp odp.DatapathHandle, dpname string) bool {
	vport, err := a.VportHandle(dp).Lookup()
	if err != nil {
		if !odp.IsNoSuchVportError(err) {
			return printErr("%s", err)
		}

		// No vport with the port number in the flow, so just
		// show the number
		fmt.Printf(" --output=%d", a)
	} else {
		fmt.Printf(" --output=%s", vport.Spec.Name())
	}

	return true
}

func printSetTunnelAction(a odp.SetTunnelAction) {
	var ta odp.TunnelAttrs = a.TunnelAttrs

	if ta.TunnelIdPresent {
		fmt.Printf(" --set-tunnel-id=%s", hex.EncodeToString(ta.TunnelId[:]))
	}

	if ta.Ipv4SrcPresent {
		fmt.Printf(" --set-tunnel-ipv4-src=%s", ipv4ToString(ta.Ipv4Src))
	}

	if ta.Ipv4DstPresent {
		fmt.Printf(" --set-tunnel-ipv4-dst=%s", ipv4ToString(ta.Ipv4Dst))
	}

	if ta.TosPresent {
		fmt.Printf(" --set-tunnel-tos=%d", ta.Tos)
	}

	if ta.TtlPresent {
		fmt.Printf(" --set-tunnel-ttl=%d", ta.Ttl)
	}

	if ta.Df {
		fmt.Printf(" --set-tunnel-df")
	}

	if ta.Csum {
		fmt.Printf(" --set-tunnel-csum")
	}
}
