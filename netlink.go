package main

import (
        "syscall"
	"unsafe"
	"errors"
	"fmt"
	"sync/atomic"
)

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

type NlMsg struct {
	buf []byte
}

func (msg NlMsg) NlMsghdrAt(pos int) *syscall.NlMsghdr {
	return (*syscall.NlMsghdr)(unsafe.Pointer(&msg.buf[pos]))
}

func (msg NlMsg) GenlMsghdrAt(pos int) *GenlMsghdr {
	return (*GenlMsghdr)(unsafe.Pointer(&msg.buf[pos]))
}

func (msg NlMsg) RtAttrAt(pos int) *syscall.RtAttr {
	return (*syscall.RtAttr)(unsafe.Pointer(&msg.buf[pos]))
}

func (s *NetlinkSocket) validateNlMsghdr(buf []byte, seq uint32) (*syscall.NlMsghdr, error) {
	h := (*syscall.NlMsghdr)(unsafe.Pointer(&buf[0]))
	if len(buf) < syscall.NLMSG_HDRLEN || len(buf) < int(h.Len) {
		return nil, fmt.Errorf("truncated netlink message (got %d bytes, expected %d)", len(buf), h.Len)
	}

	if h.Pid != s.addr.Pid {
		return nil, fmt.Errorf("netlink reply pid mismatch (got %d, expected %d)", h.Pid, s.addr.Pid)
	}

	if h.Seq != seq {
		return nil, fmt.Errorf("netlink reply sequence number mismatch (got %d, expected %d)", h.Seq, seq)
	}

	payload := buf[syscall.NLMSG_HDRLEN:h.Len]

	if h.Type == syscall.NLMSG_ERROR {
		nlerr := (*syscall.NlMsgerr)(unsafe.Pointer(&payload[0]))

		if nlerr.Error == 0 {
			// An ack response
			return nil, nil
		}

		return nil, fmt.Errorf("netlink error reply: %s",
			syscall.Errno(-nlerr.Error).Error())
	}

	return h, nil
}

type NlMsgBuilder struct {
	NlMsg
}

func NewNlMsgBuilder(flags uint16, typ uint16) *NlMsgBuilder {
	buf := make([]byte, syscall.NLMSG_HDRLEN, syscall.Getpagesize())
	nlmsg := &NlMsgBuilder{NlMsg{buf: buf}}
	h := nlmsg.NlMsghdrAt(0)
	h.Flags = flags
	h.Type = typ
	return nlmsg
}

func (nlmsg *NlMsgBuilder) Align(align int) {
	nlmsg.buf = nlmsg.buf[:(len(nlmsg.buf) + align - 1) & -align]
}

func (nlmsg *NlMsgBuilder) Grow(size uintptr) int {
	pos := len(nlmsg.buf)
	nlmsg.buf = nlmsg.buf[:pos + int(size)]
	return pos
}

var nextSeqNo uint32

func (nlmsg *NlMsgBuilder) Finish() (res []byte, seq uint32) {
	h := nlmsg.NlMsghdrAt(0)
	h.Len = uint32(len(nlmsg.buf))
	seq = atomic.AddUint32(&nextSeqNo, 1)
	h.Seq = seq
	res = nlmsg.buf
	nlmsg.buf = nil
	return
}

func (nlmsg *NlMsgBuilder) AddGenlMsghdr(cmd uint8) (res *GenlMsghdr) {
	pos := nlmsg.Grow(unsafe.Sizeof(*res))
	res = nlmsg.GenlMsghdrAt(pos)
	res.cmd = cmd
	return
}

type RtAttr struct {
	pos int
	rta *syscall.RtAttr
}

func (nlmsg *NlMsgBuilder) BeginRtAttr(typ uint16) (res RtAttr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	res.pos = nlmsg.Grow(unsafe.Sizeof(*res.rta))
	res.rta = nlmsg.RtAttrAt(res.pos)
	res.rta.Type = typ
	return
}

func (nlmsg *NlMsgBuilder) FinishRtAttr(rta RtAttr) {
	rta.rta.Len = uint16(len(nlmsg.buf) - rta.pos)
}

func (nlmsg *NlMsgBuilder) AddAttr(typ uint16, str string) {
	rta := nlmsg.BeginRtAttr(typ)
	nlmsg.Align(syscall.RTA_ALIGNTO)
	l := len(nlmsg.buf)
	n := copy(nlmsg.buf[l:cap(nlmsg.buf)], str)
	nlmsg.buf = nlmsg.buf[:l + n + 1]
	nlmsg.buf[l + n] = 0
	nlmsg.FinishRtAttr(rta)
}


func (s *NetlinkSocket) resolveFamily() error {
	nlmsg := NewNlMsgBuilder(syscall.NLM_F_REQUEST | syscall.NLM_F_ACK,
		GENL_ID_CTRL)

	nlmsg.AddGenlMsghdr(CTRL_CMD_GETFAMILY)
	nlmsg.AddAttr(CTRL_ATTR_FAMILY_NAME, "ovs_datapath")
	b, seq := nlmsg.Finish()

	if err := s.send(b); err != nil {
		return err
        }

	rb, err := s.recv(0)
        if err != nil {
		return err
        }

	_, err = s.validateNlMsghdr(rb, seq)
	if err != nil {
		return err
	}

	return nil
}
