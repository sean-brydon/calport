const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const {spawn} = require('node:child_process');
const {createRequire} = require('node:module');
const [action, root, name, work] = process.argv.slice(2);
const config = JSON.parse(fs.readFileSync(path.join(__dirname, 'config.json')));
const req = createRequire(path.join(config.root, 'package.json'));
const env = req('dotenv').parse(fs.readFileSync(path.join(root, '.env')));
if (action === 'env') { process.stdout.write(JSON.stringify(env)); process.exit(0); }
if (action === 'env-fingerprint') {
 const names=['.env','.env.local','.env.development','.env.development.local','.env.appStore'];
 const files=[...names.map(n=>path.join(root,n)),...names.map(n=>path.join(root,'apps/web',n))];
 const values=files.map(file=>fs.existsSync(file)?req('dotenv').parse(fs.readFileSync(file)):{});
 process.stdout.write(crypto.createHash('sha256').update(JSON.stringify(values.map(v=>Object.entries(v).sort()))).digest('hex'));process.exit(0);
}
const worktreeDB = n => /^calwt_[a-f0-9]{12}$/.test(n);
if (!['create','drop','sizes','prune-templates'].includes(action)) throw new Error('Invalid database request');
if ((action === 'create' || action === 'drop') && !worktreeDB(name)) throw new Error('Invalid database name');
const u = new URL(env.DATABASE_DIRECT_URL || env.DATABASE_URL);
if (!['localhost','127.0.0.1','[::1]'].includes(u.hostname)) throw new Error('Expected a local development PostgreSQL server');
u.search = '';
const sourceName = decodeURIComponent(u.pathname.slice(1));
const {Client} = req('pg');
const digest = value => crypto.createHash('sha256').update(value).digest('hex');
// Cal.com moved its Prisma schema; accept either layout.
const migrations = ['packages/infra/adapters/database/prisma/migrations', 'packages/prisma/migrations']
 .map(dir => path.join(work || '', dir)).find(dir => fs.existsSync(dir));
