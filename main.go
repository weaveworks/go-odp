package main

import (
        "syscall"
)

func main() {
	s, err := getNetlinkSocket(syscall.NETLINK_GENERIC)
        if err != nil {
                panic(err)
        }

	s.resolveFamily()

	s.Close()
}
