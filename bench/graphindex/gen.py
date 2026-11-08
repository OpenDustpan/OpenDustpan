# One shared data set for both dialects. Deterministic.
import random, sys, os
out=sys.argv[1]; random.seed(42)
units=[]  # (seq, ns, size)
seq=0
def add(ns,size):
    global seq; seq+=1; units.append((seq,ns,size))
# ns 0: 2,100 units, one hub of 30,000 resources; ns 1..9: 100 units each, one unit of 30,000 in each
add(0,30000)
for _ in range(2099): add(0, random.randint(50,400))
for ns in range(1,10):
    add(ns,30000)
    for _ in range(99): add(ns, random.randint(50,400))
# 40 units of 2,000
for i in range(40): add(i%10, 2000)
R=lambda: random.getrandbits(63)
prod={}
with open(os.path.join(out,'prod.csv'),'w') as f:
    for s,ns,size in units:
        hs=[R() for _ in range(2*size)]; prod[s]=hs
        for h in hs: f.write(f"{ns},{h},{s},{1 if h%3 else 2}\n")
byns={}
for s,ns,size in units: byns.setdefault(ns,[]).append(s)
hub={ns:[s for s in byns[ns] if dict((u[0],u[2]) for u in units)[s]==30000][0] for ns in byns}
size_of={u[0]:u[2] for u in units}
ncons=0
with open(os.path.join(out,'cons.csv'),'w') as f:
    for s,ns,size in units:
        if size==30000: continue
        want=int(1.5*size); vals=set()
        h=hub[ns]
        vals.update(random.sample(prod[h], min(int(want*0.6), len(prod[h]))))
        nb=random.sample([u for u in byns[ns] if u!=s and u!=h], 5)
        while len(vals)<want:
            p=random.choice(nb); vals.add(random.choice(prod[p]))
        vals-=set(prod[s])
        for v in vals: f.write(f"{ns},{v},{s}\n")
        ncons+=len(vals)
with open(os.path.join(out,'units.csv'),'w') as f:
    for s,ns,size in units: f.write(f"{s},{ns},{size},{s%40}\n")
print("units",len(units),"resources",sum(u[2] for u in units),"produced",sum(2*u[2] for u in units),"consumed",ncons,"hub0",hub[0])
