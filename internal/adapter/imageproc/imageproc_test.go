package imageproc

import (
	"context"
	"errors"
	"testing"

	domain "rental-management/internal/domain/property"
	"rental-management/pkg/imaging"
)

func TestRefusalsBecomeDomainErrors(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"unsupported type", imaging.ErrUnsupported, domain.ErrImageType},
		{"animated", imaging.ErrAnimated, domain.ErrImageType},
		{"dimensions", imaging.ErrDimensions, domain.ErrImageDimensions},
		{"corrupt", imaging.ErrCorrupt, domain.ErrImageCorrupt},
		{"too large", imaging.ErrTooLarge, domain.ErrImageTooLarge},
	}
	for _, tc := range cases {
		if got := refusal(tc.in); !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	boom := errors.New("boom")
	if got := refusal(boom); !errors.Is(got, boom) {
		t.Errorf("another error is returned as it is, got %v", got)
	}
}

func TestProcessRefusesWhatIsNotAnImage(t *testing.T) {
	p := New(imaging.NewProcessor(imaging.Limits{MaxBytes: 1 << 20, MaxDimension: 2048, ThumbMaxSize: 480}, 1))
	if _, err := p.Process(context.Background(), []byte("hello, this is not a picture")); !errors.Is(err, domain.ErrImageType) {
		t.Errorf("got %v", err)
	}
	small := New(imaging.NewProcessor(imaging.Limits{MaxBytes: 4, MaxDimension: 2048, ThumbMaxSize: 480}, 1))
	if _, err := small.Process(context.Background(), []byte("0123456789")); !errors.Is(err, domain.ErrImageTooLarge) {
		t.Errorf("got %v", err)
	}
}
