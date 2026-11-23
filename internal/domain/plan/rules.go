package plan

import (
	"regexp"
	"time"
)

// LimitReached reports whether current has used up the limit. A plan that sets no limit (nil) lets anything
// through; a limit is reached at the limit itself.
func LimitReached(limit *int, current int) bool {
	return limit != nil && current >= *limit
}

// CheckAssignable says why a plan cannot be given to an account, or nil when it can. The account must hold the role
// the plan is for and, when it acts as a landlord or a tenant right now, act in that one. actingRole is "" when
// nobody acts for the account (an admin handing out a plan); held are all the roles of the account.
func CheckAssignable(actingRole string, held []string, planAudience Audience, planActive bool) error {
	holds := false
	for _, r := range held {
		if r == string(planAudience) {
			holds = true
		}
	}
	actingElsewhere := (actingRole == string(AudienceLandlord) || actingRole == string(AudienceTenant)) &&
		actingRole != string(planAudience)
	if !holds || actingElsewhere {
		return ErrWrongAudience
	}
	if !planActive {
		return ErrPlanInactive
	}
	return nil
}

// EffectivePlan is the plan an account is on: the plan of its running subscription, else the default plan of the
// audience. nil means no plan at all (nothing seeded): then nothing is limited, so the app never locks people out.
func EffectivePlan(running, def *Plan) *Plan {
	if running != nil {
		return running
	}
	return def
}

// EndOf is when a subscription that starts at start lapses: the explicit end when given, none for a free plan, else
// one billing interval later.
func EndOf(p Plan, start time.Time, explicit *time.Time) *time.Time {
	switch {
	case explicit != nil:
		return explicit
	case p.IsFree():
		return nil
	}
	end := p.Interval.EndsAfter(start)
	return &end
}

// CheckEnd refuses an end that is not after now.
func CheckEnd(now time.Time, end *time.Time) error {
	if end != nil && !end.After(now) {
		return ErrEndInPast
	}
	return nil
}

var codePattern = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)

// ValidCode reports whether a plan code is lower case letters, digits and underscores, 1 to 50 characters.
func ValidCode(code string) bool { return codePattern.MatchString(code) }
