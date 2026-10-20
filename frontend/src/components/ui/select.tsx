import * as React from 'react'
import { cn } from '@/lib/utils'

export type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement>

// The focus ring is an outline, not Tailwind's usual box-shadow ring: WebKit
// drops box-shadow on a native <select>, so keyboard users on Safari saw no
// focus (#561). `py-1`, not `py-2`: with a compact `h-7 text-xs` the old
// padding left a 10px content box for a 16px line and clipped the text (#562).
// An element's own outline is not clipped by its own `contain: paint`.
export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, children, ...props }, ref) => (
    <select
      className={cn(
        'flex h-10 w-full text-ellipsis [contain:paint] rounded-md border border-input bg-background px-3 py-1 text-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[color:hsl(var(--ring))] disabled:cursor-not-allowed disabled:opacity-50',
        className
      )}
      ref={ref}
      {...props}
    >
      {children}
    </select>
  )
)
Select.displayName = 'Select'
