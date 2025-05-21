//go:build tools
// +build tools

package main

import (
	_ "github.com/campoy/embedmd"
	_ "github.com/observatorium/api"
	_ "github.com/observatorium/up/cmd/up"
)
