package main

import (
        "syscall"
	"unsafe"
	"fmt"
	"sync/atomic"
	"reflect"
)

func align(n int, a int) int {
	return (n + a - 1) & -a;
}

type NetlinkSocket struct {
	fd int
	addr *syscall.SockaddrNetlink
}

func OpenNetlinkSocket(protocol int) (*NetlinkSocket, error) {
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
		return nil, fmt.Errorf("Expected netlink sockaddr, got %s", reflect.TypeOf(localaddr))
        }
}

func (s *NetlinkSocket) Close() error {
        return syscall.Close(s.fd)
}

func nlMsghdrAt(data []byte, pos int) *syscall.NlMsghdr {
	return (*syscall.NlMsghdr)(unsafe.Pointer(&data[pos]))
}

func nlAttrAt(data []byte, pos int) *syscall.NlAttr {
	return (*syscall.NlAttr)(unsafe.Pointer(&data[pos]))
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

type PutAttr func (*NlMsgBuilder)

func (nlmsg *NlMsgBuilder) PutAttrs(attrs []PutAttr) {
	if attrs != nil {
		for i := range(attrs) {
			attrs[i](nlmsg)
		}
	}
}

func (nlmsg *NlMsgBuilder) PutAttr(typ uint16, gen func()) {
	nlmsg.Align(syscall.NLA_ALIGNTO)
	pos := nlmsg.Grow(syscall.SizeofNlAttr)
	gen()
	nla := nlAttrAt(nlmsg.buf, pos)
	nla.Type = typ
	nla.Len = uint16(len(nlmsg.buf) - pos)
}

func (nlmsg *NlMsgBuilder) PutUint32Attr(typ uint16, val uint32) {
	nlmsg.PutAttr(typ, func () {
		pos := nlmsg.Grow(4)
		*(*uint32)(unsafe.Pointer(&nlmsg.buf[pos])) = val
	})
}

func PutUint32Attr(typ uint16, val uint32) PutAttr {
	return func (nlmsg *NlMsgBuilder) { nlmsg.PutUint32Attr(typ, val) }
}

func (nlmsg *NlMsgBuilder) putStringZ(str string) {
	l := len(str)
	pos := nlmsg.Grow(uintptr(l) + 1)
	copy(nlmsg.buf[pos:], str)
	nlmsg.buf[pos + l] = 0
}

func (nlmsg *NlMsgBuilder) PutStringAttr(typ uint16, str string) {
	nlmsg.PutAttr(typ, func () { nlmsg.putStringZ(str) })
}

func PutStringAttr(typ uint16, str string) PutAttr {
	return func (nlmsg *NlMsgBuilder) { nlmsg.PutStringAttr(typ, str) }
}

type NetlinkError struct {
	Errno syscall.Errno
}

func (err NetlinkError) Error() string {
	return fmt.Sprintf("netlink error response: %s", err.Errno.Error())
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

		return NetlinkError{syscall.Errno(-nlerr.Error)}
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

func (nlmsg *NlMsgButcher) ExpectNlMsghdr(typ uint16) (*syscall.NlMsghdr, error) {
	h := nlMsghdrAt(nlmsg.data, 0)
	nlmsg.pos += syscall.NLMSG_HDRLEN

	if h.Type != typ {
		return nil, fmt.Errorf("netlink response has wrong type (got %d, expected %d)", h.Type, typ)
	}

	return h, nil
}

type Attrs map[uint16][]byte

func (attrs Attrs) Get(typ uint16) ([]byte, error) {
	val := attrs[typ]
	if val == nil {
		return nil, fmt.Errorf("missing attribute %d", typ)
	}

	return val, nil
}

func (attrs Attrs) GetUint16(typ uint16) (uint16, error) {
	val, err := attrs.Get(typ)
	if err != nil {
		return 0, err
	}

	if len(val) != 2 {
		return 0, err
	}

	return *(*uint16)(unsafe.Pointer(&val[0])), nil
}

func (nlmsg *NlMsgButcher) checkData(l uintptr, obj string) error {
	if nlmsg.pos + int(l) <= len(nlmsg.data) {
		return nil
	} else {
		return fmt.Errorf("truncated %s (have %d bytes, expected %d)", obj, len(nlmsg.data) - nlmsg.pos, l)
	}
}

func (nlmsg *NlMsgButcher) TakeAttrs() (attrs Attrs, err error) {
	attrs = make(Attrs)
	for {
		apos := align(nlmsg.pos, syscall.NLA_ALIGNTO)
		if len(nlmsg.data) <= apos {
			return
		}

		nlmsg.pos = apos

		if err = nlmsg.checkData(syscall.SizeofNlAttr, "netlink attribute"); err != nil {
			return
		}

		nla := nlAttrAt(nlmsg.data, nlmsg.pos)
		if err = nlmsg.checkData(uintptr(nla.Len), "netlink attribute"); err != nil {
			return
		}

		valpos := align(nlmsg.pos + syscall.SizeofNlAttr, syscall.NLA_ALIGNTO)
		attrs[nla.Type] = nlmsg.data[valpos:nlmsg.pos + int(nla.Len)]
		nlmsg.pos += int(nla.Len)
	}
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
		if nlfrom.Pid != peer {
			return nil, fmt.Errorf("wrong netlink peer pid (expected %d, got %d)", peer, nlfrom.Pid)
		}

		return rb[:nr], nil

	default:
		return nil, fmt.Errorf("Expected netlink sockaddr, got %s", reflect.TypeOf(from))
        }
}

func (s *NetlinkSocket) Request(reqb *NlMsgBuilder) (*NlMsgButcher, error) {
	req, seq := reqb.Finish()
	if err := s.send(req); err != nil {
		return nil, err
        }

	resp, err := s.recv(0)
        if err != nil {
		return nil, err
        }

	if err = s.checkResponse(resp, seq); err != nil {
		return nil, err
	}

	return NewNlMsgButcher(resp), nil
}
