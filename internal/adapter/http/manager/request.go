package managerhttp

import (
	"strings"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	usecase "rental-management/internal/usecase/manager"
)

// createRequest is the body of POST /v1/my/manager-invitations.
type createRequest struct {
	RoomIDs        []uuid.UUID `json:"room_ids" validate:"required,min=1,max=100"`
	ReplaceRoomIDs []uuid.UUID `json:"replace_room_ids" validate:"omitempty,max=100"`
	Capabilities   []string    `json:"capabilities" validate:"omitempty,max=20"`
	NotifyLandlord *bool       `json:"notify_landlord"`
	RecipientID    *uuid.UUID  `json:"recipient_id"`
	Email          *string     `json:"email" validate:"omitempty,email,max=190"`
}

func (r createRequest) input() (usecase.CreateInput, error) {
	caps, err := capabilities("capabilities", r.Capabilities)
	if err != nil {
		return usecase.CreateInput{}, err
	}
	notify := true
	if r.NotifyLandlord != nil {
		notify = *r.NotifyLandlord
	}
	return usecase.CreateInput{
		RoomIDs: r.RoomIDs, ReplaceRoomIDs: r.ReplaceRoomIDs, Capabilities: caps, NotifyLandlord: notify,
		RecipientID: r.RecipientID, Email: r.Email,
	}, nil
}

// updateRequest is the body of PUT /v1/my/room-managers/{id}.
type updateRequest struct {
	Grant          []string `json:"grant" validate:"omitempty,max=20"`
	Revoke         []string `json:"revoke" validate:"omitempty,max=20"`
	NotifyLandlord *bool    `json:"notify_landlord"`
}

func (r updateRequest) input() (usecase.UpdateInput, error) {
	grant, err := capabilities("grant", r.Grant)
	if err != nil {
		return usecase.UpdateInput{}, err
	}
	revoke, err := capabilities("revoke", r.Revoke)
	if err != nil {
		return usecase.UpdateInput{}, err
	}
	return usecase.UpdateInput{Grant: grant, Revoke: revoke, NotifyLandlord: r.NotifyLandlord}, nil
}

// bulkRequest is the body of POST /v1/my/room-managers/bulk.
type bulkRequest struct {
	Action         string      `json:"action" validate:"required,oneof=revoke update"`
	IDs            []uuid.UUID `json:"ids" validate:"required,min=1,max=200"`
	Grant          []string    `json:"grant" validate:"omitempty,max=20"`
	Revoke         []string    `json:"revoke" validate:"omitempty,max=20"`
	NotifyLandlord *bool       `json:"notify_landlord"`
}

// answerRequest is the body of preview, accept and decline: the secret of the link, or the id of an invitation addressed
// to the caller's account. The id is read as text so that one that does not parse is answered like any other invitation
// that cannot be used (404), not as a malformed field.
type answerRequest struct {
	Token        *string `json:"token"`
	InvitationID *string `json:"invitation_id"`
}

func (r answerRequest) ref() (usecase.Ref, error) {
	var ref usecase.Ref
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
