package main

import (
        "syscall"
	"fmt"
	"unsafe"
)

func genlMsghdrAt(data []byte, pos int) *GenlMsghdr {
	return (*GenlMsghdr)(unsafe.Pointer(&data[pos]))
}

func (nlmsg *NlMsgBuilder) PutGenlMsghdr(cmd uint8) (*GenlMsghdr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	pos := nlmsg.Grow(SizeofGenlMsghdr)
	res := genlMsghdrAt(nlmsg.buf, pos)
	res.Cmd = cmd
	return res
}

func (nlmsg *NlMsgButcher) TakeGenlMsghdr(expectCmd uint8) (*GenlMsghdr, error) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	gh := genlMsghdrAt(nlmsg.data, nlmsg.pos)
	if err := nlmsg.Advance(SizeofGenlMsghdr); err != nil {
		return nil, err
	}

	if gh.Cmd != expectCmd {
		return nil, fmt.Errorf("generic netlink response has wrong cmd (got %d, expected %d)", gh.Cmd, expectCmd)
	}

	return gh, nil
}

func (s *NetlinkSocket) LookupGenlFamily(name string) (uint16, error) {
	req := NewNlMsgBuilder(syscall.NLM_F_REQUEST, GENL_ID_CTRL)

	req.PutGenlMsghdr(CTRL_CMD_GETFAMILY)
	req.PutStringAttr(CTRL_ATTR_FAMILY_NAME, name)
	b, seq := req.Finish()

	if err := s.send(b); err != nil {
		return 0, err
        }

	rb, err := s.recv(0)
        if err != nil {
		return 0, err
        }

	if err = s.checkResponse(rb, seq); err != nil {
		return 0, err
	}

	resp := NewNlMsgButcher(rb)
	if _, err := resp.TakeNlMsghdr(GENL_ID_CTRL); err != nil {
		return 0, err
	}

	if _, err := resp.TakeGenlMsghdr(CTRL_CMD_NEWFAMILY); err != nil {
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

func (s *NetlinkSocket) Dump(typ uint16, cmd uint8) error {
	// We need the ack in order to know when all response items
	// have arrived.  MSG_F_DONE is the official way, but that
	// doesn't work when there are no items.  iproute2 doesn't
	// seemto use this technique; I've no idea how it handles the
	// no-items case.

	req := NewNlMsgBuilder(syscall.NLM_F_DUMP | syscall.NLM_F_ACK | syscall.NLM_F_REQUEST, typ)
	req.PutGenlMsghdr(cmd)
	b, _ := req.Finish()

	if err := s.send(b); err != nil {
		return err
        }

	rb, err := s.recv(0)
        if err != nil {
		return err
        }

	fmt.Printf("XXX %v\n", rb)
	return nil
}
