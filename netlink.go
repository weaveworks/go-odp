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

type GenlMsghdr struct {
	cmd uint8
	version uint8
	reserved uint16
}

// reserved static generic netlink identifiers:
const (
	GENL_ID_GENERATE = 0
	GENL_ID_CTRL = syscall.NLMSG_MIN_TYPE
	GENL_ID_VFS_DQUOT = syscall.NLMSG_MIN_TYPE + 1
	GENL_ID_PMCRAID = syscall.NLMSG_MIN_TYPE + 2
)

const (
        CTRL_CMD_UNSPEC = 0
        CTRL_CMD_NEWFAMILY = 1
        CTRL_CMD_DELFAMILY = 2
        CTRL_CMD_GETFAMILY = 3
        CTRL_CMD_NEWOPS = 4
        CTRL_CMD_DELOPS = 5
        CTRL_CMD_GETOPS = 6
        CTRL_CMD_NEWMCAST_GRP = 7
        CTRL_CMD_DELMCAST_GRP = 8
)

const (
        CTRL_ATTR_UNSPEC = 0
        CTRL_ATTR_FAMILY_ID = 1
        CTRL_ATTR_FAMILY_NAME = 2
        CTRL_ATTR_VERSION = 3
        CTRL_ATTR_HDRSIZE = 4
        CTRL_ATTR_MAXATTR = 5
        CTRL_ATTR_OPS = 6
        CTRL_ATTR_MCAST_GROUPS = 7
)

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

func (s *NetlinkSocket) validateNlMsghdr(buf []byte, seq uint32) (*syscall.NlMsghdr, error) {
	h := (*syscall.NlMsghdr)(unsafe.Pointer(&buf[0]))
	if len(buf) < syscall.NLMSG_HDRLEN || len(buf) < int(h.Len) {
		return nil, fmt.Errorf("truncated netlink message (got %d bytes, expected %d)", len(buf), h.Len)
	}

	fmt.Printf("XXX %v\n", h)

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

type NlMsg struct {
	buf []byte
}

func (nlmsg *NlMsg) Header() *syscall.NlMsghdr {
	return (*syscall.NlMsghdr)(unsafe.Pointer(&nlmsg.buf[0]))
}

func (nlmsg *NlMsg) Align(align int) {
	nlmsg.buf = nlmsg.buf[:(len(nlmsg.buf) + align - 1) & -align]
}

func (nlmsg *NlMsg) Alloc(size uintptr) unsafe.Pointer {
	l := len(nlmsg.buf)
	nlmsg.buf = nlmsg.buf[:l + int(size)]
	return unsafe.Pointer(&nlmsg.buf[l])
}

func NewNlMsg(flags uint16, typ uint16) *NlMsg {
	buf := make([]byte, syscall.NLMSG_HDRLEN, syscall.Getpagesize())
	nlmsg := &NlMsg{buf: buf}
	h := nlmsg.Header()
	h.Flags = flags
	h.Type = typ
	return nlmsg
}

var nextSeqNo uint32

func (nlmsg *NlMsg) Finish() (res []byte, seq uint32) {
	h := nlmsg.Header()
	h.Len = uint32(len(nlmsg.buf))
	seq = atomic.AddUint32(&nextSeqNo, 1)
	h.Seq = seq
	res = nlmsg.buf
	nlmsg.buf = nil
	return
}

func (nlmsg *NlMsg) AddGenlMsghdr(cmd uint8) (res *GenlMsghdr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	res = (*GenlMsghdr)(nlmsg.Alloc(unsafe.Sizeof(*res)))
	res.cmd = cmd
	return
}

type RtAttr struct {
	pos int
	rta *syscall.RtAttr
}

func (nlmsg *NlMsg) BeginRtAttr(typ uint16) (res RtAttr) {
	nlmsg.Align(syscall.NLMSG_ALIGNTO)
	res.pos = len(nlmsg.buf)
	res.rta = (*syscall.RtAttr)(nlmsg.Alloc(unsafe.Sizeof(*res.rta)))
	res.rta.Type = typ
	return
}

func (nlmsg *NlMsg) FinishRtAttr(rta RtAttr) {
	rta.rta.Len = uint16(len(nlmsg.buf) - rta.pos)
}

func (nlmsg *NlMsg) AddAttr(typ uint16, str string) {
	rta := nlmsg.BeginRtAttr(typ)
	nlmsg.Align(syscall.RTA_ALIGNTO)
	l := len(nlmsg.buf)
	n := copy(nlmsg.buf[l:cap(nlmsg.buf)], str)
	fmt.Printf("XXX %d %d\n", l, n)
	nlmsg.buf = nlmsg.buf[:l + n + 1]
	nlmsg.buf[l + n] = 0
	nlmsg.FinishRtAttr(rta)
}


func (s *NetlinkSocket) resolveFamily() {
	nlmsg := NewNlMsg(syscall.NLM_F_REQUEST | syscall.NLM_F_ACK,
		GENL_ID_CTRL)

	nlmsg.AddGenlMsghdr(CTRL_CMD_GETFAMILY)
	nlmsg.AddAttr(CTRL_ATTR_FAMILY_NAME, "ovs_datapath")
	b, seq := nlmsg.Finish()
	fmt.Printf("DDD %v\n", b)

	if err := s.send(b); err != nil {
                panic(err)
        }

	rb, err := s.recv(0)
        if err != nil {
                panic(err)
        }

	_, err = s.validateNlMsghdr(rb, seq)
	if err != nil {
		panic(err)
	}
}
