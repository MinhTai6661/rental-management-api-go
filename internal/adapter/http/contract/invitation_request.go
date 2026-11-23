package contracthttp

import (
	"strings"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/contract"
	usecase "rental-management/internal/usecase/contract"
)

// createInvitationRequest is the body of POST /v1/contract-invitations: the terms of the offer and who it is for.
type createInvitationRequest struct {
	RoomID         *uuid.UUID `json:"room_id" validate:"required"`
	StartsOn       string     `json:"starts_on" validate:"required"`
	EndsOn         *string    `json:"ends_on"`
	PriceVND       *int64     `json:"price_vnd"`
	DepositVND     *int64     `json:"deposit_vnd"`
	OccupantCount  *int       `json:"occupant_count" validate:"omitempty,min=1,max=50"`
	ContractStatus *string    `json:"contract_status"`
	Note           *string    `json:"note" validate:"omitempty,max=1000"`
	RecipientID    *uuid.UUID `json:"recipient_id"`
	Email          *string    `json:"email" validate:"omitempty,email,max=190"`
}

func (r createInvitationRequest) input() (usecase.CreateInvitationInput, error) {
	var c fieldCheck
	starts := c.date("starts_on", &r.StartsOn)
	ends := c.date("ends_on", r.EndsOn)
	c.money("price_vnd", r.PriceVND)
	c.money("deposit_vnd", r.DepositVND)
	in := usecase.CreateInvitationInput{
		RoomID: *r.RoomID, PriceVND: r.PriceVND, DepositVND: r.DepositVND, EndsOn: ends, Note: r.Note,
		RecipientID: r.RecipientID, Email: r.Email,
	}
	if r.OccupantCount != nil {
		in.OccupantCount = *r.OccupantCount
	}
	if r.ContractStatus != nil {
		if s := domain.Status(*r.ContractStatus); domain.ValidOfferStatus(s) {
			in.ContractStatus = s
		} else {
			c.add("contract_status", apperr.RuleOneOf, nil)
		}
	}
	if err := c.err(); err != nil {
		return usecase.CreateInvitationInput{}, err
	}
	in.StartsOn = *starts
	return in, nil
}

// answerRequest is the body of preview, accept and decline: the secret of the link, or the id of an invitation addressed
// to the caller's account. The id is read as text so that one that does not parse is answered like any other invitation
// that cannot be used (404), not as a malformed field.
type answerRequest struct {
	Token        *string `json:"token"`
	InvitationID *string `json:"invitation_id"`
}

func (r answerRequest) ref() (usecase.InvitationRef, error) {
	var ref usecase.InvitationRef
	if r.Token != nil && strings.TrimSpace(*r.Token) != "" {
		t := strings.TrimSpace(*r.Token)
		ref.Token = &t
	}
	if r.InvitationID != nil && strings.TrimSpace(*r.InvitationID) != "" {
		ref.InvitationID = &uuid.Nil
		if id, err := uuid.Parse(strings.TrimSpace(*r.InvitationID)); err == nil {
			ref.InvitationID = &id
		}
	}
	if ref.Token == nil && ref.InvitationID == nil {
		return ref, apperr.Invalid(apperr.Field{
			Name: "token", Rule: apperr.RuleRequiredWithout, Params: map[string]string{"other": "invitation_id"},
		})
	}
	return ref, nil
}
