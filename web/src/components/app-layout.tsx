import { useQuery } from "@tanstack/react-query"
import { Outlet, useLocation } from "react-router"

import { AppSidebar } from "@/components/app-sidebar"
import { ModeToggle } from "@/components/mode-toggle"
import { TimeRangePicker } from "@/components/time-range-picker"
import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar"
import { fetchHealth } from "@/lib/api"
import { navItems } from "@/lib/navigation"

function ServerStatus() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => fetchHealth(signal),
    refetchInterval: 30_000,
  })

  if (health.isPending) return null
  if (health.isError) return <Badge variant="destructive">Server unreachable</Badge>
  const { version } = health.data
  return <Badge variant="secondary">{/^\d/.test(version) ? `v${version}` : version}</Badge>
}

export function AppLayout() {
  const { pathname } = useLocation()
  const title = navItems.find((item) => item.path === pathname)?.title ?? "Flowmon"

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-2 data-vertical:h-4 data-vertical:self-center" />
          <h1 className="text-sm font-medium">{title}</h1>
          <div className="ml-auto flex items-center gap-2">
            <TimeRangePicker />
            <ServerStatus />
            <ModeToggle />
          </div>
        </header>
        <main className="flex-1 p-4 md:p-6">
          <Outlet />
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}
