package odp

import (
	"fmt"
	"syscall"
)

func AllBytes(data []byte, x byte) bool {
	for _, y := range data {
		if x != y {
			return false
		}
	}

	return true
}

type FlowKey interface {
	typeId() uint16
	putKeyNlAttr(*NlMsgBuilder)
	putMaskNlAttr(*NlMsgBuilder)
	Ignored() bool
	Equals(FlowKey) bool
}

type FlowKeys map[uint16]FlowKey

func (a FlowKeys) Equals(b FlowKeys) bool {
	for id, ak := range a {
		bk, ok := b[id]
		if ok {
			if !ak.Equals(bk) {
				return false
			}
		} else {
			if !ak.Ignored() {
				return false
			}
		}
	}

	for id, bk := range b {
		_, ok := a[id]
		if !ok && !bk.Ignored() {
			return false
		}
	}

	return true
}

// A FlowKeyParser describes how to parse a flow key of a particular
// type from a netlnk message
type FlowKeyParser struct {
	// Flow key parsing function
	//
	// key may be nil if the relevant attribute wasn't provided.
	// This generally means that the mask will indicate that the
	// flow key is Ignored.
	parse func(typ uint16, key []byte, mask []byte) (FlowKey, error)

	// Special mask values indicating that the flow key is an
	// exact match or Ignored.
	exactMask  []byte
	ignoreMask []byte
}

// Maps an NL attribute type to the corresponding FlowKeyParser
type FlowKeyParsers map[uint16]FlowKeyParser

func parseFlowKeys(keys Attrs, masks Attrs, parsers FlowKeyParsers) (res FlowKeys, err error) {
	res = make(FlowKeys)

	for typ, key := range keys {
		parser, ok := parsers[typ]
		if !ok {
			return nil, fmt.Errorf("unknown flow key type %d (value %v)", typ, key)
		}

		var mask []byte
		if masks == nil {
			// "OVS_FLOW_ATTR_MASK: ... If not present,
			// all flow key bits are exact match bits."
			mask = parser.exactMask
		} else {
			// "Omitting attribute is treated as
			// wildcarding all corresponding fields"
			mask, ok = masks[typ]
			if !ok {
				mask = parser.ignoreMask
			}
		}

		res[typ], err = parser.parse(typ, key, mask)
		if err != nil {
			return nil, err
		}
	}

	if masks != nil {
		for typ, mask := range masks {
			_, ok := keys[typ]
			if ok {
				continue
			}

			// flow key mask without a corresponding flow
			// key value
			parser, ok := parsers[typ]
			if !ok {
				return nil, fmt.Errorf("unknown flow key type %d (mask %v)", typ, mask)
			}

			res[typ], err = parser.parse(typ, nil, mask)
			if err != nil {
				return nil, err
			}
		}
	}

	return res, nil
}

// Most flow keys can be handled as opaque bytes.  Doing so avoids
// repetition.

type BlobFlowKey struct {
	typ uint16

	// This holds the key and the mask concatenated, so it is
	// twice their length
	keyMask []byte
}

func NewBlobFlowKey(typ uint16, size int) BlobFlowKey {
	km := MakeAlignedByteSlice(size * 2)
	mask := km[size:]
	for i := range mask {
		mask[i] = 0xff
	}
	return BlobFlowKey{typ: typ, keyMask: km}
}

func (key BlobFlowKey) typeId() uint16 {
	return key.typ
}

func (key BlobFlowKey) key() []byte {
	return key.keyMask[:len(key.keyMask)/2]
}

func (key BlobFlowKey) mask() []byte {
	return key.keyMask[len(key.keyMask)/2:]
}

func (key BlobFlowKey) putKeyNlAttr(msg *NlMsgBuilder) {
	msg.PutSliceAttr(key.typ, key.key())
}

func (key BlobFlowKey) putMaskNlAttr(msg *NlMsgBuilder) {
	msg.PutSliceAttr(key.typ, key.mask())
}

func (key BlobFlowKey) Ignored() bool {
	return AllBytes(key.mask(), 0)
}

