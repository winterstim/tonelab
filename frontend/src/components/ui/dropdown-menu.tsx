import * as React from "react"
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

const DropdownMenu = DropdownMenuPrimitive.Root
const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger

function DropdownMenuContent({ className, sideOffset = 8, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Content>) {
  return (
    <DropdownMenuPrimitive.Portal>
      <DropdownMenuPrimitive.Content
        data-slot="dropdown-menu-content"
        sideOffset={sideOffset}
        className={cn(
          "z-50 max-h-72 min-w-56 max-w-80 overflow-y-auto rounded-2xl bg-popover p-1.5 text-popover-foreground shadow-lift",
          className
        )}
        {...props}
      />
    </DropdownMenuPrimitive.Portal>
  )
}

function DropdownMenuItem({ className, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Item>) {
  return (
    <DropdownMenuPrimitive.Item
      data-slot="dropdown-menu-item"
      className={cn(
        "block w-full cursor-pointer truncate rounded-xl px-3 py-2 text-left text-[13px] text-muted-foreground outline-none select-none data-[highlighted]:bg-secondary data-[highlighted]:text-foreground aria-[pressed=true]:bg-secondary aria-[pressed=true]:font-medium aria-[pressed=true]:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem }
