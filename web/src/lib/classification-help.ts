import type { ClassificationMode, InterfaceRole } from "@/lib/api"

// The one wording of Classification Modes and Interface Roles, shown in the Approve
// dialog, the Interfaces section and the classification guide. Terms follow GLOSSARY.md.

export const classificationGuidePath = "/guide/classification"

export const classificationIntro = [
  "Every Flow is a conversation between two addresses. Classification decides which side is yours.",
  "That makes the Flow Inbound (the internet talking to one of your Local Hosts), Outbound (one of your Local Hosts talking to the internet) or Internal (two of your Local Hosts talking to each other). Every chart, total and top list is built on it.",
]

export const modeHelp: Record<
  ClassificationMode,
  { label: string; asks: string; goodAt: string; goesWrong: string; needs: string }
> = {
  interface: {
    label: "By interface",
    asks: "Which port of the Exporter did the traffic come in on, and which did it leave by?",
    goodAt:
      "Traffic in through a WAN Interface is Inbound, out through one is Outbound, and between other interfaces it's Internal. Addresses don't matter, so public IPv6 addresses or a server with a public IP on your network are sorted right.",
    goesWrong: "Only when the roles are wrong, or a WAN Interface isn't marked.",
    needs:
      "At least one interface marked WAN in the Interfaces section. A Flow through an interface with no role falls back to By address.",
  },
  address: {
    label: "By address",
    asks: "Is this address in my Local Networks?",
    goodAt: "Needs no setup: the private address ranges are Local Networks from the start.",
    goesWrong:
      "When your own devices use addresses outside the Local Networks, such as public IPv6 addresses or a server with a public IP. Their traffic then looks like the internet's. Add those ranges in Settings.",
    needs: "Nothing, beyond Local Networks that cover your addresses.",
  },
}

export const modeAdvice =
  "Pick By interface when this Exporter is your router and sends interface numbers. Otherwise pick By address."

/** Interface Roles in menu order; "" is no role. */
export const roleHelp: { role: InterfaceRole; label: string; meaning: string }[] = [
  {
    role: "wan",
    label: "WAN",
    meaning:
      "Where the internet comes in: the uplink port, a PPPoE interface, or an IPv6 tunnel broker (6in4) that carries internet traffic.",
  },
  { role: "lan", label: "LAN", meaning: "Your own networks: bridges, VLANs and LAN ports." },
  {
    role: "tunnel",
    label: "Tunnel",
    meaning:
      "A VPN whose far end counts as your own devices, such as WireGuard for your phone or a link to another site. Not an IPv6 tunnel broker: that one is WAN.",
  },
  {
    role: "",
    label: "No role",
    meaning: "Fine for unused ports and loopback. Flows through it fall back to By address.",
  },
]

export const roleLabel = (role: InterfaceRole) => roleHelp.find((r) => r.role === role)?.label ?? role

export const snmpAndRoles =
  "SNMP fills in interface names, not roles. The Reconciliation compares SNMP counters with Flows only for interfaces marked WAN, whichever mode you pick."

export const changingMode =
  "A new mode or new roles apply to Flows from now on, and charts mark the change with a dashed line. Reprocess re-sorts the raw Flows Flowmon still keeps."
