package main

import (
        "syscall"
	"fmt"
	"unsafe"
)

func genlMsghdrAt(data []byte, pos int) *GenlMsghdr {
	return (*GenlMsghdr)(unsafe.Pointer(&data[pos]))
}

func (nlmsg *NlMsgBuilder) PutGenlMsghdr(cmd uint8, version uint8) (*GenlMsghdr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	pos := nlmsg.Grow(SizeofGenlMsghdr)
	res := genlMsghdrAt(nlmsg.buf, pos)
	res.Cmd = cmd
	res.Version = version
	return res
}

func (nlmsg *NlMsgButcher) ExpectGenlMsghdr(cmd uint8) (*GenlMsghdr, error) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	gh := genlMsghdrAt(nlmsg.data, nlmsg.pos)
	if err := nlmsg.Advance(SizeofGenlMsghdr); err != nil {
		return nil, err
	}

	if gh.Cmd != cmd {
		return nil, fmt.Errorf("generic netlink response has wrong cmd (got %d, expected %d)", gh.Cmd, cmd)
	}

	return gh, nil
}

func (s *NetlinkSocket) LookupGenlFamily(name string) (uint16, error) {
	req := NewNlMsgBuilder(RequestFlags, GENL_ID_CTRL)

	req.PutGenlMsghdr(CTRL_CMD_GETFAMILY, 0)
	req.PutStringAttr(CTRL_ATTR_FAMILY_NAME, name)

	resp, err := s.Request(req)
	if err != nil {
		return 0, err
	}

	if _, err := resp.ExpectNlMsghdr(GENL_ID_CTRL); err != nil {
		return 0, err
	}

	if _, err := resp.ExpectGenlMsghdr(CTRL_CMD_NEWFAMILY); err != nil {
		return 0, err
	}

	attrs, err := resp.TakeAttrs()
	if err != nil {
		return 0, err
	}

	id, err := attrs.GetUint16(CTRL_ATTR_FAMILY_ID)
	if err != nil {
		return 0, err
	}

	return id, nil
}
