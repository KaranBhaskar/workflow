# Multi-Tenant AI Workflow Orchestration Platform

This project is a backend for running AI workflows over uploaded documents.

The overall flow is:

1. a tenant uploads documents
2. the documents are chunked for retrieval
3. a workflow retrieves relevant chunks for a query
4. an LLM-style step uses that context
5. the workflow can then call a tool, branch, pause for approval, or finish
6. the system keeps run history, retries failed async work, and records audit events

This project is mainly about the backend work around that flow: tenant isolation, workflow execution, async jobs, retries, dead-letter handling, and benchmarking.

## What It Includes

- document upload and chunking
- tenant-scoped API key auth
- JSON workflow definitions
- sync and async workflow execution
- retries and dead-letter handling
- audit events
- local benchmark runner

## Results

Measured with the local benchmark runner in this repo:

- small run: `10` documents, `20` workflow runs, `124.13 runs/sec`, `95.00%` success rate
- normal run: `800` documents, `2000` workflow runs, `7219.51 runs/sec`, `99.20%` success rate
- large run: `3000` documents, `6500` workflow runs, `10045.73 runs/sec`, `98.77%` success rate

These numbers come from the in-process benchmark path, so they are useful for comparing changes in this codebase. They are not production deployment numbers.

## Run It Locally

Start infrastructure:

```bash
docker compose up -d postgres redis minio
```

Run the API:

```bash
make run-api
```

Run the worker:

```bash
make run-worker
```

Run tests:

```bash
make test
```

Run the benchmark:

```bash
make bench BENCH_ARGS="-tenants 4 -docs-per-tenant 200 -doc-words 900 -sync-runs 1200 -async-runs 800 -concurrency 24"
```

Default dev API key from `.env.example`:

```text
example-key-tenant-a
```

## Notes

- stack: Go, PostgreSQL, Redis, MinIO, Docker Compose, OpenTelemetry
- benchmark command: `go run ./cmd/bench ...`
- tests live under `tests/`
