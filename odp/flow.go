package odp

import (
	"syscall"
	"fmt"
	"unsafe"
)

type FlowKey interface {
	typeId() uint16
	putKeyNlAttr(*NlMsgBuilder)
	putMaskNlAttr(*NlMsgBuilder)
	Ignored() bool
	Equals(FlowKey) bool
}

type FlowKeys map[uint16]FlowKey

func (keys FlowKeys) Ignored() bool {
	for _, k := range(keys) {
		if !k.Ignored() { return false }
	}

	return true
}

func (a FlowKeys) Equals(b FlowKeys) bool {
	for id, ak := range(a) {
		bk, ok := b[id]
		if ok {
			if !ak.Equals(bk) { return false }
		} else {
			if !ak.Ignored() { return false }
		}
	}

	for id, bk := range(b) {
		_, ok := a[id]
		if !ok && !bk.Ignored() { return false }
	}

	return true
}

func (keys FlowKeys) toKeyNlAttrs(msg *NlMsgBuilder, typ uint16) {
	msg.PutNestedAttrs(typ, func () {
		for _, k := range(keys) {
			if !k.Ignored() { k.putKeyNlAttr(msg) }
		}
	})
}

func (keys FlowKeys) toMaskNlAttrs(msg *NlMsgBuilder, typ uint16) {
	msg.PutNestedAttrs(typ, func () {
		for _, k := range(keys) {
			if !k.Ignored() { k.putMaskNlAttr(msg) }
		}
	})
}

// A FlowKeyParser describes how to parse a flow key of a particular
// type from a netlnk message
type FlowKeyParser struct {
	// Flow key parsing function
	//
	// key may be nil if the relevant attribute wasn't provided.
	// The generally means that the mask will indicate that the
	// flow key is Ignored.
	parse func (typ uint16, key []byte, mask []byte) (FlowKey, error)

	// Special mask values indicating that the flow key is an
	// exact match or Ignored.
	exactMask []byte
	ignoreMask []byte
}

// Maps an NL attribute type to the corresponding FlowKeyParser
type FlowKeyParsers map[uint16]FlowKeyParser

