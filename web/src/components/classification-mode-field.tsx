import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import type { ClassificationMode } from "@/lib/api"

/** The one control for an Exporter's Classification Mode, used at approval and in Interfaces. */
export function ClassificationModeField({
  value,
  onChange,
  interfaceAvailable,
  idPrefix,
}: {
  value: ClassificationMode
  onChange: (mode: ClassificationMode) => void
  /** False when the Exporter's Flows carry no interface numbers. */
  interfaceAvailable: boolean
  idPrefix: string
}) {
  const options: { value: ClassificationMode; label: string; hint: string; disabled?: boolean }[] = [
    {
      value: "interface",
      label: "By interface",
      hint: interfaceAvailable
        ? "Direction follows which way a Flow crosses a WAN Interface."
        : "Not available: this Exporter sends no interface numbers.",
      disabled: !interfaceAvailable,
    },
    { value: "address", label: "By address", hint: "Direction follows which addresses are in your Local Networks." },
  ]
  return (
    <RadioGroup
      value={value}
      onValueChange={(v) => onChange(v as ClassificationMode)}
      className="grid gap-3 sm:grid-cols-2"
    >
      {options.map((o) => (
        <label
          key={o.value}
          htmlFor={`${idPrefix}-${o.value}`}
          className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm has-[[data-state=checked]]:border-primary has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-60"
        >
          <RadioGroupItem id={`${idPrefix}-${o.value}`} value={o.value} disabled={o.disabled} className="mt-0.5" />
          <span className="flex flex-col gap-1">
            <span className="font-medium">{o.label}</span>
            <span className="text-xs text-muted-foreground">{o.hint}</span>
          </span>
        </label>
      ))}
    </RadioGroup>
  )
}
