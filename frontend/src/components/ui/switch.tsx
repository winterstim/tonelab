import * as React from "react"
import { Switch as SwitchPrimitive } from "radix-ui"
import { cn } from "@/lib/utils"

function Switch({ className, ...props }: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        "peer inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-transparent transition-colors outline-none disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="pointer-events-none block size-4 rounded-full shadow-lift transition-[transform,background-color] data-[state=checked]:translate-x-4 data-[state=checked]:bg-primary-foreground data-[state=unchecked]:translate-x-0.5 data-[state=unchecked]:bg-foreground"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
