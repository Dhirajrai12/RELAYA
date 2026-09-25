import { useState, type ReactNode } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { errorMessage } from '@/lib/api'

/** A button that asks for confirmation before running a risky action. */
export function ConfirmButton({
  children,
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
  variant = 'outline',
}: {
  children: ReactNode
  title: string
  description: ReactNode
  confirmLabel: string
  destructive?: boolean
  onConfirm: () => Promise<unknown>
  variant?: 'outline' | 'destructive' | 'ghost'
}) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function run() {
    setBusy(true)
    setError('')
    try {
      await onConfirm()
      setOpen(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button variant={variant} size="sm" onClick={() => setOpen(true)}>
        {children}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button variant={destructive ? 'destructive' : 'default'} onClick={run} disabled={busy}>
              {busy ? 'Working…' : confirmLabel}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
