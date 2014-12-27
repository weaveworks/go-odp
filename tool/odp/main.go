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

	var inPort string
	f.StringVar(&inPort, "in-port", "", "key: incoming vport")

	var ethSrc, ethDst string
	f.StringVar(&ethSrc, "eth-src", "", "key: ethernet source MAC")
	f.StringVar(&ethDst, "eth-dst", "", "key: ethernet destination MAC")

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
	f.StringVar(&output, "output", "", "action: output to vports")

	if !f.Parse() { return flow, false }

	if inPort != "" {
		vport, err := dpif.LookupVport(inPort)
		if err != nil { return flow, printErr("%s", err) }
		flow.AddKey(odp.NewInPortFlowKey(vport.Handle))
	}

	if ethSrc != "" || ethDst != "" {
		err := handleEthernetFlowKeyOptions(flow, ethSrc, ethDst)
		if err != nil { return flow, printErr("%s", err) }
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
		for _, vpname := range(strings.Split(output, ",")) {
			vport, err := dpif.LookupVport(vpname)
			if err != nil { return flow, printErr("%s", err) }
			flow.AddAction(odp.NewOutputAction(vport.Handle))
		}
	}

	return flow, true
}

func handleEthernetFlowKeyOptions(flow odp.FlowSpec, src string, dst string) (err error)  {
	var k odp.OvsKeyEthernet
	var m odp.OvsKeyEthernet

	k.EthSrc, m.EthSrc, err = handleEthernetAddrOption(src)
	if err != nil { return }
	k.EthDst, m.EthDst, err = handleEthernetAddrOption(dst)
	if err != nil { return }

	flow.AddKey(odp.NewEthernetFlowKey(k, m))
	return
}

const ETH_ALEN = odp.ETH_ALEN

func handleEthernetAddrOption(opt string) (key [ETH_ALEN]byte, mask [ETH_ALEN]byte, err error) {
	if opt != "" {
		var k, m string
		i := strings.Index(opt, "&")
		if i > 0 {
			k = opt[:i]
			m = opt[i+1:]
		} else {
			k = opt
			m = "ff:ff:ff:ff:ff:ff"
		}

		key, err = parseMAC(k)
		if err != nil { return }

		mask, err = parseMAC(m)
	}

	return
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
		case odp.InPortFlowKey:
			name, err := fk.VportHandle(dp).LookupName()
			if err != nil {
				return printErr("%s", err)
			}

			fmt.Printf(" --in-port=%s", name)
			break

		case odp.EthernetFlowKey:
			k := fk.Key()
			m := fk.Mask()
			printEthAddrOption("eth-src", k.EthSrc, m.EthSrc)
			printEthAddrOption("eth-dst", k.EthDst, m.EthDst)
			break

		default:
			fmt.Printf("%v", fk)
			break
		}
	}

	outputs := make([]string, 0)

	for _, a := range(flow.Actions) {
		switch a := a.(type) {
		case odp.OutputAction:
			name, err := a.VportHandle(dp).LookupName()
			if err != nil {
				return printErr("%s", err)
			}

			outputs = append(outputs, name)
			break

		case odp.SetTunnelAction:
			printSetTunnelAction(a)
			break

		default:
			fmt.Printf("%v", a)
			break
		}
	}

	if len(outputs) > 0 {
		fmt.Printf(" --output=%s", strings.Join(outputs, ","))
	}

	os.Stdout.WriteString("\n")
	return true
}

func printEthAddrOption(opt string, a [odp.ETH_ALEN]byte, m [odp.ETH_ALEN]byte) {
	if !odp.AllBytes(m[:], 0) {
		if odp.AllBytes(m[:], 0xff) {
			fmt.Printf(" --%s=%s", opt, net.HardwareAddr(a[:]))
		} else {
			fmt.Printf(" --%s=\"%s&%s\"", opt,
				net.HardwareAddr(a[:]),
				net.HardwareAddr(m[:]))
		}
	}
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
