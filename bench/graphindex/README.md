# Benchmark of the graph index (RFC 125)

These scripts made the measurements in
[RFC 125](../../docs/rfc/0125-blast-radius-index.md). They are a record of
how the numbers were made. `make check` and `make bench` do not run them.
Slice 1 of the RFC moves the benchmark into `make bench`.

The scripts ran one time, on the hardware that the RFC names. Treat each
number in the RFC as one measurement that nobody repeated.

## What you need

- Python 3, standard library only.
- PostgreSQL 18 with `psql`. Set `PGHOST`, `PGPORT`, `PGUSER` and
  `PGDATABASE` for a test server with no data of value. The scripts drop and
  create tables.
- Go, only for `extract/`. It has its own module and no dependencies.
- Root, only for cold runs: `run_queries.sh cold` restarts PostgreSQL and
  drops the OS page cache.

## Steps

Run each step in the data directory, except where a step takes it as an
argument.

| Step | Command | Gives |
| --- | --- | --- |
| 1 | `python3 -I gen.py DATA_DIR` | `units.csv`, `prod.csv`, `cons.csv`: 3,040 units, 1,064,500 resources, fixed seed |
| 2 | `psql -f pg_load.sql` | the PostgreSQL tables, the edges, the per-unit sets, and the bytes of each table |
| 3 | `python3 -I sqlite_load.py DATA_DIR worowid` and `... rowid` | the two SQLite files, the edges, and the bytes of each table |
| 4 | `python3 -I queries.py` | `queries.json`: the same queries and parameters for both dialects |
| 5 | `./run_queries.sh cold DATA_DIR` and `./run_queries.sh warm DATA_DIR` | the query times |
| 6 | `python3 -I hub.py` | the hub run: 60 probes and the edge upserts |
| 7 | `psql -f sets.sql` | the WAL bytes of a 3-value change: whole array against chunks |
| 8 | `python3 -I churn.py` | 5 cycles of a 10% change in both dialects, with the sizes after each cycle |
| 9 | `cd extract && go run . -n 100000` | the extraction speed on a synthetic OpenTofu state |

Run step 8 last: it changes the tables of steps 2 and 3.

`earlier/` holds the scripts of an earlier, denser data set (PostgreSQL and
a separate SQLite data set). The RFC uses them only for the size of text
values and for the time of a recursive query.
