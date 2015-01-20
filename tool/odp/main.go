package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/dpw/go-odp/odp"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"
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

	var match commandDispatch
	matches := 0

	for name, cd := range cmds {
		if strings.HasPrefix(name, args[pos]) {
			match = cd
			matches++
		}
	}

	if matches == 0 {
		return printErr("Unknown command \"%s\".  Try \"%s help\"", strings.Join(args[:pos+1], " "), args[0])
	}

	if matches > 1 {
		return printErr("Ambiguous command \"%s\".  Try \"%s help\"", strings.Join(args[:pos+1], " "), args[0])
	}

	return match.run(args, pos+1)
}

type Flags struct {
	*flag.FlagSet
	args []string
}

func (f Flags) Parse(minArgs int, maxArgs int) []string {
	// The flag package doesn't allow options and non-option args
	// to be mixed.  But we want to allow non-options first.  Work
	// around the issue by looking for the first option.
	firstOpt := findFirstOpt(f.args)
	f.FlagSet.Parse(f.args[firstOpt:])
	args := append(f.args[:firstOpt], f.Args()...)

	if len(args) > maxArgs {
		printErr("Excess arguments")
		os.Exit(1)
	}

	if len(args) < minArgs {
		printErr("Insufficient arguments")
		os.Exit(1)
	}

	return args
}

func findFirstOpt(args []string) int {
	for i, arg := range args {
		if len(arg) > 0 && arg[0] == '-' {
			return i
		}
	}

	return len(args)
}

type command struct {
	cmd func(Flags) bool
}

func (cmd command) run(args []string, pos int) bool {
	f := flag.NewFlagSet(strings.Join(args[:pos], " "), flag.ExitOnError)
	return cmd.cmd(Flags{f, args[pos:]})
}

type possibleSubcommands struct {
	command     commandDispatch
	subcommands subcommands
}

func (cmds possibleSubcommands) run(args []string, pos int) bool {
	if pos >= len(args) {
		return cmds.command.run(args, pos)
	}

	return cmds.subcommands.run(args, pos)
}

var commands = subcommands{
	"datapath": possibleSubcommands{
		command{listDatapaths},
		subcommands{
			"add":    command{addDatapath},
			"delete": command{deleteDatapath},
			"list":   command{listDatapaths},
			"listen": command{listenOnDatapath},
		},
	},
	"vport": possibleSubcommands{
		command{listVports},
		subcommands{
			"add": subcommands{
				"netdev":   command{addNetdevVport},
				"internal": command{addInternalVport},
				"vxlan":    command{addVxlanVport},
			},
			"delete": command{deleteVport},
			"list":   command{listVports},
		},
	},
	"flow": subcommands{
		"add":    command{addFlow},
		"delete": command{deleteFlow},
		"list":   command{listFlows},
	},
}

func main() {
	if !commands.run(os.Args, 1) {
		os.Exit(1)
	}
}

