import { spawn, spawnSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { loadEnvFile } from 'node:process'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const compose = ['compose', '--project-name', 'teamspace-dev', '-f', resolve(root, 'compose.dev.yml')]
const option = process.argv[2]
let child
let stopping = false

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    stopping = true
    if (child) child.kill(signal)
    else process.exit(0)
  })
}

function capture(command, args) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8', timeout: 30_000 })
  if (result.error || result.status !== 0) {
    throw new Error(result.error?.message || result.stderr.trim() || `${command} завершился с ошибкой`)
  }
  return result.stdout.trim()
}

function run(command, args) {
  return new Promise((resolvePromise, reject) => {
    child = spawn(command, args, { cwd: root, stdio: 'inherit' })
    child.once('error', reject)
    child.once('exit', (code, signal) => {
      child = undefined
      if (stopping || code === 0) resolvePromise()
      else reject(new Error(`${command} завершился: ${signal || code}`))
    })
  })
}

function prepareEnvironment() {
  const envPath = resolve(root, '.env')
  if (!existsSync(envPath)) {
    const template = readFileSync(resolve(root, '.env.development.example'), 'utf8')
    const content = template.replaceAll('GENERATE_ON_FIRST_RUN', () => randomBytes(32).toString('hex'))
    writeFileSync(envPath, content, { flag: 'wx', mode: 0o600 })
    console.log('Создан локальный .env со случайными JWT_SECRET и PIN_CRYPTO_KEY.')
  }
  loadEnvFile(envPath) // Existing shell variables take precedence.
}

function validateLocalInfrastructure() {
  const loopback = new Set(['localhost', '127.0.0.1'])
  if (process.env.ENV !== 'development') {
    throw new Error('npm run dev предназначен для ENV=development. Для своего окружения используйте npm run dev:api.')
  }
  for (const key of ['POSTGRES_HOST', 'REDIS_HOST']) {
    if (!loopback.has(process.env[key])) {
      throw new Error(`${key} должен быть localhost или 127.0.0.1 для Compose. Для своих сервисов используйте npm run dev:api.`)
    }
  }
  for (const key of ['POSTGRES_PORT', 'REDIS_PORT', 'HTTP_PORT']) {
    const value = Number(process.env[key])
    if (!/^\d+$/.test(process.env[key] || '') || value < 1 || value > 65535) {
      throw new Error(`${key}: задайте порт от 1 до 65535 в .env.`)
    }
  }
  const minio = new URL(`http://${process.env.MINIO_ENDPOINT}`)
  if (!loopback.has(minio.hostname) || minio.pathname !== '/' || process.env.MINIO_USE_SSL !== 'false') {
    throw new Error('Compose требует локальный MINIO_ENDPOINT=127.0.0.1:19000 и MINIO_USE_SSL=false. Для своих сервисов используйте npm run dev:api.')
  }
  if (process.env.REDIS_PASSWORD) {
    throw new Error('Локальный Redis в compose.dev.yml работает без пароля. Для своего Redis используйте npm run dev:api.')
  }
  const keycloak = new URL(process.env.KEYCLOAK_URL || `http://127.0.0.1:${process.env.KEYCLOAK_HTTP_PORT || '18082'}`)
  const keycloakPort = process.env.KEYCLOAK_HTTP_PORT || keycloak.port || '80'
  if (!/^\d+$/.test(keycloakPort) || Number(keycloakPort) < 1 || Number(keycloakPort) > 65535
    || (keycloak.port || '80') !== keycloakPort || keycloakPort === process.env.HTTP_PORT) {
    throw new Error('KEYCLOAK_HTTP_PORT должен совпадать с портом KEYCLOAK_URL и отличаться от HTTP_PORT API.')
  }
  const publicKeycloak = new URL(process.env.KEYCLOAK_PUBLIC_URL || `http://localhost:${keycloakPort}`)
  if (keycloak.protocol !== 'http:' || !loopback.has(keycloak.hostname)
    || publicKeycloak.protocol !== 'http:' || !loopback.has(publicKeycloak.hostname)
    || (publicKeycloak.port || '80') !== keycloakPort || !['', '/'].includes(publicKeycloak.pathname)
    || !['', '/'].includes(keycloak.pathname) || process.env.KEYCLOAK_REALM !== 'Workspace'
    || process.env.KEYCLOAK_CLIENT_ID !== 'workspace-client') {
    throw new Error('Локальный Compose использует Keycloak на localhost, realm Workspace, client workspace-client. Для своего Keycloak используйте npm run dev:api.')
  }
  const issuer = `${publicKeycloak.origin}/realms/Workspace`
  const jwks = `${keycloak.origin}/realms/Workspace/protocol/openid-connect/certs`
  for (const [key, expected] of [['KEYCLOAK_REALM_URL', issuer], ['KEYCLOAK_ISSUER', issuer], ['KEYCLOAK_JWKS_URL', jwks]]) {
    if (process.env[key] && process.env[key].replace(/\/+$/, '') !== expected) {
      throw new Error(`${key} не согласован с адресом Keycloak. Ожидается: ${expected}`)
    }
  }
  Object.assign(process.env, {
    KEYCLOAK_HTTP_PORT: keycloakPort,
    KEYCLOAK_URL: keycloak.origin,
    KEYCLOAK_PUBLIC_URL: publicKeycloak.origin,
    KEYCLOAK_REALM_URL: issuer,
    KEYCLOAK_ISSUER: issuer,
    KEYCLOAK_JWKS_URL: jwks,
  })
  process.env.TEAMSPACE_MINIO_PORT = minio.port || '80'
}

