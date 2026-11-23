package httpadapter

import (
	"errors"

	domain "rental-management/internal/domain/location"
	"rental-management/pkg/apperr"
)

// mapLocationError turns the errors of the location use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapLocationError(err error) error {
	if errors.Is(err, domain.ErrProvinceNotFound) {
		return apperr.E(apperr.KindNotFound, apperr.CodeProvinceNotFound)
	}
	return err
}
