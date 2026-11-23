// Command rental-management is the single binary of the backend. `rental-management api` serves HTTP; `rental-management migrate [up|down|status]`
// and `rental-management seed [-demo]` manage the database; `rental-management media-upload` copies the pictures of MEDIA_DIR into the S3 bucket;
// `rental-management subscriptions-expire` marks lapsed subscriptions as expired, `rental-management invoices-send-automatic` sends the bills
// of plans with automatic invoicing and `rental-management invoices-send-reminders` reminds people of bills about to fall due (run all three daily).
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
		fmt.Fprintln(os.Stderr, "usage: rental-management api|migrate|seed|media-upload|subscriptions-expire|invoices-send-automatic|invoices-send-reminders")
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
	case "media-upload":
		if err := runMediaUpload(ctx, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	case "subscriptions-expire":
		if err := runExpireSubscriptions(ctx, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	case "invoices-send-automatic":
		if err := runSendInvoices(ctx, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	case "invoices-send-reminders":
		if err := runSendReminders(ctx, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "rental-management:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "rental-management: unknown command %q (want api|migrate|seed|media-upload|subscriptions-expire|invoices-send-automatic|invoices-send-reminders)\n", cmd)
		os.Exit(2)
	}
}