// Go's anonymous struct fields are not quite a replacement for
// inheritance.  We want to have an Equals method for BlobFlowKeys,
// that works even when BlobFlowKeys are embedded as anonymous struct
// fields.  But we can use a straightforware type assertion to tell if
// another FlowKey is also a BlobFlowKey, because in the embedded
// case, it will say that the FlowKey is not an BlobFlowKey (the "has
// an anonymoys field of X" is not an "is a X" relation).  To work
// around this, we use an interface, implemented by BlobFlowKey, that
// automatically ges promoted to all structs that embed BlobFlowKey.

type BlobFlowKeyish interface {
	toBlobFlowKey() BlobFlowKey
}

func (key BlobFlowKey) toBlobFlowKey() BlobFlowKey { return key }

func (a BlobFlowKey) Equals(gb FlowKey) bool {
	bx, ok := gb.(BlobFlowKeyish)
	if !ok {
		return false
	}
	b := bx.toBlobFlowKey()

	size := len(a.keyMask)
	if len(b.keyMask) != size {
		return false
	}
	size /= 2

	amask := a.keyMask[size:]
	bmask := b.keyMask[size:]
	for i := range amask {
		if amask[i] != bmask[i] || ((a.keyMask[i]^b.keyMask[i])&amask[i]) != 0 {
			return false
		}
	}

	return true
}

func parseBlobFlowKey(typ uint16, key []byte, mask []byte, size int) (BlobFlowKey, error) {
	res := BlobFlowKey{typ: typ}

	if len(mask) != size {
		return res, fmt.Errorf("flow key mask type %d has wrong length (expected %d bytes, got %d)", typ, size, len(mask))
	}

	res.keyMask = MakeAlignedByteSlice(size * 2)
	copy(res.keyMask[size:], mask)

	if key != nil {
		if len(key) != size {
			return res, fmt.Errorf("flow key type %d has wrong length (expected %d bytes, got %d)", typ, size, len(key))
		}

		copy(res.keyMask, key)
	} else {
		// The kernel produces masks without a corresponding
		// key, but in such cases the mask should indicate
		// that the key value is ignored.
		if !AllBytes(mask, 0) {
			return res, fmt.Errorf("flow key type %d has non-zero mask without a value (mask %v)", typ, mask)
		}
	}

	return res, nil
}

func blobFlowKeyParser(size int, wrap func(BlobFlowKey) FlowKey) FlowKeyParser {
	exact := make([]byte, size)
	for i := range exact {
		exact[i] = 0xff
	}

	return FlowKeyParser{
		parse: func(typ uint16, key []byte, mask []byte) (FlowKey, error) {
			bfk, err := parseBlobFlowKey(typ, key, mask, size)
			if err != nil {
				return nil, err
			}
			if wrap == nil {
				return bfk, nil
			} else {
				return wrap(bfk), nil
			}
		},
		ignoreMask: make([]byte, size),
		exactMask:  exact,
	}
}

// OVS_KEY_ATTR_IN_PORT: Incoming port number
//
// This flow key is problematic.  First, the kernel always does an
// exact match for IN_PORT, i.e. it takes the mask to be 0xffffffff if
// the key is set at all.  Second, when reporting the mask, the kernel
// always sets the upper 16 bits, probably because port numbers are 16
// bits in the kernel, but 32 bits in the ABI to userspace.  It does
// this even if the IN_PORT flow key was not set.  As a result, we
// take any mask other than 0xffffffff to mean ignored.

type InPortFlowKey struct {
	BlobFlowKey
}

func parseInPortFlowKey(typ uint16, key []byte, mask []byte) (FlowKey, error) {
	if !AllBytes(mask, 0xff) {
		for i := range mask {
			mask[i] = 0
		}
	}
	fk, err := parseBlobFlowKey(typ, key, mask, 4)
	if err != nil {
		return nil, err
	}
	return InPortFlowKey{fk}, nil
}

func NewInPortFlowKey(vport VportHandle) FlowKey {
	fk := NewBlobFlowKey(OVS_KEY_ATTR_IN_PORT, 4)
	*uint32At(fk.key(), 0) = vport.portNo
	return fk
}

func (k InPortFlowKey) VportHandle(dp DatapathHandle) VportHandle {
	return VportHandle{
		dpif:      dp.dpif,
		portNo:    *uint32At(k.key(), 0),
		dpIfIndex: dp.ifindex,
	}
}

// OVS_KEY_ATTR_ETHERNET: Ethernet header flow key

type EthernetFlowKey struct {
	BlobFlowKey
}

