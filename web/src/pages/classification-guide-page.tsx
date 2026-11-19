import type { ReactNode } from "react"

import { GuideBackButton } from "@/components/guide-back-button"
import { Page } from "@/components/page"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { ClassificationMode, InterfaceRole } from "@/lib/api"
import {
  changingMode,
  classificationIntro,
  modeAdvice,
  modeHelp,
  roleHelp,
  roleLabel,
  snmpAndRoles,
} from "@/lib/classification-help"

function Topic({ title, description, children }: { title: string; description?: ReactNode; children: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">{children}</CardContent>
    </Card>
  )
}

const modes: ClassificationMode[] = ["interface", "address"]

const modeRows: [string, (m: ClassificationMode) => string][] = [
  ["Asks", (m) => `“${modeHelp[m].asks}”`],
  ["Good at", (m) => modeHelp[m].goodAt],
  ["Goes wrong", (m) => modeHelp[m].goesWrong],
  ["Needs", (m) => modeHelp[m].needs],
]

const example: [string, InterfaceRole, string][] = [
  ["ether1", "wan", "The cable from the modem: the internet comes in here."],
  ["he-6in4", "wan", "An IPv6 tunnel broker. IPv6 internet traffic comes through it, so it's WAN, not Tunnel."],
  ["bridge", "lan", "The home network: LAN ports and Wi-Fi."],
  ["vlan20-iot", "lan", "A VLAN of your own, for smart-home devices."],
  ["wg-phone", "tunnel", "WireGuard for your phone away from home. The phone counts as one of your devices."],
  ["ether7", "", "Nothing plugged in."],
  ["lo", "", "Loopback. Flowmon recognizes traffic to and from the router itself without a role."],
]

export function ClassificationGuidePage() {
  return (
    <Page narrow>
      <div className="flex flex-col gap-2">
        <GuideBackButton fallback="/exporters" fallbackLabel="Exporters" />
        <h1 className="text-lg font-semibold">Classification and Interface Roles</h1>
        <p className="text-sm text-muted-foreground">
          How Flowmon tells your traffic from the internet's, and what to set for each Exporter.
        </p>
      </div>

      <Topic title="What classification is">
        {classificationIntro.map((p) => (
          <p key={p}>{p}</p>
        ))}
        <p>
          Each Exporter has its own Classification Mode. You choose it when you approve the Exporter, and can change it
          later in its Interfaces section on the Exporters page.
        </p>
      </Topic>

      <Topic title="The two modes" description={modeAdvice}>
        <div className="grid gap-3 sm:grid-cols-2">
          {modes.map((m) => (
            <dl key={m} className="flex flex-col gap-2 rounded-lg border p-3">
              <dt className="font-medium">{modeHelp[m].label}</dt>
              {modeRows.map(([label, text]) => (
                <dd key={label}>
                  <span className="font-medium">{label}:</span>{" "}
                  <span className="text-muted-foreground">{text(m)}</span>
                </dd>
              ))}
            </dl>
          ))}
        </div>
      </Topic>

      <Topic
        title="Interface Roles"
        description="What each interface connects to. By interface uses them; By address ignores them."
      >
        <dl className="grid gap-x-4 gap-y-2 sm:grid-cols-[auto_1fr]">
          {roleHelp.map((r) => (
            <div key={r.label} className="contents">
              <dt className="font-medium">{r.label}</dt>
              <dd className="text-muted-foreground">{r.meaning}</dd>
            </div>
          ))}
        </dl>
      </Topic>

      <Topic
        title="A worked example"
        description="A home router with a fibre uplink, an IPv6 tunnel, a VLAN for smart-home devices and WireGuard."
      >
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-32">Interface</TableHead>
                <TableHead className="w-24">Role</TableHead>
                <TableHead>Why</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {example.map(([name, role, why]) => (
                <TableRow key={name}>
                  <TableCell className="align-top font-mono text-xs">{name}</TableCell>
                  <TableCell className="align-top">{roleLabel(role)}</TableCell>
                  <TableCell className="align-top whitespace-normal text-muted-foreground">{why}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <p className="text-muted-foreground">
          On a PPPoE connection, mark the pppoe-out interface WAN rather than the Ethernet port under it: Flows name
          the PPPoE interface.
        </p>
      </Topic>

      <Topic title="SNMP, and changing your mind">
        <p>{snmpAndRoles}</p>
        <p>
          {changingMode} Each Exporter's Classification history on the Exporters page lists the changes, and you can
          remove a dashed line there.
        </p>
      </Topic>
    </Page>
  )
}
