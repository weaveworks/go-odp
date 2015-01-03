package odp

import (
	"syscall"
	"unsafe"
)

func uint16At(data []byte, pos int) *uint16 {
	return (*uint16)(unsafe.Pointer(&data[pos]))
}

func uint32At(data []byte, pos int) *uint32 {
	return (*uint32)(unsafe.Pointer(&data[pos]))
}

func int32At(data []byte, pos int) *int32 {
	return (*int32)(unsafe.Pointer(&data[pos]))
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

func genlMsghdrAt(data []byte, pos int) *GenlMsghdr {
	return (*GenlMsghdr)(unsafe.Pointer(&data[pos]))
}

func ovsHeaderAt(data []byte, pos int) *OvsHeader {
	return (*OvsHeader)(unsafe.Pointer(&data[pos]))
}

func ovsKeyEthernetAt(data []byte, pos int) *OvsKeyEthernet {
	return (*OvsKeyEthernet)(unsafe.Pointer(&data[pos]))
}
