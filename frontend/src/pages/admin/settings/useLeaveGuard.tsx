import { useBlocker } from '@tanstack/react-router'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

/**
 * Asks before a settings page with unsaved edits is left (#674). One draft per
 * page means leaving a page discards its edits; this makes that a choice. It
 * covers every router navigation (sidebar, tab links, Back) and, through
 * enableBeforeUnload, closing or reloading the tab. Returns the dialog for the
 * page to render.
 */
export function useLeaveGuard(draft: Pick<SettingsDraft, 'dirty'>) {
  const blocker = useBlocker({
    shouldBlockFn: () => draft.dirty,
    enableBeforeUnload: () => draft.dirty,
    withResolver: true,
  })
  return (
    <ConfirmDialog
      open={blocker.status === 'blocked'}
      onOpenChange={(open) => { if (!open) blocker.reset?.() }}
      title="Leave without saving?"
      description="Your changes on this page have not been saved. Leaving discards them."
      confirmLabel="Leave without saving"
      cancelLabel="Stay"
      destructive
      onConfirm={() => blocker.proceed?.()}
    />
  )
}
