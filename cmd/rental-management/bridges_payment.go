package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"rental-management/config"
	"rental-management/internal/adapter/filestore"
	"rental-management/internal/adapter/imageproc"
	"rental-management/internal/adapter/persistence/postgres"
	paymentpg "rental-management/internal/adapter/persistence/postgres/payment"
	"rental-management/internal/adapter/security"
	invoicedomain "rental-management/internal/domain/invoice"
	paymentdomain "rental-management/internal/domain/payment"
	propertydomain "rental-management/internal/domain/property"
	invoiceuc "rental-management/internal/usecase/invoice"
	notificationuc "rental-management/internal/usecase/notification"
	paymentuc "rental-management/internal/usecase/payment"
	"rental-management/pkg/imaging"
	"rental-management/pkg/secretbox"
	"rental-management/pkg/storage"
)

// The bridges between the invoice and payment contexts (and the picture adapters that speak the property context's
// types). Neither context imports the other (the linter enforces it): this composition root adapts one's use cases to the
// other's ports. The direction of the dependencies is invoice -> its PaymentLedger port <- the payment use cases, and
// payment -> its InvoiceStatus port <- the invoice use cases, so they meet only here.

// paymentLedger answers the invoice context's PaymentLedger port with the payment use cases. The use cases are set when
// the payment context is built (newPayments); until then (the daily invoice commands, which never touch payments' files)
// it is the ledger newInvoices made, whose file store can delete nothing.
type paymentLedger struct{ l *paymentuc.Ledger }

// Totals implements invoiceuc.PaymentLedger.
func (p *paymentLedger) Totals(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]invoicedomain.Totals, error) {
	got, err := p.l.Totals(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]invoicedomain.Totals, len(got))
	for id, t := range got {
		out[id] = invoicedomain.Totals{PaidVND: t.ConfirmedVND, PendingVND: t.PendingVND}
	}
	return out, nil
}

// Settle implements invoiceuc.PaymentLedger.
func (p *paymentLedger) Settle(ctx context.Context, s invoiceuc.Settlement) error {
	return p.l.Settle(ctx, paymentuc.Settlement{InvoiceID: s.InvoiceID, ActorID: s.ActorID, AmountVND: s.AmountVND, At: s.At})
}

// Void implements invoiceuc.PaymentLedger.
func (p *paymentLedger) Void(ctx context.Context, invoiceID uuid.UUID) error {
	return p.l.Void(ctx, invoiceID)
}

// Purge implements invoiceuc.PaymentLedger.
func (p *paymentLedger) Purge(ctx context.Context, invoiceID uuid.UUID) error {
	return p.l.Purge(ctx, invoiceID)
}

// invoiceStatus lets payments move a bill to paid through the invoice use case, so its rules, its audit trail and its
// locks apply. The invoice use cases are built before the payment ones (they take the ledger above), so this is set once
// they exist.
type invoiceStatus struct{ invoices *invoiceuc.Invoices }

// MarkPaid implements paymentuc.InvoiceStatus.
func (s *invoiceStatus) MarkPaid(ctx context.Context, landlordID, invoiceID uuid.UUID) error {
	_, err := s.invoices.SetStatus(ctx, landlordID, invoiceID, invoicedomain.StatusPaid)
	return err
}

// proofFiles is the file store of the payment context on the byte store: it speaks the payment domain's "file not found"
// (the filestore adapter speaks the property domain's).
type proofFiles struct{ inner *filestore.Store }

// Put implements paymentuc.FileStore.
func (f proofFiles) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return f.inner.Put(ctx, key, data, contentType)
}

// Open implements paymentuc.FileStore.
func (f proofFiles) Open(ctx context.Context, key string) (io.ReadCloser, paymentuc.FileInfo, error) {
	rc, info, err := f.inner.Open(ctx, key)
	if errors.Is(err, propertydomain.ErrFileNotFound) {
		return nil, paymentuc.FileInfo{}, paymentdomain.ErrFileNotFound
	}
	return rc, paymentuc.FileInfo{Size: info.Size, ContentType: info.ContentType}, err
}

// Delete implements paymentuc.FileStore.
func (f proofFiles) Delete(ctx context.Context, keys ...string) error {
	return f.inner.Delete(ctx, keys...)
}

// proofImages verifies proof pictures with the image adapter and reports a refusal as the payment domain's error.
type proofImages struct{ inner *imageproc.Processor }

// Process implements paymentuc.ImageProcessor.
func (p proofImages) Process(ctx context.Context, data []byte) (paymentuc.ProcessedImage, error) {
	img, err := p.inner.Process(ctx, data)
	switch {
	case errors.Is(err, propertydomain.ErrImageType):
		return paymentuc.ProcessedImage{}, paymentdomain.ErrImageType
	case errors.Is(err, propertydomain.ErrImageCorrupt):
		return paymentuc.ProcessedImage{}, paymentdomain.ErrImageCorrupt
	case errors.Is(err, propertydomain.ErrImageDimensions):
		return paymentuc.ProcessedImage{}, paymentdomain.ErrImageDimensions
	case errors.Is(err, propertydomain.ErrImageTooLarge):
		return paymentuc.ProcessedImage{}, paymentdomain.ErrImageTooLarge
	case err != nil:
		return paymentuc.ProcessedImage{}, err
	}
	return paymentuc.ProcessedImage{Data: img.Data, ContentType: img.ContentType, Ext: img.Ext, Thumb: img.Thumb}, nil
}

