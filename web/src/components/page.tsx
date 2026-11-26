import { AlertTriangle, ArrowLeft } from "lucide-react"
import type React from "react"
import type { ReactNode } from "react"
import { useLocation, useNavigate } from "react-router"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { formatAgo, formatDateTime } from "@/lib/format"
import { routeMeta } from "@/lib/navigation"
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
export function Page({
  children,
  className,
  narrow,
  actions,
}: {
  children: ReactNode
  className?: string
  narrow?: boolean
  /** Buttons at the right end of the title row. */
  actions?: ReactNode
}) {
  return (
    <div className={cn("mx-auto flex w-full flex-col gap-6", narrow ? "max-w-6xl" : pageWidth, className)}>
      <PageTitle actions={actions} />
      {children}
    </div>
  )
}

/** The page's name beside a tile in its area's colour, after a way back for a page below another. */
function PageTitle({ actions }: { actions?: ReactNode }) {
  const { pathname, search, key } = useLocation()
  const navigate = useNavigate()
  const { title, icon: Icon, parent } = routeMeta(pathname)
  // "default" is the first page of the session (a bookmark or a reload): nothing to go
  // back to, so back is the page above it, with the same time range and Exporter.
  const first = key === "default"
  return (
    <div className="flex items-center gap-3">
      {parent && (
        <Button
          variant="ghost"
          size="icon"
          className="-ml-2 shrink-0"
          aria-label={first ? `To ${parent.title}` : "Back"}
          title={first ? `To ${parent.title}` : "Back"}
          onClick={() => (first ? navigate({ pathname: parent.path, search }) : navigate(-1))}
        >
          <ArrowLeft />
        </Button>
      )}
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-area text-white">
        <Icon className="size-5" />
      </span>
      <h1 className="min-w-0 truncate text-xl font-semibold tracking-tight">{title}</h1>
      {actions && <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
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

/** Flowmon's mark: three streams meeting and leaving as one, on its tile. */
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 64 64" aria-hidden className={cn("size-8 shrink-0", className)}>
      <rect width="64" height="64" rx="15" className="fill-logo-tile" />
      <g fill="none" strokeLinecap="round" strokeWidth="6">
        <path d="M10 17 C23 17 26 32 34 32" stroke="#60a5fa" />
        <path d="M10 32 L34 32" stroke="#4ade80" />
        <path d="M10 47 C23 47 26 32 34 32" stroke="#fb923c" />
        <path d="M36 32 L53 32" stroke="#ffffff" strokeWidth="8" />
      </g>
      <circle cx="36" cy="32" r="7" fill="#ffffff" />
    </svg>
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
