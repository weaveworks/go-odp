package odp

import (
	"fmt"
	"syscall"
)

type VportSpec interface {
	TypeName() string
	Name() string
	typeId() uint32
	optionNlAttrs(req *NlMsgBuilder)
}

type VportSpecBase struct {
	name string
}

func (v VportSpecBase) Name() string {
	return v.name
}

type SimpleVportSpec struct {
	VportSpecBase
	typ      uint32
	typeName string
}

func (s SimpleVportSpec) TypeName() string {
	return s.typeName
}

func (s SimpleVportSpec) typeId() uint32 {
	return s.typ
}

func (SimpleVportSpec) optionNlAttrs(req *NlMsgBuilder) {
}

func NewNetdevVportSpec(name string) VportSpec {
	return SimpleVportSpec{
		VportSpecBase{name},
		OVS_VPORT_TYPE_NETDEV,
		"netdev",
	}
}

func NewInternalVportSpec(name string) VportSpec {
	return SimpleVportSpec{
		VportSpecBase{name},
		OVS_VPORT_TYPE_INTERNAL,
		"internal",
	}
}

type VxlanVportSpec struct {
	VportSpecBase
	Port uint16
}

func (VxlanVportSpec) TypeName() string {
	return "vxlan"
}

func (VxlanVportSpec) typeId() uint32 {
	return OVS_VPORT_TYPE_VXLAN
}

func (v VxlanVportSpec) optionNlAttrs(req *NlMsgBuilder) {
	req.PutUint16Attr(OVS_TUNNEL_ATTR_DST_PORT, v.Port)
}

func NewVxlanVportSpec(name string, port uint16) VportSpec {
	return VxlanVportSpec{VportSpecBase{name}, port}
}

func parseVxlanVportSpec(name string, opts Attrs) (VportSpec, error) {
	port, err := opts.GetUint16(OVS_TUNNEL_ATTR_DST_PORT)
	if err != nil {
		return nil, err
	}

	return VxlanVportSpec{VportSpecBase{name}, port}, nil
}

type VportHandle struct {
	dpif *Dpif

	// Port numbers are scoped to a particular datapath
	portNo    uint32
	dpIfIndex int32
}

func (dpif *Dpif) parseVport(msg *NlMsgParser) (h VportHandle, s VportSpec, err error) {
	h.dpif = dpif

	_, err = msg.ExpectNlMsghdr(dpif.familyIds[VPORT])
	if err != nil {
		return
	}

	_, err = msg.ExpectGenlMsghdr(OVS_VPORT_CMD_NEW)
	if err != nil {
		return
	}

	ovshdr, err := msg.takeOvsHeader()
	if err != nil {
		return
	}
	h.dpIfIndex = ovshdr.DpIfIndex

	attrs, err := msg.TakeAttrs()
	if err != nil {
		return
	}

	h.portNo, err = attrs.GetUint32(OVS_VPORT_ATTR_PORT_NO)
	if err != nil {
		return
	}

	typ, err := attrs.GetUint32(OVS_VPORT_ATTR_TYPE)
	if err != nil {
		return
	}

	name, err := attrs.GetString(OVS_VPORT_ATTR_NAME)
	if err != nil {
		return
	}

	opts, err := attrs.GetNestedAttrs(OVS_VPORT_ATTR_OPTIONS, true)
	if err != nil {
		return
	}
	if opts == nil {
		opts = make(Attrs)
	}

	switch typ {
	case OVS_VPORT_TYPE_NETDEV:
		s = NewNetdevVportSpec(name)
		break

	case OVS_VPORT_TYPE_INTERNAL:
		s = NewInternalVportSpec(name)
		break

	case OVS_VPORT_TYPE_VXLAN:
		s, err = parseVxlanVportSpec(name, opts)
		break

	default:
		err = fmt.Errorf("unsupported vport type %d", typ)
	}

	return
}

