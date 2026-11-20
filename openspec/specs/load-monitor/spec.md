# load-monitor Specification

## Purpose
Define how the monitor of the load harness stops its sampler. The monitor ends a sample window between queries, bounds each query with a timeout, and measures the database transactions of the window.

## Requirements

### Requirement: The end of a window never closes the sampler connection

The monitor of the load harness SHALL stop its sampler between queries, so that the transaction read at the end of a window runs on an open connection.

#### Scenario: The window ends during a sampler query
- **WHEN** the window ends while a sampler query runs
- **THEN** the query finishes, the sampler stops, and the report holds the transaction rate of the window

#### Scenario: The window ends between queries
- **WHEN** the window ends between two sampler queries
- **THEN** the sampler stops at once, and the report holds the transaction rate of the window
