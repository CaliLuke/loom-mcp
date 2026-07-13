# loom-mcp Quickstart — Installation

## Prerequisites

Go 1.24+

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
go mod init quickstart
go get github.com/CaliLuke/loom@v1.6.2 github.com/CaliLuke/loom-mcp@latest
```
