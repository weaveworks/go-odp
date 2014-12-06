package main

import (
	"syscall"
	"fmt"
)

type Dpif struct {
	sock *NetlinkSocket
	datapathFamilyId GenlFamilyId
}

func NewDpif() (*Dpif, error) {
	sock, err := OpenNetlinkSocket(syscall.NETLINK_GENERIC)
        if err != nil {
                return nil, err
        }

	dpif := &Dpif{sock: sock}

	family := "ovs_datapath"
	dpif.datapathFamilyId, err = sock.LookupGenlFamily(family)
	if err != nil {
		goto lookupFailure
	}

	return dpif, nil

lookupFailure:
	sock.Close()

	if e, ok := err.(NetlinkError); ok && e.Errno == syscall.ENOENT {
		return nil, fmt.Errorf("Generic netlink family '%s' unavailable; the Open vSwitch kernel module is probably not loaded", family)
	} else {
		return nil, err
	}
}

func (dpif *Dpif) Close() error {
	err := dpif.sock.Close()
	dpif.sock = nil
	return err
}
