import { Field } from "@/components/page"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import type { Counting, ExporterRole } from "@/lib/api"

/**
 * The role, LAN subnets and "observes multiple points" of an Exporter: how its Flows are
 * counted when several Exporters see the same traffic.
 */
export function CountingFields({
  idPrefix,
  value,
  subnetsText,
  onChange,
  onSubnetsText,
}: {
  idPrefix: string
  value: Counting
  subnetsText: string
  onChange: (c: Counting) => void
  onSubnetsText: (text: string) => void
}) {
  return (
    <div className="flex flex-col gap-4">
      <Field
        id={`${idPrefix}-role`}
        label="Exporter role"
        hint={
          value.role === "edge"
            ? "Sees the traffic between your Local Networks and the internet, such as the main router."
            : "Sits inside your Local Networks, such as a hypervisor or a switch. The Edge Exporter already counts what it routes, so only traffic between two hosts in the same subnet is counted from it."
        }
      >
        <Select value={value.role} onValueChange={(role) => onChange({ ...value, role: role as ExporterRole })}>
          <SelectTrigger id={`${idPrefix}-role`} className="w-full sm:w-64">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="edge">Edge</SelectItem>
            <SelectItem value="internal">Internal</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {value.role === "internal" && (
        <Field
          id={`${idPrefix}-subnets`}
          label="LAN subnets"
          hint="The subnets on its LAN side, one network segment each, such as the VM bridge's 10.0.20.0/24 or an IPv6 /64. Not a whole Local Network: 10.0.0.0/8 would count routed traffic twice."
        >
          <Input
            id={`${idPrefix}-subnets`}
            className="font-mono"
            placeholder="10.0.20.0/24, fd00:20::/64"
            value={subnetsText}
            onChange={(e) => onSubnetsText(e.target.value)}
          />
        </Field>
      )}
      <label className="flex items-start gap-3 text-sm">
        <Switch
          checked={value.multiplePoints}
          onCheckedChange={(multiplePoints) => onChange({ ...value, multiplePoints })}
          className="mt-0.5"
        />
        <span className="flex flex-col gap-0.5">
          <span>Observes multiple points</span>
          <span className="text-xs text-muted-foreground">
            It reports the same packets more than once, such as softflowd on every tap of a hypervisor or a router
            exporting on several interfaces. Flowmon then removes the duplicates, matching them before and after NAT.
          </span>
        </span>
      </label>
    </div>
  )
}
