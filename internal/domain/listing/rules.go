package listing

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// CanMove reports whether a listing may go from one status to another. A waiting or live listing goes back to a
// draft when the room it offers (or its own text) is changed; taking a live listing down is the landlord's or an
// admin's call; changing a live one by moving it to pending is allowed too.
func CanMove(from, to Status) bool {
	switch from {
	case StatusDraft:
		return to == StatusPending
	case StatusPending:
		return to == StatusApproved || to == StatusRejected || to == StatusDraft
	case StatusApproved:
		return to == StatusUnpublished || to == StatusPending || to == StatusDraft
	case StatusRejected, StatusUnpublished:
		return to == StatusPending
	}
	return false
}

// EditingReturnsToDraft reports whether changing the text takes a listing off the admins' hands and back to a draft:
// it was waiting or live.
func EditingReturnsToDraft(s Status) bool { return s.IsOpen() }

// move changes the status, or says it may not be done.
func (l *Listing) move(to Status) error {
	if !CanMove(l.Status, to) {
		return ErrInvalidTransition
	}
	l.Status = to
	return nil
}

// Edit changes the headline and the text (a nil argument keeps what is there). It reports whether anything changed
// and whether the listing went back to a draft because of it: a change to a waiting or live listing takes it off
// find rooms and out of the admins' queue, and the landlord sends it again when ready. Saving without a change
// leaves everything as it is.
func (l *Listing) Edit(title *string, description *string, setDescription bool) (changed, backToDraft bool, err error) {
	if title != nil && *title != l.Title {
		l.Title, changed = *title, true
	}
	if setDescription && !sameText(l.Description, description) {
		l.Description, changed = description, true
	}
	if !changed {
		return false, false, nil
	}
	if EditingReturnsToDraft(l.Status) {
		if err := l.ReturnToDraft(); err != nil {
			return changed, false, err
		}
		return true, true, nil
	}
	return true, false, nil
}

func sameText(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == *b
}

// ReturnToDraft takes a waiting or live listing back to a draft (what an edit of the listing or of its room does).
func (l *Listing) ReturnToDraft() error {
	if err := l.move(StatusDraft); err != nil {
		return err
	}
	l.SubmittedAt, l.ReviewNote = nil, nil
	return nil
}

// Submit sends the listing to the admins (also after a refusal or a take-down).
func (l *Listing) Submit(now time.Time) error {
	if err := l.move(StatusPending); err != nil {
		return err
	}
	l.SubmittedAt, l.ReviewNote = &now, nil
	return nil
}

// Unpublish is the landlord taking a live listing off find rooms.
func (l *Listing) Unpublish(now time.Time) error {
	if err := l.move(StatusUnpublished); err != nil {
		return err
	}
	l.UnpublishedAt, l.ReviewNote = &now, nil
	return nil
}

// TakeDown is an admin taking a live listing off find rooms, with the reason the landlord is shown.
func (l *Listing) TakeDown(by uuid.UUID, now time.Time, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return ErrNoteRequired
	}
	if err := l.move(StatusUnpublished); err != nil {
		return err
	}
	l.UnpublishedAt = &now
	l.review(by, now, &reason)
	return nil
}

// Approve is an admin putting the listing on find rooms; the note is optional.
func (l *Listing) Approve(by uuid.UUID, now time.Time, note *string) error {
	if err := l.move(StatusApproved); err != nil {
		return err
	}
	l.review(by, now, note)
	l.PublishedAt, l.UnpublishedAt = &now, nil
	return nil
}

// Reject is an admin refusing the listing; the reason is required.
func (l *Listing) Reject(by uuid.UUID, now time.Time, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return ErrNoteRequired
	}
	if err := l.move(StatusRejected); err != nil {
		return err
	}
	l.review(by, now, &reason)
	return nil
}

func (l *Listing) review(by uuid.UUID, now time.Time, note *string) {
	l.ReviewedByID, l.ReviewedAt, l.ReviewNote = &by, &now, note
}

// CheckDelete says whether a listing may be deleted: a live one has to be taken down first.
func (l Listing) CheckDelete() error {
	if l.Status == StatusApproved {
		return ErrUnpublishFirst
	}
	return nil
}

// ReviewStatus is the status an admin's list shows: a draft is not theirs to see, so asking for drafts shows the
// waiting listings instead (and none is ever listed as a draft).
func ReviewStatus(asked *Status) Status {
	if asked == nil || *asked == StatusDraft {
		return StatusPending
	}
	return *asked
}

// SearchPattern turns what a user typed into the body of a lower case LIKE pattern with its wildcard characters
// escaped ("\" is the escape character); nil when there is nothing to look for.
func SearchPattern(s string) *string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	p := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
	return &p
}
