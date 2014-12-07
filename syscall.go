package main

import "syscall"

type GenlMsghdr struct {
	Cmd uint8
	Version uint8
	Reserved uint16
}

const SizeofGenlMsghdr = 4

// reserved static generic netlink identifiers:
const (
	GENL_ID_GENERATE = 0
	GENL_ID_CTRL = syscall.NLMSG_MIN_TYPE
	GENL_ID_VFS_DQUOT = syscall.NLMSG_MIN_TYPE + 1
	GENL_ID_PMCRAID = syscall.NLMSG_MIN_TYPE + 2
)

const (
        CTRL_CMD_UNSPEC = 0
        CTRL_CMD_NEWFAMILY = 1
        CTRL_CMD_DELFAMILY = 2
        CTRL_CMD_GETFAMILY = 3
        CTRL_CMD_NEWOPS = 4
        CTRL_CMD_DELOPS = 5
        CTRL_CMD_GETOPS = 6
        CTRL_CMD_NEWMCAST_GRP = 7
        CTRL_CMD_DELMCAST_GRP = 8
)

const (
        CTRL_ATTR_UNSPEC = 0
        CTRL_ATTR_FAMILY_ID = 1
        CTRL_ATTR_FAMILY_NAME = 2
        CTRL_ATTR_VERSION = 3
        CTRL_ATTR_HDRSIZE = 4
        CTRL_ATTR_MAXATTR = 5
        CTRL_ATTR_OPS = 6
        CTRL_ATTR_MCAST_GROUPS = 7
)

type OvsHeader struct {
	DpIfIndex int32
}

const SizeofOvsHeader = 4

const (
	OVS_DATAPATH_VERSION = 2
)

const ( // ovs_datapath_cmd
        OVS_DP_CMD_UNSPEC = 0
        OVS_DP_CMD_NEW = 1
        OVS_DP_CMD_DEL = 2
        OVS_DP_CMD_GET = 3
        OVS_DP_CMD_SET = 4
)

const ( // ovs_datapath_attr
        OVS_DP_ATTR_UNSPEC = 0
        OVS_DP_ATTR_NAME = 1
        OVS_DP_ATTR_UPCALL_PID = 2
        OVS_DP_ATTR_STATS = 3
        OVS_DP_ATTR_MEGAFLOW_STATS = 4
        OVS_DP_ATTR_USER_FEATURES = 5
)

const (
	OVS_DP_F_UNALIGNED = 1
	OVS_DP_F_VPORT_PIDS = 2
)