func addDatapath(f Flags) bool {
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	_, err = dpif.CreateDatapath(args[0])
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func deleteDatapath(f Flags) bool {
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil {
		return printErr("%s", err)
	}

	if odp.IsNoSuchDatapathError(err) {
		return printErr("Cannot find datapath \"%s\"", args[0])
	}

	err = dp.Delete()
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func listenOnDatapath(f Flags) bool {
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil {
		return printErr("%s", err)
	}

	if odp.IsNoSuchDatapathError(err) {
		return printErr("Cannot find datapath \"%s\"", args[0])
	}

	pipe, err := openTcpdump()
	if err != nil {
		return printErr("Error starting tcpdump: %s", err)
	}

	dp.ConsumeMisses(func(attrs odp.Attrs) error {
		return writeTcpdumpPacket(pipe, time.Now(), attrs[odp.OVS_PACKET_ATTR_PACKET])

		//fmt.Printf("XXX %v\n", attrs)
		// return nil
	}, func(err error) {
		fmt.Printf("Error: %s\n", err)
	})

	// wait forever
	ch := make(chan int, 1)
	for {
		_ = <-ch
	}
}

type pcapHeader struct {
	magicNumber  uint32
	versionMajor uint16
	versionMinor uint16
	thisZone     int32
	sigFigs      uint32
	snapLen      uint32
	network      uint32
}

type pcapPacketHeader struct {
	sec     uint32
	usec    uint32
	inclLen uint32
	origLen uint32
}

func openTcpdump() (io.Writer, error) {
	tcpdump := exec.Command("tcpdump", "-U", "-r", "-")

	pipe, err := tcpdump.StdinPipe()
	if err != nil {
		return nil, err
	}

	tcpdump.Stdout = os.Stdout
	tcpdump.Stderr = os.Stderr

	err = tcpdump.Start()
	if err != nil {
		return nil, err
	}

	header := odp.MakeAlignedByteSlice(int(unsafe.Sizeof(pcapHeader{})))
	*(*pcapHeader)(unsafe.Pointer(&header[0])) = pcapHeader{
		magicNumber:  0xa1b23c4d, // nanosecond times
		versionMajor: 2,
		versionMinor: 4,
		thisZone:     0,
		sigFigs:      0,
		snapLen:      65535,
		network:      1, // ethernet frames
	}

	_, err = pipe.Write(header)
	return pipe, err
}

func writeTcpdumpPacket(pipe io.Writer, t time.Time, data []byte) error {
	header := odp.MakeAlignedByteSlice(int(unsafe.Sizeof(pcapPacketHeader{})))
	*(*pcapPacketHeader)(unsafe.Pointer(&header[0])) = pcapPacketHeader{
		sec:     uint32(t.Unix()),
		usec:    uint32(t.Nanosecond()), // nanosecond field despite name
		inclLen: uint32(len(data)),
		origLen: uint32(len(data)),
	}

	_, err := pipe.Write(header)
	if err != nil {
		return err
	}

	_, err = pipe.Write(data)
	return err
}

func listDatapaths(f Flags) bool {
	f.Parse(0, 0)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dps, err := dpif.EnumerateDatapaths()
	for name := range dps {
		fmt.Printf("%s\n", name)
	}

	return true
}

func addNetdevVport(f Flags) bool {
	args := f.Parse(2, 2)
	return addVport(args[0], odp.NewNetdevVportSpec(args[1]))
}

func addInternalVport(f Flags) bool {
	args := f.Parse(2, 2)
	return addVport(args[0], odp.NewInternalVportSpec(args[1]))
}

func addVxlanVport(f Flags) bool {
	var port uint
	// 4789 is the IANA assigned port number for VXLAN
	f.UintVar(&port, "port", 4789, "UDP port number")
	args := f.Parse(2, 2)

	if port > 65535 {
		return printErr("port number too large")
	}

	return addVport(args[0], odp.NewVxlanVportSpec(args[1], uint16(port)))
}

func addVport(dpname string, spec odp.VportSpec) bool {
	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(dpname)
	if err != nil {
		return printErr("%s", err)
	}

	_, err = dp.CreateVport(spec)
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func deleteVport(f Flags) bool {
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	vport, err := dpif.LookupVport(args[0])
	if err != nil {
		if odp.IsNoSuchVportError(err) {
			return printErr("Cannot find port \"%s\"", args[0])
		}

		return printErr("%s", err)
	}

	err = vport.Handle.Delete()
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func listVports(f Flags) bool {
	args := f.Parse(0, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	if len(args) == 0 {
		// Although vport names are global, rather than being
		// scoped to a datapath, odp can only enumerate the
		// vports within a datapath.  So enumerating them all
		// is a bit of a faff.
		dps, err := dpif.EnumerateDatapaths()
		if err != nil {
			return printErr("%s", err)
		}

		for dpname, dp := range dps {
			if !printVports(dpname, dp) {
				return false
			}
		}

		return true
	} else {
		dp, err := dpif.LookupDatapath(args[0])
		if err != nil {
			return printErr("%s", err)
		}

		return printVports(args[0], dp)
	}
}

func printVports(dpname string, dp odp.DatapathHandle) bool {
	vports, err := dp.EnumerateVports()
	if err != nil {
		return printErr("%s", err)
	}

	for _, vport := range vports {
		spec := vport.Spec
		fmt.Printf("%s %s %s", spec.TypeName(), dpname, spec.Name())

		switch spec := spec.(type) {
		case odp.VxlanVportSpec:
			fmt.Printf(" --port=%d", spec.Port)
			break
		}

		fmt.Printf("\n")
	}

	return true
}

func parseMAC(s string) (mac [6]byte, err error) {
	hwa, err := net.ParseMAC(s)
	if err != nil {
		return
	}

	if len(hwa) != 6 {
		err = fmt.Errorf("invalid MAC address: %s", s)
		return
	}

	copy(mac[:], hwa)
	return
}

func parseIpv4(s string) (res [4]byte, err error) {
	ip := net.ParseIP(s)
	if ip != nil {
		ip = ip.To4()
	}
	if ip == nil || len(ip) != 4 {
		err = fmt.Errorf("invalid IPv4 address \"%s\"", s)
	} else {
		copy(res[:], ip)
	}
	return
}

func ipv4ToString(ip []byte) string {
	return net.IP(ip).To4().String()
}

func parseTunnelId(s string) (res [8]byte, err error) {
	x, err := hex.DecodeString(s)
	if err != nil {
		return
	}

	if len(x) == 8 {
		copy(res[:], x)
	} else {
		err = fmt.Errorf("invalid tunnel Id \"%s\"", s)
	}

	return
}

type tunnelFlags struct {
	id      string
	ipv4Src string
	ipv4Dst string
	tos     int
	ttl     int
	df      string
	csum    string
}

func addTunnelFlags(f Flags, tf *tunnelFlags, prefix string, descrPrefix string) {
	f.StringVar(&tf.id, prefix+"id", "", descrPrefix+"ID")
	f.StringVar(&tf.ipv4Src, prefix+"ipv4-src", "", descrPrefix+"ipv4 source address")
	f.StringVar(&tf.ipv4Dst, prefix+"ipv4-dst", "", descrPrefix+"ipv4 destination address")
	f.IntVar(&tf.tos, prefix+"tos", -1, descrPrefix+"ToS")
	f.IntVar(&tf.ttl, prefix+"ttl", -1, descrPrefix+"TTL")

	// the flag package doesn't support tri-state flags, hence
	// StringVar
	f.StringVar(&tf.df, prefix+"df", "", descrPrefix+"DF")
	f.StringVar(&tf.csum, prefix+"csum", "", descrPrefix+"checksum")
}

func makeBoolStrings(trueStrs, falseStrs string) map[string]bool {
	res := make(map[string]bool)

	for _, s := range strings.Split(trueStrs, " ") {
		res[s] = true
	}

	for _, s := range strings.Split(falseStrs, " ") {
		res[s] = false
	}

	return res
}

var boolStrings = makeBoolStrings("1 t T true TRUE True", "0 f F false FALSE False")

func parseBool(s string) (bool, error) {
	b, ok := boolStrings[s]
	if !ok {
		return false, fmt.Errorf("not a valid boolean value: %s", s)
	}

	return b, nil
}

func setBytes(s []byte, x byte) {
	for i := range s {
		s[i] = x
	}
}

func parseTunnelFlags(tf *tunnelFlags) (fk odp.TunnelFlowKey, err error) {
	var k, m odp.TunnelAttrs

	if tf.id != "" {
		k.TunnelId, err = parseTunnelId(tf.id)
		if err != nil {
			return
		}

		setBytes(m.TunnelId[:], 0xff)
	}

	if tf.ipv4Src != "" {
		k.Ipv4Src, err = parseIpv4(tf.ipv4Src)
		if err != nil {
			return
		}

		setBytes(m.Ipv4Src[:], 0xff)
	}

	if tf.ipv4Dst != "" {
		k.Ipv4Dst, err = parseIpv4(tf.ipv4Dst)
		if err != nil {
			return
		}

		setBytes(m.Ipv4Dst[:], 0xff)
	}

	if tf.tos >= 0 {
		k.Tos = uint8(tf.tos)
		m.Tos = 0xff
	}

	if tf.ttl >= 0 {
		k.Ttl = uint8(tf.ttl)
		m.Ttl = 0xff
	}

	if tf.df != "" {
		m.Df = true
		k.Df, err = parseBool(tf.df)
		if err != nil {
			return
		}
	}

	if tf.csum != "" {
		m.Csum = true
		k.Csum, err = parseBool(tf.csum)
		if err != nil {
			return
		}
	}

	fk = odp.NewTunnelFlowKey(k, m)
	return
}

func parseSetTunnelFlags(tf *tunnelFlags) (*odp.SetTunnelAction, error) {
	fk, err := parseTunnelFlags(tf)
	if err != nil {
		return nil, err
	}

	a := odp.SetTunnelAction{TunnelAttrs: fk.Key()}
	m := fk.Mask()
	foundMask := false

	present := func(exact, ignored bool) bool {
		if exact {
			return true
		} else if ignored {
			return false
		} else {
			foundMask = true
			return false
		}
	}

	bytesPresent := func(b []byte) bool {
		return present(odp.AllBytes(b, 0xff), odp.AllBytes(b, 0))
	}

	a.Present.TunnelId = bytesPresent(m.TunnelId[:])
	a.Present.Ipv4Src = bytesPresent(m.Ipv4Src[:])
	a.Present.Ipv4Dst = bytesPresent(m.Ipv4Dst[:])
	a.Present.Tos = present(m.Tos == 0xff, m.Tos == 0)
	a.Present.Ttl = present(m.Ttl == 0xff, m.Ttl == 0)
	a.Present.Df = m.Df
	a.Present.Csum = m.Csum

	if foundMask {
		return nil, fmt.Errorf("--set-tunnel option includes a mask")
	}

	if a.Present.TunnelId || a.Present.Ipv4Src || a.Present.Ipv4Dst ||
		a.Present.Tos || a.Present.Ttl ||
		a.Present.Df || a.Present.Csum {
		return &a, nil
	} else {
		return nil, nil
	}
}

func flagsToFlowSpec(f Flags, dpif *odp.Dpif) (dp odp.DatapathHandle, flow odp.FlowSpec, ok bool) {
	flow = odp.NewFlowSpec()

	var inPort string
	f.StringVar(&inPort, "in-port", "", "key: incoming vport")

	var ethSrc, ethDst string
	f.StringVar(&ethSrc, "eth-src", "", "key: ethernet source MAC")
	f.StringVar(&ethDst, "eth-dst", "", "key: ethernet destination MAC")

	var tun tunnelFlags
	addTunnelFlags(f, &tun, "tunnel-", "tunnel ")

	var setTun tunnelFlags
	addTunnelFlags(f, &setTun, "set-tunnel-", "action: set tunnel ")

	var output string
	f.StringVar(&output, "output", "", "action: output to vports")

	args := f.Parse(1, 1)

	if inPort != "" {
		vport, err := dpif.LookupVport(inPort)
		if err != nil {
			printErr("%s", err)
			return
		}
		flow.AddKey(odp.NewInPortFlowKey(vport.Handle))
	}

	// The ethernet flow key is mandatory
	err := handleEthernetFlowKeyOptions(flow, ethSrc, ethDst)
	if err != nil {
		printErr("%s", err)
		return
	}

	flowKey, err := parseTunnelFlags(&tun)
	if err != nil {
		printErr("%s", err)
		return
	}

	if !flowKey.Ignored() {
		flow.AddKey(flowKey)
	}

	// Actions are ordered, but flags aren't.  As a temporary
	// hack, we already put SET actions before an OUTPUT action.

	setTunAttrs, err := parseSetTunnelFlags(&setTun)
	if err != nil {
		printErr("%s", err)
		return
	}

	if setTunAttrs != nil {
		flow.AddAction(*setTunAttrs)
	}

	if output != "" {
		for _, vpname := range strings.Split(output, ",") {
			vport, err := dpif.LookupVport(vpname)
			if err != nil {
				printErr("%s", err)
				return
			}
			flow.AddAction(odp.NewOutputAction(vport.Handle))
		}
	}

	dp, err = dpif.LookupDatapath(args[0])
	if err != nil {
		printErr("%s", err)
		return
	}

	return dp, flow, true
}

func handleEthernetFlowKeyOptions(flow odp.FlowSpec, src string, dst string) (err error) {
	var k odp.OvsKeyEthernet
	var m odp.OvsKeyEthernet

	k.EthSrc, m.EthSrc, err = handleEthernetAddrOption(src)
	if err != nil {
		return
	}
	k.EthDst, m.EthDst, err = handleEthernetAddrOption(dst)
	if err != nil {
		return
	}

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
		if err != nil {
			return
		}

		mask, err = parseMAC(m)
	}

	return
}

func addFlow(f Flags) bool {
	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, flow, ok := flagsToFlowSpec(f, dpif)
	if !ok {
		return false
	}

	err = dp.CreateFlow(flow)
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func deleteFlow(f Flags) bool {
	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, flow, ok := flagsToFlowSpec(f, dpif)
	if !ok {
		return false
	}

	err = dp.DeleteFlow(flow)
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func listFlows(f Flags) bool {
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, err := dpif.LookupDatapath(args[0])
	if err != nil {
		return printErr("%s", err)
	}

	flows, err := dp.EnumerateFlows()
	if err != nil {
		return printErr("%s", err)
	}

	for _, flow := range flows {
		if !printFlow(flow, dp, args[0]) {
			return false
		}
	}

	return true
}

func printFlow(flow odp.FlowSpec, dp odp.DatapathHandle, dpname string) bool {
	os.Stdout.WriteString(dpname)

	for _, fk := range flow.FlowKeys {
		if fk.Ignored() {
			continue
		}

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
			printEthAddrOption("eth-src", k.EthSrc[:], m.EthSrc[:])
			printEthAddrOption("eth-dst", k.EthDst[:], m.EthDst[:])
			break

		case odp.TunnelFlowKey:
			printTunnelOptions(fk, "tunnel-")
			break

		default:
			fmt.Printf("%v", fk)
			break
		}
	}

	outputs := make([]string, 0)

	for _, a := range flow.Actions {
		switch a := a.(type) {
		case odp.OutputAction:
			name, err := a.VportHandle(dp).LookupName()
			if err != nil {
				return printErr("%s", err)
			}

			outputs = append(outputs, name)
			break

		case odp.SetTunnelAction:
			printSetTunnelOptions(a)
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

func printEthAddrOption(opt string, k []byte, m []byte) {
	printBytesOption(opt, k, m, func(a []byte) string {
		return net.HardwareAddr(a).String()
	})
}

func printBytesOption(opt string, k []byte, m []byte, f func([]byte) string) {
	if !odp.AllBytes(m, 0) {
		if odp.AllBytes(m, 0xff) {
			fmt.Printf(" --%s=%s", opt, f(k))
		} else {
			fmt.Printf(" --%s=\"%s&%s\"", opt, f(k), f(m))
		}
	}
}

func printByteOption(opt string, k byte, m byte) {
	if m != 0 {
		if m == 0xff {
			fmt.Printf(" --%s=%d", opt, k)
		} else {
			fmt.Printf(" --%s=\"%d&%d\"", opt, k, m)
		}
	}
}

func printTunnelOptions(fk odp.TunnelFlowKey, prefix string) {
	k := fk.Key()
	m := fk.Mask()

	printBytesOption(prefix+"id", k.TunnelId[:], m.TunnelId[:], hex.EncodeToString)
	printBytesOption(prefix+"ipv4-src", k.Ipv4Src[:], m.Ipv4Src[:], ipv4ToString)
	printBytesOption(prefix+"ipv4-dst", k.Ipv4Dst[:], m.Ipv4Dst[:], ipv4ToString)
	printByteOption(prefix+"tos", k.Tos, m.Tos)
	printByteOption(prefix+"ttl", k.Ttl, m.Ttl)

	if m.Df {
		fmt.Printf(" --%sdf=%t", prefix, k.Df)
	}

	if m.Csum {
		fmt.Printf(" --%scsum=%t", prefix, k.Csum)
	}
}

func printSetTunnelOptions(a odp.SetTunnelAction) {
	presentToByte := func(p bool) byte {
		if p {
			return 0xff
		} else {
			return 0
		}
	}

	fillBytes := func(bs []byte, v byte) {
		for i := range bs {
			bs[i] = v
		}
	}

	var m odp.TunnelAttrs
	fillBytes(m.TunnelId[:], presentToByte(a.Present.TunnelId))
	fillBytes(m.Ipv4Src[:], presentToByte(a.Present.Ipv4Src))
	fillBytes(m.Ipv4Dst[:], presentToByte(a.Present.Ipv4Dst))
	m.Tos = presentToByte(a.Present.Tos)
	m.Ttl = presentToByte(a.Present.Ttl)
	m.Df = a.Present.Df
	m.Csum = a.Present.Csum
	printTunnelOptions(odp.NewTunnelFlowKey(a.TunnelAttrs, m), "set-tunnel-")
}
