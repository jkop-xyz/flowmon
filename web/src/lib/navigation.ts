import { Activity, Cable, Globe, LayoutDashboard, Monitor, Settings, type LucideIcon } from "lucide-react"

export type NavItem = {
  title: string
  path: string
  icon: LucideIcon
  /** What the screen will show; displayed on placeholder pages until the screen exists. */
  summary: string
}

export const navItems: NavItem[] = [
  {
    title: "Overview",
    path: "/",
    icon: LayoutDashboard,
    summary: "Bandwidth over time, top Local Hosts, Remote IPs, ASN Owners, countries and Service Ports.",
  },
  {
    title: "Local Hosts",
    path: "/hosts",
    icon: Monitor,
    summary: "Each Local Host's traffic over time, its Peers and the Service Ports it uses.",
  },
  {
    title: "Remote IPs",
    path: "/remote",
    icon: Globe,
    summary: "Who is on the other end: country, ASN Owner, reverse DNS and registration details.",
  },
  {
    title: "Flow Explorer",
    path: "/flows",
    icon: Activity,
    summary: "A filterable, sortable table of raw Flows with every decoded field.",
  },
  {
    title: "Exporters",
    path: "/exporters",
    icon: Cable,
    summary: "Each Exporter's health, Templates, Interfaces and decode counters.",
  },
  {
    title: "Settings",
    path: "/settings",
    icon: Settings,
    summary: "Local Networks, GeoIP databases, retention and accounts.",
  },
]
