// mtl_helpers.h — C helper functions for cgo access to MTL structs.
// These work around cgo's inability to access anonymous union members.

#ifndef ST2110_MTL_HELPERS_H
#define ST2110_MTL_HELPERS_H

#include <mtl/st40_api.h>
#include <mtl/st_pipeline_api.h>
#include <string.h>

// st40_rx_ops has ip_addr inside an anonymous union.
static inline void st2110_st40_rx_set_ip(struct st40_rx_ops* ops, int port_idx,
    uint8_t a, uint8_t b, uint8_t c, uint8_t d) {
    ops->ip_addr[port_idx][0] = a;
    ops->ip_addr[port_idx][1] = b;
    ops->ip_addr[port_idx][2] = c;
    ops->ip_addr[port_idx][3] = d;
}

static inline void st2110_st40_rx_set_mcast_sip(struct st40_rx_ops* ops, int port_idx,
    uint8_t a, uint8_t b, uint8_t c, uint8_t d) {
    ops->mcast_sip_addr[port_idx][0] = a;
    ops->mcast_sip_addr[port_idx][1] = b;
    ops->mcast_sip_addr[port_idx][2] = c;
    ops->mcast_sip_addr[port_idx][3] = d;
}

// st40_tx_ops has dip_addr inside an anonymous union.
static inline void st2110_st40_tx_set_dip(struct st40_tx_ops* ops, int port_idx,
    uint8_t a, uint8_t b, uint8_t c, uint8_t d) {
    ops->dip_addr[port_idx][0] = a;
    ops->dip_addr[port_idx][1] = b;
    ops->dip_addr[port_idx][2] = c;
    ops->dip_addr[port_idx][3] = d;
}

// st_rx_port.mcast_sip_addr helper for consistency.
static inline void st2110_rx_port_set_mcast_sip(struct st_rx_port* p, int port_idx,
    uint8_t a, uint8_t b, uint8_t c, uint8_t d) {
    p->mcast_sip_addr[port_idx][0] = a;
    p->mcast_sip_addr[port_idx][1] = b;
    p->mcast_sip_addr[port_idx][2] = c;
    p->mcast_sip_addr[port_idx][3] = d;
}

#endif // ST2110_MTL_HELPERS_H
