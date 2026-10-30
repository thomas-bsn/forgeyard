import { isMap, isScalar, isSeq, parseDocument } from 'yaml'

function document(compose: string) {
  const doc = parseDocument(compose)
  if (doc.errors.length) throw new Error('Corrigez la syntaxe du Compose avant de modifier les montages.')
  return doc
}

export function composeServices(compose: string): string[] {
  try {
    const services = document(compose).get('services', true)
    return isMap(services) ? services.items.map(({ key }) => String(key)) : []
  } catch { return [] }
}

export type ComposeBind = { service: string; index: number; source: string; target: string; readOnly: boolean }

export function composeBinds(compose: string): ComposeBind[] {
  const doc = document(compose)
  const services = doc.get('services', true)
  if (!isMap(services)) return []
  const result: ComposeBind[] = []
  for (const { key, value } of services.items) {
    if (!isMap(value)) continue
    const volumes = value.get('volumes', true)
    if (!isSeq(volumes)) continue
    volumes.items.forEach((item, index) => {
      if (isScalar(item) && typeof item.value === 'string') {
        const [source, target, options = ''] = item.value.split(':')
        if (target && /^(\/|\.|~|\$)/.test(source)) result.push({ service: String(key), index, source, target, readOnly: options.split(',').includes('ro') })
      } else if (isMap(item) && item.get('type') === 'bind') {
        result.push({ service: String(key), index, source: String(item.get('source') ?? ''), target: String(item.get('target') ?? ''), readOnly: item.get('read_only') === true })
      }
    })
  }
  return result
}

export function addBindToCompose(compose: string, service: string, source: string, target: string, readOnly: boolean): string | null {
  try {
    const doc = document(compose)
    const entry = doc.getIn(['services', service], true)
    if (!isMap(entry)) return null
    let volumes = entry.get('volumes', true)
    if (volumes === undefined) { entry.set('volumes', doc.createNode([])); volumes = entry.get('volumes', true) }
    if (!isSeq(volumes)) return null
    if (volumes.items.some(item => isScalar(item) && String(item.value).split(':')[1] === target || isMap(item) && item.get('target') === target)) return null
    volumes.add(doc.createNode({ type: 'bind', source, target, read_only: readOnly }))
    volumes.flow = false
    return doc.toString()
  } catch { return null }
}

/** Edit only the selected mount; keep named volumes and other mount options. */
export function changeComposeBind(compose: string, bind: ComposeBind, readOnly: boolean | null): string {
  const doc = document(compose)
  const volumes = doc.getIn(['services', bind.service, 'volumes'], true)
  if (!isSeq(volumes)) throw new Error('Montage introuvable.')
  const item = volumes.items[bind.index]
  if (readOnly === null) volumes.items.splice(bind.index, 1)
  else if (isMap(item)) item.set('read_only', readOnly)
  else if (isScalar(item) && typeof item.value === 'string') {
    const [source, target, options = ''] = item.value.split(':')
    const kept = options.split(',').filter(option => option && option !== 'ro' && option !== 'rw')
    item.value = `${source}:${target}:${[readOnly ? 'ro' : 'rw', ...kept].join(',')}`
  } else throw new Error('Ce montage doit être modifié dans le Compose.')
  return doc.toString()
}
