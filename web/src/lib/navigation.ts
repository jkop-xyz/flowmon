import { Activity, Cable, CircleHelp, Globe, LayoutDashboard, Monitor, Settings, type LucideIcon } from "lucide-react"

/** Each area of the app has its own colour (index.css, [data-area]). */
export type Area = "overview" | "hosts" | "remote" | "explorer" | "exporters" | "settings"

export type NavItem = {
  title: string
  path: string
  area: Area
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
    area: "overview",
    ready: true,
    filters: { exporter: true, timeRange: true },
    icon: LayoutDashboard,
    summary: "Bandwidth over time, top Local Hosts, Remote IPs, ASN Owners, countries and Service Ports.",
  },
  {
    title: "Local Hosts",
    path: "/hosts",
    area: "hosts",
    ready: true,
    filters: { exporter: true, timeRange: true },
    icon: Monitor,
    summary: "Each Local Host's traffic over time, its Peers and the Service Ports it uses.",
  },
  {
    title: "Remote IPs",
    path: "/remote",
    area: "remote",
    ready: true,
    filters: { exporter: true, timeRange: true },
    icon: Globe,
    summary: "Who is on the other end: country, ASN Owner, reverse DNS and registration details.",
  },
  {
    title: "Flow Explorer",
    path: "/flows",
    area: "explorer",
    ready: true,
    filters: { exporter: true, timeRange: true },
    icon: Activity,
    summary: "A filterable, sortable table of raw Flows with every decoded field.",
  },
  {
    title: "Exporters",
    path: "/exporters",
    area: "exporters",
    ready: true,
    filters: { exporter: true, timeRange: false },
    icon: Cable,
    summary: "Each Exporter's health, Templates, Interfaces and decode counters.",
  },
  {
    title: "Settings",
    path: "/settings",
    area: "settings",
    ready: true,
    filters: none,
    icon: Settings,
    summary: "Local Networks, GeoIP databases, retention and accounts.",
  },
]

export type RouteMeta = {
  title: string
  area: Area
  icon: LucideIcon
  /** The sidebar item to highlight. */
  activePath: string
  filters: Filters
}

/** Title, active sidebar item and filters for any path, including ones not in the sidebar. */
export function routeMeta(pathname: string): RouteMeta {
  const item = navItems.find((i) => i.path === pathname)
  if (item) return { ...within(item.path), filters: item.ready ? item.filters : none }
  if (pathname.startsWith("/hosts/")) return { ...within("/hosts", "Local Host"), filters: { exporter: true, timeRange: true } }
  if (pathname.startsWith("/remote/")) return { ...within("/remote", "Remote IP"), filters: { exporter: true, timeRange: true } }
  if (pathname === "/guide/mikrotik") return { ...within("/exporters", "MikroTik setup guide"), filters: none }
  if (pathname === "/guide/classification") return { ...within("/exporters", "Classification guide"), filters: none }
  return { title: "Page not found", area: "overview", icon: CircleHelp, activePath: "", filters: none }
}

/** A page inside a sidebar item's area: its colour, icon and highlight, and its own title. */
function within(path: string, title?: string) {
  const item = navItems.find((i) => i.path === path)!
  return { title: title ?? item.title, area: item.area, icon: item.icon, activePath: item.path }
}
