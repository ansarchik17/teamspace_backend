import { spawn, spawnSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { createServer } from 'node:net'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'

// This launcher is versioned with the backend; the two checkouts live in axis_workspace.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const projects = {
  axis: {
    name: 'TeamSpace', service: 'workspace-api', port: 5174,
    front: 'axis_workspace/workspace-front', url: 'http://localhost:5174/web/',
    health: 'http://localhost:18081/health', statuses: [200],
    env: { VITE_API_PROXY_TARGET: 'http://localhost:18081' },
  },
  senim: {
    name: 'SenimDesk', service: 'sd-backend', port: 5175,
    front: 'senim_desk/workspace-sd-front', url: 'http://localhost:5175/',
    health: 'http://localhost:18080/', statuses: [200, 401], env: {},
  },
}
const project = projects[process.argv[2]]
const options = process.argv.slice(3)
const compose = ['compose', '-f', resolve(root, 'docker-compose.local.yml')]
const infrastructure = [
  'workspace-local-db', 'workspace-local-redis', 'workspace-local-minio',
  'senimdesk-local-db', 'senimdesk-local-minio', 'workspace-local-keycloak',
]
let child
let stopping = false

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => {
    stopping = true
    child?.kill(signal)
    // Infrastructure is shared by both projects; never stop it on Ctrl+C.
    if (!child) process.exit(0)
  })
}

function capture(command, args) {
  const result = spawnSync(command, args, { cwd: root, encoding: 'utf8' })
  if (result.error || result.status !== 0) {
    throw new Error(result.error?.message || result.stderr.trim() || `${command} failed`)
  }
  return result.stdout.trim()
}

async function run(command, args, extra = {}) {
  if (stopping) return
  await new Promise((resolvePromise, reject) => {
    const proc = spawn(command, args, { cwd: root, stdio: 'inherit', ...extra })
    child = proc
    proc.on('error', reject)
    proc.on('exit', (code, signal) => {
      child = undefined
      if (stopping || code === 0) resolvePromise()
      else reject(new Error(`${command} завершился: ${signal || code}`))
    })
  })
}

async function checkPort(port) {
  await new Promise((resolvePromise, reject) => {
    const server = createServer()
    server.once('error', () => reject(new Error(`Порт ${port} занят или недоступен. Остановите прежний npm run dev и повторите запуск.`)))
    server.listen(port, () => server.close(resolvePromise))
  })
}

async function waitForBackend() {
  const deadline = Date.now() + 120_000
  while (!stopping && Date.now() < deadline) {
    try {
      const response = await fetch(project.health, { signal: AbortSignal.timeout(3000) })
      await response.body?.cancel()
      if (project.statuses.includes(response.status)) return
    } catch { /* The container may still be starting or applying migrations. */ }
    await delay(1000)
  }
  if (!stopping) throw new Error('Бэкенд не готов через 120 секунд. Посмотрите npm run logs.')
}

async function main() {
  if (!project || options.some(option => !['--build', '--logs', '--stop'].includes(option)) || options.length > 1) {
    throw new Error('Использование: local-dev.mjs axis|senim [--build|--logs|--stop]')
  }
  try { capture('docker', ['info', '--format', '{{.ServerVersion}}']) }
  catch { throw new Error('Docker недоступен. Откройте Docker Desktop, дождитесь запуска и повторите npm run dev.') }

  if (options.includes('--logs')) {
    await run('docker', [...compose, 'logs', '--tail', '100', '-f', project.service])
    return
  }
  if (options.includes('--stop')) {
    await run('docker', [...compose, 'stop', project.service])
    console.log('Бэкенд остановлен. Фронтенд остановите через Ctrl+C в его терминале. Общая инфраструктура сохранена.')
    return
  }

  if (!existsSync(resolve(root, 'axis_workspace/workspace-backend/.env'))) {
    throw new Error('Нет axis_workspace/workspace-backend/.env — нужен локальный конфиг бэкенда для Compose.')
  }
  if (!existsSync(resolve(root, project.front, 'node_modules/.bin/vite'))) {
    console.log(`\nУстанавливаю зависимости ${project.name} после первого клонирования (npm ci)…`)
    await run('npm', ['ci'], { cwd: resolve(root, project.front) })
    if (stopping) return
    if (!existsSync(resolve(root, project.front, 'node_modules/.bin/vite'))) {
      throw new Error(`Vite не установлен. Проверьте зависимости в ${project.front}.`)
    }
  }
  await checkPort(project.port)

  // Check all containers before changing anything; never substitute fresh databases.
  const states = infrastructure.map(name => {
    try { return [name, capture('docker', ['inspect', '--format', '{{.State.Running}}', name])] }
    catch { throw new Error(`Нет контейнера ${name}. Восстановите локальную инфраструктуру по ЗАПУСК.md; новые базы автоматически не создаются.`) }
  })
  const networks = capture('docker', ['network', 'ls', '--format', '{{.Name}}']).split('\n')
  if (!networks.includes('belesai-local')) await run('docker', ['network', 'create', 'belesai-local'])
  for (const [name, running] of states) {
    if (stopping) return
    const attached = JSON.parse(capture('docker', ['inspect', '--format', '{{json .NetworkSettings.Networks}}', name]))
    if (!attached['belesai-local']) await run('docker', ['network', 'connect', 'belesai-local', name])
    if (running !== 'true') await run('docker', ['start', name])
  }

  console.log(`\nЗапускаю ${project.name}: ${options.includes('--build') ? 'с пересборкой бэкенда' : 'из существующего Docker-образа'}…`)
  await run('docker', [...compose, 'up', '-d', '--no-deps', options.includes('--build') ? '--build' : '--no-build', project.service])
  if (stopping) return
  await waitForBackend()
  if (stopping) return
  console.log(`\n${project.name}: ${project.url}\nCtrl+C остановит фронтенд. Бэкенд и общие сервисы останутся в Docker.\n`)
  await run('npm', ['run', 'dev', '--', '--port', String(project.port), '--strictPort'], {
    cwd: resolve(root, project.front), env: { ...process.env, ...project.env },
  })
}

main().catch(error => {
  console.error(`\nНе удалось запустить: ${error.message}`)
  process.exitCode = 1
})
