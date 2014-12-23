package odp

import (
        "syscall"
	"fmt"
	"unsafe"
)

func genlMsghdrAt(data []byte, pos int) *GenlMsghdr {
	return (*GenlMsghdr)(unsafe.Pointer(&data[pos]))
}

func (nlmsg *NlMsgBuilder) PutGenlMsghdr(cmd uint8, version uint8) (*GenlMsghdr) {
	pos := nlmsg.AlignGrow(syscall.NLMSG_ALIGNTO, SizeofGenlMsghdr)
	res := genlMsghdrAt(nlmsg.buf, pos)
	res.Cmd = cmd
	res.Version = version
	return res
}

func (nlmsg *NlMsgParser) ExpectGenlMsghdr(cmd uint8) (*GenlMsghdr, error) {
	pos, err := nlmsg.AlignAdvance(syscall.NLMSG_ALIGNTO, SizeofGenlMsghdr)
	if err != nil {
		return nil, err
	}

	gh := genlMsghdrAt(nlmsg.data, pos)
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