func (key EthernetFlowKey) Ignored() bool {
	// An ethernet flow key is mandatory, so don't omit it just
	// because the mask is all zeros
	return false
}

func NewEthernetFlowKey(key OvsKeyEthernet, mask OvsKeyEthernet) FlowKey {
	fk := NewBlobFlowKey(OVS_KEY_ATTR_ETHERNET, SizeofOvsKeyEthernet)
	*ovsKeyEthernetAt(fk.key(), 0) = key
	*ovsKeyEthernetAt(fk.mask(), 0) = mask
	return EthernetFlowKey{fk}
}

func (k EthernetFlowKey) Key() OvsKeyEthernet {
	return *ovsKeyEthernetAt(k.key(), 0)
}

func (k EthernetFlowKey) Mask() OvsKeyEthernet {
	return *ovsKeyEthernetAt(k.mask(), 0)
}

var ethernetFlowKeyParser = blobFlowKeyParser(SizeofOvsKeyEthernet,
	func(fk BlobFlowKey) FlowKey { return EthernetFlowKey{fk} })

// OVS_KEY_ATTR_TUNNEL: Tunnel flow key.  This is more elaborate than
// other flow keys because it consists of a set of attributes.

type TunnelAttrs struct {
	TunnelId [8]byte
	Ipv4Src  [4]byte
	Ipv4Dst  [4]byte
	Tos      uint8
	Ttl      uint8
	Df       bool
	Csum     bool
}

type TunnelAttrsPresence struct {
	TunnelId bool
	Ipv4Src  bool
	Ipv4Dst  bool
	Tos      bool
	Ttl      bool
	Df       bool
	Csum     bool
}

