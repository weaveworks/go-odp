package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/weaveworks/go-odp/odp"
)

func printErr(f string, a ...interface{}) bool {
	fmt.Fprintf(os.Stderr, f, a...)
	os.Stderr.WriteString("\n")
	return false
}

type commandDispatch interface {
	usage(path []string)
	run(args []string, pos int) bool
}

type subcommands map[string]commandDispatch

func (cmds subcommands) run(args []string, pos int) bool {
	if pos >= len(args) {
		return printErr("Subcommand required by \"%s\".  Try \"%s help\"", strings.Join(args[:pos], " "), args[0])
	}

	if args[pos] == "help" {
		fmt.Fprintln(os.Stderr, "Usage:")
		path := make([]string, pos)
		copy(path, args)
		cmds.usage(path)
		return false
	}

	var match commandDispatch
	matches := 0

	for name, cd := range cmds {
		if name == args[pos] {
			// exact match
			match = cd
			matches = 1
			break
		}

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

func (cmds subcommands) usage(path []string) {
	for name, cd := range cmds {
		cd.usage(append(path, name))
	}
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
	args  string
	descr string
	cmd   func(Flags) bool
}

func (cmd command) run(args []string, pos int) bool {
	f := flag.NewFlagSet(strings.Join(args[:pos], " "), flag.ExitOnError)
	return cmd.cmd(Flags{f, args[pos:]})
}

func (cmd command) usage(path []string) {
	if cmd.args != "" {
		path = append(path, cmd.args)
	}
	fmt.Fprintf(os.Stderr, "%-45s %s\n", strings.Join(path, " "),
		cmd.descr)
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

func (cmds possibleSubcommands) usage(path []string) {
	cmds.command.usage(path)
	cmds.subcommands.usage(path)
}

var commands = subcommands{
	"datapath": possibleSubcommands{
		command{"", "List datapaths", listDatapaths},
		subcommands{
			"add": command{
				"<datapath>", "Add datapath",
				addDatapath,
			},
			"delete": command{
				"<datapath>", "Delete datapath",
				deleteDatapath,
			},
			"list": command{"", "List datapaths", listDatapaths},
			"listen": command{
				"<datapath>", "Listen to misses on datapath",
				listenOnDatapath,
			},
		},
	},
	"vport": possibleSubcommands{
		command{"", "List vports", listVports},
		subcommands{
			"add": subcommands{
				"netdev": command{
					"<datapath> <netdev>",
					"Add netdev vport",
					addNetdevVport,
				},
				"internal": command{
					"<datapath> <vport>",
					"Add internal vport",
					addInternalVport,
				},
				"vxlan": command{
					"<datapath> <vport>",
					"Add vxlan vport",
					addVxlanVport,
				},
			},
			"delete": command{
				"<vport>", "Delete vport",
				deleteVport,
			},
			"list": command{
				"[<datapath>]", "List vports",
				listVports,
			},
			"listen": command{
				"", "Listen for vport changes",
				listenForVports,
			},
		},
	},
	"flow": subcommands{
		"add": command{
			"<datapath> <options>...", "Add flow",
			addFlow,
		},
		"delete": command{
			"<datapath> <options>...", "Delete flow",
			deleteFlow,
		},
		"list": command{
			"[<datapath>]", "List flows",
			listFlows,
		},
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
		if odp.IsDatapathNameAlreadyExistsError(err) {
			return printErr("Network device named %s already exists", args[0])
		} else {
			return printErr("%s", err)
		}
	}

	return true
}

func lookupDatapath(dpif *odp.Dpif, name string) (*odp.DatapathHandle, string) {
	dph, err := dpif.LookupDatapath(name)
	if err == nil {
		return &dph, name
	}

	if !odp.IsNoSuchDatapathError(err) {
		printErr("%s", err)
		return nil, ""
	}

	// If the name is a number, try to use it as an ifindex
	ifindex, err := strconv.ParseUint(name, 10, 32)
	if err == nil {
		dp, err := dpif.LookupDatapathByIndex(int32(ifindex))
		if err == nil {
			return &dp.Handle, dp.Name
		}

		if !odp.IsNoSuchDatapathError(err) {
			printErr("%s", err)
			return nil, ""
		}
	}

	printErr("Cannot find datapath \"%s\"", name)
	return nil, ""
}

func deleteDatapath(f Flags) bool {
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, _ := lookupDatapath(dpif, args[0])
	if dp == nil {
		return false
	}

	err = dp.Delete()
	if err != nil {
		return printErr("%s", err)
	}

	return true
}

func listenOnDatapath(f Flags) bool {
	var showKeys bool
	f.BoolVar(&showKeys, "keys", false, "show flow keys on reported packets")

	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, dpname := lookupDatapath(dpif, args[0])
	if dp == nil {
		return false
	}

	pipe, err := openTcpdump()
	if err != nil {
		return printErr("Error starting tcpdump: %s", err)
	}

	miss := func(packet []byte, flowKeys odp.FlowKeys) error {
		if showKeys {
			os.Stdout.WriteString("[" + dpname)
			if err := printFlowKeys(flowKeys, *dp); err != nil {
				return err
			}
			os.Stdout.WriteString("]\n")
		}

		return writeTcpdumpPacket(pipe, time.Now(), packet)
	}

	done := make(chan struct{})
	if err := dp.ConsumeMisses(missConsumer{consumer{done}, miss}); err != nil {
		return printErr("%s", err)
	}

	<-done
	return true
}

type consumer struct {
	done chan<- struct{}
}

func (c consumer) Error(err error, stopped bool) {
	fmt.Printf("Error: %s\n", err)
	if stopped {
		close(c.done)
	}
}

type missConsumer struct {
	consumer
	miss func([]byte, odp.FlowKeys) error
}

func (c missConsumer) Miss(packet []byte, flowKeys odp.FlowKeys) error {
	return c.miss(packet, flowKeys)
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
	for name, dp := range dps {
		fmt.Printf("%d: %s\n", dp.IfIndex(), name)
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

	dp, dpname := lookupDatapath(dpif, dpname)
	if dp == nil {
		return false
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

	dp, vport, err := dpif.LookupVportByName(args[0])
	if err != nil {
		if odp.IsNoSuchVportError(err) {
			return printErr("Cannot find port \"%s\"", args[0])
		}

		return printErr("%s", err)
	}

	err = dp.DeleteVport(vport.ID)
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
		dp, dpname := lookupDatapath(dpif, args[0])
		if dp == nil {
			return false
		}

		return printVports(dpname, *dp)
	}
}

func printVports(dpname string, dp odp.DatapathHandle) bool {
	vports, err := dp.EnumerateVports()
	if err != nil {
		return printErr("%s", err)
	}

	for _, vport := range vports {
		printVport("", dpname, vport)
	}

	return true
}

func listenForVports(f Flags) bool {
	f.Parse(0, 0)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	done := make(chan struct{})
	if err := dpif.ConsumeVportEvents(vportEventsConsumer{dpif, consumer{done}}); err != nil {
		return printErr("%s", err)
	}

	<-done
	return true
}

type vportEventsConsumer struct {
	dpif *odp.Dpif
	consumer
}

func (c vportEventsConsumer) New(ifindex int32, vport odp.Vport) error {
	dp, err := c.dpif.LookupDatapathByIndex(ifindex)
	if err != nil {
		return err
	}

	printVport("add ", dp.Name, vport)
	return nil
}

func (c vportEventsConsumer) Delete(ifindex int32, vport odp.Vport) error {
	dp, err := c.dpif.LookupDatapathByIndex(ifindex)
	if err != nil {
		return err
	}

	printVport("delete ", dp.Name, vport)
	return nil
}

func printVport(prefix string, dpname string, vport odp.Vport) {
	spec := vport.Spec
	fmt.Printf("%s%s %s %s", prefix, spec.TypeName(), dpname, spec.Name())

	switch spec := spec.(type) {
	case odp.VxlanVportSpec:
		fmt.Printf(" --port=%d", spec.Port)
		break
	}

	fmt.Printf("\n")
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

func parseTunnelFlags(tf *tunnelFlags) (odp.TunnelFlowKey, error) {
	var fk odp.TunnelFlowKey

	if tf.id != "" {
		tunnelId, err := parseTunnelId(tf.id)
		if err != nil {
			return fk, err
		}

		fk.SetTunnelId(tunnelId)
	}

	if tf.ipv4Src != "" {
		addr, err := parseIpv4(tf.ipv4Src)
		if err != nil {
			return fk, err
		}

		fk.SetIpv4Src(addr)
	}

	if tf.ipv4Dst != "" {
		addr, err := parseIpv4(tf.ipv4Dst)
		if err != nil {
			return fk, err
		}

		fk.SetIpv4Dst(addr)
	}

	if tf.tos >= 0 {
		fk.SetTos(uint8(tf.tos))
	}

	if tf.ttl >= 0 {
		fk.SetTtl(uint8(tf.ttl))
	}

	if tf.df != "" {
		df, err := parseBool(tf.df)
		if err != nil {
			return fk, err
		}

		fk.SetDf(df)
	}

	if tf.csum != "" {
		csum, err := parseBool(tf.csum)
		if err != nil {
			return fk, err
		}

		fk.SetCsum(csum)
	}

	return fk, nil
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
	dpp, _ := lookupDatapath(dpif, args[0])
	if dpp == nil {
		return
	}

	if inPort != "" {
		vport, err := dpp.LookupVportByName(inPort)
		if err != nil {
			printErr("%s", err)
			return
		}
		flow.AddKey(odp.NewInPortFlowKey(vport.ID))
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
			vport, err := dpp.LookupVportByName(vpname)
			if err != nil {
				printErr("%s", err)
				return
			}
			flow.AddAction(odp.NewOutputAction(vport.ID))
		}
	}

	return *dpp, flow, true
}

func handleEthernetFlowKeyOptions(flow odp.FlowSpec, src string, dst string) error {
	var err error
	takeErr := func(key [ETH_ALEN]byte, mask [ETH_ALEN]byte,
		e error) ([ETH_ALEN]byte, [ETH_ALEN]byte) {
		err = e
		return key, mask
	}

	fk := odp.NewEthernetFlowKey()

	fk.SetMaskedEthSrc(takeErr(handleEthernetAddrOption(src)))
	fk.SetMaskedEthDst(takeErr(handleEthernetAddrOption(dst)))

	if err != nil {
		return err
	}

	flow.AddKey(fk)
	return nil
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
		if odp.IsNoSuchFlowError(err) {
			return printErr("No such flow")
		} else {
			return printErr("%s", err)
		}
	}

	return true
}

func listFlows(f Flags) bool {
	var showStats bool
	f.BoolVar(&showStats, "stats", false, "show statistics")
	args := f.Parse(1, 1)

	dpif, err := odp.NewDpif()
	if err != nil {
		return printErr("%s", err)
	}
	defer dpif.Close()

	dp, dpname := lookupDatapath(dpif, args[0])
	if dp == nil {
		return false
	}

	flows, err := dp.EnumerateFlows()
	if err != nil {
		return printErr("%s", err)
	}

	for _, flow := range flows {
		os.Stdout.WriteString(dpname)

		err = printFlowKeys(flow.FlowKeys, *dp)
		if err != nil {
			return printErr("%s", err)
		}

		err = printFlowActions(flow.Actions, *dp)
		if err != nil {
			return printErr("%s", err)
		}

		if showStats {
			fmt.Printf(": %d packets, %d bytes", flow.Packets,
				flow.Bytes)
		}

		os.Stdout.WriteString("\n")
	}

	return true
}

func printFlowKeys(fks odp.FlowKeys, dp odp.DatapathHandle) error {
	for _, fk := range fks {
		if fk.Ignored() {
			continue
		}

		switch fk := fk.(type) {
		case odp.InPortFlowKey:
			name, err := dp.LookupVportName(fk.VportID())
			if err != nil {
				return err
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
			fmt.Printf(" %T:%v", fk, fk)
			break
		}
	}

	return nil
}

func printFlowActions(as []odp.Action, dp odp.DatapathHandle) error {
	outputs := make([]string, 0)

	for _, a := range as {
		switch a := a.(type) {
		case odp.OutputAction:
			name, err := dp.LookupVportName(a.VportID())
			if err != nil {
				return err
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

	return nil
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
	var fk odp.TunnelFlowKey
	if a.Present.TunnelId {
		fk.SetTunnelId(a.TunnelId)
	}
	if a.Present.Ipv4Src {
		fk.SetIpv4Src(a.Ipv4Src)
	}
	if a.Present.Ipv4Dst {
		fk.SetIpv4Dst(a.Ipv4Dst)
	}
	if a.Present.Tos {
		fk.SetTos(a.Tos)
	}
	if a.Present.Ttl {
		fk.SetTtl(a.Ttl)
	}
	if a.Present.Df {
		fk.SetDf(a.Df)
	}
	if a.Present.Csum {
		fk.SetCsum(a.Csum)
	}
	printTunnelOptions(fk, "set-tunnel-")
}
