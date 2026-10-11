/**
 * After Forgeyard is updated, a page left open still asks for the code it loads on demand (the terminal)
 * under its old file names, which are gone: the page would go blank. It reloads instead, to get the new
 * ones, unless it just did (then the error shows).
 */
export function reloadOnStaleCode() {
  window.addEventListener('vite:preloadError', (event) => {
    try {
      if (Date.now() - Number(sessionStorage.getItem('forgeyard.staleReload') ?? 0) < 10_000) return
      sessionStorage.setItem('forgeyard.staleReload', String(Date.now()))
    } catch {
      // storage refused: reload anyway
    }
    event.preventDefault()
    window.location.reload()
  })
}
