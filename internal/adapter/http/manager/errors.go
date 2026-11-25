// Package managerhttp is the HTTP adapter of the manager context: the landlord's invitations to manage rooms and the
// hand-overs, the invited person's answers, and the manager's own rooms.
package managerhttp

import (
	"errors"
	"strings"

	"rental-management/internal/adapter/http/apperr"
	"rental-management/internal/domain/invitation"
	domain "rental-management/internal/domain/manager"
)

// mapManagerError turns the errors of the manager use cases into the API's errors. Anything it does not know is
// returned as it is: httpx.Fail answers 503 for an overloaded database and 500 for the rest.
func mapManagerError(err error) error {
	if e := managerError(err); e != nil {
		return e
	}
	return err
}

// managerError is the API error of a domain error, or nil when err is not one of them.
func managerError(err error) *apperr.Error {
	field := func(name, rule string, params map[string]string) *apperr.Error {
		return apperr.Invalid(apperr.Field{Name: name, Rule: rule, Params: params})
	}
	var rooms *domain.RoomsError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &rooms):
		rule := apperr.RuleRoomsManaged
		if errors.Is(rooms.Err, domain.ErrRoomsPending) {
			rule = apperr.RuleRoomsPending
		}
		return field("room_ids", rule, map[string]string{"rooms": domain.NamesLabel(rooms.Names)})
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound()
	case errors.Is(err, domain.ErrInvitationInvalid):
		return apperr.E(apperr.KindNotFound, apperr.CodeInvitationInvalid)
	case errors.Is(err, domain.ErrInvitationNotPending):
		return apperr.Conflict(apperr.CodeInvitationNotPending)
	case errors.Is(err, domain.ErrLinkUnavailable):
		return apperr.E(apperr.KindNotFound, apperr.CodeInvitationLinkUnavailable)
	case errors.Is(err, domain.ErrCannotManage):
		return field("token", apperr.RuleCannotManage, nil)
	case errors.Is(err, domain.ErrRoomsChanged):
		return field("token", apperr.RuleRoomsChanged, nil)
	case errors.Is(err, domain.ErrRoomNotOwned):
		return field("room_ids", apperr.RuleRoomNotOwned, nil)
	case errors.Is(err, domain.ErrNoRooms):
		return field("room_ids", apperr.RuleMinItems, map[string]string{"min": "1"})
	case errors.Is(err, domain.ErrReplaceNotListed):
		return field("replace_room_ids", apperr.RuleReplaceNotListed, nil)
	case errors.Is(err, domain.ErrUnknownCapability):
		return field("capabilities", apperr.RuleOneOf, map[string]string{"values": strings.Join(capabilityCodes(), ", ")})
	case errors.Is(err, invitation.ErrRecipientInvalid):
		return field("recipient_id", apperr.RuleRecipientInvalid, nil)
	case errors.Is(err, invitation.ErrOwnEmail):
		return field("email", apperr.RuleOwnEmail, nil)
	case errors.Is(err, invitation.ErrRecipientMismatch):
		return field("email", apperr.RuleRecipientMismatch, nil)
	}
	return nil
}

func capabilityCodes() []string {
	cat := domain.Catalogue()
	out := make([]string, len(cat))
	for i, c := range cat {
		out[i] = string(c.Code)
	}
	return out
}
