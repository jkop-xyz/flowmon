import { useQuery } from "@tanstack/react-query"
import { Outlet, useLocation } from "react-router"

import { AppSidebar } from "@/components/app-sidebar"
import { ExporterPicker } from "@/components/exporter-picker"
import { ModeToggle } from "@/components/mode-toggle"
import { TimeRangePicker } from "@/components/time-range-picker"
import { UserMenu } from "@/components/user-menu"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { fetchHealth } from "@/lib/api"
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

export function AppLayout() {
  const { pathname } = useLocation()
  const meta = routeMeta(pathname)
  const hasFilters = meta.filters.exporter || meta.filters.timeRange

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        {/* On phones the filters move to a second row, so nothing is pushed off-screen. */}
        <header className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-2 border-b px-4 py-2.5 sm:min-h-14">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-2 data-vertical:h-4 data-vertical:self-center" />
          <h1 className="min-w-0 flex-1 truncate text-sm font-medium sm:flex-none">{meta.title}</h1>
          {hasFilters && (
            <div className="order-last flex w-full items-center gap-2 sm:order-none sm:ml-auto sm:w-auto">
              {meta.filters.exporter && <ExporterPicker />}
              {meta.filters.timeRange && <TimeRangePicker />}
            </div>
          )}
          <div className={hasFilters ? "flex items-center gap-1" : "ml-auto flex items-center gap-1"}>
            <ServerStatus />
            <ModeToggle />
            <UserMenu />
          </div>
        </header>
        <main className="flex-1 p-4 md:p-6">
          <Outlet />
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}
