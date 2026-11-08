import { useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'

// Starts a horizontal drag from a resize handle: onMove gets how far the
// pointer has moved since the press, until the button is released.
export function dragX(e: ReactPointerEvent, onMove: (dx: number) => void, onEnd?: () => void) {
  if (e.button !== 0) return
  e.preventDefault() // no text selection while dragging
  e.stopPropagation()
  const startX = e.clientX
  document.body.classList.add('resizing') // keeps the resize cursor off the handle
  const move = (ev: PointerEvent) => onMove(ev.clientX - startX)
  const end = () => {
    document.removeEventListener('pointermove', move)
    document.removeEventListener('pointerup', end)
    document.removeEventListener('pointercancel', end)
    document.body.classList.remove('resizing')
    onEnd?.()
  }
  document.addEventListener('pointermove', move)
  document.addEventListener('pointerup', end)
  document.addEventListener('pointercancel', end)
}

const SIDEBAR_DEFAULT = 264
const SIDEBAR_MIN = 240 // below this the database picker and filter box get cramped
const SIDEBAR_MAX = 640
const SIDEBAR_KEY = 'spydb.sidebarWidth'

const clampSidebar = (width: number) => Math.round(Math.min(Math.max(width, SIDEBAR_MIN), SIDEBAR_MAX))

// The sidebar width, kept across launches. Both sidebars (tables, saved
// connections) share it, so it doesn't jump on connect / disconnect.
export function useSidebarWidth(): [number, (width: number) => void] {
  const [width, setWidth] = useState(() => {
    try {
      const stored = Number(localStorage.getItem(SIDEBAR_KEY))
      return stored ? clampSidebar(stored) : SIDEBAR_DEFAULT
    } catch {
      return SIDEBAR_DEFAULT
    }
  })
  const update = (next: number) => {
    const clamped = clampSidebar(next)
    setWidth(clamped)
    try {
      localStorage.setItem(SIDEBAR_KEY, String(clamped))
    } catch {
      // Storage unavailable: the width just isn't remembered.
    }
  }
  return [width, update]
}

// The handle on a sidebar's right edge: drag to resize, double-click to reset,
// or focus it and use ← / →.
export function SidebarResizer({ width, onResize }: { width: number; onResize: (width: number) => void }) {
  const [dragging, setDragging] = useState(false)

  const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    const start = width
    setDragging(true)
    dragX(e, (dx) => onResize(start + dx), () => setDragging(false))
  }

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault()
      onResize(width + (e.key === 'ArrowRight' ? 16 : -16))
    }
  }

  return (
    <div
      className={dragging ? 'sidebar-resizer dragging' : 'sidebar-resizer'}
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize sidebar"
      aria-valuenow={width}
      aria-valuemin={SIDEBAR_MIN}
      aria-valuemax={SIDEBAR_MAX}
      tabIndex={0}
      title="Drag to resize · double-click to reset"
      onPointerDown={onPointerDown}
      onDoubleClick={() => onResize(SIDEBAR_DEFAULT)}
      onKeyDown={onKeyDown}
    />
  )
}