const compatible = rows => migrations && rows.length > 0 && rows.every(row => {
 const file = path.join(migrations, row.migration_name, 'migration.sql');
 return row.finished_at && !row.rolled_back_at && fs.existsSync(file) && digest(fs.readFileSync(file)) === row.checksum;
});
function client(database) {const url = new URL(u); url.pathname = '/' + database; return new Client({connectionString:url.toString()});}
// pg_dump and pg_restore run inside the Postgres container when the box has no
// client tools of its own; the installer records that container's name.
function pgTool(tool, database, args, file, output) {
 const docker = config.postgres_container;
 const penv = {...process.env, PGPASSWORD:decodeURIComponent(u.password)};
 const connection = ['--host',docker ? '127.0.0.1' : u.hostname,'--port',docker ? '5432' : (u.port || '5432'),'--username',decodeURIComponent(u.username),'--dbname',database];
 const command = docker ? 'docker' : tool;
 const argv = docker ? ['exec','-i','-e','PGPASSWORD',docker,tool,...connection,...args] : [...connection,...args];
 const fd = fs.openSync(file, output ? 'w' : 'r', 0o600);
 return new Promise((resolve,reject) => {
  const child=spawn(command,argv,{env:penv,stdio:[output?'ignore':fd,output?fd:'ignore','pipe']});
  fs.closeSync(fd); let error=''; child.stderr.on('data',data=>{error+=data;});
  child.on('error',reject);child.on('close',code=>code===0?resolve():reject(new Error(tool+' failed: '+error)));
 });
}
// Copying the main database needs pg_dump, here or in the Postgres container.
function canSnapshot() {
 return Boolean(config.postgres_container) || (process.env.PATH || '').split(':').some(dir => dir && fs.existsSync(path.join(dir, 'pg_dump')));
}
// The template the last snapshot clone used; any other is from older migrations.
const currentTemplateFile = path.join(__dirname, 'current-template');
async function drop(c, database) {
 try { await c.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`); }
 catch { await c.query(`DROP DATABASE IF EXISTS "${database}"`); } // PostgreSQL before 13
}
async function housekeeping() {
 const c=client('postgres'); await c.connect();
 try {
  if (action === 'drop') { await drop(c, name); console.log(JSON.stringify({dropped:name})); return; }
  if (action === 'sizes') {
   const names=(name||'').split(',').filter(worktreeDB);
   const rows=(await c.query('SELECT datname, pg_database_size(datname)::bigint AS bytes FROM pg_database WHERE datname = ANY($1)',[names])).rows;
   console.log(JSON.stringify(Object.fromEntries(rows.map(r=>[r.datname,Number(r.bytes)])))); return;
  }
  // prune-templates keeps the template the last clone used or, with no
  // record of one yet, the newest.
  await c.query("SELECT pg_advisory_lock(719245831)");
  const rows=(await c.query("SELECT datname, oid::bigint AS oid, pg_database_size(datname)::bigint AS bytes FROM pg_database WHERE datname LIKE 'caltpl\\_%' ORDER BY oid::bigint")).rows;
  const recorded=fs.existsSync(currentTemplateFile)?fs.readFileSync(currentTemplateFile,'utf8').trim():'';
  const current=rows.some(r=>r.datname===recorded)?recorded:(rows.length?rows[rows.length-1].datname:'');
  const stale=rows.filter(r=>r.datname!==current);
  if (name !== 'dry-run') for (const r of stale) { await c.query(`ALTER DATABASE "${r.datname}" IS_TEMPLATE false`).catch(()=>{}); await drop(c, r.datname); }
  console.log(JSON.stringify({templates:stale.map(r=>({name:r.datname,bytes:Number(r.bytes)}))}));
 } finally { await c.end(); }
}
if (action !== 'create') { housekeeping().catch(e=>{console.error(e.message);process.exit(1)}); return; }
(async () => {
 const c=client('postgres'); await c.connect(); let source;
 try {
  // Serialize template publication and database creation across all setup processes.
  await c.query("SELECT pg_advisory_lock(719245831)");
  const existing=await c.query('SELECT shobj_description(oid,\'pg_database\') AS note FROM pg_database WHERE datname=$1',[name]);
  if(existing.rowCount) { console.log(JSON.stringify({cloned:existing.rows[0].note==='cal-worktree snapshot',existing:true})); return; }
  source=client(sourceName);await source.connect();
  await source.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY');
  const hasHistory=await source.query("SELECT to_regclass('public._prisma_migrations') AS present");
  const rows=hasHistory.rows[0].present ? (await source.query('SELECT migration_name,checksum,finished_at,rolled_back_at FROM public._prisma_migrations ORDER BY migration_name')).rows : [];
  // Rolled-back attempts are not applied migrations. Unfinished attempts force a fresh DB.
  const applied=rows.filter(row=>!row.rolled_back_at);
  if(!work || !compatible(applied) || !canSnapshot()) {
   await c.query(`CREATE DATABASE "${name}"`);
   const reason=canSnapshot()?'Source migrations do not match this worktree':'No pg_dump on this box';
   console.log(JSON.stringify({cloned:false,reason:reason+'; using fresh migrations and seed'}));return;
  }
  const epochFile=path.join(__dirname,'snapshot-generation');
  const epoch=fs.existsSync(epochFile)?fs.readFileSync(epochFile,'utf8'):'';
  const template='caltpl_'+digest(JSON.stringify([u.host,sourceName,u.username,applied.map(r=>[r.migration_name,r.checksum]),epoch])).slice(0,20);
  const exists=await c.query('SELECT datallowconn FROM pg_database WHERE datname=$1',[template]);
  if(exists.rowCount && exists.rows[0].datallowconn) await c.query(`DROP DATABASE "${template}"`); // Interrupted, unpublished build only.
  if(!exists.rowCount || exists.rows[0].datallowconn) {
   console.error('Building reusable development database snapshot');
   const snapshot=(await source.query('SELECT pg_export_snapshot() AS id')).rows[0].id;
   const archive=path.join(__dirname,template+'.dump');
   try {
    await pgTool('pg_dump',sourceName,['--format=custom','--no-owner','--no-acl','--snapshot',snapshot],archive,true);
    await c.query(`CREATE DATABASE "${template}"`);
    await pgTool('pg_restore',template,['--no-owner','--no-acl','--exit-on-error'],archive,false);
    await c.query(`ALTER DATABASE "${template}" ALLOW_CONNECTIONS false`);
   } catch(error) {
    await c.query(`DROP DATABASE IF EXISTS "${template}"`);throw error;
   } finally { fs.rmSync(archive,{force:true}); }
  }
  await source.query('COMMIT');
  await c.query(`CREATE DATABASE "${name}" TEMPLATE "${template}"`);
  fs.writeFileSync(currentTemplateFile, template);
  await c.query(`COMMENT ON DATABASE "${name}" IS 'cal-worktree snapshot'`);
  console.log(JSON.stringify({cloned:true,template}));
 } finally {if(source)await source.end();await c.end();}
})().catch(e=>{console.error(e.message);process.exit(1)});
