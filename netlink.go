package main

import (
        "syscall"
	"unsafe"
	"errors"
	"fmt"
	"sync/atomic"
)

func align(n int, a int) int {
	return (n + a - 1) & -a;
}

type NetlinkSocket struct {
	fd int
	addr *syscall.SockaddrNetlink
}

func getNetlinkSocket(protocol int) (*NetlinkSocket, error) {
        fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW, protocol)
        if err != nil {
                return nil, err
        }

	addr := syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}
        if err := syscall.Bind(fd, &addr); err != nil {
                syscall.Close(fd)
                return nil, err
        }

	localaddr, err := syscall.Getsockname(fd)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}

	switch nladdr := localaddr.(type) {
        case *syscall.SockaddrNetlink:
		return &NetlinkSocket{fd: fd, addr: nladdr}, nil

	default:
		return nil, errors.New("Wrong socket address type")
        }
}

func (s *NetlinkSocket) Close() {
        syscall.Close(s.fd)
}

func (s *NetlinkSocket) send(buf []byte) error {
	fmt.Printf("AAA %v\n", buf)
	sa := syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Pid: 0,
		Groups: 0,
	}

	return syscall.Sendto(s.fd, buf, 0, &sa)
}

func (s *NetlinkSocket) recv(peer uint32) ([]byte, error) {
        rb := make([]byte, syscall.Getpagesize())
        nr, from, err := syscall.Recvfrom(s.fd, rb, 0)
        if err != nil {
                return nil, err
        }

	switch nlfrom := from.(type) {
        case *syscall.SockaddrNetlink:
		if (nlfrom.Pid != peer) {
			return nil, errors.New("netlink peer mismatch")
		}

		return rb[:nr], nil

	default:
		return nil, errors.New("Wrong socket address type")
        }
}

func nlMsghdrAt(data []byte, pos int) *syscall.NlMsghdr {
	return (*syscall.NlMsghdr)(unsafe.Pointer(&data[pos]))
}

func genlMsghdrAt(data []byte, pos int) *GenlMsghdr {
	return (*GenlMsghdr)(unsafe.Pointer(&data[pos]))
}

func rtAttrAt(data []byte, pos int) *syscall.RtAttr {
	return (*syscall.RtAttr)(unsafe.Pointer(&data[pos]))
}

func nlMsgerrAt(data []byte, pos int) *syscall.NlMsgerr {
	return (*syscall.NlMsgerr)(unsafe.Pointer(&data[pos]))
}


type NlMsgBuilder struct {
	buf []byte
}

func NewNlMsgBuilder(flags uint16, typ uint16) *NlMsgBuilder {
	//buf := make([]byte, syscall.NLMSG_HDRLEN, syscall.Getpagesize())
	buf := make([]byte, syscall.NLMSG_HDRLEN, syscall.NLMSG_HDRLEN)
	nlmsg := &NlMsgBuilder{buf: buf}
	h := nlMsghdrAt(buf, 0)
	h.Flags = flags
	h.Type = typ
	return nlmsg
}

// Expand the array underlying a slice to have capacity of at least l
func expand(buf []byte, l int) []byte {
	c := (cap(buf) + 1) * 3 / 2
	for l > c { c = (c + 1) * 3 / 2 }
	new := make([]byte, len(buf), c)
	copy(new, buf)
	return new
}

func (nlmsg *NlMsgBuilder) Align(a int) {
	l := align(len(nlmsg.buf), a)
	if l > cap(nlmsg.buf) { nlmsg.buf = expand(nlmsg.buf, l) }
	nlmsg.buf = nlmsg.buf[:l]
}

func (nlmsg *NlMsgBuilder) Grow(size uintptr) int {
	pos := len(nlmsg.buf)
	l := pos + int(size)
	if l > cap(nlmsg.buf) { nlmsg.buf = expand(nlmsg.buf, l) }
	nlmsg.buf = nlmsg.buf[:l]
	return pos
}

var nextSeqNo uint32

func (nlmsg *NlMsgBuilder) Finish() (res []byte, seq uint32) {
	h := nlMsghdrAt(nlmsg.buf, 0)
	h.Len = uint32(len(nlmsg.buf))
	seq = atomic.AddUint32(&nextSeqNo, 1)
	h.Seq = seq
	res = nlmsg.buf
	nlmsg.buf = nil
	return
}

func (nlmsg *NlMsgBuilder) AddGenlMsghdr(cmd uint8) (res *GenlMsghdr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	pos := nlmsg.Grow(unsafe.Sizeof(*res))
	res = genlMsghdrAt(nlmsg.buf, pos)
	res.Cmd = cmd
	return
}

type RtAttr struct {
	pos int
}

func (nlmsg *NlMsgBuilder) BeginRtAttr(typ uint16) (res RtAttr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	var rta *syscall.RtAttr
	res.pos = nlmsg.Grow(unsafe.Sizeof(*rta))
	rta = rtAttrAt(nlmsg.buf, res.pos)
	rta.Type = typ
	return
}

func (nlmsg *NlMsgBuilder) FinishRtAttr(rta RtAttr) {
	rtap := rtAttrAt(nlmsg.buf, rta.pos)
	rtap.Len = uint16(len(nlmsg.buf) - rta.pos)
}

func (nlmsg *NlMsgBuilder) addStringZ(str string) {
	l := len(str)
	pos := nlmsg.Grow(uintptr(l) + 1)
	copy(nlmsg.buf[pos:], str)
	nlmsg.buf[pos + l] = 0
}

func (nlmsg *NlMsgBuilder) AddAttr(typ uint16, str string) {
	rta := nlmsg.BeginRtAttr(typ)
	nlmsg.Align(syscall.RTA_ALIGNTO)
	nlmsg.addStringZ(str)
	nlmsg.FinishRtAttr(rta)
}

