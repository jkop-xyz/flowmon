import { NavLink, useLocation } from "react-router"

import { Logo } from "@/components/page"
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { navItems, routeMeta } from "@/lib/navigation"

export function AppSidebar() {
  // Links keep the query string so the global time range survives navigation.
  const { pathname, search } = useLocation()
  const active = routeMeta(pathname).activePath

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild>
              <NavLink to="/">
                <Logo />
                <span className="font-semibold">Flowmon</span>
              </NavLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {navItems.map((item) => (
                <SidebarMenuItem key={item.path}>
                  <SidebarMenuButton
                    asChild
                    isActive={active === item.path}
                    tooltip={item.ready ? item.title : `${item.title} (coming soon)`}
                    className={item.ready ? undefined : "text-muted-foreground"}
                  >
                    <NavLink to={{ pathname: item.path, search }} end>
                      <item.icon />
                      <span>{item.title}</span>
                    </NavLink>
                  </SidebarMenuButton>
                  {!item.ready && <SidebarMenuBadge className="text-muted-foreground">Soon</SidebarMenuBadge>}
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
    </Sidebar>
  )
}
