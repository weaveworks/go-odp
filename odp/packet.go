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
		err = vport.Handle.setUpcallPortId(sock.PortId())
		if err != nil {
			return err
		}
	}

	return nil
}

func consumeMisses(dp DatapathHandle, sock *NetlinkSocket, consumer MissConsumer) {
	handleUpcall := func(msg *NlMsgParser) error {
		_, err := msg.ExpectNlMsghdr(dp.dpif.familyIds[PACKET])
		if err != nil {
			return err
		}

		_, err = msg.ExpectGenlMsghdr(OVS_PACKET_CMD_MISS)
		if err != nil {
			return err
		}

		err = dp.checkOvsHeader(msg)
		if err != nil {
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
	}

	for {
		err := sock.Receive(0, 0, func(msg *NlMsgParser) (bool, error) {
			err := handleUpcall(msg)
			if err != nil {
				consumer.Error(err, false)
			}

			return false, nil
		})

		if err != nil {
			consumer.Error(err, true)
		}
	}
}
