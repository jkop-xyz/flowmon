import { Activity, Cable, Globe, LayoutDashboard, Monitor, Settings, type LucideIcon } from "lucide-react"

export type NavItem = {
  title: string
  path: string
  icon: LucideIcon
  /** What the screen will show; displayed on placeholder pages until the screen exists. */
  summary: string
  /** False while the screen is a placeholder; the sidebar marks it "Soon". */
  ready: boolean
  /** Which global filters apply on this screen. */
  filters: Filters
}

export type Filters = { exporter: boolean; timeRange: boolean }

const none: Filters = { exporter: false, timeRange: false }

export const navItems: NavItem[] = [
  {
    title: "Overview",
    path: "/",
    ready: true,
    filters: { exporter: true, timeRange: true },
    icon: LayoutDashboard,
    summary: "Bandwidth over time, top Local Hosts, Remote IPs, ASN Owners, countries and Service Ports.",
  },
  {
    title: "Local Hosts",
    path: "/hosts",
    ready: false,
    filters: none,
    icon: Monitor,
    summary: "Each Local Host's traffic over time, its Peers and the Service Ports it uses.",
  },
  {
    title: "Remote IPs",
    path: "/remote",
    ready: false,
    filters: none,
    icon: Globe,
    summary: "Who is on the other end: country, ASN Owner, reverse DNS and registration details.",
  },
  {
    title: "Flow Explorer",
    path: "/flows",
    ready: false,
    filters: none,
    icon: Activity,
    summary: "A filterable, sortable table of raw Flows with every decoded field.",
  },
  {
    title: "Exporters",
    path: "/exporters",
    ready: true,
    filters: { exporter: true, timeRange: false },
    icon: Cable,
    summary: "Each Exporter's health, Templates, Interfaces and decode counters.",
  },
  {
    title: "Settings",
    path: "/settings",
    ready: false,
    filters: none,
    icon: Settings,
    summary: "Local Networks, GeoIP databases, retention and accounts.",
  },
]

export type RouteMeta = {
  title: string
  /** The sidebar item to highlight. */
  activePath: string
  filters: Filters
}

/** Title, active sidebar item and filters for any path, including ones not in the sidebar. */
export function routeMeta(pathname: string): RouteMeta {
  const item = navItems.find((i) => i.path === pathname)
  if (item) return { title: item.title, activePath: item.path, filters: item.ready ? item.filters : none }
  if (pathname === "/guide/mikrotik") return { title: "MikroTik setup guide", activePath: "/exporters", filters: none }
  return { title: "Page not found", activePath: "", filters: none }
}
