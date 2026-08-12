# loom-mcp Quickstart — Installation

## Prerequisites

Go 1.27rc2 or later.

```bash
go version
```

Install the Loom CLI:

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.8.0-alpha.1
loom version
```

## Project setup

```bash
mkdir quickstart && cd quickstart
go mod init example.com/quickstart
go get github.com/CaliLuke/loom@v1.8.0-alpha.1 github.com/CaliLuke/loom-mcp/v2@latest
```

Temporal is optional for local development. The generated example starts with
the in-memory engine.
