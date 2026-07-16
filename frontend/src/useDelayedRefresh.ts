import { useCallback, useEffect, useRef } from 'react'

export function useDelayedRefresh(refresh: () => void | Promise<void>, delayMs = 2500) {
  const timer = useRef<number | null>(null)
  const latest = useRef(refresh)
  latest.current = refresh

  useEffect(() => () => {
    if (timer.current != null) window.clearTimeout(timer.current)
  }, [])

  return useCallback(async () => {
    await latest.current()
    if (timer.current != null) window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => {
      timer.current = null
      void latest.current()
    }, delayMs)
  }, [delayMs])
}
