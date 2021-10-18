# Exploring Postgres Write Ahead Logs (WAL)

I interact with Postgres on a daily basis, albeit typically managed by a service provider like [AWS RDS](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/CHAP_GettingStarted.CreatingConnecting.PostgreSQL.html#CHAP_GettingStarted.Creating.PostgreSQL). Hot take - here you pay a premium for RDS to accomodate the luxury of your ignorance - rightfully so. RDS abstracts the internals of Postgres allowing you to view it more and more like an intuitive store of data rather than dwelling on the implementation details.

Some **features** of personal interest these services typically offer are:

- Point in time recovery (PITR)
- Heated (or ready to go) backup replica's
- Logical replication

I learnt however that these are offered via proxy of _ease_ rather than PostgresQL component expansion.

With this study into Postgres WAL recently and it has given me the ability to clearly appreciate Postgres providing service's offerings.

# WAL Introduction

Database systems intend to garantuee _data validity_. In turn we find that this directly assumes that database transactions (units of change done) behave **correctly**. Correctly here is defined by four properties known as [ACID](https://en.wikipedia.org/wiki/ACID). WAL is a technique that directly targets [atomocity](<https://en.wikipedia.org/wiki/Atomicity_(database_systems)>) and [durability](<https://en.wikipedia.org/wiki/Durability_(database_systems)>).

With such a significant influence on the _data validity_ of a database system, you can understand my interest in it.

**WAL in Postgres - all modifications are written to a log before they are applied. Both redo and undo information is stored in the log.**

_Where do they live:_

```
 % docker-compose up --detach postgres
Creating network "postgres-wal_default" with the default driver
Creating postgres-wal_postgres_1 ... done
 % docker-compose exec postgres bash
root@5bfadf672170:/# cd $PGDATA/pg_wal
root@5bfadf672170:/var/lib/postgresql/data# ls -l
total 16388
-rw------- 1 postgres postgres 16777216 Oct 17 21:59 000000010000000000000001
drwx------ 2 postgres postgres     4096 Oct 17 21:59 archive_status
```

_Notes_:

- The filenaming convention here is seperated into two parts, leading 8 hexadecimal values represent a time element (epoched to when the DB cluster first started). Remaining 16 values increment as needed.
- WAL files are binary files with allocation of 16MB - this is changeable.

You can `--follow` these WAL files;

```
root@5bfadf672170:/var/lib/postgresql/data/pg_wal# pg_waldump 000000010000000000000001 -f
```

And in another terminal create DB changes

```
 % pgcli -h localhost -U postgres postgres
Password for postgres:
Server: PostgreSQL 14.0 (Debian 14.0-1.pgdg110+1)
Version: 3.2.0
Home: http://pgcli.com
postgres@localhost:postgres> CREATE TABLE tmp(val int);
CREATE TABLE
Time: 0.005s
postgres@localhost:postgres> INSERT INTO tmp(val) SELECT g.id FROM generate_series(1, 10) as g(id);
INSERT 0 10
Time: 0.005s
```

You can see how these WAL files are written before the query is returned in the earlier terminal session.

## Why do we have WAL files?
TODO: Write notes highlighting
- System diagram (shared buffer, `bg_writer`, `checkpoint`)
- Consequences without
- Replication

## Streaming these WAL files (principle behind backups)

Postgres exports a utility [`pg_receivewal`](https://www.postgresql.org/docs/14/app-pgreceivewal.html) that acts as a read once, immutable [message queue](https://en.wikipedia.org/wiki/Message_queue) allowing you to _stream_ these wall files to.. anywhere (for example archiving).

```
 % docker-compose exec postgres bash
root@03c55a788579:/# su postgres
postgres@03c55a788579:/$ cd $PGDATA/
postgres@03c55a788579:~/data$ cd ..
postgres@03c55a788579:~$ mkdir stream
postgres@03c55a788579:~$ pg_receivewal -D stream/
```

In another terminal you can view these files

```
root@03c55a788579:/# ls -l $PGDATA/../stream/
total 16384
-rw------- 1 postgres postgres 16777216 Oct 17 23:36 000000010000000000000001.partial
```

`.partial` files are actively streaming the current WAL file being written to. Once this `WAL` file is either filled up or switched for example;

```
postgres@localhost:postgres> select pg_switch_wal();
+-----------------+
| pg_switch_wal   |
|-----------------|
| 0/16FAC80       |
+-----------------+
SELECT 1
Time: 0.041s
```

You'll notice

```
root@03c55a788579:/# ls -l $PGDATA/../stream/
total 32768
-rw------- 1 postgres postgres 16777216 Oct 17 23:41 000000010000000000000001
-rw------- 1 postgres postgres 16777216 Oct 17 23:41 000000010000000000000002.partial
```

Although this is just simple archiving, you will see how this is an important concept to know about when it comes to general archiving, backing up and interestingly, the fundamental tool behind: [DB replication](https://www.postgresql.org/docs/14/runtime-config-replication.html).

## Replication Servers

Colloquially known as [replication slots](https://www.postgresql.org/docs/9.4/catalog-pg-replication-slots.html), are mechanisms that more formally wrap `pg_receivewal` that offers easy replication connections with the aim of providing a consistent interface among replication connectors.

You can create these replication slots via
```
postgres@localhost:postgres> select * from pg_create_physical_replication_slot('replica');
+-------------+--------+
| slot_name   | lsn    |
|-------------+--------|
| replica     | <null> |
+-------------+--------+
SELECT 1
Time: 0.018s
postgres@localhost:postgres> select slot_name, active from pg_replication_slots
+-------------+----------+
| slot_name   | active   |
|-------------+----------|
| replica     | False    |
+-------------+----------+
SELECT 1
Time: 0.009s
```

Interestingly, you can also use `pg_receivewal` to stream WAL files to somewhere too! Which technically just chains `pg_receivewal`.

```
postgres@993e83a382c4:~$ pg_receivewal -D stream/ -S replica
```

## Logical Replication

By default WAL file provides just enough information for basic replica support, which writes enough data to support WAL archiving and replication, including running read-only queries on a standby server. This is informed _physically_ which uses exact block addresses and byte-by-byte replication. We can change the `wal_level` to allow logical decoding of the WAL files allowing a more generic consumer ([difference between logical and physical replication](https://www.postgresql.org/docs/14/logical-replication.html)).

\_edit `postgres.conf` to change [`wal_level`](https://www.postgresql.org/docs/14/runtime-config-wal.html)

```
root@73d37a504686:/# cd $PGDATA/
root@73d37a504686:/var/lib/postgresql/data# vim postgresql.conf
```

_inside `postgresql.conf`_

1. search "WRITE-AHEAD LOG"
2. find option `wal_level` and change this to `logical`

_Let postgres cluster react to this config change by cluster restart_

```
root@73d37a504686:/var/lib/postgresql/data# su postgres
postgres@73d37a504686:~/data$ pg_ctl restart
waiting for server to shut down....
 % docker-compose up postgres
```

TODO:
- pg receive logical
- then table equivalent; https://www.postgresql.org/docs/14/logicaldecoding-example.html


## WAL files as changesets in JSON

- show use of `wal2json`, create stream
- integration of tom arrells messaging queue?

# Interesting utulisations of knowledge above

- unlogged tables (why you would)
- asynchronous commits
