package openvswitch

import (
	"syscall"
)

type portInfo struct {
	portNo uint32
	dpIfIndex int32
	name string
}

func (dpif *Dpif) parsePortInfo(msg *NlMsgParser) (res portInfo, err error) {
	_, err = msg.ExpectNlMsghdr(dpif.familyIds[VPORT])
	if err != nil { return }

	_, err = msg.ExpectGenlMsghdr(OVS_VPORT_CMD_NEW)
	if err != nil { return }

	ovshdr, err := msg.takeOvsHeader()
	if err != nil { return }
	res.dpIfIndex = ovshdr.DpIfIndex

	attrs, err := msg.TakeAttrs()
	if err != nil { return }

	res.portNo, err = attrs.GetUint32(OVS_VPORT_ATTR_PORT_NO)
	if err != nil { return }

	res.name, err = attrs.GetString(OVS_VPORT_ATTR_NAME)
	return
}

type Port struct {
	dpif *Dpif
	portNo uint32
	dpIfIndex int32
}

func (dp *Datapath) CreatePort(name string) (*Port, error) {
	dpif := dp.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_NEW, OVS_VPORT_VERSION)
	req.putOvsHeader(dp.ifindex)
	req.PutStringAttr(OVS_VPORT_ATTR_NAME, name)
	req.PutUint32Attr(OVS_VPORT_ATTR_TYPE, OVS_VPORT_TYPE_INTERNAL)
	req.PutUint32Attr(OVS_VPORT_ATTR_UPCALL_PID, dpif.sock.Pid())

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return nil, err
	}

	pi, err := dpif.parsePortInfo(resp)
	if err != nil {
		return nil, err
	}

	return &Port{dpif: dpif, portNo: pi.portNo, dpIfIndex: pi.dpIfIndex}, nil
}

func lookupPort(dpif *Dpif, dpifindex int32, name string) (*Port, error) {
	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(dpifindex)
	req.PutStringAttr(OVS_VPORT_ATTR_NAME, name)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		if err == NetlinkError(syscall.ENODEV) {
			// no port with the given name
			return nil, nil
		}

		return nil, err
	}

	pi, err := dpif.parsePortInfo(resp)
	if err != nil {
		return nil, err
	}

	return &Port{dpif: dpif, portNo: pi.portNo, dpIfIndex: pi.dpIfIndex}, nil
}

func (dpif *Dpif) LookupPort(name string) (*Port, error) {
	return lookupPort(dpif, 0, name)
}

func (dp *Datapath) LookupPort(name string) (*Port, error) {
	return lookupPort(dp.dpif, dp.ifindex, name)
}

func (dp *Datapath) EnumeratePorts() (map[string]*Port, error) {
	dpif := dp.dpif
	res := make(map[string]*Port)

	req := NewNlMsgBuilder(DumpFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(dp.ifindex)

	consumer := func (resp *NlMsgParser) error {
		pi, err := dpif.parsePortInfo(resp)
		if err != nil {	return err }
		res[pi.name] = &Port{dpif: dpif, portNo: pi.portNo}
		return nil
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (port *Port) Delete() error {
	dpif := port.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_DEL, OVS_VPORT_VERSION)
	req.putOvsHeader(port.dpIfIndex)
	req.PutUint32Attr(OVS_VPORT_ATTR_PORT_NO, port.portNo)

	_, err := dpif.sock.Request(req)
	if err != nil { return err }

	port.dpif = nil
	port.portNo = 0
	return nil
}
