package odp

import (
	"fmt"
	"syscall"
)

type datapathInfo struct {
	ifindex int32
	name    string
}

func (dpif *Dpif) parseDatapathInfo(msg *NlMsgParser) (res datapathInfo, err error) {
	_, err = msg.ExpectNlMsghdr(dpif.families[DATAPATH].id)
	if err != nil {
		return
	}

	_, err = msg.ExpectGenlMsghdr(OVS_DP_CMD_NEW)
	if err != nil {
		return
	}

	ovshdr, err := msg.takeOvsHeader()
	if err != nil {
		return
	}
	res.ifindex = ovshdr.DpIfIndex

	attrs, err := msg.TakeAttrs()
	if err != nil {
		return
	}

	res.name, err = attrs.GetString(OVS_DP_ATTR_NAME)
	return
}

type DatapathHandle struct {
	dpif    *Dpif
	ifindex int32
}

func (dp DatapathHandle) IfIndex() int32 {
	return dp.ifindex
}

func (dpif *Dpif) CreateDatapath(name string) (DatapathHandle, error) {
	var features uint32 = OVS_DP_F_UNALIGNED | OVS_DP_F_VPORT_PIDS

	req := NewNlMsgBuilder(RequestFlags, dpif.families[DATAPATH].id)
	req.PutGenlMsghdr(OVS_DP_CMD_NEW, OVS_DATAPATH_VERSION)
	req.putOvsHeader(0)
	req.PutStringAttr(OVS_DP_ATTR_NAME, name)
	req.PutUint32Attr(OVS_DP_ATTR_UPCALL_PID, 0)
	req.PutUint32Attr(OVS_DP_ATTR_USER_FEATURES, features)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return DatapathHandle{}, err
	}

	dpi, err := dpif.parseDatapathInfo(resp)
	if err != nil {
		return DatapathHandle{}, err
	}

	return DatapathHandle{dpif: dpif, ifindex: dpi.ifindex}, nil
}

func IsDatapathNameAlreadyExistsError(err error) bool {
	return err == NetlinkError(syscall.EEXIST)
}

func (dpif *Dpif) LookupDatapath(name string) (DatapathHandle, error) {
	req := NewNlMsgBuilder(RequestFlags, dpif.families[DATAPATH].id)
	req.PutGenlMsghdr(OVS_DP_CMD_GET, OVS_DATAPATH_VERSION)
	req.putOvsHeader(0)
	req.PutStringAttr(OVS_DP_ATTR_NAME, name)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return DatapathHandle{}, err
	}

	dpi, err := dpif.parseDatapathInfo(resp)
	if err != nil {
		return DatapathHandle{}, err
	}

	return DatapathHandle{dpif: dpif, ifindex: dpi.ifindex}, nil
}

type Datapath struct {
	Handle DatapathHandle
	Name   string
}

func (dpif *Dpif) LookupDatapathByIndex(ifindex int32) (Datapath, error) {
	req := NewNlMsgBuilder(RequestFlags, dpif.families[DATAPATH].id)
	req.PutGenlMsghdr(OVS_DP_CMD_GET, OVS_DATAPATH_VERSION)
	req.putOvsHeader(ifindex)

	resp, err := dpif.sock.Request(req)
	if err != nil {
		return Datapath{}, err
	}

	dpi, err := dpif.parseDatapathInfo(resp)
	if err != nil {
		return Datapath{}, err
	}

	return Datapath{
		Handle: DatapathHandle{dpif: dpif, ifindex: ifindex},
		Name:   dpi.name,
	}, nil
}

func IsNoSuchDatapathError(err error) bool {
	return err == NetlinkError(syscall.ENODEV)
}

func (dpif *Dpif) EnumerateDatapaths() (map[string]DatapathHandle, error) {
	res := make(map[string]DatapathHandle)

	req := NewNlMsgBuilder(DumpFlags, dpif.families[DATAPATH].id)
	req.PutGenlMsghdr(OVS_DP_CMD_GET, OVS_DATAPATH_VERSION)
	req.putOvsHeader(0)

	consumer := func(resp *NlMsgParser) error {
		dpi, err := dpif.parseDatapathInfo(resp)
		if err != nil {
			return err
		}
		res[dpi.name] = DatapathHandle{dpif: dpif, ifindex: dpi.ifindex}
		return nil
	}

	err := dpif.sock.RequestMulti(req, consumer)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (dp DatapathHandle) Delete() error {
	req := NewNlMsgBuilder(RequestFlags, dp.dpif.families[DATAPATH].id)
	req.PutGenlMsghdr(OVS_DP_CMD_DEL, OVS_DATAPATH_VERSION)
	req.putOvsHeader(dp.ifindex)

	_, err := dp.dpif.sock.Request(req)
	if err != nil {
		return err
	}

	dp.dpif = nil
	dp.ifindex = 0
	return nil
}

func (dp DatapathHandle) checkOvsHeader(msg *NlMsgParser) error {
	ovshdr, err := msg.takeOvsHeader()
	if err != nil {
		return err
	}

	if ovshdr.DpIfIndex != dp.ifindex {
		return fmt.Errorf("wrong datapath ifindex received (got %d, expected %d)", ovshdr.DpIfIndex, dp.ifindex)
	}

	return nil
}
