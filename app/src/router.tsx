// A minimal history-API router.
//
// Waxgrove has five routes and one dynamic segment. A routing library would be
// more surface area than the thing it routes, so this is hand-rolled: a
// subscription to popstate, a navigate() that pushes, and a <Link> that does
// not reload the page.

import { createContext, useCallback, useContext, useEffect, useMemo, useSyncExternalStore } from 'react'
import type { ReactNode, MouseEvent } from 'react'

const listeners = new Set<() => void>()

function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

function notify() { listeners.forEach((fn) => fn()) }

function currentPath(): string {
  return window.location.pathname + window.location.search
}

export function navigate(to: string, opts: { replace?: boolean } = {}): void {
  if (to === currentPath()) return
  if (opts.replace) window.history.replaceState(null, '', to)
  else window.history.pushState(null, '', to)
  notify()
}

/** The current path, re-rendering any component that reads it. */
export function usePath(): string {
  return useSyncExternalStore(subscribe, currentPath, () => '/')
}

export function useRouter() {
  const path = usePath()
  useEffect(() => {
    const onPop = () => notify()
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  return useMemo(() => ({ path: path.split('?')[0] ?? '/', full: path, navigate }), [path])
}

export function Link(
  { to, children, className, ...rest }:
  { to: string; children: ReactNode; className?: string } & Record<string, unknown>,
) {
  const onClick = useCallback((e: MouseEvent<HTMLAnchorElement>) => {
    // Let the browser handle modified clicks so "open in new tab" still works.
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return
    e.preventDefault()
    navigate(to)
  }, [to])
  return <a href={to} onClick={onClick} className={className} {...rest}>{children}</a>
}

// --- route matching ----------------------------------------------------------

/**
 * Matches a pattern with `:param` segments against a path.
 * Returns the captured params, or null when it does not match.
 */
export function match(pattern: string, path: string): Record<string, string> | null {
  const pp = pattern.split('/').filter(Boolean)
  const ap = path.split('/').filter(Boolean)
  if (pp.length !== ap.length) return null
  const out: Record<string, string> = {}
  for (let i = 0; i < pp.length; i++) {
    const p = pp[i]!, a = ap[i]!
    if (p.startsWith(':')) out[p.slice(1)] = decodeURIComponent(a)
    else if (p !== a) return null
  }
  return out
}

// --- toast -------------------------------------------------------------------
// Lives here because every route needs it and nothing else does.

export interface Toast { message: string; bad?: boolean }
export const ToastContext = createContext<(t: Toast) => void>(() => {})
export const useToast = () => useContext(ToastContext)
