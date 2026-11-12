import { spawnSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { loadEnvFile } from 'node:process'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const credentialsPath = resolve(root, '.cache/dev-admin.json')
const email = 'admin@admin.com'
// The older migration already reserves username "admin" for admin@admin.kz.
const username = 'admin.platform'
const compose = ['compose', '--project-name', 'teamspace-dev', '-f', resolve(root, 'compose.dev.yml')]
let password = ''
const literal = value => "'" + value.replaceAll("'", "''") + "'"

function docker(args, input) {
  const result = spawnSync('docker', [...compose, 'exec', '-T', 'postgres', ...args], {
    cwd: root, encoding: 'utf8', timeout: 30_000, input,
  })
  if (result.error || result.status !== 0) {
    throw new Error(result.error?.message || result.stderr.trim() || 'PostgreSQL недоступен. Сначала запустите npm run dev.')
  }
  return result.stdout.trim()
}

function main() {
  if (process.argv.length !== 2) throw new Error('Использование: npm run seed:admin')
  if (!existsSync(resolve(root, '.env'))) throw new Error('Нет .env. Сначала запустите npm run dev.')
  loadEnvFile(resolve(root, '.env'))
  if (process.env.ENV !== 'development' || !['localhost', '127.0.0.1'].includes(process.env.POSTGRES_HOST)) {
    throw new Error('Создание тестового администратора разрешено только для локального ENV=development.')
  }
  if (!existsSync(credentialsPath)) {
    throw new Error('Нет .cache/dev-admin.json с полями email, username и password для локального администратора.')
  }
  const credentials = JSON.parse(readFileSync(credentialsPath, 'utf8'))
  if (credentials.email !== email || credentials.username !== username) {
    throw new Error('Данные в .cache/dev-admin.json должны относиться к admin@admin.com / admin.platform.')
  }
  password = credentials.password
  if (typeof password !== 'string' || password.length < 8 || Buffer.byteLength(password) > 72) {
    throw new Error('Локальный пароль должен содержать от 8 символов до 72 байт.')
  }
  docker(['sh', '-c', 'exec pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"'])

  const sql = `
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '20s';
SELECT pg_advisory_xact_lock(hashtextextended('teamspace.dev.admin',0));
CREATE TEMP TABLE dev_admin_seed ON COMMIT DROP AS
 SELECT ${literal(email)}::text email,${literal(username)}::text username,${literal(password)}::text password;
DO $seed$
DECLARE
 seed record;
 admin_user users%ROWTYPE;
 admin_id bigint;
BEGIN
 SELECT * INTO seed FROM dev_admin_seed;
 IF (SELECT count(*) FROM users WHERE lower(email)=seed.email OR lower(username)=seed.username)>1 THEN
  RAISE EXCEPTION 'Admin identity collides with multiple existing users; no accounts were changed';
 END IF;
 SELECT * INTO admin_user FROM users
 WHERE lower(email)=seed.email OR lower(username)=seed.username FOR UPDATE;
 IF FOUND THEN
  IF admin_user.email IS DISTINCT FROM seed.email OR admin_user.username IS DISTINCT FROM seed.username
    OR admin_user.role IS DISTINCT FROM 'admin' OR admin_user.is_active IS DISTINCT FROM true
    OR admin_user.deleted_at IS NOT NULL OR COALESCE(admin_user.keycloak_id,'')<>''
    OR admin_user.organization_id IS NOT NULL OR admin_user.password_hash IS NULL
    OR crypt(seed.password,admin_user.password_hash) IS DISTINCT FROM admin_user.password_hash THEN
   RAISE EXCEPTION 'Admin identity is occupied or its credentials differ; no accounts were changed';
  END IF;
 ELSE
  INSERT INTO users(email,username,password_hash,first_name,last_name,full_name_local,
                    position,role,is_active,is_first_login)
   VALUES(seed.email,seed.username,crypt(seed.password,gen_salt('bf',12)),
          'Администратор','Платформы','Администратор платформы','Администратор платформы','admin',true,false)
   RETURNING id INTO admin_id;
  INSERT INTO security_audit_events(actor_id,action,outcome,object_type,object_id)
   VALUES(admin_id,'development.admin.seed','success','user',admin_id::text);
 END IF;
END;
$seed$;
SELECT json_build_object('user_id',u.id,'email',u.email,'role',u.role,
                        'is_platform_admin',u.role='admin' AND u.organization_id IS NULL,
                        'can_login',workspace_user_can_login(u.id),
                        'password_ok',crypt(s.password,u.password_hash)=u.password_hash)
 FROM users u JOIN dev_admin_seed s ON u.username=s.username AND u.email=s.email;
COMMIT;
`
  const output = docker([
    'sh', '-c', 'exec psql -X -q -t -A -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"',
  ], sql)
  const result = JSON.parse(output.split('\n').filter(line => line.trim()).at(-1))
  if (!result.is_platform_admin || !result.can_login || !result.password_ok) {
    throw new Error('Проверка роли или пароля администратора не пройдена.')
  }
  console.log(`Администратор платформы создан или проверен (user_id=${result.user_id}, role=${result.role}).`)
  console.log(`Email для входа: ${email}`)
  console.log('Пароль: задан из .cache/dev-admin.json')
  console.log('Вход: http://localhost:5173/web/login')
  console.log('Создание компаний: http://localhost:5173/web/platform/administration')
}

try { main() }
catch (error) {
  const message = error instanceof Error ? error.message : String(error)
  console.error('Не удалось создать администратора: ' + (password ? message.replaceAll(password, '[redacted]') : message))
  process.exitCode = 1
}
