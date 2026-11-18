import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { LogOut, User as UserIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { fetchSession, logout } from "@/lib/auth"

export function UserMenu() {
  const queryClient = useQueryClient()
  const session = useQuery({ queryKey: ["session"], queryFn: ({ signal }) => fetchSession(signal) })
  const out = useMutation({ mutationFn: logout, onSuccess: () => queryClient.invalidateQueries() })

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="Account">
          <UserIcon className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel className="font-normal">
          Logged in as <span className="font-medium">{session.data?.username}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => out.mutate()}>
          <LogOut /> Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
