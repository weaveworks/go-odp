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

	id, err := s.lookupGenlFamily("foobar")
	if err != nil {
		if err, ok := err.(NetlinkError); ok && err.Errno == syscall.ENOENT {
			fmt.Printf("No such family\n")
		} else {
			panic(err)
		}
	} else {
		fmt.Printf("Family id %d\n", id)
	}

	s.Close()
}
