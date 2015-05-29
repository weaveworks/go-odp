package odp

import (
	"fmt"
	"syscall"
)

const (
	DATAPATH     = iota
	VPORT        = iota
	FLOW         = iota
	PACKET       = iota
	FAMILY_COUNT = iota
)

var familyNames = [FAMILY_COUNT]string{
	"ovs_datapath",
	"ovs_vport",
	"ovs_flow",
	"ovs_packet",
}

type Dpif struct {
	sock     *NetlinkSocket
	families [FAMILY_COUNT]GenlFamily
}

func lookupFamily(sock *NetlinkSocket, name string) (GenlFamily, error) {
	family, err := sock.LookupGenlFamily(name)
	if err == nil {
		return family, nil
	}

	if err == NetlinkError(syscall.ENOENT) {
		err = fmt.Errorf("Generic netlink family '%s' unavailable; the Open vSwitch kernel module is probably not loaded, try 'modprobe openvswitch'", name)
	}

	return GenlFamily{}, err
}

func NewDpif() (*Dpif, error) {
	sock, err := OpenNetlinkSocket(syscall.NETLINK_GENERIC)
	if err != nil {
		return nil, err
	}

	dpif := &Dpif{sock: sock}

	for i := 0; i < FAMILY_COUNT; i++ {
		dpif.families[i], err = lookupFamily(sock, familyNames[i])
		if err != nil {
			sock.Close()
			return nil, err
		}
	}

	return dpif, nil
}

func (dpif *Dpif) Close() error {
	if dpif.sock == nil {
		return nil
	}
	err := dpif.sock.Close()
	dpif.sock = nil
	return err
}

func (nlmsg *NlMsgBuilder) putOvsHeader(ifindex int32) {
	pos := nlmsg.AlignGrow(syscall.NLMSG_ALIGNTO, SizeofOvsHeader)
	h := ovsHeaderAt(nlmsg.buf, pos)
	h.DpIfIndex = ifindex
}

func (nlmsg *NlMsgParser) takeOvsHeader() (*OvsHeader, error) {
	pos, err := nlmsg.AlignAdvance(syscall.NLMSG_ALIGNTO, SizeofOvsHeader)
	if err != nil {
		return nil, err
	}

	return ovsHeaderAt(nlmsg.data, pos), nil
}
