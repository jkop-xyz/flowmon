import { AlertTriangle, ExternalLink } from "lucide-react"
import type { ReactNode } from "react"

import { Page } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

// Keep in step with docs/mikrotik-setup.md.

const TRAFFIC_FLOW_DOCS = "https://help.mikrotik.com/docs/spaces/ROS/pages/21102653/Traffic+Flow"
const SNMP_DOCS = "https://help.mikrotik.com/docs/spaces/ROS/pages/8978519/SNMP"

function DocsLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 underline underline-offset-4"
    >
      {children}
      <ExternalLink className="size-3" />
    </a>
  )
}

function Where({ menu, cli }: { menu: string; cli: string }) {
  return (
    <p className="text-sm">
      <span className="text-muted-foreground">Where:</span> WinBox or WebFig → <strong>{menu}</strong>{" "}
      <span className="text-muted-foreground">
        (command line: <code className="font-mono">{cli}</code>)
      </span>
    </p>
  )
}

function Settings({ rows }: { rows: [string, ReactNode, ReactNode][] }) {
  return (
    <div className="rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-1/4">Setting</TableHead>
            <TableHead className="w-1/3">Value</TableHead>
            <TableHead>Why</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(([k, v, why]) => (
            <TableRow key={k}>
              <TableCell className="font-medium whitespace-normal">{k}</TableCell>
              <TableCell className="whitespace-normal">{v}</TableCell>
              <TableCell className="whitespace-normal text-muted-foreground">{why}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function Step({
  n,
  title,
  description,
  children,
}: {
  n: number
  title: string
  description?: ReactNode
  children: ReactNode
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">
          {n}. {title}
        </CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">{children}</CardContent>
    </Card>
  )
}

const ipfixFields: [string, string][] = [
  ["bytes, packets", ""],
  ["src-address, dst-address, src-port, dst-port, protocol", ""],
  ["in-interface, out-interface", "needed to classify by interface"],
  [
    "first-forwarded, last-forwarded, sys-init-time",
    "Flow times; without sys-init-time Flowmon estimates times from the export time",
  ],
  [
    "nat-src-address, nat-dst-address, nat-src-port, nat-dst-port",
    "without them, downloads can't be attributed to the right Local Host behind NAT",
  ],
  ["nat-events", ""],
]

export function MikroTikGuidePage() {
  return (
    <Page narrow>
      <div className="flex flex-col gap-2">
        <p className="text-sm text-muted-foreground">
          What to set on a router running RouterOS 7, and where to find it. There are no copy-paste scripts on purpose:
          the settings are few, and setting them in WinBox or WebFig shows what each one does. MikroTik's documentation
          is the reference: <DocsLink href={TRAFFIC_FLOW_DOCS}>Traffic Flow</DocsLink> and{" "}
          <DocsLink href={SNMP_DOCS}>SNMP</DocsLink>.
        </p>
      </div>

      <Step n={1} title="Traffic Flow settings">
        <Where menu="IP → Traffic Flow" cli="/ip traffic-flow" />
        <Settings
          rows={[
            ["Enabled", "yes", ""],
            [
              "Interfaces",
              <code key="i">all</code>,
              "Flowmon needs to see traffic on both your WAN and your LAN interfaces.",
            ],
            [
              "Active Flow Timeout",
              <span key="a">15 s at 500 Mbit/s or more, 1 min below</span>,
              "The default of 30 min turns a long download into one record half an hour later. Some RouterOS versions send a 4-byte byte counter, which wraps after 4 GiB per export (about 34 s at 1 Gbit/s); Flowmon warns if yours does.",
            ],
            ["Inactive Flow Timeout", "15 s (the default)", ""],
            ["Packet Sampling", "off (the default)", "Flowmon counts every packet."],
          ]}
        />
      </Step>

      <Step n={2} title="Target: where to send Flows">
        <Where menu="IP → Traffic Flow → Targets" cli="/ip traffic-flow target" />
        <Settings
          rows={[
            ["Dst. Address", "the address of this Flowmon host", ""],
            ["Port", <span key="p">4739 (IPFIX) or 2055</span>, "Flowmon listens on both by default."],
            ["Version", "IPFIX", "NetFlow v9 works too, but the field choices in step 3 are IPFIX settings."],
            [
              "Src. Address",
              "the router's LAN address",
              "Flowmon identifies an Exporter by its source address; pinning it keeps it stable.",
            ],
          ]}
        />
      </Step>

      <Step n={3} title="IPFIX fields" description="Make sure these fields are enabled.">
        <Where menu="IP → Traffic Flow → IPFIX" cli="/ip traffic-flow ipfix" />
        <ul className="flex flex-col gap-2 text-sm">
          {ipfixFields.map(([fields, why]) => (
            <li key={fields}>
              <code className="font-mono">{fields}</code>
              {why && <span className="text-muted-foreground">: {why}</span>}
            </li>
          ))}
        </ul>
      </Step>

      <Step n={4} title="In Flowmon">
        <ol className="flex list-decimal flex-col gap-2 pl-5 text-sm">
          <li>Open Exporters. The router appears as waiting for approval as soon as its first packet arrives.</li>
          <li>
            Name and approve it. Choose By interface when it is offered (it is when the router sends interface numbers).
          </li>
          <li>
            Under Interfaces, name each interface number (or let SNMP do it) and set roles: the internet uplink is WAN,
            bridges and VLANs are LAN, WireGuard and other VPNs are Tunnel. Flowmon marks the interface that looks like
            your WAN.
          </li>
          <li>Save the classification. Flowmon offers to reprocess the Flows it already has.</li>
        </ol>
      </Step>

      <Step
        n={5}
        title="SNMP (optional, recommended)"
        description="Fills in interface names and lets Flowmon check Flow totals against the router's own counters."
      >
        <Where menu="IP → SNMP, then Communities" cli="/snmp, /snmp community" />
        <ol className="flex list-decimal flex-col gap-2 pl-5 text-sm">
          <li>In IP → SNMP, set Enabled.</li>
          <li>
            In Communities, add one for Flowmon: Name (e.g. <code className="font-mono">flowmon</code>; this is the
            SNMPv3 user), Addresses = only this Flowmon host (e.g. <code className="font-mono">192.168.10.44/32</code>),
            Security <code className="font-mono">private</code>, Authentication Protocol{" "}
            <code className="font-mono">SHA1</code>, Encryption Protocol <code className="font-mono">AES</code>,
            passwords of at least 8 characters, read access only.
          </li>
          <li>
            The default <code className="font-mono">public</code> community lets anyone read without a password:
            restrict its Addresses or disable it.
          </li>
          <li>
            In Flowmon, on the Exporter, open SNMP: SNMPv3, the community name as user, authentication SHA, privacy AES,
            the overhead preset that matches your WAN (Ethernet, or PPPoE when you dial in with PPPoE). Test, then save.
          </li>
        </ol>
      </Step>

      <Alert variant="warning">
        <AlertTriangle />
        <AlertTitle>Traffic the router can't show</AlertTitle>
        <AlertDescription className="flex flex-col gap-2">
          <p>
            Traffic Flow only sees traffic that reaches the router's CPU. MikroTik's documentation says: "HW offloaded
            traffic will not be seen in Traffic Flow flows". That includes:
          </p>
          <ul className="list-disc pl-5">
            <li>traffic switched between ports of a bridge with hardware offload,</li>
            <li>
              FastTrack connections with hardware offload, and L3 hardware offloading on CRS3xx/CRS5xx and
              CCR2116/CCR2216-class devices,
            </li>
            <li>traffic between VMs on the same hypervisor bridge, which never reaches the router.</li>
          </ul>
          <p>With SNMP set up, Reconciliation shows this as a lasting gap between WAN counters and Flows.</p>
        </AlertDescription>
      </Alert>
    </Page>
  )
}
