import re,glob,statistics as st,sys,collections
S=sys.argv[1]
d=collections.defaultdict(lambda: collections.defaultdict(list))
for f in glob.glob(S+'/raw/*-*-*.txt'):
    m=re.match(r'.*/(\w+)-(old|new)-(\d+)\.txt',f)
    if not m or m.group(1)=='rt': continue
    for line in open(f):
        if 'PERF12' not in line: continue
        body=line.split('PERF12',1)[1].strip()
        name,*kv=body.split()
        name=re.sub(r'n=\d+','',name)
        # name may be like 'lock' then kv
        keyparts=[x for x in kv if '=' not in x]
        key=name+(' '+' '.join(keyparts) if keyparts else '')
        for x in kv:
            if '=' in x:
                k,v=x.split('=',1)
                try: d[key+' '+k][m.group(2)].append(float(v))
                except: pass
print('%-48s %6s %10s %10s %8s  n'%('metric','','old','new','change'))
for k in sorted(d):
    o=d[k].get('old',[]); n=d[k].get('new',[])
    mo=st.median(o) if o else float('nan'); mn=st.median(n) if n else float('nan')
    ch=(mn/mo-1)*100 if o and n and mo else float('nan')
    print('%-55s %10.3f %10.3f %+7.1f%%  %d/%d'%(k,mo,mn,ch,len(o),len(n)))