func parseFlowKeys(keys Attrs, masks Attrs, parsers FlowKeyParsers) (res FlowKeys, err error) {
	res = make(FlowKeys)

	for typ, key := range(keys) {
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
			if !ok { mask = parser.ignoreMask }
		}


		res[typ], err = parser.parse(typ, key, mask)
		if err != nil {
			return nil, err
		}
	}

	if masks != nil {
		for typ, mask := range(masks) {
			_, ok := keys[typ]
			if ok { continue }

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
	km := make([]byte, size * 2)
	mask := km[size:]
	for i := range(mask) { mask[i] = 0xff }
	return BlobFlowKey{typ: typ, keyMask: km}
}

func (key BlobFlowKey) pointer() unsafe.Pointer {
	return unsafe.Pointer(&key.keyMask[0])
}

func (key BlobFlowKey) typeId() uint16 {
	return key.typ
}

func (key BlobFlowKey) putKeyNlAttr(msg *NlMsgBuilder) {
	msg.PutSliceAttr(key.typ, key.keyMask[:len(key.keyMask) / 2])
}

func (key BlobFlowKey) putMaskNlAttr(msg *NlMsgBuilder) {
	msg.PutSliceAttr(key.typ, key.keyMask[len(key.keyMask) / 2:])
}

func (key BlobFlowKey) Ignored() bool {
	for _, b := range(key.keyMask[len(key.keyMask) / 2:]) {
		if b != 0 { return false }
	}

	return true
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
	if !ok { return false }
	b := bx.toBlobFlowKey()

	size := len(a.keyMask)
	if len(b.keyMask) != size { return false }
	size /= 2

	amask := a.keyMask[size:]
	bmask := b.keyMask[size:]
	for i := range(amask) {
		if amask[i] != bmask[i] || ((a.keyMask[i] ^ b.keyMask[i]) & amask[i]) != 0 { return false }
	}

	return true
}

func parseBlobFlowKey(typ uint16, key []byte, mask []byte, size int) (BlobFlowKey, error) {
	res := BlobFlowKey{typ:typ}
	res.keyMask = make([]byte, size * 2)

	if mask != nil {
		if len(mask) != size {
			return res, fmt.Errorf("flow key mask type %d has wrong length (expected %d bytes, got %d)", typ, size, len(mask))
		}

		copy(res.keyMask[size:], mask)
	} else {
		// "OVS_FLOW_ATTR_MASK: ... Omitting attribute is
		// treated as wildcarding all corresponding fields."
		mask = res.keyMask[size:]
		for i := range(mask) { mask[i] = 0x00 }
	}

	if key != nil {
		if len(key) != size {
			return res, fmt.Errorf("flow key type %d has wrong length (expected %d bytes, got %d)", typ, size, len(key))
		}

		copy(res.keyMask, key)
	} else {
		// The kernel does produce masks without a
		// corresponding key.  But in such cases the mask
		// should show that the key value is Ignored.
		for _, mb := range(mask) {
			if mb != 0 {
				return res, fmt.Errorf("flow key type %d has non-zero mask without a value (mask %v)", typ, mask)
			}
		}
	}

	return res, nil
}

func blobFlowKeyParser(size int, wrap func (BlobFlowKey) FlowKey) FlowKeyParser {
	exact := make([]byte, size)
	for i := range(exact) { exact[i] = 0xff }

	return FlowKeyParser{
		parse: func (typ uint16, key []byte, mask []byte) (FlowKey, error) {
			bfk, err := parseBlobFlowKey(typ, key, mask, size)
			if err != nil { return nil, err }
			if wrap == nil {
				return bfk, nil
			} else {
				return wrap(bfk), nil
			}
		},
		ignoreMask: make([]byte, size),
		exactMask: exact,
	}
}

// OVS_KEY_ATTR_IN_PORT: Incoming port number
//
// This flow key is problematic.  First, the kernel always does
// an exact match for IN_PORT, i.e. it takes the mask to be 0xffffffff
// if the key is set at all.  Second, when reporting the mask, the
// kernel always sets the upper 16 bits, probably because port numbers
// are 16 bits in the kernel, but IN_PORT is 32-bits.  It does this
// even if the IN_PORT flow key was not set.  As a result, we take any
// mask other than 0xffffffff to mean ignored.

func parseInPortFlowKey(typ uint16, key []byte, mask []byte) (FlowKey, error) {
	exact := true
	for _, b := range(mask) { exact = exact && b == 0xff }
	if !exact { for i := range(mask) { mask[i] = 0 } }
	return parseBlobFlowKey(typ, key, mask, 4)
}

// OVS_KEY_ATTR_ETHERNET: Ethernet header flow key

type EthernetFlowKey struct {
	BlobFlowKey
}

func NewEthernetFlowKey(src [ETH_ALEN]byte, dst [ETH_ALEN]byte) FlowKey {
	fk := NewBlobFlowKey(OVS_KEY_ATTR_ETHERNET, SizeofOvsKeyEthernet)
	ek := (*OvsKeyEthernet)(fk.pointer())
	ek.EthSrc = src
	ek.EthDst = dst
	return EthernetFlowKey{fk}
}

func (k EthernetFlowKey) toOvsKeyEthernet() *OvsKeyEthernet {
	return (*OvsKeyEthernet)(k.pointer())
}

func (k EthernetFlowKey) EthSrc() [ETH_ALEN]byte {
	return k.toOvsKeyEthernet().EthSrc
}

func (k EthernetFlowKey) EthDst() [ETH_ALEN]byte {
	return k.toOvsKeyEthernet().EthDst
}

var ethernetFlowKeyParser = blobFlowKeyParser(SizeofOvsKeyEthernet,
	func (fk BlobFlowKey) FlowKey { return EthernetFlowKey{fk} })

// OVS_KEY_ATTR_TUNNEL: Tunnel flow key.  This is more elaborate than
// other flow keys because it consists of a set of attributes.

type TunnelFlowKey struct {
	FlowKeys
}

func (TunnelFlowKey) typeId() uint16 {
	return OVS_KEY_ATTR_TUNNEL
}

func (key TunnelFlowKey) putKeyNlAttr(msg *NlMsgBuilder) {
	key.FlowKeys.toKeyNlAttrs(msg, OVS_KEY_ATTR_TUNNEL)
}

func (key TunnelFlowKey) putMaskNlAttr(msg *NlMsgBuilder) {
	key.FlowKeys.toMaskNlAttrs(msg, OVS_KEY_ATTR_TUNNEL)
}

func (a TunnelFlowKey) Equals(gb FlowKey) bool {
	b, ok := gb.(TunnelFlowKey)
	if !ok { return false }

	return a.FlowKeys.Equals(b.FlowKeys)
}

var tunnelSubkeyParsers = FlowKeyParsers {
	OVS_TUNNEL_KEY_ATTR_TTL: blobFlowKeyParser(1, nil),
}

func parseTunnelFlowKey(typ uint16, key []byte, mask []byte) (FlowKey, error) {
	var keys Attrs
	var err error
	if key != nil {
		keys, err = ParseNestedAttrs(key)
		if err != nil { return nil, err }
	} else {
		keys = make(Attrs)
	}

	masks, err := ParseNestedAttrs(mask)
	if err != nil { return nil, err }

	fk, err := parseFlowKeys(keys, masks, tunnelSubkeyParsers)
	if err != nil { return nil, err }

	return TunnelFlowKey{fk}, nil
}

var flowKeyParsers = FlowKeyParsers {
	// Packet QoS priority flow key
	OVS_KEY_ATTR_PRIORITY: blobFlowKeyParser(4, nil),

	OVS_KEY_ATTR_IN_PORT: FlowKeyParser{
		parse: parseInPortFlowKey,
		exactMask: []byte { 0xff, 0xff, 0xff, 0xff },
		ignoreMask: []byte { 0, 0, 0, 0 },
	},

	OVS_KEY_ATTR_ETHERNET: ethernetFlowKeyParser,
	OVS_KEY_ATTR_ETHERTYPE: blobFlowKeyParser(2, nil),
	OVS_KEY_ATTR_SKB_MARK: blobFlowKeyParser(4, nil),

	OVS_KEY_ATTR_TUNNEL: FlowKeyParser{
		parse: parseTunnelFlowKey,
		exactMask: nil,
		ignoreMask: []byte {},
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
		dpif: dp.dpif,
		portNo: uint32(oa),
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
	if !ok { return false }
	return a == b;
}

func parseOutputAction(typ uint16, data []byte) (Action, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("flow action type %d has wrong length (expects 4 bytes, got %d)", typ, len(data))
	}

	return OutputAction(getUint32(data, 0)), nil
}

var actionParsers = map[uint16](func (uint16, []byte) (Action, error)) {
	OVS_ACTION_ATTR_OUTPUT: parseOutputAction,
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
	f.FlowKeys.toKeyNlAttrs(msg, OVS_FLOW_ATTR_KEY)
	f.FlowKeys.toMaskNlAttrs(msg, OVS_FLOW_ATTR_MASK)

	// ACTIONS is required
	msg.PutNestedAttrs(OVS_FLOW_ATTR_ACTIONS, func () {
		for _, a := range(f.Actions) { a.toNlAttr(msg) }
	})
}

func (a FlowSpec) Equals(b FlowSpec) bool {
	if !a.FlowKeys.Equals(b.FlowKeys) { return false }
	if len(a.Actions) != len(b.Actions) { return false }

	for i := range(a.Actions) {
		if !a.Actions[i].Equals(b.Actions[i]) {	return false }
	}

	return true
}

func (dp DatapathHandle) checkOvsHeader(msg *NlMsgParser) error {
	ovshdr, err := msg.takeOvsHeader()
	if err != nil { return err }

	if ovshdr.DpIfIndex != dp.ifindex {
		return fmt.Errorf("wrong datapath ifindex in response (got %d, expected %d)", ovshdr.DpIfIndex, dp.ifindex)
	}

	return nil
}

func (dp DatapathHandle) parseFlowSpec(msg *NlMsgParser) (FlowSpec, error) {
	f := FlowSpec{}

	_, err := msg.ExpectNlMsghdr(dp.dpif.familyIds[FLOW])
	if err != nil { return f, err }

	_, err = msg.ExpectGenlMsghdr(OVS_FLOW_CMD_NEW)
	if err != nil { return f, err }

	err = dp.checkOvsHeader(msg)
	if err != nil { return f, err }

	attrs, err := msg.TakeAttrs()
	if err != nil { return f, err }

	keys, err := attrs.GetNestedAttrs(OVS_FLOW_ATTR_KEY, false)
	if err != nil { return f, err}

	masks, err := attrs.GetNestedAttrs(OVS_FLOW_ATTR_MASK, true)
	if err != nil { return f, err}

	f.FlowKeys, err = parseFlowKeys(keys, masks, flowKeyParsers)
	if err != nil { return f, err }

	actattrs, err := attrs.GetOrderedAttrs(OVS_FLOW_ATTR_ACTIONS)
	if err != nil { return f, err }

	actions := make([]Action, 0)
	for _, actattr := range(actattrs) {
		parser, ok := actionParsers[actattr.typ]
		if !ok {
			return f, fmt.Errorf("unknown action type %d (value %v)", actattr.typ, actattr.val)
		}

		action, err := parser(actattr.typ, actattr.val)
		if err != nil { return f, err }
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

type NoSuchFlowError struct {}
func (NoSuchFlowError) Error() string {	return "no such flow" }

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

	consumer := func (resp *NlMsgParser) error {
		f, err := dp.parseFlowSpec(resp)
		if err != nil {	return err }
		res = append(res, f)
		return nil
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return nil, err
	}

	return res, nil
}
