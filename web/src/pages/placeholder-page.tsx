import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import type { NavItem } from "@/lib/navigation"

/** Stand-in for a screen that hasn't been built yet. */
export function PlaceholderPage({ item }: { item: NavItem }) {
  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <div className="flex items-center gap-2">
          <item.icon className="size-5 text-muted-foreground" />
          <CardTitle>{item.title}</CardTitle>
        </div>
        <CardDescription>{item.summary}</CardDescription>
        <CardDescription>This screen isn't built yet.</CardDescription>
      </CardHeader>
    </Card>
  )
}

export function NotFoundPage() {
  return (
    <Card className="max-w-2xl">
      <CardHeader>
        <CardTitle>Page not found</CardTitle>
        <CardDescription>There's nothing at this address.</CardDescription>
      </CardHeader>
    </Card>
  )
}
