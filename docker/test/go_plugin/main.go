package main

import (
	"context"

	squadron "github.com/mlund01/squadron-sdk"
)

func main() {
	app := squadron.New()

	squadron.Tool(app, "ping", "Returns pong.",
		func(ctx context.Context, _ struct{}) (string, error) {
			return "pong", nil
		})

	app.Serve()
}
