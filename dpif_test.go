package main

import (
	"testing"
	"math/rand"
	"fmt"
	"time"
)

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

func maybeFatal(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func checkedCloseDpif(dpif *Dpif, t *testing.T) {
	maybeFatal(t, dpif.Close())
}

func TestCreateDatapath(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	name := fmt.Sprintf("test%d", rand.Intn(100000))

	dp, err := dpif.CreateDatapath(name)
	maybeFatal(t, err)

	maybeFatal(t, dp.Delete())
}

func TestLookupDatapath(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	name := fmt.Sprintf("test%d", rand.Intn(100000))
	dp, err := dpif.LookupDatapath(name)
	maybeFatal(t, err)
	if dp != nil { t.Fatal("LookupDatapath should return nil for non-existent name") }

	_, err = dpif.CreateDatapath(name)
	maybeFatal(t, err)

	checkedCloseDpif(dpif, t)
	dpif, err = NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	dp, err = dpif.LookupDatapath(name)
	maybeFatal(t, err)
	if dp == nil { t.Fatal("LookupDatapath returned nil") }

	maybeFatal(t, dp.Delete())
}

func TestEnumerateDatapaths(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	const n = 10
	var names [n]string
	var dps [n]*Datapath

	cleanup := func () {
		for i, dp := range(dps) {
			if dp != nil {
				dp.Delete()
			}

			dps[i] = nil
		}
	}

	defer cleanup()

	for i := range(names) {
		names[i] = fmt.Sprintf("test%d", rand.Intn(100000))
		dp, err := dpif.CreateDatapath(names[i])
		maybeFatal(t, err)
		dps[i] = dp
	}

	name2dp, err := dpif.EnumerateDatapaths()
	maybeFatal(t, err)
	for _, name := range(names) {
		_, ok := name2dp[name]
		if !ok { t.Fatal() }
	}

	cleanup()

	name2dp, err = dpif.EnumerateDatapaths()
	maybeFatal(t, err)
	for _, name := range(names) {
		_, ok := name2dp[name]
		if ok { t.Fatal() }
	}
}

func checkedDeleteDatapath(dp *Datapath, t *testing.T) {
	maybeFatal(t, dp.Delete())
}

func TestCreatePort(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	dp, err := dpif.CreateDatapath(fmt.Sprintf("test%d", rand.Intn(100000)))
	maybeFatal(t, err)
	defer checkedDeleteDatapath(dp, t)

	name := fmt.Sprintf("test%d", rand.Intn(100000))
	port, err := dp.CreatePort(name)
	maybeFatal(t, err)

	err = port.Delete()
	maybeFatal(t, err)
}

func TestLookupPort(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	dpname := fmt.Sprintf("test%d", rand.Intn(100000))
	dp, err := dpif.CreateDatapath(dpname)
	maybeFatal(t, err)

	name := fmt.Sprintf("test%d", rand.Intn(100000))
	port, err := dp.LookupPort(name)
	maybeFatal(t, err)
	if port != nil { t.Fatal("LookupPort should return nil for non-existent name") }

	_, err = dp.CreatePort(name)
	maybeFatal(t, err)

	checkedCloseDpif(dpif, t)
	dpif, err = NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	dp, err = dpif.LookupDatapath(dpname)
	maybeFatal(t, err)
	if dp == nil { t.Fatal("LookupDatapath returned nil") }
	defer dp.Delete()

	port, err = dp.LookupPort(name)
	maybeFatal(t, err)
	if port == nil { t.Fatal("LookupPort return nil") }

	maybeFatal(t, port.Delete())
}

func TestEnumeratePorts(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	dp, err := dpif.CreateDatapath(fmt.Sprintf("test%d", rand.Intn(100000)))
	maybeFatal(t, err)
	defer checkedDeleteDatapath(dp, t)

	const n = 10
	var names [n]string
	var ports [n]*Port

	cleanup := func () {
		for i, port := range(ports) {
			if port != nil {
				port.Delete()
			}

			ports[i] = nil
		}
	}

	defer cleanup()

	for i := range(names) {
		names[i] = fmt.Sprintf("test%d", rand.Intn(100000))
		port, err := dp.CreatePort(names[i])
		maybeFatal(t, err)
		ports[i] = port
	}

	name2port, err := dp.EnumeratePorts()
	maybeFatal(t, err)
	for _, name := range(names) {
		_, ok := name2port[name]
		if !ok { t.Fatal() }
	}

	cleanup()

	name2port, err = dp.EnumeratePorts()
	maybeFatal(t, err)
	for _, name := range(names) {
		_, ok := name2port[name]
		if ok { t.Fatal() }
	}
}

func TestCreateFlow(t *testing.T) {
	dpif, err := NewDpif()
	maybeFatal(t, err)
	defer checkedCloseDpif(dpif, t)

	dp, err := dpif.CreateDatapath(fmt.Sprintf("test%d", rand.Intn(100000)))
	maybeFatal(t, err)
	defer checkedDeleteDatapath(dp, t)

	f := NewFlowSpec()
	f.AddKey(NewEthernetFlowKey([...]byte { 1,2,3,4,5,6 }, [...]byte { 6,5,4,3,2,1 }))

	_, err = dp.CreateFlow(f)
	maybeFatal(t, err)

	maybeFatal(t, dp.DeleteFlow(f))

	err = dp.DeleteFlow(f)
	if err != (NoSuchFlowError{}) {
		t.Fatal()
	}
}
