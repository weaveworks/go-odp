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

func (nlmsg *NlMsgBuilder) Grow(size uintptr) int {
	pos := len(nlmsg.buf)
	l := pos + int(size)
	if l > cap(nlmsg.buf) { nlmsg.buf = expand(nlmsg.buf, l) }
	nlmsg.buf = nlmsg.buf[:l]
	return pos
}

func (nlmsg *NlMsgBuilder) AlignGrow(a int, size uintptr) int {
	apos := align(len(nlmsg.buf), a)
	l := apos + int(size)
	if l > cap(nlmsg.buf) { nlmsg.buf = expand(nlmsg.buf, l) }
	nlmsg.buf = nlmsg.buf[:l]
	return apos
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

func (nlmsg *NlMsgBuilder) PutAttr(typ uint16, gen func()) {
	pos := nlmsg.AlignGrow(syscall.NLA_ALIGNTO, syscall.SizeofNlAttr)
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

func (nlmsg *NlMsgBuilder) putStringZ(str string) {
	l := len(str)
	pos := nlmsg.Grow(uintptr(l) + 1)
	copy(nlmsg.buf[pos:], str)
	nlmsg.buf[pos + l] = 0
}

func (nlmsg *NlMsgBuilder) PutStringAttr(typ uint16, str string) {
	nlmsg.PutAttr(typ, func () { nlmsg.putStringZ(str) })
}

type NetlinkError struct {
	Errno syscall.Errno
}

func (err NetlinkError) Error() string {
	return fmt.Sprintf("netlink error response: %s", err.Errno.Error())
}

type NlMsgParser struct {
	data []byte
	pos int
}

func (msg *NlMsgParser) nextNlMsg() (*NlMsgParser, error) {
	pos := msg.pos
	avail := len(msg.data) - pos
	if avail <= 0 {
		return nil, nil
	}

	if avail < syscall.SizeofNlMsghdr {
		return nil, fmt.Errorf("netlink message header truncated")
	}

	h := nlMsghdrAt(msg.data, pos)
	if avail < int(h.Len) {
		return nil, fmt.Errorf("netlink message truncated (%d bytes available, %d expected)", avail, h.Len)
	}

	end := pos + int(h.Len)
	msg.pos = align(end, syscall.NLMSG_ALIGNTO)
	return &NlMsgParser{data: msg.data[:end], pos: pos}, nil
}

func (nlmsg *NlMsgParser) CheckAvailable(size uintptr) error {
	if nlmsg.pos + int(size) > len(nlmsg.data) {
		return fmt.Errorf("netlink message truncated")
	}

	return nil
}

func (nlmsg *NlMsgParser) Advance(size uintptr) error {
	if err := nlmsg.CheckAvailable(size); err != nil {
		return err
	}

	nlmsg.pos += int(size)
	return nil
}

func (nlmsg *NlMsgParser) AlignAdvance(a int, size uintptr) (int, error) {
	pos := align(nlmsg.pos, a)
	nlmsg.pos = pos
	if err := nlmsg.Advance(size); err != nil {
		return 0, err
	}

	return pos, nil
}

func (nlmsg *NlMsgParser) checkHeader(s *NetlinkSocket, expectedSeq uint32) (*syscall.NlMsghdr, error) {
	h := nlMsghdrAt(nlmsg.data, nlmsg.pos)
	if h.Pid != s.addr.Pid {
		return nil, fmt.Errorf("netlink reply pid mismatch (got %d, expected %d)", h.Pid, s.addr.Pid)
	}

	if h.Seq != expectedSeq {
		return nil, fmt.Errorf("netlink reply sequence number mismatch (got %d, expected %d)", h.Seq, expectedSeq)
	}

	if h.Type == syscall.NLMSG_ERROR {
		nlerr := nlMsgerrAt(nlmsg.data, nlmsg.pos + syscall.NLMSG_HDRLEN)

		if nlerr.Error != 0 {
			return nil, NetlinkError{syscall.Errno(-nlerr.Error)}
		}

		// an error code of 0 means the erorr is an ack, so
		// return normally.
	}

	return h, nil
}

func (nlmsg *NlMsgParser) ExpectNlMsghdr(typ uint16) (*syscall.NlMsghdr, error) {
	h := nlMsghdrAt(nlmsg.data, nlmsg.pos)

	if err := nlmsg.Advance(syscall.SizeofNlMsghdr); err != nil {
		return nil, err
	}

	if h.Type != typ {
		return nil, fmt.Errorf("netlink response has wrong type (got %d, expected %d)", h.Type, typ)
	}

	return h, nil
}

type Attrs map[uint16][]byte

func (attrs Attrs) Get(typ uint16) ([]byte, error) {
	val, ok := attrs[typ]
	if !ok {
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
		return 0, fmt.Errorf("uint16 attribute %d has wrong length (%d bytes)", typ, len(val))
	}

	return *(*uint16)(unsafe.Pointer(&val[0])), nil
}

func (attrs Attrs) GetString(typ uint16) (string, error) {
	val, err := attrs.Get(typ)
	if err != nil {
		return "", err
	}

	if len(val) == 0 {
		return "", fmt.Errorf("string attribute %d has zero length", typ);
	}

	if val[len(val) - 1] != 0 {
		return "", fmt.Errorf("string attribute %d does not end with nul byte", typ);
	}

	return string(val[0:len(val) - 1]), nil
}

func (nlmsg *NlMsgParser) checkData(l uintptr, obj string) error {
	if nlmsg.pos + int(l) <= len(nlmsg.data) {
		return nil
	} else {
		return fmt.Errorf("truncated %s (have %d bytes, expected %d)", obj, len(nlmsg.data) - nlmsg.pos, l)
	}
}

func (nlmsg *NlMsgParser) TakeAttrs() (attrs Attrs, err error) {
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

func (s *NetlinkSocket) send(msg *NlMsgBuilder) (uint32, error) {
	sa := syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Pid: 0,
		Groups: 0,
	}

	data, seq := msg.Finish()
	return seq, syscall.Sendto(s.fd, data, 0, &sa)
}

func (s *NetlinkSocket) recv(peer uint32) (*NlMsgParser, error) {
        buf := make([]byte, syscall.Getpagesize())
        nr, from, err := syscall.Recvfrom(s.fd, buf, 0)
        if err != nil {
                return nil, err
        }

	switch nlfrom := from.(type) {
        case *syscall.SockaddrNetlink:
		if nlfrom.Pid != peer {
			return nil, fmt.Errorf("wrong netlink peer pid (expected %d, got %d)", peer, nlfrom.Pid)
		}

		return &NlMsgParser{data: buf[:nr], pos: 0}, nil

	default:
		return nil, fmt.Errorf("Expected netlink sockaddr, got %s", reflect.TypeOf(from))
        }
}

// Some generic netlink operations always return a reply message (e.g
// *_GET), others don't by default (e.g. *_NEW).  In the latter case,
// NLM_F_ECHO forces a reply.  This is undocumented AFAICT.
const RequestFlags = syscall.NLM_F_REQUEST | syscall.NLM_F_ECHO

// Do a netlink request that yields a single response message.
func (s *NetlinkSocket) Request(req *NlMsgBuilder) (*NlMsgParser, error) {
	seq, err := s.send(req)
	if err != nil { return nil, err }

	resp, err := s.recv(0)
        if err != nil { return nil, err }

	msg, err := resp.nextNlMsg()
	if err != nil { return nil, err }
	if msg == nil {
		return nil, fmt.Errorf("netlink response message missing")
	}

	_, err = msg.checkHeader(s, seq)
	if err != nil {	return nil, err	}

	extra, err := resp.nextNlMsg()
	if err != nil { return nil, err }
	if extra != nil {
		return nil, fmt.Errorf("unexpected netlink message")
	}

	return msg, nil
}

const DumpFlags = syscall.NLM_F_DUMP | syscall.NLM_F_REQUEST

// Do a netlink request that yield multiple response messages.
func (s *NetlinkSocket) RequestMulti(req *NlMsgBuilder, consumer func (*NlMsgParser)) error {
	seq, err := s.send(req)
	if err != nil { return err }

	for {
		resp, err := s.recv(0)
		if err != nil { return err }

		msg, err := resp.nextNlMsg()
		if err != nil { return err }
		if msg == nil {
			return fmt.Errorf("netlink response message missing")
		}

		for {
			h, err := msg.checkHeader(s, seq)
			if err != nil {	return err }

			if h.Type == syscall.NLMSG_DONE {
				return nil
			}

			consumer(msg)

			msg, err = resp.nextNlMsg()
			if err != nil { return err }
			if msg == nil { break }
		}
	}
}
