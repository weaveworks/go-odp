package main

import (
	"fmt"
        "syscall"
)

func main() {
	s, err := getNetlinkSocket(syscall.NETLINK_GENERIC)
        if err != nil {
                panic(err)
        }

	id, err := s.lookupGenlFamily("ovs_datapath")
	if err != nil {
		panic(err)
	}

	fmt.Printf("Family id %d\n", id)

	s.Close()
}
