import { spawnSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { loadEnvFile } from 'node:process'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const credentialsPath = resolve(root, '.cache/dev-director.json')
const identity = {
  company: 'TeamSpace Demo',
  slug: 'teamspace-demo',
  username: 'director.demo',
  email: 'director@teamspace.test',
}
const compose = ['compose', '--project-name', 'teamspace-dev', '-f', resolve(root, 'compose.dev.yml')]
let password = ''

function docker(args, input) {
  const result = spawnSync('docker', [...compose, 'exec', '-T', 'postgres', ...args], {
    cwd: root, encoding: 'utf8', timeout: 30_000, input,
  })
  if (result.error || result.status !== 0) {
    const details = result.error?.message || result.stderr.trim()
    throw new Error(details || 'PostgreSQL недоступен. Сначала запустите npm run dev и дождитесь готовности API.')
  }
  return result.stdout.trim()
}

// Values are sent through stdin, never through process arguments or a SQL file.
const literal = value => "'" + value.replaceAll("'", "''") + "'"

function main() {
  if (process.argv.length !== 2) throw new Error('Использование: npm run seed:director')
  if (!existsSync(resolve(root, '.env'))) throw new Error('Нет .env. Сначала запустите npm run dev.')
  loadEnvFile(resolve(root, '.env'))
  if (process.env.ENV !== 'development' || !['127.0.0.1', 'localhost'].includes(process.env.POSTGRES_HOST)) {
    throw new Error('Создание демо-директора разрешено только для локального ENV=development.')
  }

  // This command targets only the postgres service of our dedicated dev project.
  docker(['sh', '-c', 'exec pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"'])
  let credentials
  if (existsSync(credentialsPath)) {
    credentials = JSON.parse(readFileSync(credentialsPath, 'utf8'))
    if (Object.entries(identity).some(([key, value]) => credentials[key] !== value)) {
      throw new Error('Сохранённые данные демо-директора относятся к другому аккаунту.')
    }
    password = credentials.password
    if (typeof password !== 'string' || password.length < 16 || Buffer.byteLength(password) > 72) {
      throw new Error('Сохранённый пароль демо-директора имеет неверный формат.')
    }
  } else {
    password = randomBytes(18).toString('base64url') + '!aA1'
    credentials = { ...identity, password }
    mkdirSync(dirname(credentialsPath), { recursive: true, mode: 0o700 })
    // Persist before committing SQL so an interrupted invocation can be retried.
    writeFileSync(credentialsPath, JSON.stringify(credentials, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
  }

  const sql = `
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '20s';
SELECT pg_advisory_xact_lock(hashtextextended('teamspace.dev.director', 0));
CREATE TEMP TABLE dev_director_seed ON COMMIT DROP AS
 SELECT ${literal(identity.company)}::text company, ${literal(identity.slug)}::text slug,
        ${literal(identity.username)}::text username, ${literal(identity.email)}::text email,
        ${literal(password)}::text password;
DO $seed$
DECLARE
 seed record;
 demo_user users%ROWTYPE;
 demo_company organizations%ROWTYPE;
 company_exists boolean;
 user_exists boolean;
 company_id uuid;
 director_id bigint;
BEGIN
 SELECT * INTO seed FROM dev_director_seed;
 SELECT * INTO demo_company FROM organizations WHERE slug=seed.slug FOR UPDATE;
 company_exists := FOUND;
 IF (SELECT count(*) FROM users WHERE lower(username)=seed.username OR lower(email)=seed.email) > 1 THEN
  RAISE EXCEPTION 'Demo identity collides with multiple existing users; no accounts were changed';
 END IF;
 SELECT * INTO demo_user FROM users
 WHERE lower(username)=seed.username OR lower(email)=seed.email FOR UPDATE;
 user_exists := FOUND;
 IF user_exists THEN
  IF NOT company_exists OR demo_company.name IS DISTINCT FROM seed.company
    OR demo_company.status IS DISTINCT FROM 'active'
    OR demo_user.username IS DISTINCT FROM seed.username OR demo_user.email IS DISTINCT FROM seed.email
    OR demo_user.role IS DISTINCT FROM 'user' OR demo_user.is_active IS DISTINCT FROM true
    OR demo_user.deleted_at IS NOT NULL OR COALESCE(demo_user.keycloak_id,'')<>''
    OR demo_user.organization_id IS DISTINCT FROM demo_company.id
    OR demo_company.owner_id IS DISTINCT FROM demo_user.id
    OR demo_user.password_hash IS NULL
    OR crypt(seed.password,demo_user.password_hash) IS DISTINCT FROM demo_user.password_hash THEN
   RAISE EXCEPTION 'Demo identity already belongs to another or modified account; no accounts were changed';
  END IF;
 ELSIF company_exists THEN
  RAISE EXCEPTION 'Demo company already exists without this director; no accounts were changed';
 ELSE
  INSERT INTO organizations(name,slug,status)
   VALUES(seed.company,seed.slug,'active') RETURNING id INTO company_id;
  INSERT INTO users(email,username,password_hash,first_name,last_name,full_name_local,
                    position,role,is_active,is_first_login,organization_id)
   VALUES(seed.email,seed.username,crypt(seed.password,gen_salt('bf',12)),
          'Тестовый','Директор','Тестовый Директор','Директор','user',true,false,company_id)
   RETURNING id INTO director_id;
  UPDATE organizations SET owner_id=director_id,updated_at=now() WHERE id=company_id;
  INSERT INTO organization_admin_audit(organization_id,actor_id,action,object_type,object_id,after_value)
   VALUES(company_id,director_id,'development.director.seed','organization',company_id::text,
          jsonb_build_object('owner_id',director_id,'source','local-development'));
 END IF;
END;
$seed$;
SELECT json_build_object('user_id',u.id,'organization_id',o.id,'is_director',o.owner_id=u.id,
                        'role',u.role,'can_login',workspace_user_can_login(u.id),
                        'password_ok',crypt(s.password,u.password_hash)=u.password_hash)
 FROM users u JOIN organizations o ON o.id=u.organization_id
 JOIN dev_director_seed s ON u.username=s.username AND u.email=s.email;
COMMIT;
`
  const output = docker([
    'sh', '-c', 'exec psql -X -q -t -A -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"',
  ], sql)
  const result = JSON.parse(output.split('\n').filter(line => line.trim()).at(-1))
  if (!result.is_director || result.role !== 'user' || !result.can_login || !result.password_ok) {
    throw new Error('Проверка аккаунта не пройдена. Данные входа сохранены в .cache/dev-director.json.')
  }
  console.log(`Демо-директор создан или проверен: ${identity.company} (user_id=${result.user_id}).`)
  console.log('Вход: http://localhost:5173/web/login')
  console.log(`Логин: ${identity.username}`)
  console.log(`Почта: ${identity.email}`)
  console.log(`Пароль: ${password}`)
  console.log(`Данные входа сохранены: ${credentialsPath}`)
}

try { main() }
catch (error) {
  const message = error instanceof Error ? error.message : String(error)
  console.error('Не удалось создать демо-директора: ' + (password ? message.replaceAll(password, '[redacted]') : message))
  process.exitCode = 1
}
