# Internal Exporters: a hypervisor or a switch

Most homes need one Exporter: the router, an **Edge Exporter**. It sees everything between your Local Networks and the internet, and everything it routes between your VLANs. What it can't see is traffic that never reaches it: two VMs on the same hypervisor bridge, or two machines on the same switch. An **Internal Exporter** adds that.

An Internal Exporter sees much of what the router sees too: a VM's traffic to the internet passes the hypervisor first. Counting both would double it. So Flowmon counts only one thing from an Internal Exporter: **Flows between two hosts in the same subnet**. Everything else it reports is kept as Flows marked "not counted", so you can still look at them in the Flow Explorer, but left out of every chart, list and total.

## Setting one up

1. Point the Exporter at Flowmon. It appears on the Exporters page, waiting for approval.
2. Approve it with the role **Internal**, and list its **LAN subnets**: the network segments on its LAN side, such as the VM bridge's `10.0.20.0/24` and its IPv6 `/64`.
   - List each segment, not a whole Local Network. With `10.0.0.0/8`, every Flow the router routes between your VLANs would look like same-subnet traffic and be counted twice. Flowmon refuses subnets broader than /16 for IPv4 or /64 for IPv6.
   - A Flow counts when both of its addresses are in the **same** listed subnet. A VM on `10.0.20.0/24` talking to a host on `10.0.30.0/24` went through the router, so the router counts it.
3. If the Exporter reports the same packets more than once, turn on **Observes multiple points** (below).

You can change the role, the subnets and this switch later, in the Exporter's Counting section. Changes apply from then on and are marked on the charts. To count stored Flows again, reprocess the Exporter.

## Observes multiple points

Some Exporters see the same packets at more than one place: a router that exports on both its LAN and WAN interfaces, or one softflowd per VM tap (below), where a Flow between two VMs passes both VMs' taps. With **Observes multiple points**, Flowmon removes the repeats within that Exporter before counting: a Flow is a duplicate when it has the same addresses, ports and protocol as one already reported and their times overlap. Addresses are compared before and after NAT, so the same packets seen on both sides of the router's NAT match too. The Exporters page shows how many duplicates were removed.

Duplicates are only removed within one Exporter, never between two: between an Edge and an Internal Exporter, the counting rule above already decides.

## Proxmox and softflowd

softflowd is the usual way to export Flows from a Linux hypervisor. Where it captures matters:

- **On the bridge (`vmbr0`) it sees little of the traffic between VMs.** A capture on a Linux bridge's own interface sees what the bridge delivers to the host itself, plus broadcasts and frames it floods. Unicast frames the bridge switches from one VM's port to another's don't pass through it, and those are exactly the Flows an Internal Exporter is for. Most of what it does see is traffic the router counts anyway.
- **Capture on each VM's tap interface instead.** Proxmox names them `tap<VMID>i<N>` (`tap100i0` is VM 100's first network device), and every frame a VM sends or receives passes its tap. Run one softflowd per tap, all sending to Flowmon:

  ```sh
  softflowd -i tap100i0 -n 192.168.88.10:2055 -v 9
  softflowd -i tap101i0 -n 192.168.88.10:2055 -v 9
  ```

  They all send from the hypervisor's address, so Flowmon sees one Exporter. A Flow between VM 100 and VM 101 is captured at both taps, so turn on **Observes multiple points**. Containers (LXC) have `veth<VMID>i<N>` interfaces that work the same way.

softflowd's `-v 9` (NetFlow v9) and `-v 10` (IPFIX) both work. NetFlow v5 does too, without IPv6.