async function main() {
  if (process.argv.length > 3 || (option && !['--api-only', '--infra-only', '--auth-only', '--stop', '--logs'].includes(option))) {
    throw new Error('Использование: npm run dev или npm run dev:api|dev:infra|dev:auth|dev:stop|dev:logs')
  }
  prepareEnvironment()
  if (option !== '--api-only') {
    validateLocalInfrastructure()
    try {
      if (!capture('docker', ['info', '--format', '{{.ServerVersion}}'])) throw new Error('Docker Engine is not running')
    }
    catch {
      throw new Error('Docker Engine недоступен. Запустите Docker Desktop и повторите npm run dev. Если сервисы уже установлены отдельно, задайте .env и выполните npm run dev:api.')
    }
    if (option === '--stop') {
      await run('docker', [...compose, 'stop'])
      return
    }
    if (option === '--logs') {
      await run('docker', [...compose, 'logs', '--tail', '100', '-f'])
      return
    }
    console.log('Подготавливаю локальные сервисы, включая Keycloak для регистрации. Первый запуск может занять несколько минут…')
    const services = option === '--auth-only' ? ['keycloak'] : []
    console.log(`Keycloak: ${process.env.KEYCLOAK_PUBLIC_URL}`)
    await run('docker', [...compose, 'up', '-d', '--wait', '--wait-timeout', '120', ...services])
    if (stopping || option === '--infra-only' || option === '--auth-only') return
  }

  console.log('Собираю Go API (версия Go выбирается по go.mod)…')
  mkdirSync(resolve(root, 'bin'), { recursive: true })
  const executable = resolve(root, 'bin', process.platform === 'win32' ? 'api.exe' : 'api')
  await run('go', ['build', '-o', executable, './cmd/api'])
  if (stopping) return
  console.log(`Адрес API после завершения старта: http://localhost:${process.env.HTTP_PORT || '8080'} — /health, /ready, /swagger/index.html`)
  console.log('Миграции при старте включены по умолчанию; RUN_DB_MIGRATIONS=false отключает их. Ctrl+C останавливает API; данные Compose сохраняются.')
  console.log('После изменения Go-кода перезапустите npm run dev.')
  await run(executable, [])
}

main().catch(error => {
  if (!stopping) {
    console.error(`Не удалось запустить: ${error.message}`)
    process.exitCode = 1
  }
})
