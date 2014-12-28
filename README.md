# go-odp: A Go library to control the Open vSwitch in-kernel datapath

## Background

Open vSwitch (OVS) consists of two parts:

* The datapath: An in-kernel component that processes packet flows
  according to a set of rules.

* A group of user-space daemons that manages the datapath, setting the
  rules and handling any "miss" packets reported by the datapath as
  not matching any rules.

The datapath is [part of the mainline Linux
kernel](https://github.com/torvalds/linux/tree/master/net/openvswitch),
and ships with the kernel packages in Linux distros.  The OVS daemons
are distributed in the [OVS
repository](https://github.com/openvswitch/ovs), and are installed
separately.  It is possible to use the former without the latter.

This library allows Go programs to control the datapath without using
the userspcace OVS code.  It is still very much a work in progress.
