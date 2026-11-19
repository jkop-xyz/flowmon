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
import { cn } from "@/lib/utils"

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
                <Logo className="size-8!" />
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
                <SidebarMenuItem key={item.path} data-area={item.area}>
                  <SidebarMenuButton
                    asChild
                    isActive={active === item.path}
                    tooltip={item.ready ? item.title : `${item.title} (coming soon)`}
                    className={cn(
                      "h-10 hover:bg-area-tint hover:text-area-ink data-[active=true]:bg-area-tint data-[active=true]:text-area-ink group-data-[collapsible=icon]:p-1!",
                      !item.ready && "text-muted-foreground",
                    )}
                  >
                    <NavLink to={{ pathname: item.path, search }} end>
                      <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-area-tint group-data-[collapsible=icon]:size-6 text-area-ink group-data-[active=true]/menu-button:bg-area group-data-[active=true]/menu-button:text-white">
                        <item.icon className="size-4" />
                      </span>
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
