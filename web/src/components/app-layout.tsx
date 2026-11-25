import { useQuery } from "@tanstack/react-query"
import { useEffect } from "react"
import { Link, Outlet, useLocation } from "react-router"

import { AppSidebar } from "@/components/app-sidebar"
import { ExporterPicker } from "@/components/exporter-picker"
import { ModeToggle } from "@/components/mode-toggle"
import { TimeRangePicker } from "@/components/time-range-picker"
import { UserMenu } from "@/components/user-menu"
import { Badge } from "@/components/ui/badge"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { StatusBanners } from "@/components/status-banners"
import { appStoreNames, fetchHealth, fetchStatus } from "@/lib/api"
import { routeMeta } from "@/lib/navigation"

function ServerStatus() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => fetchHealth(signal),
    refetchInterval: 30_000,
  })

  if (health.isPending) return null
  if (health.isError) return <Badge variant="destructive">Server unreachable</Badge>
  const { version } = health.data
  return (
    <Badge variant="secondary" className="hidden sm:inline-flex">
      {/^\d/.test(version) ? `v${version}` : version}
    </Badge>
  )
}

/** The version and which databases are in use, on every page. */
function AppFooter() {
  const health = useQuery({ queryKey: ["health"], queryFn: ({ signal }) => fetchHealth(signal), refetchInterval: 30_000 })
  const status = useQuery({ queryKey: ["status"], queryFn: ({ signal }) => fetchStatus(signal), refetchInterval: 15_000 })
  const version = health.data?.version
  const driver = status.data?.appStore?.driver
  return (
    <footer className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 border-t px-4 py-3 text-xs text-muted-foreground">
      <span>Flowmon {version ? (/^\d/.test(version) ? `v${version}` : version) : ""}</span>
      {driver && (
        <>
          <span aria-hidden>·</span>
          <Link to="/settings/databases" className="underline-offset-4 hover:underline">
            App Store: {appStoreNames[driver]}
          </Link>
        </>
      )}
      <span aria-hidden>·</span>
      <span>Flow Store: DuckDB</span>
    </footer>
  )
}

export function AppLayout() {
  const { pathname } = useLocation()
  const meta = routeMeta(pathname)
  const hasFilters = meta.filters.exporter || meta.filters.timeRange
  // On <html>, so menus and dialogs, which render outside this tree, take the page's colour too.
  useEffect(() => {
    document.documentElement.dataset.area = meta.area
  }, [meta.area])

  return (
    <SidebarProvider>
      <AppSidebar />
      {/* min-w-0: a wide table scrolls in its own box rather than widening the page. */}
      <SidebarInset className="min-w-0">
        {/* On phones the filters move to a second row, so nothing is pushed off-screen. */}
        <header className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-2 border-b px-4 py-2.5 sm:min-h-14">
          <SidebarTrigger className="-ml-1" />
          <div className="flex-1" />
          {hasFilters && (
            <div className="order-last flex w-full items-center gap-2 sm:order-none sm:w-auto">
              {meta.filters.exporter && <ExporterPicker />}
              {meta.filters.timeRange && <TimeRangePicker />}
            </div>
          )}
          <div className="flex items-center gap-1">
            <ServerStatus />
            <ModeToggle />
            <UserMenu />
          </div>
        </header>
        <main className="flex-1 p-4 md:p-6">
          <StatusBanners />
          <Outlet />
        </main>
        <AppFooter />
      </SidebarInset>
    </SidebarProvider>
  )
}
