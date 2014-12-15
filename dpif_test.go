package main

import (
	"testing"
	"math/rand"
	"fmt"
)

func TestDatapath(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }
	defer dpif.Close()

	name := fmt.Sprintf("test%d", rand.Intn(100000))

	dp, err := dpif.CreateDatapath(name)
	if err != nil { t.Fatal(err) }
	defer func () { if dpif != nil { dp.Delete() } }()

	err = dp.Delete()
	if err != nil { t.Fatal(err) }

	err = dpif.Close()
	if err != nil { t.Fatal(err) }
	dpif = nil
}

func TestLookupDatapath(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }
	defer dpif.Close()

	name := fmt.Sprintf("test%d", rand.Intn(100000))
	dp, err := dpif.LookupDatapath(name)
	if err != nil { t.Fatal(err) }
	if dp != nil { t.Fatal("LookupDatapath should return nil for non-existent name") }

	dp, err = dpif.CreateDatapath(name)
	if err != nil { t.Fatal(err) }
	defer func () { if dpif != nil { dp.Delete() } }()

	err = dpif.Close()
	if err != nil { t.Fatal(err) }

	dpif, err = NewDpif()
	if err != nil { t.Fatal(err) }

	dp, err = dpif.LookupDatapath(name)
	if err != nil { t.Fatal(err) }
	if dp == nil { t.Fatal("LookupDatapath failed to return datapath") }

	err = dp.Delete()
	if err != nil { t.Fatal(err) }

	err = dpif.Close()
	if err != nil { t.Fatal(err) }
	dpif = nil
}

func TestEnumerateDatapaths(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }
	defer dpif.Close()

	const n = 10
	var names [n]string
	var dps [n]*Datapath

	cleanup := func () {
		if dpif == nil { return }
		for i := range(dps) {
			if dps[i] != nil {
				dps[i].Delete()
			}
		}
	}

	defer cleanup()

	for i := range(names) {
		names[i] = fmt.Sprintf("test%d", rand.Intn(100000))
		dp, err := dpif.CreateDatapath(names[i])
		if err != nil { t.Fatal(err) }
		dps[i] = dp
	}

	name2dp, err := dpif.EnumerateDatapaths()
	if err != nil { t.Fatal(err) }
	for i := range(names) {
		_, ok := name2dp[names[i]]
		if !ok { t.Fatal() }
	}

	cleanup()

	name2dp, err = dpif.EnumerateDatapaths()
	if err != nil { t.Fatal(err) }
	for i := range(names) {
		_, ok := name2dp[names[i]]
		if ok { t.Fatal() }
	}

	err = dpif.Close()
	if err != nil { t.Fatal(err) }
	dpif = nil
}

func TestPort(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }
	defer dpif.Close()

	dpname := fmt.Sprintf("test%d", rand.Intn(100000))
	dp, err := dpif.CreateDatapath(dpname)
	if err != nil { t.Fatal(err) }
	defer func () { if dpif != nil { dp.Delete() } }()

	name := fmt.Sprintf("test%d", rand.Intn(100000))
	_, err = dp.CreatePort(name)
	if err != nil { t.Fatal(err) }

	err = dp.Delete()
	if err != nil { t.Fatal(err) }

	err = dpif.Close()
	if err != nil { t.Fatal(err) }
	dpif = nil
}
