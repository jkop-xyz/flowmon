import { AlertTriangle, Waves } from "lucide-react"
import type React from "react"
import type { ReactNode } from "react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { formatAgo, formatDateTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/*
 * Shared building blocks, so every screen uses the same widths, headings, fields,
 * errors and times:
 * - Every page is a Page: one width, one gap.
 * - Sections inside a card start with a SectionHeading.
 * - Form fields are Fields (label text-sm, gap-2). Form buttons are right aligned,
 *   secondary before primary (FormActions); size "sm" inside app cards, the default size
 *   on the full-screen login and setup forms.
 * - A failed query shows QueryError; a failed form action shows FormError.
 * - "When it happened" is relative, with the full time on hover (TimeAgo).
 */

/**
 * How wide pages get: 72rem, then more on wide screens (#58), where pages lay their
 * cards out in more columns. Status banners use the same width.
 */
export const pageWidth = "max-w-6xl 3xl:max-w-[100rem] 4xl:max-w-[180rem]"

/**
 * A page's column. narrow keeps reading pages (guides, settings) at 72rem, a readable
 * width, however wide the screen.
 */
export function Page({ children, className, narrow }: { children: ReactNode; className?: string; narrow?: boolean }) {
  return (
    <div className={cn("mx-auto flex w-full flex-col gap-6", narrow ? "max-w-6xl" : pageWidth, className)}>{children}</div>
  )
}

export function SectionHeading({ title, aside }: { title: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <h3 className="flex items-center gap-1 text-sm font-medium">{title}</h3>
      {aside && <div className="text-xs text-muted-foreground">{aside}</div>}
    </div>
  )
}

export function Section({ children, className }: { children: ReactNode; className?: string }) {
  return <section className={cn("flex flex-col gap-3", className)}>{children}</section>
}

export function Stat({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-lg border p-3">
      <span className="truncate text-xs text-muted-foreground">{label}</span>
      <span className="truncate text-lg font-semibold tabular-nums">{value}</span>
    </div>
  )
}

export function QueryError({ title, error }: { title: string; error: Error }) {
  return (
    <Alert variant="destructive">
      <AlertTriangle />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{error.message}</AlertDescription>
    </Alert>
  )
}

export function SectionSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-2">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-8 w-full" />
      ))}
    </div>
  )
}

export function FormError({ error }: { error: Error | null | undefined }) {
  if (!error) return null
  return <p className="text-sm text-destructive">{error.message}</p>
}

export function Field({ id, label, hint, children }: { id?: string; label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

export function FormActions({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center justify-end gap-2">{children}</div>
}

/** A past time as "3 minutes ago", with the full date and time on hover. */
export function TimeAgo({ iso, now }: { iso: string; now: number }) {
  return (
    <time dateTime={iso} title={formatDateTime(iso)}>
      {formatAgo(iso, now)}
    </time>
  )
}

export function Logo({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "flex aspect-square size-8 shrink-0 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground",
        className,
      )}
    >
      <Waves className="size-4" />
    </div>
  )
}

/** The one empty-state card: placeholders, 404, "nothing here yet". */
export function EmptyState({
  icon: Icon,
  title,
  children,
  action,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  children: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-start gap-3 rounded-xl border bg-card p-6 text-card-foreground">
      <div className="flex items-center gap-2">
        <Icon className="size-4 text-muted-foreground" />
        <h2 className="font-medium">{title}</h2>
      </div>
      <div className="flex max-w-prose flex-col gap-1 text-sm text-muted-foreground">{children}</div>
      {action}
    </div>
  )
}
