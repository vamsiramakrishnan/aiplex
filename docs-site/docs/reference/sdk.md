---
title: Go SDK
description: The Go client for the AIPlex API.
---

# Go SDK

`github.com/vamsiramakrishnan/aiplex/sdk/aiplex` is a Go client for the control plane API. The CLI is built on it.

```go
package main

import (
	"context"
	"fmt"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func main() {
	c := aiplex.NewClient("https://aiplex.example.com")
	c.SetToken("TOKEN")

	instances, err := c.ListInstances(context.Background(), &aiplex.ListInstancesOpts{Plane: "mcplex"})
	if err != nil {
		panic(err)
	}
	fmt.Println(len(instances), "instances")
}
```

Errors are `*aiplex.Error` values carrying the API's `code`, `message` and HTTP status.

## Coverage

The client has methods for the catalog, instances, agents, the identity features, model surfaces, access points and the overview, the usage ledger, QuotaPlex inspection and administration, and the connected-module resources. The source is in [`sdk/aiplex`](https://github.com/vamsiramakrishnan/aiplex/tree/main/sdk/aiplex); each file's package comments document its methods, and `go doc` lists them:

```bash
go doc github.com/vamsiramakrishnan/aiplex/sdk/aiplex
```
