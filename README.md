# go-odp: A Go library to control the Open vSwitch in-kernel datapath

## Background

Open vSwitch (OVS) consists of two parts:

* The "open vSwitch datapath" (ODP): An in-kernel component that
  processes packet flows according to a set of rules.  This has been
  [part of the mainline Linux
  kernel](https://github.com/torvalds/linux/tree/master/net/openvswitch)
  for a while, and the main Linux distros already include the relevant
  module in their kernel packages.  ODP is controlled from userspace
  via [netlink](http://en.wikipedia.org/wiki/Netlink).

* A group of userspace daemons that manage the datapath, setting the
  rules and handling any misses reported by the datapath when a packet
  does not match any rules. The source for this part is in the [main
  Open vSwitch repository](https://github.com/openvswitch/ovs), and
  this is what gets packaged as openvswitch by Linux distros.

This library allows Go programs to control the datapath directly.
This avoids the need to install and manage the OVS userspace daemons,
which can be preferable if the functionality provided by the OVS
userspace is not required.

go-odp also includes a command-line tool that can be used to
experiment with ODP without writing code.

## Introduction to ODP

ODP allows the creation of multiple *datapaths*.  A datapath is a
named scope for processing of network packets.  Associated with each
datapath is:

* A set of *vports*.  Packets enter and exit a datapath through
  vports.  The most obvious example of a vport is a network device (of
  any of the types supported by the kernel), but there are other
  kinds.

* A set of *flows*.  Flows are rules saying which packets belong to
  the flow (the *key*) and what should be done with those packets (the
  actions).  A flow key is specified as a set of packet attribute
  values.  Most kinds of packet attributes are header fields within
  the packet data (e.g. the source MAC address), but not all (e.g. the
  vport through which the packet entered the datapath is an
  attribute).  So, for example, a flow could select packets with a
  certain destination MAC address and, as its action, send them out on
  a particular vport.

Note that when processing a packet, the order in which the flows are
evaluated is unspecified, and the first flow to match "wins" (its
actions are executed).

If no flows match a packet, then the packet is considered a *miss*.  A
userspace process can register to receive miss notifications, which
include the full packet data.

## Installation

Set `GOPATH`, then:

    go get github.com/dpw/go-odp/tool/odp

The command line tool will end up at `$GOPATH/bin/odp`

The ODP module needs to be explicitly loaded:

    sudo modprobe openvswitch

## Command line tool

### Datapaths

List all datapaths with:

    $GOPATH/bin/odp datapath

Create a new datapath with:

    $GOPATH/bin/odp datapath add <datapath name>

A datapath is also exposed as a network device with the same name (and
for this reason a datapath name cannot clash with a network device
name).  The main practical use of this seems to be to tcpdump the
packets on the datapath.

You can delete a datapath with:

    $GOPATH/bin/odp datapath delete <datapath name>

### Vports

List all vport definitions with:

    $GOPATH/bin/odp vport

Or just the vports within a datapath with:

    $GOPATH/bin/odp vport list <datapath name>

The format of these listings corresponds to how vports are specified
to the `vport add` command.

#### Netdev vports

A network device can be exposed within a datapath as a vport with:

    $GOPATH/bin/odp vport add netdev <netdev name>

This simply allows packets into and out of the datapath via the
network device (which can be one end of a veth pair).

Note that this hijacks all the traffic from the netdev, preventing its
normal use.  Also, a netdev can only be attached to a single datapath
at a time.

#### Vxlan vports

A vxlan vport encapsulates and decapsulates VXLAN packets.  See the
vxlan section below.

### Flows

TODO

## Vxlan



