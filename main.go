package main

func main() {
	dpif, err := NewDpif()
	if err != nil {
		panic(err)
	}

	dp, err := dpif.LookupDatapath("foo")
	//err = dpif.EnumerateDatapaths()
	//_, err = dpif.CreateDatapath("foo")
	if err != nil {
		panic(err)
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
