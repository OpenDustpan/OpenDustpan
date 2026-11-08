import re, sys, statistics as st
def load(p):
    d = {}
    for l in open(p):
        if not l.startswith("Benchmark"): continue
        f = l.split()
        name = re.sub(r"-\d+$", "", f[0])
        m = {}
        for i in range(2, len(f) - 1):
            if f[i+1] in ("ns/op", "B/op", "allocs/op"):
                m[f[i+1]] = float(f[i])
        d.setdefault(name, {}).setdefault("ns", []).append(m.get("ns/op", 0))
        d[name].setdefault("B", []).append(m.get("B/op", 0))
        d[name].setdefault("al", []).append(m.get("allocs/op", 0))
    return d
o, n = load(sys.argv[1]), load(sys.argv[2])
print("| benchmark | old ns/op | new ns/op | change | old allocs | new allocs | old B/op | new B/op |")
print("|---|---|---|---|---|---|---|---|")
for k in sorted(set(o) | set(n)):
    if k not in o or k not in n:
        print(f"| {k} | only in {'old' if k in o else 'new'} |||||||"); continue
    a, b = st.median(o[k]["ns"]), st.median(n[k]["ns"])
    ch = (b - a) / a * 100 if a else 0
    print(f"| {k} | {a:.0f} | {b:.0f} | {ch:+.1f}% | {st.median(o[k]['al']):.0f} | {st.median(n[k]['al']):.0f} | {st.median(o[k]['B']):.0f} | {st.median(n[k]['B']):.0f} |")
