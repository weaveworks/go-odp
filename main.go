package main

import (
	"fmt"
)

func main() {
	dpif, err := NewDpif()
	if err != nil {
		panic(err)
	}

	fmt.Printf("Dpif: %v\n", dpif)
	err = dpif.Close()
	if err != nil {
		panic(err)
	}
}
