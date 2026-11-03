import json,glob,sys,statistics as st
def flat(d,p=''):
    for k,v in d.items():
        if isinstance(v,dict): yield from flat(v,p+k+'.')
        elif isinstance(v,(int,float)): yield p+k,v
for store in ('sqlite','pg'):
    r={}
    for v in ('old','new'):
        r[v]=[dict(flat(json.load(open(f)))) for f in sorted(glob.glob(f'/tmp/claude-0/-home-user-OpenDustpan/8d462246-b08e-5b84-b084-5542bc39a675/scratchpad/raw/load-{store}-{v}-*.json'))]
    keys=[k for k in r['old'][0] if k.endswith(('Ms','p50','p95','p99','Mean','mean')) or 'ms' in k.lower()]
    print('##',store,len(r['old']),len(r['new']))
    for k in keys:
        o=[x[k] for x in r['old'] if k in x]; n=[x[k] for x in r['new'] if k in x]
        if o and n and st.mean(o)>0: print(f'{k:50s} old {st.mean(o):10.1f} new {st.mean(n):10.1f} {100*(st.mean(n)/st.mean(o)-1):+6.1f}%  old{[round(x) for x in o]} new{[round(x) for x in n]}')