var ExactTunnelAttrsMask TunnelAttrs = TunnelAttrs{
	[8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	[4]byte{0xff, 0xff, 0xff, 0xff},
	[4]byte{0xff, 0xff, 0xff, 0xff},
	0xff, 0xff,
	true, true,
}

// Extract presence information from a TunnelAttrs mask
func (ta TunnelAttrs) present() TunnelAttrsPresence {
	return TunnelAttrsPresence{
		TunnelId: !AllBytes(ta.TunnelId[:], 0),
		Ipv4Src:  !AllBytes(ta.Ipv4Src[:], 0),
		Ipv4Dst:  !AllBytes(ta.Ipv4Dst[:], 0),
		Tos:      ta.Tos != 0,
		Ttl:      ta.Ttl != 0,
		Df:       ta.Df,
		Csum:     ta.Csum,
	}
}

func (ta TunnelAttrs) toNlAttrs(msg *NlMsgBuilder, present TunnelAttrsPresence) {
	if present.TunnelId {
		msg.PutSliceAttr(OVS_TUNNEL_KEY_ATTR_ID, ta.TunnelId[:])
	}

	if present.Ipv4Src {
		msg.PutSliceAttr(OVS_TUNNEL_KEY_ATTR_IPV4_SRC, ta.Ipv4Src[:])
	}

	if present.Ipv4Dst {
		msg.PutSliceAttr(OVS_TUNNEL_KEY_ATTR_IPV4_DST, ta.Ipv4Dst[:])
	}

	if present.Tos {
		msg.PutUint8Attr(OVS_TUNNEL_KEY_ATTR_TOS, ta.Tos)
	}

	if present.Ttl {
		msg.PutUint8Attr(OVS_TUNNEL_KEY_ATTR_TTL, ta.Ttl)
	}

	if present.Df && ta.Df {
		msg.PutEmptyAttr(OVS_TUNNEL_KEY_ATTR_DONT_FRAGMENT)
	}

	if present.Csum && ta.Csum {
		msg.PutEmptyAttr(OVS_TUNNEL_KEY_ATTR_CSUM)
	}
}

func parseTunnelAttrs(data []byte) (ta TunnelAttrs, present TunnelAttrsPresence, err error) {
	attrs, err := ParseNestedAttrs(data)
	if err != nil {
		return
	}

	present.TunnelId, err = attrs.GetOptionalBytes(OVS_TUNNEL_KEY_ATTR_ID, ta.TunnelId[:])
	if err != nil {
		return
	}

	present.Ipv4Src, err = attrs.GetOptionalBytes(OVS_TUNNEL_KEY_ATTR_IPV4_SRC, ta.Ipv4Src[:])
	if err != nil {
		return
	}

	present.Ipv4Dst, err = attrs.GetOptionalBytes(OVS_TUNNEL_KEY_ATTR_IPV4_DST, ta.Ipv4Dst[:])

	ta.Tos, present.Tos, err = attrs.GetOptionalUint8(OVS_TUNNEL_KEY_ATTR_TOS)
	if err != nil {
		return
	}

	ta.Ttl, present.Ttl, err = attrs.GetOptionalUint8(OVS_TUNNEL_KEY_ATTR_TTL)
	if err != nil {
		return
	}

	ta.Df, err = attrs.GetEmpty(OVS_TUNNEL_KEY_ATTR_DONT_FRAGMENT)
	present.Df = ta.Df
	if err != nil {
		return
	}

	ta.Csum, err = attrs.GetEmpty(OVS_TUNNEL_KEY_ATTR_CSUM)
	present.Csum = ta.Csum
	if err != nil {
		return
	}

	return
}

type TunnelFlowKey struct {
	key  TunnelAttrs
	mask TunnelAttrs
}

func NewTunnelFlowKey(key TunnelAttrs, mask TunnelAttrs) TunnelFlowKey {
	return TunnelFlowKey{key: key, mask: mask}
}

func (fk TunnelFlowKey) Key() TunnelAttrs {
	return fk.key
}

func (fk TunnelFlowKey) Mask() TunnelAttrs {
	return fk.mask
}

func (TunnelFlowKey) typeId() uint16 {
	return OVS_KEY_ATTR_TUNNEL
}

func (key TunnelFlowKey) putKeyNlAttr(msg *NlMsgBuilder) {
	msg.PutNestedAttrs(OVS_KEY_ATTR_TUNNEL, func() {
		key.key.toNlAttrs(msg, key.mask.present())
	})
}

func (key TunnelFlowKey) putMaskNlAttr(msg *NlMsgBuilder) {
	msg.PutNestedAttrs(OVS_KEY_ATTR_TUNNEL, func() {
		key.mask.toNlAttrs(msg, key.mask.present())
	})
}

func (a TunnelFlowKey) Equals(gb FlowKey) bool {
	b, ok := gb.(TunnelFlowKey)
	if !ok {
		return false
	}
	return a.key == b.key && a.mask == b.mask
}

func (key TunnelFlowKey) Ignored() bool {
	m := key.mask
	return AllBytes(m.TunnelId[:], 0) &&
		AllBytes(m.Ipv4Src[:], 0) &&
		AllBytes(m.Ipv4Dst[:], 0) &&
		m.Tos == 0 &&
		m.Ttl == 0 &&
		!m.Df && !m.Csum
}

func parseTunnelFlowKey(typ uint16, key []byte, mask []byte) (FlowKey, error) {
	var k, m TunnelAttrs
	var err error

	if key != nil {
		k, _, err = parseTunnelAttrs(key)
		if err != nil {
			return nil, err
		}
	}

	if mask != nil {
		// We don't care about mask presence information, because a
		// missing mask attribute means the field is
		// wildcarded
		m, _, err = parseTunnelAttrs(mask)
		if err != nil {
			return nil, err
		}
	} else {
		m = ExactTunnelAttrsMask
	}

	return TunnelFlowKey{key: k, mask: m}, err
}

var flowKeyParsers = FlowKeyParsers{
	// Packet QoS priority flow key
	OVS_KEY_ATTR_PRIORITY: blobFlowKeyParser(4, nil),

	OVS_KEY_ATTR_IN_PORT: FlowKeyParser{
		parse:      parseInPortFlowKey,
		exactMask:  []byte{0xff, 0xff, 0xff, 0xff},
		ignoreMask: []byte{0, 0, 0, 0},
	},

	OVS_KEY_ATTR_ETHERNET:  ethernetFlowKeyParser,
	OVS_KEY_ATTR_ETHERTYPE: blobFlowKeyParser(2, nil),
	OVS_KEY_ATTR_SKB_MARK:  blobFlowKeyParser(4, nil),

	OVS_KEY_ATTR_TUNNEL: FlowKeyParser{
		parse:      parseTunnelFlowKey,
		exactMask:  nil,
		ignoreMask: []byte{},
	},
}

// Actions

type Action interface {
	typeId() uint16
	toNlAttr(*NlMsgBuilder)
	Equals(Action) bool
}

type OutputAction uint32

func NewOutputAction(port VportHandle) OutputAction {
	return OutputAction(port.portNo)
}

func (oa OutputAction) VportHandle(dp DatapathHandle) VportHandle {
	return VportHandle{
		dpif:      dp.dpif,
		portNo:    uint32(oa),
		dpIfIndex: dp.ifindex,
	}
}

func (OutputAction) typeId() uint16 {
	return OVS_ACTION_ATTR_OUTPUT
}

func (oa OutputAction) toNlAttr(msg *NlMsgBuilder) {
	msg.PutUint32Attr(OVS_ACTION_ATTR_OUTPUT, uint32(oa))
}

func (a OutputAction) Equals(bx Action) bool {
	b, ok := bx.(OutputAction)
	if !ok {
		return false
	}
	return a == b
}

func parseOutputAction(typ uint16, data []byte) (Action, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("flow action type %d has wrong length (expects 4 bytes, got %d)", typ, len(data))
	}

	return OutputAction(*uint32At(data, 0)), nil
}

