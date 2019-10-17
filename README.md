# Service to optimize stale GraphiteMergeTree tables
This software looking for tables with GraphiteMergeTree engine and evaluate if some of partitions should be optimized. It could work both as one-shot script and background daemon.

The next query is executed as search for the partitions to optimize:

```sql
SELECT
    concat(p.database, '.', p.table) AS table,
    p.partition AS partition,
    max(g.age) AS age,
    countDistinct(p.name) AS parts,
    toDateTime(max(p.max_date + 1)) AS max_time,
    max_time + age AS rollup_time,
    min(p.modification_time) AS modified_at
FROM system.parts AS p
INNER JOIN
(
    SELECT
        Tables.database AS database,
        Tables.table AS table,
        age
    FROM system.graphite_retentions
    ARRAY JOIN Tables
    GROUP BY
        database,
        table,
        age
) AS g ON (p.table = g.table) AND (p.database = g.database)
WHERE p.active AND ((toDateTime(p.max_date + 1) + g.age) < now())
GROUP BY
    table,
    partition
HAVING (modified_at < rollup_time) OR (parts > 1)
ORDER BY
    table ASC,
    partition ASC,
    age ASC
```

Before and after running you could run the next query:

```sql
SELECT
    database,
    table,
    count() AS parts,
    active,
    partition,
    min(min_date) AS min_date,
    max(max_date) AS max_date,
    formatReadableSize(sum(bytes_on_disk)) AS size,
    sum(rows) AS rows
FROM system.parts
INNER JOIN
(
    SELECT
        Tables.database AS database,
        Tables.table AS table
    FROM system.graphite_retentions
    ARRAY JOIN Tables
    GROUP BY
        database,
        table
) USING (database, table)
GROUP BY
    database,
    table,
    partition,
    active
ORDER BY
    database,
    table,
    partition,
    active ASC
```

It will show general info about every GraphiteMergeTree table on the server.

## Run the graphite-ch-optimizer
If you run the ClickHouse locally, you could just run `graphite-ch-optimizer -n --log-level debug` and see how many partitions on the instance are able to be merged automatically.

Default config:

```toml
[clickhouse]
  optimize-interval = 259200
  server-dsn = "tcp://localhost:9000?&optimize_throw_if_noop=1&read_timeout=3600&debug=true"

[daemon]
  dry-run = false
  loop-interval = 3600
  one-shot = false

[logging]
  log-level = "info"
  output = "-"
```

Possible command arguments:

```
Usage of graphite-ch-optimizer:
  -c, --config string            Filename of the custom config. CLI arguments override it
      --print-defaults           Print default config values and exit
      --optimize-interval uint   The active partitions won't be optimized more than once per this interval, seconds (default 259200)
  -s, --server-dsn string        DSN to connect to ClickHouse server (default "tcp://localhost:9000?&optimize_throw_if_noop=1&read_timeout=3600&debug=true")
  -n, --dry-run                  Will print how many partitions would be merged without actions
      --loop-interval uint       Daemon will check if there partitions to merge once per this interval, seconds (default 3600)
      --one-shot                 Program will make only one optimization instead of working in the loop (true if dry-run)
      --log-level string         Valid options are: panic, fatal, error, warn, warning, info, debug, trace
      --output string            The logs file. '-' is accepted as STDOUT (default "-")
```
