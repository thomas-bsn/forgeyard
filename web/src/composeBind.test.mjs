import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parse } from 'yaml'
import { addBindToCompose, changeComposeBind, composeBinds } from './composeBind.ts'

test('inline volumes: add RW, switch RO and remove without changing named volumes or environment', () => {
  const original = 'services:\n  app:\n    image: example\n    environment:\n      KEY: ${KEY:-}\n    volumes: [media:/data/media, "secrets:/data/secrets"]\nvolumes:\n  media:\n  secrets:\n'
  const added = addBindToCompose(original, 'app', '/volume1/Vidéos', '/videos', false)
  assert.ok(added)
  assert.equal(composeBinds(added)[0].readOnly, false)
  assert.equal(parse(added).services.app.environment.KEY, '${KEY:-}')
  const ro = changeComposeBind(added, composeBinds(added)[0], true)
  assert.equal(composeBinds(ro)[0].readOnly, true)
  const removed = changeComposeBind(ro, composeBinds(ro)[0], null)
  assert.deepEqual(parse(removed), parse(original))
  assert.equal(addBindToCompose(added, 'app', '/other', '/videos', false), null)
})

test('short syntax retains extra mount options and other services', () => {
  const original = 'services:\n  app:\n    volumes:\n      - /srv/media:/media:ro,z\n  db:\n    volumes: [db:/data]\n'
  const updated = changeComposeBind(original, composeBinds(original)[0], false)
  assert.equal(parse(updated).services.app.volumes[0], '/srv/media:/media:rw,z')
  assert.deepEqual(parse(updated).services.db, parse(original).services.db)
  assert.equal(addBindToCompose('services: [broken', 'app', '/a', '/b', false), null)
})
