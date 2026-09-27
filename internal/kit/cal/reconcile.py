#!/usr/bin/env python3
"""Reconcile only registered Cal worktrees with Orca's authoritative archive state."""
import json, os, pathlib, subprocess, time
base=pathlib.Path.home()/'.local/share/cal-worktrees'
config=json.loads((base/'config.json').read_text())
env={k:v for k,v in os.environ.items() if not k.startswith('ORCA_')}
env['PATH']=str(pathlib.Path.home()/'.local/bin')+':/usr/local/bin:/usr/bin:/bin'
ORCA=config.get('orca')

def tick():
    result=subprocess.run([ORCA,'worktree','list','--repo','path:'+config['root'],'--json'],env=env,capture_output=True,text=True,timeout=30)
    if result.returncode: return
    data=json.loads(result.stdout)
    if not data.get('ok') or data['result'].get('truncated'): return
    trees={r['path']:r for r in data['result']['worktrees']}
    if config['root'] not in trees: return
    for p in (base/'routes').glob('*.json'):
        record=json.loads(p.read_text()); key=p.stem
        tree=trees.get(record['path'])
        archived=tree is None or tree.get('isArchived',False)
        marker=base/(key+'.orca-archived')
        if archived:
            marker.touch(exist_ok=True)
            if record['active']:
                subprocess.run([str(base/'cal-worktree'),'stop',key],env=env,check=True,timeout=45)
        elif marker.exists() and pathlib.Path(record['path']).is_dir():
            # A successful CLI snapshot explicitly shows this saved workspace active again.
            name='cal-worktree-setup-'+key
            active=subprocess.run(['systemctl','--user','is-active','--quiet',name+'.service'],env=env).returncode==0
            if not active:
                result=subprocess.run(['systemd-run','--user','--collect','--unit='+name,
                    '--setenv=ORCA_ROOT_PATH='+record['root'],'--setenv=ORCA_WORKTREE_PATH='+record['path'],
                    str(base/'cal-worktree'),'setup'],env=env)
                if result.returncode==0: marker.unlink(missing_ok=True)

if __name__ == '__main__':
    if not ORCA: raise SystemExit('No Orca CLI on this box; nothing to reconcile')
    while True:
        try: tick()
        except Exception as e: print('Cal lifecycle reconciliation deferred: '+str(e),flush=True)
        time.sleep(15)
