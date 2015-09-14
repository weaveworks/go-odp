package odp

import (
	"sync"
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

	portId := sock.PortId()
	go consumeMisses(dp, sock, consumer)

	// We need to set the upcall port ID on all vports.  That
	// includes vports that get added while we are listening, so
	// we need to listen for them too.
	vportDpif, err := dp.dpif.Reopen()
	if err != nil {
		return err
	}

	vportConsumer := &missVportConsumer{
		dpif:         vportDpif,
		upcallPortId: portId,
		missConsumer: consumer,
		vportsDone:   make(map[VportID]struct{}),
	}
	if err = dp.dpif.ConsumeVportEvents(vportConsumer); err != nil {
		return err
	}

	vports, err := dp.EnumerateVports()
	if err != nil {
		return err
	}

	for _, vport := range vports {
		err = vportConsumer.setVportUpcallPortId(dp, vport.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

type missVportConsumer struct {
	dpif         *Dpif
	upcallPortId uint32
	missConsumer MissConsumer

	lock       sync.Mutex
	vportsDone map[VportID]struct{}
}

// Set a vport's upcall port ID.  This generates a OVS_VPORT_CMD_NEW
// (not a OVS_VPORT_CMD_SET), leading to a call of the New method
// below.  So we need to record which vports we already processed in
// order to avoid a vicious circle.
func (c *missVportConsumer) setVportUpcallPortId(dp DatapathHandle, vport VportID) error {
	c.lock.Lock()
	_, doneAlready := c.vportsDone[vport]
	c.lock.Unlock()

	if doneAlready {
		return nil
	}

	if err := dp.setVportUpcallPortId(vport, c.upcallPortId); err != nil {
		return err
	}

	c.lock.Lock()
	c.vportsDone[vport] = struct{}{}
	c.lock.Unlock()
	return nil
}

func (c *missVportConsumer) New(ifindex int32, vport Vport) error {
	return c.setVportUpcallPortId(DatapathHandle{c.dpif, ifindex}, vport.ID)
}

func (c *missVportConsumer) Delete(ifindex int32, vport Vport) error {
	c.lock.Lock()
	delete(c.vportsDone, vport.ID)
	c.lock.Unlock()
	return nil
}

func (c *missVportConsumer) Error(err error, stopped bool) {
	c.missConsumer.Error(err, stopped)
}

func consumeMisses(dp DatapathHandle, sock *NetlinkSocket, consumer MissConsumer) {
	defer sock.Close()
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
