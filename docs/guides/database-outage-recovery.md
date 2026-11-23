# Recover from a database outage

This guide shows what happens when the database stops during a state write or a lock write, and how to continue.

## One transaction for each write

The server commits a state version and the current state pointer in one transaction.
It commits a lock acquire and a lock release in one transaction too.
The transaction also rejects a write from an instance that lost the namespace to a failover.

When the database stops in the middle of a commit, the server rolls back the whole transaction.
No state version becomes current.
No lock stays held.

## What the CLI shows

OpenTofu reports a stored-write error.
The workspace keeps its prior state and its prior lock.

## Retry the command

Wait until the database returns, and run the same command again:

```sh
tofu apply
```

A retry with the same lock identity returns the result that the first request committed when the reply was lost.
The server writes no second state version.
When the first commit rolled back, the retry writes the state version once.

A committed lock stays held by its holder.
When the lock expires, another instance can take it over.
You do not need to unlock the workspace by hand.

## Check the state

Run:

```sh
tofu plan
```

The plan shows the state of the last successful write.
