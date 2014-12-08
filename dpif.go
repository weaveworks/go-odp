package main

import (
	"syscall"
	"fmt"
	"unsafe"
)

const (
	DATAPATH = iota
	VPORT = iota
	FLOW = iota
	PACKET = iota
	FAMILY_COUNT = iota
)

var familyNames = [FAMILY_COUNT]string {
	"ovs_datapath",
	"ovs_vport",
	"ovs_flow",
	"ovs_packet",
}

type Dpif struct {
	sock *NetlinkSocket
	familyIds [FAMILY_COUNT]uint16
}

func lookupFamily(sock *NetlinkSocket, name string) (uint16, error) {
	id, err := sock.LookupGenlFamily(name)
	if err == nil {
		return id, nil
	}

	if e, ok := err.(NetlinkError); ok && e.Errno == syscall.ENOENT {
		return 0, fmt.Errorf("Generic netlink family '%s' unavailable; the Open vSwitch kernel module is probably not loaded", name)
	}

	return 0, err
}

func NewDpif() (*Dpif, error) {
	sock, err := OpenNetlinkSocket(syscall.NETLINK_GENERIC)
        if err != nil {
                return nil, err
        }

	dpif := &Dpif{sock: sock}

	for i := 0; i < FAMILY_COUNT; i++ {
		dpif.familyIds[i], err = lookupFamily(sock, familyNames[i])
		if err != nil {
			sock.Close()
			return nil, err
		}
	}

	return dpif, nil
}

func (dpif *Dpif) Close() error {
	err := dpif.sock.Close()
	dpif.sock = nil
	return err
}

func ovsHeaderAt(data []byte, pos int) *OvsHeader {
	return (*OvsHeader)(unsafe.Pointer(&data[pos]))
}

func (nlmsg *NlMsgBuilder) PutOvsHeader(ifindex int32) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	pos := nlmsg.Grow(SizeofOvsHeader)
	h := ovsHeaderAt(nlmsg.buf, pos)
	h.DpIfIndex = ifindex
}

func (nlmsg *NlMsgParser) TakeOvsHeader() (*OvsHeader, error) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	h := ovsHeaderAt(nlmsg.data, nlmsg.pos)
	if err := nlmsg.Advance(SizeofOvsHeader); err != nil {
		return nil, err
	}

	return h, nil
}

type DatapathInfo struct {
	ifindex int32
	name string
}

func (dpif *Dpif) makeDatapathInfo(msg *NlMsgParser) (*DatapathInfo, error) {
	if _, err := msg.ExpectNlMsghdr(dpif.familyIds[DATAPATH]); err != nil {
		return nil, err
	}

	if _, err := msg.ExpectGenlMsghdr(OVS_DP_CMD_NEW); err != nil {
		return nil, err
	}

	ovshdr, err := msg.TakeOvsHeader()
	if err != nil {
		return nil, err
	}

	attrs, err := msg.TakeAttrs()
	if err != nil {
		return nil, err
	}

	name, err := attrs.GetString(OVS_DP_ATTR_NAME)
	if err != nil {
		return nil, err
	}

	return &DatapathInfo{ifindex: ovshdr.DpIfIndex, name: name}, nil
}

func (dpif *Dpif) EnumerateDatapaths() error {
	req := NewNlMsgBuilder(DumpFlags, dpif.familyIds[DATAPATH])
	req.PutGenlMsghdr(OVS_DP_CMD_GET, OVS_DATAPATH_VERSION)
	req.PutOvsHeader(0)

	consumer := func (resp *NlMsgParser) {
		dpi, err := dpif.makeDatapathInfo(resp)
		if err != nil {
			panic(err)
		}

		fmt.Printf("YYY %v\n", dpi)
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return err
	}

	return nil
}

type Datapath struct {
	dpif *Dpif
	ifindex int32
}

func (dpif *Dpif) CreateDatapath(name string) (*Datapath, error) {
	var features uint32 = OVS_DP_F_UNALIGNED | OVS_DP_F_VPORT_PIDS

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[DATAPATH])
	req.PutGenlMsghdr(OVS_DP_CMD_NEW, OVS_DATAPATH_VERSION)
	req.PutOvsHeader(0)
	req.PutStringAttr(OVS_DP_ATTR_NAME, name)
	req.PutUint32Attr(OVS_DP_ATTR_UPCALL_PID, 0)
	req.PutUint32Attr(OVS_DP_ATTR_USER_FEATURES, features)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return nil, err
	}

	dpi, err := dpif.makeDatapathInfo(resp)
	if err != nil {
		return nil, err
	}

	return &Datapath{dpif: dpif, ifindex: dpi.ifindex}, nil
}

func (dpif *Dpif) LookupDatapath(name string) (*Datapath, error) {
	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[DATAPATH])
	req.PutGenlMsghdr(OVS_DP_CMD_GET, OVS_DATAPATH_VERSION)
	req.PutOvsHeader(0)
	req.PutStringAttr(OVS_DP_ATTR_NAME, name)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		if e, ok := err.(NetlinkError); ok && e.Errno == syscall.ENODEV {
			// no datapath with the given name
			return nil, nil
		}

		return nil, err
	}

	dpi, err := dpif.makeDatapathInfo(resp)
	if err != nil {
		return nil, err
	}

	return &Datapath{dpif: dpif, ifindex: dpi.ifindex}, nil
}

func (dp *Datapath) Delete() error {
	req := NewNlMsgBuilder(RequestFlags, dp.dpif.familyIds[DATAPATH])
	req.PutGenlMsghdr(OVS_DP_CMD_DEL, OVS_DATAPATH_VERSION)
	req.PutOvsHeader(dp.ifindex)

	_, err := dp.dpif.sock.Request(req)
	if err != nil {
		return err
	}

	dp.dpif = nil
	dp.ifindex = -1
	return nil
}
