# loom-mcp Quickstart — Installation

## Prerequisites

Go 1.26.1+

```bash
go version
```

Install the Loom CLI:

```bash
go install github.com/CaliLuke/loom/cmd/loom@v1.6.2
loom version
```

## Project setup

```bash
mkdir quickstart && cd quickstart
go mod init example.com/quickstart
go get github.com/CaliLuke/loom@v1.6.2 github.com/CaliLuke/loom-mcp@latest
```

Temporal is optional for local development. The generated example starts with
the in-memory engine.
