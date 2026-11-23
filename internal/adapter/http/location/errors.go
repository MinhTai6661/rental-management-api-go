package locationhttp

import (
	"errors"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/location"
)

// mapLocationError turns the errors of the location use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapLocationError(err error) error {
	if errors.Is(err, domain.ErrProvinceNotFound) {
		return apperr.E(apperr.KindNotFound, apperr.CodeProvinceNotFound)
	}
	return err
}