type SetTunnelAction struct {
	TunnelAttrs
	TunnelIdPresent bool
	Ipv4SrcPresent  bool
	Ipv4DstPresent  bool
	TosPresent      bool
	TtlPresent      bool
}

func (SetTunnelAction) typeId() uint16 {
	return OVS_ACTION_ATTR_SET
}

func (ta SetTunnelAction) toNlAttr(msg *NlMsgBuilder) {
	msg.PutNestedAttrs(OVS_ACTION_ATTR_SET, func() {
		msg.PutNestedAttrs(OVS_KEY_ATTR_TUNNEL, func() {
			ta.toNlAttrs(msg, TunnelAttrsPresence{
				TunnelId: ta.TunnelIdPresent,
				Ipv4Src:  ta.Ipv4SrcPresent,
				Ipv4Dst:  ta.Ipv4DstPresent,
				Tos:      ta.TosPresent,
				Ttl:      ta.TtlPresent,
				Df:       ta.Df,
				Csum:     ta.Csum,
			})
		})
	})
}

func (a SetTunnelAction) Equals(bx Action) bool {
	b, ok := bx.(SetTunnelAction)
	if !ok {
		return false
	}
	return a.TunnelAttrs == b.TunnelAttrs
}

func parseSetAction(typ uint16, data []byte) (Action, error) {
	attrs, err := ParseNestedAttrs(data)
	if err != nil {
		return nil, err
	}

	var res Action
	first := true
	for typ, data := range attrs {
		if !first {
			return nil, fmt.Errorf("multiple attributes within OVS_ACTION_ATTR_SET")
		}

		switch typ {
		case OVS_KEY_ATTR_TUNNEL:
			ta, tap, err := parseTunnelAttrs(data)
			if err != nil {
				return nil, err
			}
			res = SetTunnelAction{
				TunnelAttrs:     ta,
				TunnelIdPresent: tap.TunnelId,
				Ipv4SrcPresent:  tap.Ipv4Src,
				Ipv4DstPresent:  tap.Ipv4Dst,
				TtlPresent:      tap.Ttl,
				TosPresent:      tap.Tos,
			}
			break

		default:
			return nil, fmt.Errorf("unsupported OVS_ACTION_ATTR_SET attribute %d", typ)
		}

		first = false
	}

	return res, nil
}

var actionParsers = map[uint16](func(uint16, []byte) (Action, error)){
	OVS_ACTION_ATTR_OUTPUT: parseOutputAction,
	OVS_ACTION_ATTR_SET:    parseSetAction,
}

// Complete flows

type FlowSpec struct {
	FlowKeys
	Actions []Action
}

func NewFlowSpec() FlowSpec {
	return FlowSpec{FlowKeys: make(FlowKeys), Actions: make([]Action, 0)}
}

func (f *FlowSpec) AddKey(k FlowKey) {
	// TODO check for collisions
	f.FlowKeys[k.typeId()] = k
}

func (f *FlowSpec) AddAction(a Action) {
	f.Actions = append(f.Actions, a)
}

