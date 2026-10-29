const indentOf = (line: string) => line.length - line.trimStart().length
const blank = (line: string) => line.trim() === '' || line.trim().startsWith('#')

/** The service names of a Compose file, in order. Empty when the file has no readable `services:` block. */
export function composeServices(compose: string): string[] {
  const lines = compose.split('\n')
  const start = lines.findIndex((l) => /^services:\s*(#.*)?$/.test(l))
  if (start < 0) return []
  const names: string[] = []
  let level = -1
  for (const line of lines.slice(start + 1)) {
    if (blank(line)) continue
    const indent = indentOf(line)
    if (indent === 0) break
    if (level < 0) level = indent
    if (indent !== level) continue
    const m = line.trim().match(/^["']?([A-Za-z0-9][A-Za-z0-9._-]*)["']?:\s*(#.*)?$/)
    if (m) names.push(m[1])
  }
  return names
}

/**
 * Adds a host folder to a service's `volumes:` and returns the new file, or null when the file's layout is
 * not one this can edit safely (a one-line `volumes: [...]`, or a service it cannot find): the caller then
 * asks the person to write the line themselves.
 */
export function addBindToCompose(compose: string, service: string, source: string, target: string, readOnly: boolean): string | null {
  const lines = compose.split('\n')
  const start = lines.findIndex((l) => /^services:\s*(#.*)?$/.test(l))
  if (start < 0) return null
  let level = -1
  let at = -1
  for (let i = start + 1; i < lines.length; i++) {
    if (blank(lines[i])) continue
    const indent = indentOf(lines[i])
    if (indent === 0) break
    if (level < 0) level = indent
    if (indent === level && new RegExp(`^["']?${service.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}["']?:\\s*(#.*)?$`).test(lines[i].trim())) {
      at = i
      break
    }
  }
  if (at < 0) return null
  let end = at + 1
  let child = -1
  for (; end < lines.length; end++) {
    if (blank(lines[end])) continue
    const indent = indentOf(lines[end])
    if (indent <= level) break
    if (child < 0) child = indent
  }
  while (end > at + 1 && blank(lines[end - 1])) end--
  if (child < 0) child = level + 2

  const spec = `${source}:${target}${readOnly ? ':ro' : ''}`
  const item = /[\s#"']/.test(spec) ? `"${spec}"` : spec
  const key = lines.findIndex((l, i) => i > at && i < end && indentOf(l) === child && /^volumes:/.test(l.trim()))
  if (key < 0) {
    lines.splice(end, 0, `${' '.repeat(child)}volumes:`, `${' '.repeat(child + 2)}- ${item}`)
    return lines.join('\n')
  }
  if (!/^volumes:\s*(#.*)?$/.test(lines[key].trim())) return null
  let last = key
  let itemIndent = child + 2
  for (let i = key + 1; i < end; i++) {
    if (blank(lines[i])) continue
    if (indentOf(lines[i]) <= child && !lines[i].trim().startsWith('-')) break
    if (!lines[i].trim().startsWith('-')) continue
    if (last === key) itemIndent = indentOf(lines[i])
    last = i
  }
  lines.splice(last + 1, 0, `${' '.repeat(itemIndent)}- ${item}`)
  return lines.join('\n')
}
