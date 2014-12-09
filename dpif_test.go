package main

import (
	"testing"
	"math/rand"
	"fmt"
)

func TestDatapath(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }

	name := fmt.Sprintf("test%d", rand.Intn(100000))

	dp, err := dpif.CreateDatapath(name)
	if err != nil { t.Fatal(err) }

	err = dp.Delete()
	if err != nil { t.Fatal(err) }

	err = dpif.Close()
	if err != nil { t.Fatal(err) }
}

func TestLookupDatapath(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }

	name := fmt.Sprintf("test%d", rand.Intn(100000))
	dp, err := dpif.LookupDatapath(name)
	if err != nil { t.Fatal(err) }
	if dp != nil { t.Fatal("LookupDatapath should return nil for non-existent name") }

	dp, err = dpif.CreateDatapath(name)
	if err != nil { t.Fatal(err) }

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
}

func TestEnumerateDatapaths(t *testing.T) {
	dpif, err := NewDpif()
	if err != nil { t.Fatal(err) }

	name := fmt.Sprintf("test%d", rand.Intn(100000))

	dp, err := dpif.CreateDatapath(name)
	if err != nil { t.Fatal(err) }

	dps, err := dpif.EnumerateDatapaths()
	dp, ok := dps[name]
	if !ok { t.Fatal() }

	err = dp.Delete()
	if err != nil { t.Fatal(err) }

	err = dpif.Close()
	if err != nil { t.Fatal(err) }
}
