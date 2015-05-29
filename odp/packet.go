package odp

import (
	"syscall"
)

type MissConsumer interface {
	Miss(packet []byte, flowKeys FlowKeys) error
	Error(err error, stopped bool)
}

func (dp DatapathHandle) ConsumeMisses(consumer MissConsumer) error {
	sock, err := OpenNetlinkSocket(syscall.NETLINK_GENERIC)
	if err != nil {
		return err
	}

	go consumeMisses(dp, sock, consumer)

	vports, err := dp.EnumerateVports()
	if err != nil {
		return err
	}

	for _, vport := range vports {
		err = dp.setUpcallPortId(vport.ID, sock.PortId())
		if err != nil {
			return err
		}
	}

	return nil
}

func consumeMisses(dp DatapathHandle, sock *NetlinkSocket, consumer MissConsumer) {
	sock.consume(consumer, func(msg *NlMsgParser) error {
		if err := dp.checkNlMsgHeaders(msg, PACKET, OVS_PACKET_CMD_MISS); err != nil {
			return err
		}

		attrs, err := msg.TakeAttrs()
		if err != nil {
			return err
		}

		fkattrs, err := attrs.GetNestedAttrs(OVS_PACKET_ATTR_KEY, false)
		if err != nil {
			return err
		}

		fks, err := ParseFlowKeys(fkattrs, nil)
		if err != nil {
			return err
		}

		return consumer.Miss(attrs[OVS_PACKET_ATTR_PACKET], fks)
	})
}

func (dp DatapathHandle) Execute(packet []byte, keys FlowKeys, actions []Action) error {
	dpif := dp.dpif

	req := NewNlMsgBuilder(RequestFlags, dpif.families[PACKET].id)
	req.PutGenlMsghdr(OVS_PACKET_CMD_EXECUTE, OVS_PACKET_VERSION)
	req.putOvsHeader(dp.ifindex)
	req.PutSliceAttr(OVS_PACKET_ATTR_PACKET, packet)

	req.PutNestedAttrs(OVS_PACKET_ATTR_KEY, func() {
		for _, k := range keys {
			k.putKeyNlAttr(req)
		}
	})

	req.PutNestedAttrs(OVS_PACKET_ATTR_ACTIONS, func() {
		for _, a := range actions {
			a.toNlAttr(req)
		}
	})

	_, err := dpif.sock.send(req)
	return err
}
