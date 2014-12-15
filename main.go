package main

import (
	"os"
	"fmt"
)

func main() {
	dpif, err := NewDpif()
	if err != nil {
		panic(err)
	}

	dp, err := dpif.LookupDatapath(os.Args[1])
	//err = dpif.EnumerateDatapaths()
	//_, err = dpif.CreateDatapath("foo")
	if err != nil {
		panic(err)
	}

	if dp == nil {
		fmt.Fprintf(os.Stderr, "no datapath named \"%s\"\n", os.Args[1])
		return
	}

	err = dp.Delete()
	if err != nil {
		panic(err)
	}

	err = dpif.Close()
	if err != nil {
		panic(err)
	}
}
