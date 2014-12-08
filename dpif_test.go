package main

import (
	"testing"
)

func check(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func TestDatapath(t *testing.T) {
	dpif, err := NewDpif()
	check(t, err)

	dp, err := dpif.CreateDatapath("foo")
	check(t, err)

	check(t, dp.Delete())

	check(t, dpif.Close())
}
