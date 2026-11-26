# Setting up a MikroTik for Flowmon

This guide says what to set on a MikroTik router running RouterOS 7 and where to find it. It deliberately has no copy-paste scripts: the settings are few, and doing them in WinBox or WebFig shows you what each one does. Every section links to MikroTik's own documentation, which is the reference.

The same guide is in Flowmon itself, linked from the Exporters page.

## 1. Traffic Flow settings

**Where:** WinBox or WebFig → **IP → Traffic Flow** (command line: `/ip traffic-flow`).
**Docs:** [Traffic Flow](https://help.mikrotik.com/docs/spaces/ROS/pages/21102653/Traffic+Flow)

| Setting | Value | Why |
|---|---|---|
| Enabled | yes | |
| Interfaces | `all` (the default) | Flowmon needs to see traffic on both your WAN and your LAN interfaces |
| Active Flow Timeout | **15 s** on links of 500 Mbit/s or more, **1 min** below that | The default of 30 min turns a long download into one record half an hour later. Some RouterOS versions send a 4-byte byte counter, which wraps after 4 GiB per export (about 34 s at 1 Gbit/s); Flowmon shows a warning if yours does |
| Inactive Flow Timeout | 15 s (the default) | |
| Packet Sampling | off (the default) | Flowmon counts every packet |

## 2. Target: where to send Flows

**Where:** **IP → Traffic Flow → Targets**, add one (`/ip traffic-flow target`).

| Setting | Value | Why |
|---|---|---|
| Dst. Address | the address of the host running Flowmon | |
| Port | **4739** (the IPFIX port) or 2055 | Flowmon listens on both by default |
| Version | **IPFIX** | NetFlow v9 works too, but the field choices in step 3 are IPFIX settings |
| Src. Address | the router's LAN address | Flowmon identifies an Exporter by its source address; pinning it keeps it the same when routes change |

## 3. IPFIX fields

**Where:** **IP → Traffic Flow → IPFIX** (`/ip traffic-flow ipfix`).

Make sure these fields are enabled:

- `bytes`, `packets`
- `src-address`, `dst-address`, `src-port`, `dst-port`, `protocol`
- `in-interface`, `out-interface`: needed to classify by interface
- `first-forwarded`, `last-forwarded`, `sys-init-time`: flow times. Without `sys-init-time`, Flowmon has to estimate times from the export time
- `nat-src-address`, `nat-dst-address`, `nat-src-port`, `nat-dst-port`: without them, download traffic can't be attributed to the right Local Host behind NAT
- `nat-events`

## 4. In Flowmon

1. Open **Exporters**. The router appears as **waiting for approval** as soon as its first packet arrives.
2. Give it a name and approve it. Choose **By interface** if it is offered (it is when the router sends interface numbers).
3. Under **Interfaces**, name each interface number (or let SNMP do it, below) and set roles: your internet uplink is **WAN**, bridges and VLANs are **LAN**, WireGuard and other VPNs are **Tunnel**. Flowmon marks the interface that looks like your WAN.
4. Save the classification. Flowmon offers to reprocess the Flows it already has.

## 5. SNMP (optional, recommended)

SNMP fills in interface names, and lets Flowmon check Flow totals against the router's own interface counters (Reconciliation).

**Where:** **IP → SNMP** (`/snmp`); communities under **IP → SNMP → Communities** (`/snmp community`).
**Docs:** [SNMP](https://help.mikrotik.com/docs/spaces/ROS/pages/8978519/SNMP)

1. In **IP → SNMP**, set **Enabled**.
2. In **Communities**, add one for Flowmon:
   - **Name**: e.g. `flowmon`. This is the SNMPv3 user name.
   - **Addresses**: only the Flowmon host, e.g. `192.168.88.10/32`.
   - **Security**: `private` (authentication and encryption).
   - **Authentication Protocol**: `SHA1`. **Encryption Protocol**: `AES`.
   - Passwords of at least 8 characters.
   - **Read Access** only.
3. The default `public` community allows anyone to read without a password. Restrict its Addresses or disable it.
4. In Flowmon, under **Settings → SNMP devices**, add an **SNMP device**:
   - Address: the router's address, the same as the Exporter's.
   - Version SNMPv3, with the community name as the user.
   - Authentication: SHA. Privacy: AES.
   - The layer-2 overhead: Ethernet for a plain Ethernet WAN, PPPoE if you dial in with PPPoE.
   - Test, then add.

## 6. Traffic the router can't show

Traffic Flow only sees traffic that reaches the router's CPU. MikroTik's documentation says: *"HW offloaded traffic will not be seen in Traffic Flow flows."* That includes:

- traffic switched between ports of a bridge with hardware offload,
- FastTrack connections with hardware offload, and L3 hardware offloading on CRS3xx/CRS5xx and CCR2116/CCR2216-class devices,
- traffic between VMs on the same hypervisor bridge, which never reaches the router at all.

If you have SNMP set up, Reconciliation shows this as a lasting gap between the WAN counters and the Flows.
