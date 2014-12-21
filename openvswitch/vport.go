package openvswitch

import (
	"syscall"
)

type VportSpec interface {
	typeId() uint32
	optionNlAttrs(req *NlMsgBuilder)
}

type SimpleVportSpec struct {
	typ uint32
}

func (s SimpleVportSpec) typeId() uint32 {
	return s.typ
}

func (SimpleVportSpec) optionNlAttrs(req *NlMsgBuilder) {
}

var INTERNAL_VPORT_SPEC VportSpec = SimpleVportSpec{OVS_VPORT_TYPE_INTERNAL}


type VxlanVportSpec struct {
	destPort uint16
}

func (VxlanVportSpec) typeId() uint32 {
	return OVS_VPORT_TYPE_VXLAN
}

func (v VxlanVportSpec) optionNlAttrs(req *NlMsgBuilder) {
	req.PutUint16Attr(OVS_TUNNEL_ATTR_DST_PORT, v.destPort)
}

func NewVxlanVportSpec(destPort uint16) VportSpec {
	return VxlanVportSpec{destPort}
}

type vportInfo struct {
	portNo uint32
	dpIfIndex int32
	name string
}

func (dpif *Dpif) parseVportInfo(msg *NlMsgParser) (res vportInfo, err error) {
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

type Vport struct {
	dpif *Dpif
	portNo uint32
	dpIfIndex int32
}

func (dp *Datapath) CreateVport(name string, spec VportSpec) (*Vport, error) {
	dpif := dp.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_NEW, OVS_VPORT_VERSION)
	req.putOvsHeader(dp.ifindex)
	req.PutStringAttr(OVS_VPORT_ATTR_NAME, name)
	req.PutUint32Attr(OVS_VPORT_ATTR_TYPE, spec.typeId())
	req.PutNestedAttrs(OVS_VPORT_ATTR_OPTIONS, func () {
		spec.optionNlAttrs(req)
	})
	req.PutUint32Attr(OVS_VPORT_ATTR_UPCALL_PID, dpif.sock.Pid())

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return nil, err
	}

	pi, err := dpif.parseVportInfo(resp)
	if err != nil {
		return nil, err
	}

	return &Vport{dpif: dpif, portNo: pi.portNo, dpIfIndex: pi.dpIfIndex}, nil
}

func lookupVport(dpif *Dpif, dpifindex int32, name string) (*Vport, error) {
	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(dpifindex)
	req.PutStringAttr(OVS_VPORT_ATTR_NAME, name)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		if err == NetlinkError(syscall.ENODEV) {
			// no vport with the given name
			return nil, nil
		}

		return nil, err
	}

	pi, err := dpif.parseVportInfo(resp)
	if err != nil {
		return nil, err
	}

	return &Vport{dpif: dpif, portNo: pi.portNo, dpIfIndex: pi.dpIfIndex}, nil
}

func (dpif *Dpif) LookupVport(name string) (*Vport, error) {
	return lookupVport(dpif, 0, name)
}

func (dp *Datapath) LookupVport(name string) (*Vport, error) {
	return lookupVport(dp.dpif, dp.ifindex, name)
}

func (dp *Datapath) EnumerateVports() (map[string]*Vport, error) {
	dpif := dp.dpif
	res := make(map[string]*Vport)

	req := NewNlMsgBuilder(DumpFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(dp.ifindex)

	consumer := func (resp *NlMsgParser) error {
		pi, err := dpif.parseVportInfo(resp)
		if err != nil {	return err }
		res[pi.name] = &Vport{dpif: dpif, portNo: pi.portNo}
		return nil
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (vport *Vport) Delete() error {
	dpif := vport.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_DEL, OVS_VPORT_VERSION)
	req.putOvsHeader(vport.dpIfIndex)
	req.PutUint32Attr(OVS_VPORT_ATTR_PORT_NO, vport.portNo)

	_, err := dpif.sock.Request(req)
	if err != nil { return err }

	vport.dpif = nil
	vport.portNo = 0
	return nil
}
