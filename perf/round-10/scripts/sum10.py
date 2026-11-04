import json,glob,sys,statistics as st
S='/tmp/claude-0/r10/raw'
def flat(d,p=''):
    if isinstance(d,dict):
        for k,v in d.items(): yield from flat(v,p+k+'.')
    elif isinstance(d,list):
        for i,v in enumerate(d): yield from flat(v,p+str(i)+'.')
    elif isinstance(d,(int,float)) and not isinstance(d,bool): yield p[:-1],d
for store in ('sqlite','pg'):
    r={v:[dict(flat(json.load(open(f)))) for f in sorted(glob.glob(f'{S}/load-{store}-{v}-*.json'))] for v in ('old','new')}
    print('##',store,len(r['old']),len(r['new']))
    for k in r['old'][0]:
        if not (k.lower().endswith(('ms','bytes')) or 'Ms' in k): continue
        o=[x[k] for x in r['old'] if k in x]; n=[x[k] for x in r['new'] if k in x]
        if o and n and st.mean(o)>0 and not k.endswith(('sizeBytes','checkpointBytes')):
            print(f'{k:48s} old {st.mean(o):12.1f} new {st.mean(n):12.1f} {100*(st.mean(n)/st.mean(o)-1):+7.1f}%  old {[round(x,1) for x in o]} new {[round(x,1) for x in n]}')