func (f FlowSpec) toNlAttrs(msg *NlMsgBuilder) {
	msg.PutNestedAttrs(OVS_FLOW_ATTR_KEY, func() {
		for _, k := range f.FlowKeys {
			if !k.Ignored() {
				k.putKeyNlAttr(msg)
			}
		}
	})

	msg.PutNestedAttrs(OVS_FLOW_ATTR_MASK, func() {
		for _, k := range f.FlowKeys {
			if !k.Ignored() {
				k.putMaskNlAttr(msg)
			}
		}
	})

	// ACTIONS is required
	msg.PutNestedAttrs(OVS_FLOW_ATTR_ACTIONS, func() {
		for _, a := range f.Actions {
			a.toNlAttr(msg)
		}
	})
}

func (a FlowSpec) Equals(b FlowSpec) bool {
	if !a.FlowKeys.Equals(b.FlowKeys) {
		return false
	}
	if len(a.Actions) != len(b.Actions) {
		return false
	}

	for i := range a.Actions {
		if !a.Actions[i].Equals(b.Actions[i]) {
			return false
		}
	}

	return true
}

func (dp DatapathHandle) parseFlowSpec(msg *NlMsgParser) (FlowSpec, error) {
	f := FlowSpec{}

	_, err := msg.ExpectNlMsghdr(dp.dpif.familyIds[FLOW])
	if err != nil {
		return f, err
	}

	_, err = msg.ExpectGenlMsghdr(OVS_FLOW_CMD_NEW)
	if err != nil {
		return f, err
	}

	err = dp.checkOvsHeader(msg)
	if err != nil {
		return f, err
	}

	attrs, err := msg.TakeAttrs()
	if err != nil {
		return f, err
	}

	keys, err := attrs.GetNestedAttrs(OVS_FLOW_ATTR_KEY, false)
	if err != nil {
		return f, err
	}

	masks, err := attrs.GetNestedAttrs(OVS_FLOW_ATTR_MASK, true)
	if err != nil {
		return f, err
	}

	f.FlowKeys, err = parseFlowKeys(keys, masks, flowKeyParsers)
	if err != nil {
		return f, err
	}

	actattrs, err := attrs.GetOrderedAttrs(OVS_FLOW_ATTR_ACTIONS)
	if err != nil {
		return f, err
	}

	actions := make([]Action, 0)
	for _, actattr := range actattrs {
		parser, ok := actionParsers[actattr.typ]
		if !ok {
			return f, fmt.Errorf("unknown action type %d (value %v)", actattr.typ, actattr.val)
		}

		action, err := parser(actattr.typ, actattr.val)
		if err != nil {
			return f, err
		}
		actions = append(actions, action)
	}

	f.Actions = actions
	return f, nil
}

func (dp DatapathHandle) CreateFlow(f FlowSpec) error {
	dpif := dp.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[FLOW])
	req.PutGenlMsghdr(OVS_FLOW_CMD_NEW, OVS_FLOW_VERSION)
	req.putOvsHeader(dp.ifindex)
	f.toNlAttrs(req)

	_, err := dpif.sock.Request(req)
	if err != nil {
		return err
	}

	return nil
}

type NoSuchFlowError struct{}

func (NoSuchFlowError) Error() string { return "no such flow" }

func (dp DatapathHandle) DeleteFlow(f FlowSpec) error {
	dpif := dp.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.familyIds[FLOW])
	req.PutGenlMsghdr(OVS_FLOW_CMD_DEL, OVS_FLOW_VERSION)
	req.putOvsHeader(dp.ifindex)
	f.toNlAttrs(req)

	_, err := dpif.sock.Request(req)
	if err == NetlinkError(syscall.ENOENT) {
		err = NoSuchFlowError{}
	}

	return err
}

func (dp DatapathHandle) EnumerateFlows() ([]FlowSpec, error) {
	dpif := dp.dpif
	res := make([]FlowSpec, 0)

	req := NewNlMsgBuilder(DumpFlags, dpif.familyIds[FLOW])
	req.PutGenlMsghdr(OVS_FLOW_CMD_GET, OVS_FLOW_VERSION)
	req.putOvsHeader(dp.ifindex)

	consumer := func(resp *NlMsgParser) error {
		f, err := dp.parseFlowSpec(resp)
		if err != nil {
			return err
		}
		res = append(res, f)
		return nil
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return nil, err
	}

	return res, nil
}
