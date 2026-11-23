// Command rental-management is the single binary of the backend. `rental-management api` serves HTTP; `rental-management migrate [up|down|status]`
// and `rental-management seed [-demo]` manage the database.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: rental-management api|migrate|seed")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd := os.Args[1]; cmd {
	case "api":
		if err := runAPI(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(ctx, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	case "seed":
		if err := runSeed(ctx, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "rental-management: unknown command %q (want api|migrate|seed)\n", cmd)
		os.Exit(2)
	}
}
