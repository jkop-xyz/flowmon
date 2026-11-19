import { ChevronRight } from "lucide-react"
import { Link } from "react-router"

import { Badge } from "@/components/ui/badge"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import type { ClassificationMode } from "@/lib/api"
import { classificationGuidePath, classificationIntro, modeAdvice, modeHelp } from "@/lib/classification-help"

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
  const modes: ClassificationMode[] = ["interface", "address"]
  return (
    <div className="flex flex-col gap-3">
      <ClassificationExplainer />
      <RadioGroup
        value={value}
        onValueChange={(v) => onChange(v as ClassificationMode)}
        className="grid gap-3 sm:grid-cols-2"
      >
        {modes.map((mode) => {
          const help = modeHelp[mode]
          const disabled = mode === "interface" && !interfaceAvailable
          return (
            <label
              key={mode}
              htmlFor={`${idPrefix}-${mode}`}
              className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 text-sm has-[[data-state=checked]]:border-primary has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-60"
            >
              <RadioGroupItem id={`${idPrefix}-${mode}`} value={mode} disabled={disabled} className="mt-0.5" />
              <span className="flex flex-col gap-1">
                <span className="flex items-center gap-2 font-medium">
                  {help.label}
                  {mode === "interface" && interfaceAvailable && <Badge variant="outline">Recommended</Badge>}
                </span>
                {disabled ? (
                  <span className="text-xs text-muted-foreground">
                    Not available: this Exporter sends no interface numbers.
                  </span>
                ) : (
                  <>
                    <span className="text-xs">Asks: “{help.asks}”</span>
                    <span className="text-xs text-muted-foreground">
                      <span className="text-foreground">Goes wrong</span> {lowerFirst(help.goesWrong)}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      <span className="text-foreground">Needs</span> {lowerFirst(help.needs)}
                    </span>
                  </>
                )}
              </span>
            </label>
          )
        })}
      </RadioGroup>
    </div>
  )
}

/** What classification is, folded away so it doesn't push the form down for returning users. */
function ClassificationExplainer() {
  return (
    <Collapsible className="rounded-lg border bg-muted/30 text-sm">
      <CollapsibleTrigger className="group flex w-full items-center gap-1 px-3 py-2 text-left font-medium">
        <ChevronRight className="size-4 transition-transform group-data-[state=open]:rotate-90" />
        What is classification?
      </CollapsibleTrigger>
      <CollapsibleContent className="flex flex-col gap-2 px-3 pb-3 text-muted-foreground">
        {classificationIntro.map((p) => (
          <p key={p}>{p}</p>
        ))}
        <p>
          Flowmon can answer “which side is yours?” in two ways, below. {modeAdvice}
        </p>
        <p>
          <Link to={classificationGuidePath} className="text-foreground underline underline-offset-4">
            Read the classification guide
          </Link>{" "}
          for the Interface Roles and a worked example.
        </p>
      </CollapsibleContent>
    </Collapsible>
  )
}

const lowerFirst = (s: string) => s.charAt(0).toLowerCase() + s.slice(1)
