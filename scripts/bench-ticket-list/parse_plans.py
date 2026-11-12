# Reads the auto_explain output of the test database container and prints, per caller, the median
# buffers and server-side duration of the last 12 list queries (runs 7-12, after any plan switch).
# Enable auto_explain first (once per database):
#   ALTER DATABASE helpdesk_test SET session_preload_libraries = 'auto_explain';
#   ALTER DATABASE helpdesk_test SET auto_explain.log_min_duration = 0;
#   ALTER DATABASE helpdesk_test SET auto_explain.log_analyze = on;
#   ALTER DATABASE helpdesk_test SET auto_explain.log_buffers = on;
#   ALTER DATABASE helpdesk_test SET auto_explain.log_timing = off;
# Run: BENCH_LIST=1 go test ./internal/database -run TestListScale -v   (from backend/)
# then: python3 scripts/bench-ticket-list/parse_plans.py ghd-test-bench-testdb-1 [caller-to-print-the-plan-of]
import re,subprocess,sys,statistics
cont=sys.argv[1]
log=subprocess.run(["docker","logs",cont],capture_output=True,text=True).stderr
# split into entries at lines starting with a timestamp
ents=re.split(r'\n(?=\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+ UTC \[)',log)
cur=None; res={}
for e in ents:
    m=re.search(r"MARK (\S+) (\d+)",e)
    if m and "plan:" in e:
        cur=(m.group(1),int(m.group(2))); continue
    if "plan:" in e and "FROM tickets t" in e and cur:
        b=re.search(r"Buffers: shared (?:hit=(\d+))?(?: read=(\d+))?",e)
        hit=int(b.group(1) or 0) if b else 0; rd=int(b.group(2) or 0) if b and b.group(2) else 0
        # first Buffers line is the top node (whole query)
        ex=re.search(r"Execution Time: ([\d.]+) ms",e)
        d=re.search(r"duration: ([\d.]+) ms",e)
        res.setdefault(cur[0],[]).append((cur[1],hit+rd,float(d.group(1)) if d else None,e))
import os
LAST=int(os.environ.get('LAST','12'))
for k,v in res.items():
    v=v[-LAST:]
    late=[x for x in v if x[0]>=6]
    print(f"{k:22s} runs={len(v)} buffers(median runs7-12)={int(statistics.median(x[1] for x in late))} duration_ms(median)={statistics.median(x[2] for x in late):.1f}")
if len(sys.argv)>2:
    k=sys.argv[2]; print(res[k][-1][3][:6000])
