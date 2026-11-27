import * as React from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

type ConfirmDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: React.ReactNode
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
  isPending?: boolean
  // A failure to show inside the dialog, which stays open.
  error?: string
  // Called as the dialog closes, before focus goes back to what opened it.
  // preventDefault() stops that, for a caller whose opener is gone by then.
  onCloseAutoFocus?: (e: Event) => void
  onConfirm: () => void
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = 'Delete',
  cancelLabel = 'Cancel',
  destructive = true,
  isPending = false,
  error,
  onCloseAutoFocus,
  onConfirm,
}: ConfirmDialogProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-overlay data-[state=open]:animate-in data-[state=open]:fade-in-0" />
        <Dialog.Content
          onCloseAutoFocus={onCloseAutoFocus}
          className={cn(
            'fixed left-1/2 top-1/2 z-50 w-[90vw] max-w-md -translate-x-1/2 -translate-y-1/2',
            'rounded-lg bg-card p-5 shadow-lg focus:outline-none',
            'data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95'
          )}
        >
          <Dialog.Title className="text-base font-semibold text-fg">{title}</Dialog.Title>
          {description && (
            <Dialog.Description asChild>
              <div className="mt-2 text-sm text-fg-muted">{description}</div>
            </Dialog.Description>
          )}
          {error && (
            <p role="alert" className="mt-3 text-sm text-danger-fg">
              {error}
            </p>
          )}
          <div className="mt-5 flex justify-end gap-2">
            <Button size="sm" variant="outline" onClick={() => onOpenChange(false)} disabled={isPending}>
              {cancelLabel}
            </Button>
            <Button
              size="sm"
              variant={destructive ? 'destructive' : 'default'}
              onClick={onConfirm}
              disabled={isPending}
            >
              {isPending ? 'Working…' : confirmLabel}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
