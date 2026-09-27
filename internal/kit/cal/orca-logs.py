#!/usr/bin/env python3
"""Best-effort, idempotent creation of the worktree's Orca log viewer."""
import fcntl,json,os,pathlib,subprocess,sys
base=pathlib.Path.home()/'.local/share/cal-worktrees'
cli=json.loads((base/'config.json').read_text()).get('orca') or 'orca'
env={k:v for k,v in os.environ.items() if not k.startswith('ORCA_')}
env['PATH']=str(pathlib.Path.home()/'.local/bin')+':/usr/local/bin:/usr/bin:/bin'
def api(*args):
    result=subprocess.run([cli,*args,'--json'],capture_output=True,text=True,env=env,timeout=12)
    data=json.loads(result.stdout)
    if result.returncode or not data.get('ok'): raise RuntimeError(data.get('error',{}).get('message','Orca request failed'))
    return data['result']
def handles(node):
    if isinstance(node,dict):
        if node.get('handle'):yield node['handle']
        for v in node.values():yield from handles(v)
    elif isinstance(node,list):
        for v in node:yield from handles(v)
def named_handles(node,title='Cal logs'):
    if isinstance(node,dict):
        if node.get('title')==title:yield from handles(node)
        else:
            for v in node.values():yield from named_handles(v,title)
    elif isinstance(node,list):
        for v in node:yield from named_handles(v,title)
def ensure(work,title='Cal logs',action='logs'):
    import hashlib
    key=hashlib.sha256(str(work).encode()).hexdigest()[:12]
    with (base/(key+'-logs.lock')).open('w') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        result=api('terminal','list','--worktree','path:'+str(work))
        if result.get('truncated'):raise RuntimeError('Incomplete terminal list; not creating a duplicate')
        record=base/(key+'-'+action+'-terminal.json')
        known=json.loads(record.read_text()).get('handle') if record.exists() else None
        named=set(named_handles(result,title))
        for t in result['terminals']:
            if t['handle'] in named or t['handle']==known:
                if t.get('connected') and t.get('writable'):
                    print(title+' terminal already available');return
        created=api('terminal','create','--worktree','path:'+str(work),'--title',title,'--focus','--command',str(base/'cal-worktree')+' '+action)['terminal']
        record.write_text(json.dumps({'handle':created['handle']})+'\n')
        print('Created Orca terminal: '+title)
        if created.get('warning'):print(created['warning'])
if __name__=='__main__':
    try:
        work=pathlib.Path(sys.argv[1]).resolve()
        ensure(work)
        ensure(work,'Cal links','links')
    except Exception as error:print('Orca logs tab unavailable: '+str(error)+'. Run ~/.local/bin/cal-worktree logs in a terminal.',file=sys.stderr)