func (s *NetlinkSocket) checkResponse(data []byte, expectedSeq uint32) error {
	if len(data) < syscall.NLMSG_HDRLEN {
		return fmt.Errorf("truncated netlink message header (have %d bytes)", len(data))
	}

	h := nlMsghdrAt(data, 0)
	if len(data) < int(h.Len) {
		return fmt.Errorf("truncated netlink message (have %d bytes, expected %d)", len(data), h.Len)
	}

	if h.Pid != s.addr.Pid {
		return fmt.Errorf("netlink reply pid mismatch (got %d, expected %d)", h.Pid, s.addr.Pid)
	}

	if h.Seq != expectedSeq {
		return fmt.Errorf("netlink reply sequence number mismatch (got %d, expected %d)", h.Seq, expectedSeq)
	}

	payload := data[syscall.NLMSG_HDRLEN:h.Len]
	if h.Type == syscall.NLMSG_ERROR {
		nlerr := nlMsgerrAt(payload, 0)

		if nlerr.Error == 0 {
			// An ack response
			return nil
		}

		return fmt.Errorf("netlink error response: %s",
			syscall.Errno(-nlerr.Error).Error())
	}

	if int(h.Len) > align(len(data), syscall.NLMSG_ALIGNTO) {
		return fmt.Errorf("multiple netlink messages recieved")
	}

	return nil
}

type NlMsgButcher struct {
	data []byte
	pos int
}

func NewNlMsgButcher(data []byte) *NlMsgButcher {
	return &NlMsgButcher{data: data, pos: 0}
}

func (nlmsg *NlMsgButcher) Align(a int) {
	nlmsg.pos = align(nlmsg.pos, a)
}

func (nlmsg *NlMsgButcher) Advance(n uintptr) error {
	pos := nlmsg.pos + int(n)
	if pos > len(nlmsg.data) {
		return fmt.Errorf("netlink response payload truncated (at %d, expected at least %d bytes)", nlmsg.pos, n)
	}

	nlmsg.pos = pos
	return nil
}

func (nlmsg *NlMsgButcher) TakeNlMsghdr(expectType uint16) (*syscall.NlMsghdr, error) {
	h := nlMsghdrAt(nlmsg.data, 0)
	nlmsg.pos += syscall.NLMSG_HDRLEN

	if h.Type != expectType {
		return nil, fmt.Errorf("netlink response has wrong type (got %d, expected %d)", h.Type, expectType)
	}

	return h, nil
}

func (nlmsg *NlMsgButcher) TakeGenlMsghdr(expectCmd uint8) (*GenlMsghdr, error) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	gh := genlMsghdrAt(nlmsg.data, nlmsg.pos)
	if err := nlmsg.Advance(unsafe.Sizeof(*gh)); err != nil {
		return nil, err
	}

	if gh.Cmd != expectCmd {
		return nil, fmt.Errorf("generic netlink response has wrong cmd (got %d, expected %d)", gh.Cmd, expectCmd)
	}

	return gh, nil
}

func uint16At(data []byte, pos int) *uint16 {
	return (*uint16)(unsafe.Pointer(&data[pos]))
}

type Attr struct {
	val []byte
}

func (nlmsg *NlMsgButcher) checkData(l uintptr, obj string) error {
	if nlmsg.pos + int(l) <= len(nlmsg.data) {
		return nil
	} else {
		return fmt.Errorf("truncated %s (have %d bytes, expected %d)", obj, len(nlmsg.data) - nlmsg.pos, l)
	}
}

func (nlmsg *NlMsgButcher) TakeAttrs() (attrs map[uint16]Attr, err error) {
	attrs = make(map[uint16]Attr)
	for {
		apos := align(nlmsg.pos, syscall.RTA_ALIGNTO)
		if len(nlmsg.data) <= apos {
			return
		}

		nlmsg.pos = apos

		var rta *syscall.RtAttr
		if err = nlmsg.checkData(unsafe.Sizeof(*rta), "netlink attribute"); err != nil {
			return
		}

		rta = rtAttrAt(nlmsg.data, nlmsg.pos)
		rtaLen := uintptr(rta.Len)
		if err = nlmsg.checkData(rtaLen, "netlink attribute"); err != nil {
			return
		}

		valpos := align(nlmsg.pos + int(unsafe.Sizeof(*rta)),
			syscall.RTA_ALIGNTO)
		attrs[rta.Type] = Attr{nlmsg.data[valpos:nlmsg.pos + int(rta.Len)]}
		nlmsg.pos += int(rtaLen)
	}
}

func (s *NetlinkSocket) resolveFamily() error {
	req := NewNlMsgBuilder(syscall.NLM_F_REQUEST | syscall.NLM_F_ACK,
		GENL_ID_CTRL)

	req.AddGenlMsghdr(CTRL_CMD_GETFAMILY)
	req.AddAttr(CTRL_ATTR_FAMILY_NAME, "ovs_datapath")
	b, seq := req.Finish()

	if err := s.send(b); err != nil {
		return err
        }

	rb, err := s.recv(0)
        if err != nil {
		return err
        }

	if err = s.checkResponse(rb, seq); err != nil {
		return err
	}

	resp := NewNlMsgButcher(rb)
	if _, err := resp.TakeNlMsghdr(GENL_ID_CTRL); err != nil {
		return err
	}

	if _, err := resp.TakeGenlMsghdr(CTRL_CMD_NEWFAMILY); err != nil {
		return err
	}

	attrs, err := resp.TakeAttrs()
	if err != nil {
		return err
	}

	fmt.Println("XXX %v\n", attrs)

	return nil
}
