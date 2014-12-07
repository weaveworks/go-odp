package main

func main() {
	dpif, err := NewDpif()
	if err != nil {
		panic(err)
	}

	//dpif.EnumerateDatapaths()
	err = dpif.CreateDatapath("foo")
	if err != nil {
		panic(err)
	}

	err = dpif.Close()
	if err != nil {
		panic(err)
	}
}
