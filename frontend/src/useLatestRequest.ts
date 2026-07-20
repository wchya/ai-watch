import { useCallback, useEffect, useRef } from 'react'

export function useLatestRequest() {
  const currentVersion = useRef(0)

  useEffect(() => () => {
    currentVersion.current += 1
  }, [])

  const beginRequest = useCallback(() => {
    currentVersion.current += 1
    return currentVersion.current
  }, [])

  const isLatestRequest = useCallback((version: number) => version === currentVersion.current, [])

  return { beginRequest, isLatestRequest }
}
