package notification

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "rental-management/internal/domain/notification"
	"rental-management/internal/domain/shared"
)

type imageWorld struct {
	*annWorld
	uc     *Images
	images *imageFake
	files  *fileFake
	imager *imagerFake
}

func newImageWorld() *imageWorld {
	w := &imageWorld{annWorld: newAnnWorld(), images: newImageFake(), files: newFileFake(), imager: &imagerFake{}}
	w.uc = NewImages(Deps{
		Tx: txFake{}, Announcements: w.repo, Images: w.images, Files: w.files, Imager: w.imager, Audit: w.audit,
		Clock: ClockFunc(func() time.Time { return w.now }),
	}, Options{ImageMaxPlatform: 10, ImageMaxLandlord: 3, MaxDimension: 2048, MaxUploadBytes: 1 << 20})
	return w
}

func (w *imageWorld) draft(t *testing.T, sender domain.Sender, scope domain.Scope) uuid.UUID {
	t.Helper()
	w.repo.people, w.repo.mine = users(2), users(2)
	d, err := w.annWorld.uc.Create(ctx, sender, popupContent(0), false)
	if sender.Role == "landlord" {
		c := popupContent(0)
		c.Scope = scope
		d, err = w.annWorld.uc.Create(ctx, sender, c, false)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestUploadStoresTheFileAndTheRowAtTheEnd(t *testing.T) {
	w := newImageWorld()
	id := w.draft(t, adminS, domain.ScopeTenants)
	first, err := w.uc.Upload(ctx, adminS, id, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := w.uc.Upload(ctx, adminS, id, []byte("two"))
	if first.DisplayOrder != 0 || second.DisplayOrder != 1 || !strings.HasPrefix(first.FileKey, domain.ImageKeyPrefix) || !strings.HasSuffix(first.FileKey, ".webp") {
		t.Errorf("%+v %+v", first, second)
	}
	if string(w.files.files[first.FileKey]) != "one" {
		t.Error("file stored")
	}
	if got := w.audit.actions[len(w.audit.actions)-1]; got != AuditAnnouncementImages {
		t.Errorf("audit %s", got)
	}
}

func TestUploadLimitsFollowTheSender(t *testing.T) {
	w := newImageWorld()
	id := w.draft(t, landlordS, domain.ScopeMyTenants)
	for range 3 {
		if _, err := w.uc.Upload(ctx, landlordS, id, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := w.uc.Upload(ctx, landlordS, id, []byte("x"))
	var limit *domain.ImageLimitError
	if !errors.As(err, &limit) || limit.Max != 3 {
		t.Fatalf("a landlord has 3: %v", err)
	}
	if len(w.files.deleted) != 1 {
		t.Error("the file of the refused upload is removed again")
	}
	admin := w.draft(t, adminS, domain.ScopeTenants)
	for range 10 {
		if _, err := w.uc.Upload(ctx, adminS, admin, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.uc.Upload(ctx, adminS, admin, []byte("x")); !errors.As(err, &limit) || limit.Max != 10 {
		t.Errorf("an admin has 10: %v", err)
	}
}

func TestUploadRefusals(t *testing.T) {
	w := newImageWorld()
	id := w.draft(t, adminS, domain.ScopeTenants)
	if _, err := w.uc.Upload(ctx, landlordS, id, []byte("x")); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another sender's message: %v", err)
	}
	w.imager.err = domain.ErrImageDimensions
	_, err := w.uc.Upload(ctx, adminS, id, []byte("x"))
	var dims *domain.ImageDimensionsError
	if !errors.As(err, &dims) || dims.Max != 2048 {
		t.Errorf("dimensions carry the limit: %v", err)
	}
	w.imager.err = nil
	if err := w.annWorld.uc.Recall(ctx, adminS, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.uc.Upload(ctx, adminS, id, []byte("x")); !errors.Is(err, domain.ErrRecalled) {
		t.Errorf("recalled: %v", err)
	}
}

func TestArrangeOrdersLinksAndRemovesTheUnlisted(t *testing.T) {
	w := newImageWorld()
	id := w.draft(t, adminS, domain.ScopeTenants)
	var imgs []domain.Image
	for _, n := range []string{"a", "b", "c"} {
		img, _ := w.uc.Upload(ctx, adminS, id, []byte(n))
		imgs = append(imgs, img)
	}
	link := "/find-rooms"
	out, err := w.uc.Arrange(ctx, adminS, id, []domain.ArrangeItem{{ID: imgs[2].ID, LinkURL: &link}, {ID: imgs[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].ID != imgs[2].ID || out[0].LinkURL != link || out[1].ID != imgs[0].ID || out[1].LinkURL != "" {
		t.Fatalf("%+v", out)
	}
	if len(w.files.deleted) != 1 || w.files.deleted[0] != imgs[1].FileKey {
		t.Errorf("the unlisted file is deleted after the commit: %v", w.files.deleted)
	}
	// Leaving the link out keeps it; an empty one clears it.
	out, _ = w.uc.Arrange(ctx, adminS, id, []domain.ArrangeItem{{ID: imgs[2].ID}})
	if out[0].LinkURL != link {
		t.Error("kept")
	}
	empty := ""
	out, _ = w.uc.Arrange(ctx, adminS, id, []domain.ArrangeItem{{ID: imgs[2].ID, LinkURL: &empty}})
	if out[0].LinkURL != "" {
		t.Error("cleared")
	}
}

func TestArrangeRefusals(t *testing.T) {
	w := newImageWorld()
	id := w.draft(t, adminS, domain.ScopeTenants)
	img, _ := w.uc.Upload(ctx, adminS, id, []byte("a"))
	bad := "javascript:alert(1)"
	if _, err := w.uc.Arrange(ctx, adminS, id, []domain.ArrangeItem{{ID: img.ID, LinkURL: &bad}}); !isInvalid(err, "items.0.link_url", domain.RuleURLUnsafe) {
		t.Errorf("unsafe link: %v", err)
	}
	if _, err := w.uc.Arrange(ctx, adminS, id, []domain.ArrangeItem{{ID: uuid.New()}}); !isInvalid(err, "items", domain.RuleExists) {
		t.Errorf("a picture of nobody: %v", err)
	}
	if _, err := w.uc.Arrange(ctx, adminS, id, []domain.ArrangeItem{{ID: img.ID}, {ID: img.ID}}); !isInvalid(err, "items", domain.RuleExists) {
		t.Errorf("listed twice: %v", err)
	}
	mine := w.draft(t, landlordS, domain.ScopeMyTenants)
	own, _ := w.uc.Upload(ctx, landlordS, mine, []byte("a"))
	link := "/find-rooms"
	if _, err := w.uc.Arrange(ctx, landlordS, mine, []domain.ArrangeItem{{ID: own.ID, LinkURL: &link}}); !errors.Is(err, domain.ErrLinkNotAllowed) {
		t.Errorf("a landlord may not link: %v", err)
	}
}

func isInvalid(err error, field, rule string) bool {
	var v *domain.ValidationError
	if !errors.As(err, &v) {
		return false
	}
	for _, x := range v.Violations {
		if x.Name == field && x.Rule == rule {
			return true
		}
	}
	return false
}

func TestDeleteClosesUpPositionsAndRemovesTheFile(t *testing.T) {
	w := newImageWorld()
	id := w.draft(t, adminS, domain.ScopeTenants)
	var imgs []domain.Image
	for _, n := range []string{"a", "b", "c"} {
		img, _ := w.uc.Upload(ctx, adminS, id, []byte(n))
		imgs = append(imgs, img)
	}
	if err := w.uc.Delete(ctx, adminS, id, imgs[0].ID); err != nil {
		t.Fatal(err)
	}
	left, _ := w.images.List(ctx, id)
	if len(left) != 2 || left[0].DisplayOrder != 0 || left[1].DisplayOrder != 1 || w.files.deleted[0] != imgs[0].FileKey {
		t.Errorf("%+v %v", left, w.files.deleted)
	}
	if err := w.uc.Delete(ctx, adminS, id, imgs[0].ID); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("twice: %v", err)
	}
}

func TestOpenServesOnlyAnnouncementKeys(t *testing.T) {
	w := newImageWorld()
	w.files.files["property/2026/11/x.webp"] = []byte("p")
	w.files.files["announcements/2026/11/x.webp"] = []byte("a")
	if _, _, err := w.uc.Open(ctx, "property/2026/11/x.webp"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("another context's key: %v", err)
	}
	rc, info, err := w.uc.Open(ctx, "announcements/2026/11/x.webp")
	if err != nil || info.Size != 1 {
		t.Fatalf("%v %+v", err, info)
	}
	_ = rc.Close()
	if _, _, err := w.uc.Open(ctx, "announcements/2026/11/missing.webp"); !errors.Is(err, shared.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}
