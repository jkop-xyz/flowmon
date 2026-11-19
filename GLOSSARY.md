# Flowmon

A self-hosted dashboard that collects IPFIX flow records from network devices on a home network and shows where traffic goes and who is on the other end.

## Flow collection

**Exporter**:
A network device (typically a MikroTik router) that sends flow records to Flowmon. It is identified by its source address and Observation Domain.
_Avoid_: Router, source, probe, device

**Pending Exporter**:
An Exporter that has started sending but that the user hasn't approved yet. Its Flows are held, not stored.

**Edge Exporter**:
An Exporter that sits on the boundary between the Local Networks and the internet, such as the main router.

**Internal Exporter**:
An Exporter inside the Local Networks, such as a hypervisor. In totals across Exporters it only contributes Flows between two hosts in the same subnet, by the LAN subnets set when it is approved. That is traffic no Edge Exporter could have seen, so nothing is counted twice. Its other Flows are kept but not counted.

**Interface Role**:
What the user says an Interface connects to: **WAN** (the internet), **LAN** (Local Networks), or **Tunnel** (a VPN whose peers count as Local Hosts). Direction is decided by which way a Flow crosses a WAN Interface.
_Avoid_: Uplink, outside interface, zone

**Classification Mode**:
How an Exporter's Flows are sorted into local and remote. **By interface** uses Interface Roles. **By address** uses Local Networks, for exporters that send no interface information.

**Flow**:
One flow record sent by an Exporter. It describes packets that shared a key (addresses, ports, protocol) between a start time and an end time.
_Avoid_: Connection, session, conversation

**Sampling Rate**:
The share of packets an Exporter counts: at 1 in N it counts one packet in every N, so a Flow's real bytes and packets are about N times what it reported. Flowmon takes the rate the Exporter announces (in its records or Options records), or one set by hand on the Exporters page when it announces none, and scales the counts up. The results are estimates, and every chart, list and Flow made of them says so.
_Avoid_: sample ratio, sampling interval (the IPFIX element names vary)

**Template**:
A description an Exporter sends of the field layout of its later Flows. Flows can't be read without it.

**Data Source** _(v2)_:
A configured integration that Flowmon collects from besides Exporters, such as one AdGuard Home instance or one SNMP device. Each has a kind, its own settings and a health status on the Sources page.
_Avoid_: Integration, plugin, input

## Network perspective

**Local Network**:
An address range that the user owns. It defaults to the private (RFC 1918), ULA and link-local ranges, and the user can add more. Flowmon falls back on Local Networks to decide Direction when an Exporter has no WAN Interfaces marked.
_Avoid_: LAN, internal subnet, home network

**Local Host**:
An address inside a Local Network.
_Avoid_: Client, device, internal IP

**Exporter Host**:
A Local Host that stands for an Exporter's own addresses, such as whatever address its WAN Interface has right now. Traffic the router sends or receives itself counts as local through it, and is attributed to the Exporter's own address, so its history doesn't split when a DHCP WAN address changes.
_Avoid_: Router IP, WAN host

**Remote IP**:
An address that is outside every Local Network and does not belong to an Exporter Host.
_Avoid_: External IP, public IP, peer

**Direction**:
The way a Flow crosses the boundary between the local side and the internet, decided by WAN Interfaces first and Local Networks second: **Inbound** (Remote IP to Local Host), **Outbound** (Local Host to Remote IP), or **Internal** (Local Host to Local Host).

**Peer**:
The other end of a Local Host's traffic: a Remote IP, or another Local Host for Internal traffic.
_Avoid_: Counterpart, destination

**Network Total**:
Traffic for the whole network with every byte counted once: received from Remote IPs, sent to Remote IPs, and Internal traffic counted only on the side that sent it.

