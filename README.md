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
  values (the set of packet attributes available is defined by ODP).
  So, for example, a flow could, as its flow key, select packets with
  a certain destination MAC address and, as its action, send them out
  on a particular vport.

Note that when processing a packet, the order in which the flows are
evaluated is unspecified, and the first flow to match "wins" (its
actions are executed).

If no flows match a packet, then the packet is considered a *miss*.  A
userspace process can register to receive miss notifications, which
include the full packet data.

As mentioned, flows match a packet based on its attributes.  Most of
these attributes correspond to fields in the packet data, but there
are also attributes that represent data attached to the packet as it
passes through the datapath.  For example, the vport through which the
packet entered the datapath is available as an attribute, and there
are tunnel attributes which are important to VXLAN encapsulation,
discussed below.

## Installation

Set `GOPATH`, then:

    go get github.com/weaveworks/go-odp/tool/odp

The command line tool will end up at `$GOPATH/bin/odp`

The ODP module needs to be explicitly loaded:

    sudo modprobe openvswitch

## Command line tool

This section describes the use of the `odp` command line tool.  The
operations available through this tool map more or less directly onto
the operations exposed by the Open vSwitch datapath over netlink, and
are a good way to understand what the ODP interface looks like.

The `odp` program supports a number of subcommands (in a similar
fashion to `ip`).  Subcommands can be abbreviated as long as they
remain unambiguous.

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

Each line describes a vport.  The format corresponds to how vports are
specified to the `vport add` command, described below.

#### Netdev vports

A network device can be exposed within a datapath as a vport with:

    $GOPATH/bin/odp vport add netdev <datapath name> <netdev name>

This simply allows packets into and out of the datapath via the
network device (which can be one end of a veth pair).

Note that this hijacks all the traffic from the netdev, preventing its
normal use.  Also, a netdev can only be attached to a single datapath
at a time.

#### VXLAN vports

A VXLAN vport encapsulates and decapsulates VXLAN packets.  See the
VXLAN section below.

### Flows

List the flows within a datapath with:

    $GOPATH/bin/odp flow list <datapath name>

Each line describes a flow.  The format corresponds to how flows are
specified to the `flow add` command.

The general syntax for creating a flow is:

    $GOPATH/bin/odp flow add <datapath name> <flow key options> <flow action options>

They can only be one flow with a given key.  Adding another flow with
the same key as one that already exists simply assigns new actions to
the existing flow.

The currently supported flow key options are:

* `--in-port=<vport name>`: match packets that arrived on the given vport.

* `--eth-src=<MAC address>[:<MAC mask>]`: match packets with the given
  ethernet source MAC address.  An optional bitmask for the match can
  be specified, expressed in the usual MAC address sytax.

* `--eth-dst=<MAC address>[:<MAC mask>]`: match packets with the given
  ethernet destination MAC address, with an optional bitmask for the
  match.

* `--tunnel-id=<hex bytes>`, `--tunnel-ipv4-src=<ipv4 address>`, `--tunnel-ipv4-dst=<ipv4 address>`, `--tunnel-tos=<ipv4 ToS byte value>`, `--tunnel-ttl=<ipv4 TTL value>`, `--tunnel-df=<DF flag boolean>`, `--tunnel-csum=<boolean>`: tunnel attributes; see the VXLAN section below.

The currently supported actions are:

* `--output=<vport names>`: output the packet on the given vports
  (names are comma separated rather than given by multiple options due
  to limitations in golang's flag package).

* `--set-tunnel-id=<hex bytes>`, `--set-tunnel-ipv4-src=<ipv4 address>`, `--set-tunnel-ipv4-dst=<ipv4 address>`, `--set-tunnel-tos=<ipv4 ToS byte value>`, `--set-tunnel-ttl=<ipv4 TTL value>`, `--set-tunnel-df=<DF flag boolean>`, `--set-tunnel-csum=<boolean>`: set tunnel attributes; see the VXLAN section below.

### VXLAN

The way ODP specifies VXLAN packet encapsulation is somewhat
intricate, so it's easier to consider decapsulation first (i.e. how an
incoming VXLAN packet gets handled, resulting in the payload packet
being placed onto the datapath).

A VXLAN port is created with

    $GOPATH/bin/odp vport add vxlan <datapath name> <vport name> --port=<port number>

The `--port` option specifies the UDP port to bind to for receiving
VXLAN packets.  The socket is not bound to a specific address (i.e. it
does the equivalent of binding with `INADDR_ANY`), and the port cannot
be shared.  If the `--port` option is omitted, it defaults to 4789,
the IANA assign port number for VXLAN.

When a VXLAN packet arrives to the specified UDP socket, it is
decapsulated and the payload packet is injected through the vport into
the datapath.  Underlay-network packet information is attached to the
packet as the tunnel attributes and so made available for flow
matching, using the `--tunnel-*` flow key options.

Conversely, a packet can be encapsulated by outputting it from the
datapath through a VXLAN vport.  Several parameters are needed to
produce a VXLAN packet, e.g. the IP address to send the packet to.
These parameters are obtained from the tunnel attributes attached to
the packet.  The `OVS_ACTION_ATTR_SET` flow action is used to set the
tunnel attributes, exposed through the `--set-tunnel-*` options.  So a
flow that performs VXLAN encapsulation has two elements: Setting the
tunnel attributes, and then outputting the packet on a VXLAN vport.
For example:

    $GOPATH/bin/odp flow add dp --in-port=ethx --set-tunnel-ipv4-src=10.0.0.112 \
        --set-tunnel-ipv4-dst=10.0.0.113 --set-tunnel-ttl=64 --output=vx

The `--set-tunnel-id` option can be used to set the VXLAN network
identifier (VNI) field on the VXLAN packets.

The destination UDP port for the VXLAN packets is the port number
setting for the outgoing VXLAN vport (the same port number that is
used for binding).  The source UDP port for the VXLAN packets cannot
be configured; it is based on a hash of inner packet fields, as
recommended in [RFC7348](https://tools.ietf.org/html/rfc7348).

### Misses

The command line tool can display misses reported for a datapath, with:

    $GOPATH/bin/odp datapath listen <datapath name>

The reported packets are piped through tcpdump to display their
contents.

You can also ask for the flow keys associated with misses to be
reported with:

    $GOPATH/bin/odp datapath listen --keys <datapath name>

## <a name="help"></a>Getting Help

If you have any questions about, feedback for or problems with `go-odp`:

* Invite yourself to the [Weave Users Slack](https://slack.weave.works).
* Ask a question on the [#general](https://weave-community.slack.com/messages/general) slack channel.
* [File an issue](https://github.com/weaveworks/go-odp/issues/new).

Weaveworks follows the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md). Instances of abusive, harassing, or otherwise unacceptable behavior may be reported by contacting a Weaveworks project maintainer, or Alexis Richardson (alexis@weave.works).

Your feedback is always welcome!
