package odp

import (
	"syscall"
)

func (dp DatapathHandle) ConsumeMisses(consumer func(Attrs) error, errorHandler func(error)) error {
	sock, err := OpenNetlinkSocket(syscall.NETLINK_GENERIC)
	if err != nil {
		return err
	}

	go consumeMisses(dp, sock, consumer, errorHandler)

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

func consumeMisses(dp DatapathHandle, sock *NetlinkSocket, consumer func(attrs Attrs) error, errorHandler func(error)) {
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

		return consumer(attrs)
	}

	for {
		err := sock.Receive(0, 0, func(msg *NlMsgParser) (bool, error) {
			err := handleUpcall(msg)
			if err != nil {
				errorHandler(err)
			}

			return false, nil
		})

		if err != nil {
			errorHandler(err)
		}
	}
}