func (dp DatapathHandle) CreateVport(spec VportSpec) (VportHandle, error) {
	dpif := dp.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_NEW, OVS_VPORT_VERSION)
	req.putOvsHeader(dp.ifindex)
	req.PutStringAttr(OVS_VPORT_ATTR_NAME, spec.Name())
	req.PutUint32Attr(OVS_VPORT_ATTR_TYPE, spec.typeId())
	req.PutNestedAttrs(OVS_VPORT_ATTR_OPTIONS, func() {
		spec.optionNlAttrs(req)
	})
	req.PutUint32Attr(OVS_VPORT_ATTR_UPCALL_PID, 0)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return VportHandle{}, err
	}

	h, _, err := dpif.parseVport(resp)
	if err != nil {
		return VportHandle{}, err
	}

	return h, nil
}

func IsNoSuchVportError(err error) bool {
	return err == NetlinkError(syscall.ENODEV)
}

type Vport struct {
	Handle VportHandle
	Spec   VportSpec
}

func lookupVport(dpif *Dpif, dpifindex int32, name string) (Vport, error) {
	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(dpifindex)
	req.PutStringAttr(OVS_VPORT_ATTR_NAME, name)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return Vport{}, err
	}

	h, s, err := dpif.parseVport(resp)
	if err != nil {
		return Vport{}, err
	}

	return Vport{h, s}, nil
}

func (dpif *Dpif) LookupVport(name string) (Vport, error) {
	return lookupVport(dpif, 0, name)
}

func (dp DatapathHandle) LookupVport(name string) (Vport, error) {
	return lookupVport(dp.dpif, dp.ifindex, name)
}

func (h VportHandle) Lookup() (Vport, error) {
	dpif := h.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(h.dpIfIndex)
	req.PutUint32Attr(OVS_VPORT_ATTR_PORT_NO, h.portNo)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return Vport{}, err
	}

	h, s, err := dpif.parseVport(resp)
	if err != nil {
		return Vport{}, err
	}

	return Vport{h, s}, nil
}

func (h VportHandle) LookupName() (string, error) {
	vport, err := h.Lookup()
	if err != nil {
		if !IsNoSuchVportError(err) {
			return "", err
		}

		// No vport with the port number in the flow, so just
		// show the number
		return fmt.Sprintf("%d:%d", h.dpIfIndex, h.portNo), nil
	}

	return vport.Spec.Name(), nil
}

func (dp DatapathHandle) EnumerateVports() ([]Vport, error) {
	dpif := dp.dpif
	res := make([]Vport, 0)

	req := NewNlMsgBuilder(DumpFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_GET, OVS_VPORT_VERSION)
	req.putOvsHeader(dp.ifindex)

	consumer := func(resp *NlMsgParser) error {
		h, spec, err := dpif.parseVport(resp)
		if err != nil {
			return err
		}
		res = append(res, Vport{h, spec})
		return nil
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (vport VportHandle) Delete() error {
	dpif := vport.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_DEL, OVS_VPORT_VERSION)
	req.putOvsHeader(vport.dpIfIndex)
	req.PutUint32Attr(OVS_VPORT_ATTR_PORT_NO, vport.portNo)

	_, err := dpif.sock.Request(req)
	if err != nil {
		return err
	}

	vport.dpif = nil
	vport.portNo = 0
	return nil
}

func (vport VportHandle) setUpcallPortId(pid uint32) error {
	dpif := vport.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[VPORT])
	req.PutGenlMsghdr(OVS_VPORT_CMD_SET, OVS_VPORT_VERSION)
	req.putOvsHeader(vport.dpIfIndex)
	req.PutUint32Attr(OVS_VPORT_ATTR_PORT_NO, vport.portNo)
	req.PutUint32Attr(OVS_VPORT_ATTR_UPCALL_PID, pid)

	_, err := dpif.sock.Request(req)
	if err != nil {
		return err
	}

	return nil
}