**Device** _(v2)_:
A piece of hardware on the network, identified by its MAC address, with everything seen from it: the Local Hosts (addresses) it used over time, its traffic, DNS Queries and, through SNMP, where it is connected. A Local Host stays the address-level unit; a Device groups its addresses across DHCP changes and IPv4/IPv6.
_Avoid_: Client, host (for the hardware), endpoint, node

**Private Address** _(v2)_:
A randomized MAC address (the "locally administered" bit set), which phones and laptops use per network and may rotate. It has no vendor. A Device on a Private Address can be merged with the Device it was before.
_Avoid_: Random MAC, fake MAC

**Host Name**:
The name shown for a Local Host. A Host Label wins over the reverse-DNS name, and the address is used when there is neither.

**Host Label**:
A name the user gives by hand to a Local Host, or to a prefix so that a host's rotating addresses stay together.
_Avoid_: Alias, nickname

**Service Port**:
The port in a Flow that identifies the service: the well-known port at either end, otherwise the lower of the two.
_Avoid_: Destination port, app port

**Service Name**:
The name shown for a Service Port and protocol, such as `443/tcp https`. A name the user sets wins over the IANA name.

**Interface**:
A port on an Exporter that a Flow entered or left by. It is identified by its name, because interface numbers can change. Flows carry the number, and Flowmon maps it to the name.
_Avoid_: Port (that word is reserved for transport ports)

## Enrichment

**IP Enrichment**:
What Flowmon knows about a Remote IP beyond the address itself: country/city, ASN and organisation, reverse DNS name, and registration (RDAP) details. Country and ASN Owner are recorded with each Flow as they were when it arrived. Reverse DNS and registration details are looked up when someone views them.
_Avoid_: Lookup, geo info, whois

**ASN Owner**:
The organisation that announces the autonomous system the Remote IP belongs to, for example "AS15169 Google LLC".
_Avoid_: ISP, provider

**DNS Query** _(v2)_:
One entry from a DNS Data Source's log (AdGuard Home): which Local Host asked for which name, when, whether it was blocked, and the addresses the answer gave.
_Avoid_: Lookup (as a stored thing), request

**Registrable Domain** _(v2)_:
The part of a name that someone registered: the public suffix plus one label (eTLD+1), such as `googlevideo.com` for `r3---sn-5go7yner.googlevideo.com`. Lists and charts group by it; Flows keep the full name.
_Avoid_: Root domain, base domain, site

**Domain Attribution** _(v2)_:
The name a Flow is given from a DNS Query: the most recent lookup by the same Local Host, within 24 hours before the Flow, whose answer contained the Flow's Remote IP. Fixed when the Flow is stored, like its GeoIP data.

## Trust

**Reconciliation**:
A comparison of the bytes an Exporter reported in Flows with the bytes its own interface counters show over the same period. A large difference means traffic is missing or counted twice.

**Interface Counters** _(v2)_:
Per interface and minute, from SNMP: bytes, packets, errors, discards and up/down status, for every interface of every SNMP Data Source.

**Device Health** _(v2)_:
A device's own state from SNMP, sampled each minute: uptime, CPU, memory, and where the device reports them, temperature and voltage.
_Avoid_: Metrics, stats

## Storage

**Raw Flow Retention**:
How long individual Flows are kept so they can be drilled into.

**Rollup**:
Traffic totals per time bucket (minute or hour), grouped by a fixed set of dimensions. Totals are always seen from the Local Host: bytes and packets received, and bytes and packets sent. Charts and history are built from Rollups.
_Avoid_: Aggregate, summary, stats

**Top Remote IPs**:
A Rollup that keeps the busiest Remote IPs per Local Host, ranked by bytes and by Flow count. The rest are summed into an "other" row, so it still adds up to the total.

**Watermark**:
The point in time, per Exporter, after which no more Flows are expected for a time bucket. It is the Exporter's longest Flow duration plus a margin.

**Domain Rollup** _(v2)_:
Traffic totals per time bucket (minute or hour) by Local Host or Device and Registrable Domain, kept apart from the Rollups so the domain dimension doesn't multiply their size.
