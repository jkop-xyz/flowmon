import { FileQuestion } from "lucide-react"
import { Link } from "react-router"

import { EmptyState, Page } from "@/components/page"
import { Button } from "@/components/ui/button"
import type { NavItem } from "@/lib/navigation"

/** Stand-in for a screen that hasn't been built yet. */
export function PlaceholderPage({ item }: { item: NavItem }) {
  return (
    <Page>
      <EmptyState icon={item.icon} title={`${item.title} is coming soon`}>
        <p>{item.summary}</p>
        <p>This screen isn't built yet.</p>
      </EmptyState>
    </Page>
  )
}

export function NotFoundPage() {
  return (
    <Page>
      <EmptyState
        icon={FileQuestion}
        title="Page not found"
        action={
          <Button asChild size="sm" variant="outline">
            <Link to="/">Go to the Overview</Link>
          </Button>
        }
      >
        <p>There's nothing at this address.</p>
      </EmptyState>
    </Page>
  )
}
