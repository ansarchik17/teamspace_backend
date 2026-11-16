import { spawn } from 'node:child_process'
import { createServer } from 'node:net'
import { loadEnvFile } from 'node:process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// Reuse the local database and Keycloak without rewriting development .env.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const port = 8080
let child
let stopping = false
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    stopping = true
    child?.kill(signal)
  })
}

async function main() {
  if (process.platform !== 'darwin') throw new Error('Эта команда предназначена для Mac.')
  if (process.argv.length !== 2) throw new Error('Использование: npm run host:mac')
  loadEnvFile(resolve(root, '.env'))
  if (process.env.ENV !== 'development') throw new Error('Ожидается локальный .env с ENV=development.')

  // On macOS an IPv4-specific bind can coexist with an older IPv6 wildcard
  // listener. Check both so we cannot start two APIs against the same data.
  for (const host of ['127.0.0.1', '::']) {
    await new Promise((resolvePromise, reject) => {
      const probe = createServer()
      probe.once('error', () => reject(new Error('Порт 8080 занят. Остановите текущий npm run dev через Ctrl+C, затем повторите npm run host:mac.')))
      probe.listen(port, host, () => probe.close(resolvePromise))
    })
  }
  if (stopping) return
  console.log('API для https://unitespace.kz; внутренний адрес http://127.0.0.1:8080.')
  console.log('Локальный .env и данные сохранены. Docker Desktop и локальные сервисы должны работать.')
  const bucket = process.env.MINIO_BUCKET || 'workspace'
  if (bucket !== 'workspace') {
    throw new Error('Этот профиль ожидает MINIO_BUCKET=workspace. Согласуйте имя bucket с frontend deploy/mac/routes.caddy.')
  }
  child = spawn(process.execPath, [resolve(root, 'scripts/dev.mjs'), '--api-only'], {
    cwd: root,
    stdio: 'inherit',
    env: {
      ...process.env,
      HTTP_HOST: '127.0.0.1',
      HTTP_PORT: String(port),
      APP_PUBLIC_URL: 'https://unitespace.kz',
      TENANT_BASE_DOMAIN: 'unitespace.kz',
      MINIO_PUBLIC_BASE_URL: 'https://unitespace.kz/workspace',
      CORS_ALLOWED_ORIGINS: 'https://unitespace.kz,http://localhost:5173,http://127.0.0.1:5173',
    },
  })
  await new Promise((resolvePromise, reject) => {
    child.once('error', reject)
    child.once('exit', (code, signal) => {
      child = undefined
      if (stopping || code === 0) resolvePromise()
      else reject(new Error(`API завершился: ${signal || code}`))
    })
  })
}

main().catch(error => {
  if (!stopping) {
    console.error(error.message)
    process.exitCode = 1
  }
})
