package main

import (
        "syscall"
	"unsafe"
	"errors"
	"fmt"
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

// Round the length of a netlink message up to align it properly.
func nlmsgAlign(len int) int {
        return (len + syscall.NLMSG_ALIGNTO - 1) & -syscall.NLMSG_ALIGNTO
}

// Round the length of a netlink route attribute up to align it
// properly.
func rtaAlign(len int) int {
        return (len + syscall.RTA_ALIGNTO - 1) & -syscall.RTA_ALIGNTO
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

func (s *NetlinkSocket) resolveFamily() {
	var b [4096]byte

	var seq uint32 = 1234

	pos := 0
	nlh := (*syscall.NlMsghdr)(unsafe.Pointer(&b[pos]))
	nlh.Flags = syscall.NLM_F_REQUEST | syscall.NLM_F_ACK
        nlh.Type = GENL_ID_CTRL

	pos = nlmsgAlign(pos + int(unsafe.Sizeof(*nlh)))
	fmt.Printf("AAA %d\n", pos)
	gh := (*GenlMsghdr)(unsafe.Pointer(&b[pos]))
	gh.cmd = CTRL_CMD_GETFAMILY

	pos = nlmsgAlign(pos + int(unsafe.Sizeof(*gh)))
	fmt.Printf("BBB %d\n", pos)
	rtapos := pos
	rta := (*syscall.RtAttr)(unsafe.Pointer(&b[pos]))
	rta.Type = CTRL_ATTR_FAMILY_NAME

	pos = rtaAlign(pos + int(unsafe.Sizeof(*rta)))
	fmt.Printf("CCC %d\n", pos)
	copy(b[pos:], "ovs_datapath")
	pos += 12
	b[pos] = 0
	pos += 1
	rta.Len = uint16(pos - rtapos)
	pos = nlmsgAlign(pos)

	nlh.Len = uint32(pos)
	nlh.Seq = seq
	fmt.Printf("DDD %v\n",b[0:pos])

	if err := s.send(b[0:pos]); err != nil {
                panic(err)
        }

	rb, err := s.recv(0)
        if err != nil {
                panic(err)
        }

	nlh, err = s.validateNlMsghdr(rb, seq)
	if err != nil {
		panic(err)
	}
}