// noFiles is the file store of a payment ledger that was built without storage (the daily invoice commands and the
// seed): it deletes nothing and says so, so a leftover proof is logged and never lost silently.
type noFiles struct{}

var errNoFiles = errors.New("no file store is wired")

func (noFiles) Put(context.Context, string, []byte, string) error { return errNoFiles }

func (noFiles) Open(context.Context, string) (io.ReadCloser, paymentuc.FileInfo, error) {
	return nil, paymentuc.FileInfo{}, errNoFiles
}

func (noFiles) Delete(context.Context, ...string) error { return errNoFiles }

// paymentOptions are the settings of the payment use cases.
func paymentOptions(cfg config.Config) paymentuc.Options {
	return paymentuc.Options{
		MaxProofFiles: cfg.PaymentProofMaxFiles, ProofLinkTTL: time.Duration(cfg.PaymentProofURLTTLMinutes) * time.Minute, WebURL: cfg.WebURL,
	}
}

// newPaymentLedger builds the payment ledger the invoice use cases are given. It reads and writes payments on Postgres
// and has no file store (see noFiles); newPayments replaces it with the full one.
func newPaymentLedger(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) *paymentLedger {
	deps := paymentuc.Deps{
		Tx: postgres.NewTxManager(pool), Payments: paymentpg.NewPaymentRepository(pool), Invoices: paymentpg.NewInvoiceReader(pool),
		Files: noFiles{}, Clock: paymentuc.ClockFunc(time.Now), Log: log,
	}
	return &paymentLedger{l: paymentuc.NewLedger(deps, paymentOptions(cfg))}
}

// payments is the use cases of payments and payment methods, on Postgres. The invoice use cases reach the money of
// their bills through paymentLedger (see newInvoices).
type payments struct {
	payments *paymentuc.Payments
	methods  *paymentuc.Methods
	seeder   *paymentuc.Seeder
	limits   proofLimits
}

// proofLimits are the limits the HTTP side applies to the proof pictures of a payment.
type proofLimits struct {
	maxFiles int
	maxBytes int64
}

// newPayments builds the payment use cases on Postgres, the file store over files and the image processor. The bills are
// a read-only view in the payment database adapter and are moved to paid through the invoice use cases (invoiceStatus);
// the settings of the payment methods are encrypted with the sealer, the proof links are signed with a key derived from
// DATA_ENCRYPTION_KEY, the messages go through n (nil: nobody is told), and the audit and mail ports keep their no-op
// defaults until those contexts exist. It also gives the invoice context's ledger its file store, so a deleted bill takes its proof pictures with it.
func newPayments(cfg config.Config, log *slog.Logger, pool *pgxpool.Pool, files storage.Storage, sealer *secretbox.Box, b invoices, n *notificationuc.Deliveries) (payments, error) {
	key, err := cfg.DataKey()
	if err != nil {
		return payments{}, err
	}
	links, err := security.NewProofLinks(key, cfg.APIPublicURL)
	if err != nil {
		return payments{}, err
	}
	maxBytes := cfg.PaymentProofMaxSizeKB * 1024
	deps := paymentuc.Deps{
		Tx: postgres.NewTxManager(pool), Payments: paymentpg.NewPaymentRepository(pool), Methods: paymentpg.NewMethodRepository(pool),
		Invoices: paymentpg.NewInvoiceReader(pool), Status: &invoiceStatus{invoices: b.invoices}, Files: proofFiles{inner: filestore.New(files)},
		Images: proofImages{inner: imageproc.New(imaging.NewProcessor(imaging.Limits{
			MaxBytes: maxBytes, MaxDimension: cfg.MediaMaxDimension, ThumbMaxSize: cfg.MediaThumbnailMaxSize,
		}, cfg.ImageProcessingConcurrency))},
		Links: links, Sealer: sealer, Clock: paymentuc.ClockFunc(time.Now), Log: log, Audit: paymentAudit{r: newAuditRecorder(log, pool)},
	}
	if n != nil {
		deps.Notifier = paymentNotifier{m: n}
	}
	opts := paymentOptions(cfg)
	b.ledger.l = paymentuc.NewLedger(deps, opts)
	return payments{
		payments: paymentuc.NewPayments(deps, opts), methods: paymentuc.NewMethods(deps, opts), seeder: paymentuc.NewSeeder(deps, opts),
		limits: proofLimits{maxFiles: cfg.PaymentProofMaxFiles, maxBytes: int64(maxBytes)},
	}, nil
}

var (
	_ invoiceuc.PaymentLedger  = (*paymentLedger)(nil)
	_ paymentuc.InvoiceStatus  = (*invoiceStatus)(nil)
	_ paymentuc.FileStore      = proofFiles{}
	_ paymentuc.ImageProcessor = proofImages{}
	_ paymentuc.FileStore      = noFiles{}
)
